package observability

import (
	"testing"

	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
)

// TestNewResource_MergesServiceNameWithoutSchemaConflict is a regression guard. With
// OTEL_SERVICE_NAME unset, newResource merges our per-binary service.name onto the SDK default.
// A prior version pinned semconv.SchemaURL on the override, which conflicts with the (newer)
// schema URL carried by resource.Default(); resource.Merge then returns ErrSchemaURLConflict and
// newResource propagates it — aborting telemetry (and thus process) startup whenever
// OTEL_SERVICE_NAME was unset and an OTLP exporter was configured. The schemaless override must
// merge cleanly, and our service.name must win.
//
// (The OTEL_SERVICE_NAME-set branch is a trivial early return of resource.Default(); it is not
// asserted here because resource.Default() is memoized by the SDK, so its service.name cannot be
// steered deterministically from a test.)
func TestNewResource_MergesServiceNameWithoutSchemaConflict(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")

	res, err := newResource("hub-test")
	if err != nil {
		t.Fatalf("newResource() error = %v, want nil (a schemaless override must not conflict with the default schema URL)", err)
	}

	if res == nil {
		t.Fatal("newResource() resource = nil")
	}

	got, ok := res.Set().Value(semconv.ServiceNameKey)
	if !ok {
		t.Fatal("resource has no service.name attribute")
	}

	if got.AsString() != "hub-test" {
		t.Fatalf("service.name = %q, want %q (our per-binary name must win when OTEL_SERVICE_NAME is unset)",
			got.AsString(), "hub-test")
	}
}
