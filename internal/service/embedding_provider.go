package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"golang.org/x/text/unicode/norm"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
)

// EmbeddingProvider implements eventPublisher by enqueueing one River job per feedback record event
// when the event is FeedbackRecordCreated (with non-empty value_text) or FeedbackRecordUpdated
// (with value_text in ChangedFields, including when value_text is now empty so the worker can clear).
type EmbeddingProvider struct {
	inserter    FeedbackEmbeddingInserter
	apiKey      string
	model       string
	queueName   string
	maxAttempts int
	docPrefix   string // model-specific prefix for document embedding; OpenAI and Google use ""
	metrics     observability.EmbeddingMetrics
}

// NewEmbeddingProvider creates a provider that enqueues feedback_embedding jobs.
// model is the embedding model name (e.g. text-embedding-3-small) from EMBEDDING_MODEL.
// docPrefix is the prefix for document text (from EmbeddingPrefixForProvider); use "" for OpenAI/Google.
// metrics may be nil when metrics are disabled.
func NewEmbeddingProvider(
	inserter FeedbackEmbeddingInserter,
	apiKey string,
	model string,
	queueName string,
	maxAttempts int,
	docPrefix string,
	metrics observability.EmbeddingMetrics,
) *EmbeddingProvider {
	return &EmbeddingProvider{
		inserter:    inserter,
		apiKey:      apiKey,
		model:       model,
		queueName:   queueName,
		maxAttempts: maxAttempts,
		docPrefix:   docPrefix,
		metrics:     metrics,
	}
}

// PublishEvent enqueues a feedback_embedding job when the event is FeedbackRecordCreated (with non-empty value_text)
// or FeedbackRecordUpdated (with value_text in ChangedFields). On update, the job is enqueued even when value_text
// is now empty so the worker can clear the embedding for text fields.
// API key is required for openai and google (validated at startup).
func (p *EmbeddingProvider) PublishEvent(ctx context.Context, event Event) {
	if event.Type == datatypes.FeedbackRecordUpdated {
		if !contains(event.ChangedFields, "value_text") && !contains(event.ChangedFields, "field_label") {
			slog.Debug("embedding: skip, value_text/field_label not in changed fields",
				"event_id", event.ID,
				"feedback_record_id", recordIDFromEventData(event.Data),
			)

			return
		}
	} else if event.Type != datatypes.FeedbackRecordCreated {
		return
	}

	record, ok := event.Data.(*models.FeedbackRecord)
	if !ok {
		slog.Debug("embedding: skip, event data is not *FeedbackRecord", "event_id", event.ID)

		return
	}

	// On create, only enqueue when there is embeddable text. On update we enqueue regardless so the worker can clear.
	if event.Type == datatypes.FeedbackRecordCreated &&
		BuildEmbeddingInput(record.FieldLabel, record.ValueText, p.docPrefix) == "" {
		slog.Debug("embedding: skip, no value_text on create", "feedback_record_id", record.ID)

		return
	}

	valueTextHash := embeddingValueTextHash(record.FieldLabel, record.ValueText, p.docPrefix)

	opts := &river.InsertOpts{
		Queue:       p.queueName,
		MaxAttempts: p.maxAttempts,
		UniqueOpts:  river.UniqueOpts{ByArgs: true, ByPeriod: uniqueByPeriodEmbedding},
	}

	_, err := p.inserter.Insert(ctx, FeedbackEmbeddingArgs{
		FeedbackRecordID: record.ID,
		Model:            p.model,
		ValueTextHash:    valueTextHash,
	}, opts)
	if err != nil {
		if p.metrics != nil {
			p.metrics.RecordProviderError(ctx, "enqueue_failed")
		}

		slog.Error("embedding: enqueue failed",
			"event_id", event.ID,
			"feedback_record_id", record.ID,
			"error", err,
		)

		return
	}

	slog.Info("embedding: job enqueued",
		"event_id", event.ID,
		"feedback_record_id", record.ID,
	)

	if p.metrics != nil {
		p.metrics.RecordJobsEnqueued(ctx, 1)
	}
}

func contains(s []string, v string) bool {
	return slices.Contains(s, v)
}

func recordIDFromEventData(data any) uuid.UUID {
	if r, ok := data.(*models.FeedbackRecord); ok {
		return r.ID
	}

	return uuid.Nil
}

const (
	questionPrefix = "Question: "
	answerPrefix   = "\nAnswer: "
)

// BuildEmbeddingInput prepares text for vector embedding.
// Uses a pre-allocated strings.Builder to reduce allocations on this hot path.
//
// We feed the model raw, natural text: only TrimSpace and Unicode NFC normalization are applied.
// Case, diacritics, and punctuation are preserved so the model retains semantic clues (e.g. "US" vs "us", "résumé" vs "resume").
//
// Arguments:
//   - fieldLabel: The "question" or metadata key (e.g. "What is your reasoning?").
//   - valueText:  The "answer" or main content (e.g. "I chose option B because...").
//   - prefix:     Model-specific task instruction; OpenAI and Google use "".
//
// Returns formatted string: "[prefix]Question: [label]\nAnswer: [value]" (or "[prefix][value]" when label is empty).
func BuildEmbeddingInput(fieldLabel, valueText *string, prefix string) string {
	if valueText == nil {
		return ""
	}

	trimmedValue := strings.TrimSpace(*valueText)
	if trimmedValue == "" {
		return ""
	}

	// Unicode NFC normalization: consistent byte representation without changing meaning (e.g. ñ as single codepoint).
	val := norm.NFC.String(trimmedValue)

	label := ""
	if fieldLabel != nil {
		label = norm.NFC.String(strings.TrimSpace(*fieldLabel))
	}

	// Pre-allocate to avoid builder growth and reduce GC pressure.
	bufCap := len(prefix) + len(val)
	if label != "" {
		bufCap += len(questionPrefix) + len(label) + len(answerPrefix)
	}

	var builder strings.Builder
	builder.Grow(bufCap)

	builder.WriteString(prefix)

	if label != "" {
		builder.WriteString(questionPrefix)
		builder.WriteString(label)
		builder.WriteString(answerPrefix)
	}

	builder.WriteString(val)

	return builder.String()
}

// EmbeddingPrefixForProvider returns the document prefix for the given embedding provider.
// OpenAI, Google, and Google Vertex AI use no prefix. Returns "" for all supported providers.
func EmbeddingPrefixForProvider(_ string) string {
	return ""
}

// embeddingValueTextHash returns a hash of the embedding input for dedupe (same content => same job within window).
// Uses BuildEmbeddingInput; empty content returns "empty".
func embeddingValueTextHash(fieldLabel, valueText *string, prefix string) string {
	content := BuildEmbeddingInput(fieldLabel, valueText, prefix)
	if content == "" {
		return "empty"
	}

	sum := sha256.Sum256([]byte(content))

	return hex.EncodeToString(sum[:])
}
