package service

import (
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	openaisdk "github.com/openai/openai-go/v3"
	"google.golang.org/genai"

	"github.com/formbricks/hub/internal/googleai"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/openai"
)

// EmbeddingPreflightReason is a bounded, safe readiness label. Provider error text is never
// returned to the caller or recorded by this component.
type EmbeddingPreflightReason string

// Preflight reasons are fixed labels that cannot contain provider response text.
const (
	PreflightReady         EmbeddingPreflightReason = "ready"
	PreflightInvalidConfig EmbeddingPreflightReason = "invalid_config"
	PreflightEndpoint      EmbeddingPreflightReason = "endpoint"
	PreflightAuth          EmbeddingPreflightReason = "authentication"
	PreflightPermission    EmbeddingPreflightReason = "permission"
	PreflightModel         EmbeddingPreflightReason = "model"
	PreflightDimension     EmbeddingPreflightReason = "dimension"
	PreflightInput         EmbeddingPreflightReason = "input"
	PreflightRateLimited   EmbeddingPreflightReason = "rate_limited"
	PreflightTimeout       EmbeddingPreflightReason = "timeout"
	PreflightProvider      EmbeddingPreflightReason = "provider"
	PreflightExpired       EmbeddingPreflightReason = "expired"
)

const (
	embeddingPreflightInput             = "Hub embedding readiness check"
	embeddingPreflightBatchInputs       = 2
	defaultEmbeddingPreflightTimeout    = 10 * time.Second
	defaultEmbeddingPreflightSuccessTTL = 120 * time.Second
)

// EmbeddingPreflight probes the exact client used for embedding jobs. A transient failure keeps
// the last success valid for a bounded time; a configuration failure invalidates it immediately.
type EmbeddingPreflight struct {
	client     EmbeddingClient
	batch      BatchEmbeddingClient
	batchSize  int
	timeout    time.Duration
	successTTL time.Duration
	now        func() time.Time

	mu          sync.RWMutex
	lastSuccess time.Time
	reason      EmbeddingPreflightReason
}

// NewEmbeddingPreflight creates a probe for the exact embedding client and optional batch route.
func NewEmbeddingPreflight(client EmbeddingClient, batchSize int, timeout, successTTL time.Duration) *EmbeddingPreflight {
	if timeout <= 0 {
		timeout = defaultEmbeddingPreflightTimeout
	}

	if successTTL <= 0 {
		successTTL = defaultEmbeddingPreflightSuccessTTL
	}

	preflight := &EmbeddingPreflight{
		client: client, batchSize: batchSize, timeout: timeout, successTTL: successTTL,
		reason: PreflightProvider, now: time.Now,
	}
	if batchSize > 1 {
		preflight.batch, _ = client.(BatchEmbeddingClient)
	}

	return preflight
}

// NewFailedEmbeddingPreflight keeps a misconfigured worker running for unrelated queues while
// leaving embedding readiness false until the deployment configuration is corrected.
func NewFailedEmbeddingPreflight(reason EmbeddingPreflightReason) *EmbeddingPreflight {
	return &EmbeddingPreflight{reason: reason, now: time.Now}
}

// Probe calls the configured provider with synthetic text and validates its vector response.
func (p *EmbeddingPreflight) Probe(ctx context.Context) EmbeddingPreflightReason {
	if p.client == nil {
		return p.Reason()
	}

	probeCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	var err error

	//nolint:nestif // the batch route validates both response count and each returned vector
	if p.batchSize > 1 && p.batch != nil {
		var vectors [][]float32

		vectors, err = p.batch.CreateEmbeddings(probeCtx, []string{embeddingPreflightInput, embeddingPreflightInput})
		if err == nil {
			if len(vectors) != embeddingPreflightBatchInputs {
				p.setFailure(PreflightDimension)

				return PreflightDimension
			}

			for _, vector := range vectors {
				if reason := validatePreflightVector(vector); reason != PreflightReady {
					p.setFailure(reason)

					return reason
				}
			}
		}
	} else {
		var vector []float32

		vector, err = p.client.CreateEmbedding(probeCtx, embeddingPreflightInput)
		if err == nil {
			if reason := validatePreflightVector(vector); reason != PreflightReady {
				p.setFailure(reason)

				return reason
			}
		}
	}

	if err != nil {
		reason := ClassifyEmbeddingPreflightError(err)
		p.setFailure(reason)

		return reason
	}

	p.mu.Lock()
	p.lastSuccess = p.now()
	p.reason = PreflightReady
	p.mu.Unlock()

	return PreflightReady
}

func validatePreflightVector(vector []float32) EmbeddingPreflightReason {
	if len(vector) != models.EmbeddingVectorDimensions {
		return PreflightDimension
	}

	nonzero := false

	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return PreflightProvider
		}

		if value != 0 {
			nonzero = true
		}
	}

	if !nonzero {
		return PreflightProvider
	}

	return PreflightReady
}

// Ready reports whether a successful provider call is still within its validity window.
func (p *EmbeddingPreflight) Ready() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return !p.lastSuccess.IsZero() && p.now().Before(p.lastSuccess.Add(p.successTTL))
}

// ExpiresAt returns the deadline of the last successful probe, or zero if none is valid.
func (p *EmbeddingPreflight) ExpiresAt() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.lastSuccess.IsZero() {
		return time.Time{}
	}

	return p.lastSuccess.Add(p.successTTL)
}

// Reason returns the current bounded readiness reason, including last-success validity.
func (p *EmbeddingPreflight) Reason() EmbeddingPreflightReason {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if !p.lastSuccess.IsZero() {
		if p.now().Before(p.lastSuccess.Add(p.successTTL)) {
			return PreflightReady
		}

		return PreflightExpired
	}

	return p.reason
}

// LastFailureReason reports the latest bounded probe failure independently of the grace-period
// readiness state. It distinguishes transient failures even while readiness still says ready or expired.
func (p *EmbeddingPreflight) LastFailureReason() EmbeddingPreflightReason {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.reason
}

func (p *EmbeddingPreflight) setFailure(reason EmbeddingPreflightReason) {
	p.mu.Lock()

	p.reason = reason
	if reason != PreflightRateLimited && reason != PreflightTimeout && reason != PreflightProvider {
		p.lastSuccess = time.Time{}
	}
	p.mu.Unlock()
}

// ClassifyEmbeddingPreflightError maps provider errors to fixed labels. These errors are
// deployment-level failures for a synthetic probe, not terminal errors for customer records.
func ClassifyEmbeddingPreflightError(err error) EmbeddingPreflightReason {
	if errors.Is(err, ErrEmbeddingClientInitialization) {
		return PreflightAuth
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return PreflightTimeout
	}

	if _, ok := errors.AsType[*huberrors.RateLimitError](err); ok {
		return PreflightRateLimited
	}

	if errors.Is(err, openai.ErrDimensionMismatch) || errors.Is(err, googleai.ErrDimensionMismatch) {
		return PreflightDimension
	}

	if errors.Is(err, openai.ErrEmbeddingModelMismatch) {
		return PreflightModel
	}

	if _, ok := errors.AsType[*huberrors.TerminalProviderError](err); ok {
		return PreflightInput
	}

	if openaiErr, ok := errors.AsType[*openaisdk.Error](err); ok {
		return classifyPreflightHTTP(openaiErr.StatusCode, openaiErr.Code+" "+openaiErr.Message)
	}

	if googleErr, ok := errors.AsType[genai.APIError](err); ok {
		return classifyPreflightHTTP(googleErr.Code, googleErr.Status+" "+googleErr.Message)
	}

	if netErr, ok := errors.AsType[net.Error](err); ok {
		if netErr.Timeout() {
			return PreflightTimeout
		}

		// A reset during a provider restart is transient; an unreachable endpoint remains
		// a deployment fault and invalidates the previous success immediately.
		if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
			return PreflightProvider
		}

		return PreflightEndpoint
	}

	return PreflightProvider
}

func classifyPreflightHTTP(status int, code string) EmbeddingPreflightReason {
	switch status {
	case http.StatusUnauthorized:
		return PreflightAuth
	case http.StatusForbidden:
		return PreflightPermission
	case http.StatusNotFound:
		if strings.Contains(strings.ToLower(code), "model") {
			return PreflightModel
		}

		return PreflightEndpoint
	case http.StatusTooManyRequests:
		return PreflightRateLimited
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return PreflightTimeout
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		code = strings.ToLower(code)
		if strings.Contains(code, "dimension") {
			return PreflightDimension
		}

		if strings.Contains(code, "model") {
			return PreflightModel
		}

		return PreflightInput
	default:
		return PreflightProvider
	}
}
