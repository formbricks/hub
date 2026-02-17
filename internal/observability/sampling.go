package observability

import (
	"os"
	"strconv"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// env names for OTEL trace sampling (standard env vars, not in config to keep config minimal).
const (
	envTracesSampler    = "OTEL_TRACES_SAMPLER"
	envTracesSamplerArg = "OTEL_TRACES_SAMPLER_ARG"
)

// defaultTraceIDRatio is used when OTEL_TRACES_SAMPLER is traceidratio or parentbased_traceidratio
// but OTEL_TRACES_SAMPLER_ARG is missing or invalid.
const defaultTraceIDRatio = 1.0

// newSampler returns a Sampler from OTEL_TRACES_SAMPLER and OTEL_TRACES_SAMPLER_ARG.
// Supported values: always_on, always_off, traceidratio, parentbased_traceidratio,
// parentbased_always_on, parentbased_always_off. Empty or unknown => parentbased_always_on.
func newSampler() sdktrace.Sampler {
	switch os.Getenv(envTracesSampler) {
	case "always_on":
		return sdktrace.AlwaysSample()
	case "always_off":
		return sdktrace.NeverSample()
	case "traceidratio":
		return sdktrace.TraceIDRatioBased(parseTraceIDRatio(os.Getenv(envTracesSamplerArg)))
	case "parentbased_traceidratio":
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(parseTraceIDRatio(os.Getenv(envTracesSamplerArg))))
	case "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample())
	default:
		// Empty or unknown: default to parentbased_always_on (same as SDK default).
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
}

func parseTraceIDRatio(s string) float64 {
	if s == "" {
		return defaultTraceIDRatio
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 1 {
		return defaultTraceIDRatio
	}

	return f
}
