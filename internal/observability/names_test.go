package observability

import "testing"

func TestAllowedDispatchReasonIncludesTenantBoundaryReasons(t *testing.T) {
	reasons := []string{
		"get_webhook_failed",
		"missing_tenant_id",
		"tenant_mismatch",
	}

	for _, reason := range reasons {
		if !AllowedDispatchReason(reason) {
			t.Errorf("AllowedDispatchReason(%q) = false, want true", reason)
		}

		if got := NormalizeReason(reason, AllowedDispatchReason); got != reason {
			t.Errorf("NormalizeReason(%q) = %q, want %q", reason, got, reason)
		}
	}
}
