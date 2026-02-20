package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/metric"
)

// APIMetrics records API-level metrics (e.g. request body limit exceeded).
type APIMetrics interface {
	RecordRequestBodyTooLarge(ctx context.Context)
}

// apiMetrics implements APIMetrics.
type apiMetrics struct {
	requestBodyTooLarge metric.Int64Counter
}

// NewAPIMetrics creates APIMetrics. Returns (nil, nil) when meter is nil (metrics disabled).
func NewAPIMetrics(meter metric.Meter) (APIMetrics, error) {
	if meter == nil {
		//nolint:nilnil // intentional: callers use "if metrics != nil" when metrics disabled
		return nil, nil
	}

	desc := "Total number of requests rejected because the request body exceeded the configured limit (413)."

	counter, err := meter.Int64Counter(
		MetricNameRequestBodyTooLarge,
		metric.WithDescription(desc),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, fmt.Errorf("create request body too large counter: %w", err)
	}

	return &apiMetrics{requestBodyTooLarge: counter}, nil
}

func (a *apiMetrics) RecordRequestBodyTooLarge(ctx context.Context) {
	a.requestBodyTooLarge.Add(ctx, 1)
}
