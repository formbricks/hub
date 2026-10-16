package service

import (
	"context"
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
	inserter      RiverJobInserter
	model         string
	queueName     string
	maxAttempts   int
	docPrefix     string // model-specific prefix for document embedding; OpenAI and Google use ""
	maxInputBytes int    // cap on the embedding input, in bytes; 0 = unlimited
	metrics       observability.EmbeddingMetrics
	inputKind     models.EmbeddingInputKind
}

// NewEmbeddingProvider creates a provider that enqueues feedback_embedding jobs.
// model is the embedding model name (e.g. text-embedding-3-small) from EMBEDDING_MODEL.
// docPrefix is the prefix for document text (from EmbeddingPrefixForProvider); use "" for OpenAI/Google.
// maxInputBytes caps the input (EMBEDDING_MAX_INPUT_BYTES); 0 is unlimited.
// metrics may be nil when metrics are disabled.
func NewEmbeddingProvider(
	inserter RiverJobInserter,
	model string,
	queueName string,
	maxAttempts int,
	docPrefix string,
	maxInputBytes int,
	metrics observability.EmbeddingMetrics,
) *EmbeddingProvider {
	return NewEmbeddingProviderForInputKind(
		inserter, model, queueName, maxAttempts, docPrefix, maxInputBytes, metrics, models.EmbeddingInputKindRaw)
}

// NewEmbeddingProviderForInputKind creates a provider for a specific embedding input kind.
func NewEmbeddingProviderForInputKind(
	inserter RiverJobInserter,
	model string,
	queueName string,
	maxAttempts int,
	docPrefix string,
	maxInputBytes int,
	metrics observability.EmbeddingMetrics,
	inputKind models.EmbeddingInputKind,
) *EmbeddingProvider {
	return &EmbeddingProvider{
		inserter:      inserter,
		model:         model,
		queueName:     queueName,
		maxAttempts:   maxAttempts,
		docPrefix:     docPrefix,
		maxInputBytes: maxInputBytes,
		metrics:       metrics,
		inputKind:     models.NormalizeEmbeddingInputKind(inputKind),
	}
}

// PublishEvent enqueues a feedback_embedding job when the event is FeedbackRecordCreated (with non-empty value_text)
// or FeedbackRecordUpdated (with value_text in ChangedFields). On update, the job is enqueued even when value_text
// is now empty so the worker can clear the embedding for text fields.
// API key is required for openai and google (validated at startup).
func (p *EmbeddingProvider) PublishEvent(ctx context.Context, event Event) {
	if event.Type == datatypes.FeedbackRecordUpdated {
		if !p.hasEmbeddingRelevantChange(event.ChangedFields) {
			slog.Debug("embedding: skip, embedding input fields not in changed fields",
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

	// Build the embedding input once and reuse it for both the create-time empty check and the
	// dedupe hash; it was otherwise computed twice on the create path. The same capped string the
	// worker will embed is what gets hashed, so a change past the cap cannot present as a different
	// embedding than the one already stored.
	input, _ := BuildEmbeddingInputForKind(record, p.inputKind, p.docPrefix, TextCap{MaxBytes: p.maxInputBytes})

	// On create, only enqueue when there is embeddable text. On update we enqueue regardless so the worker can clear.
	if event.Type == datatypes.FeedbackRecordCreated && input == "" {
		slog.Debug("embedding: skip, no value_text on create", "event_id", event.ID, "feedback_record_id", record.ID)

		return
	}

	valueTextHash := hashContent(input)

	// Deliberately no UniqueOpts on the event path — mirrors the classify providers: River's
	// completed-state dedupe swallowed the re-embed after content transitioned away and back
	// within the dedupe bucket, leaving the record without an embedding (the clear path deletes
	// the row). Update events fire only on actual field changes, so each event is a real
	// transition worth embedding.
	opts := &river.InsertOpts{
		Queue:       p.queueName,
		MaxAttempts: p.maxAttempts,
	}

	_, err := p.inserter.Insert(ctx, FeedbackEmbeddingArgs{
		FeedbackRecordID: record.ID,
		EventID:          event.ID,
		Model:            p.model,
		InputKind:        p.inputKind,
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

func (p *EmbeddingProvider) hasEmbeddingRelevantChange(changedFields []string) bool {
	return slices.Contains(changedFields, "value_text") || slices.Contains(changedFields, "field_label")
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
//   - inputCap:   Maximum bytes to send (see TextCap); the zero value is unlimited.
//
// Returns the formatted string — "[prefix]Question: [label]\nAnswer: [value]" (or "[prefix][value]"
// when label is empty) — and whether the cap had to cut it.
func BuildEmbeddingInput(fieldLabel, valueText *string, prefix string, inputCap TextCap) (string, bool) {
	return BuildEmbeddingInputFromValues(fieldLabel, valueText, nil, models.EmbeddingInputKindRaw, prefix, inputCap)
}

// BuildEmbeddingInputForKind prepares text for a specific embedding input kind.
func BuildEmbeddingInputForKind(
	record *models.FeedbackRecord, kind models.EmbeddingInputKind, prefix string, inputCap TextCap,
) (string, bool) {
	if record == nil {
		return "", false
	}

	return BuildEmbeddingInputFromValues(
		record.FieldLabel, record.ValueText, record.ValueTextTranslated, kind, prefix, inputCap)
}

// BuildEmbeddingInputFromValues prepares text for vector embedding from raw record values.
// Taxonomy embeddings prefer translated text when present, falling back to original value_text.
//
// The cap is applied HERE, not at the call sites, because this string is both what is sent to the
// provider and what the write-time supersession guard compares against (see the stillCurrent closure
// in FeedbackEmbeddingWorker.Work). The two must agree byte for byte: a caller that remembered the
// cap in one place and not the other would let an edited record's vector keep describing text it no
// longer has, which nothing downstream can detect.
func BuildEmbeddingInputFromValues(
	fieldLabel, valueText, valueTextTranslated *string,
	kind models.EmbeddingInputKind,
	prefix string,
	inputCap TextCap,
) (string, bool) {
	if models.NormalizeEmbeddingInputKind(kind) == models.EmbeddingInputKindTaxonomyTranslated {
		if normalizedText(valueTextTranslated) != "" {
			return buildEmbeddingInput(fieldLabel, valueTextTranslated, prefix, inputCap)
		}
	}

	return buildEmbeddingInput(fieldLabel, valueText, prefix, inputCap)
}

// buildEmbeddingInput assembles the provider input and applies the cap to the assembled string —
// the instruction prefix and field label are part of what the provider sees, so they are part of
// what is bounded. A cap smaller than the prefix plus the label therefore cuts inside them and the
// answer never reaches the provider, which is why the recommended caps are orders of magnitude
// larger than a label can be (2,048 runes, up to ~8 KiB of multi-byte text).
func buildEmbeddingInput(fieldLabel, valueText *string, prefix string, inputCap TextCap) (string, bool) {
	if valueText == nil {
		return "", false
	}

	trimmedValue := strings.TrimSpace(*valueText)
	if trimmedValue == "" {
		return "", false
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

	// Slicing shares the builder's buffer, so an over-cap input costs no extra allocation here.
	return inputCap.Apply(builder.String())
}
