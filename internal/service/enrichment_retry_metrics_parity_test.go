package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
)

// TestRetryMetricOutcomesMatchTheDomain binds the metric's outcome allowlist to the domain enum it
// mirrors.
//
// observability deliberately does not import models -- it depends on nothing -- so the retry
// counter re-declares the three outcome strings. That duplication is only safe while something
// checks it: normalizeRetryOutcome silently rewrites anything it does not recognise to "error", so
// renaming a domain outcome would not fail to compile, would not fail any existing test, and would
// simply make every retry of that kind report as an error forever.
func TestRetryMetricOutcomesMatchTheDomain(t *testing.T) {
	for domain, metricValue := range map[models.RetryOutcome]string{
		models.RetryOutcomeCleared:     observability.RetryOutcomeCleared,
		models.RetryOutcomeCoolingDown: observability.RetryOutcomeCoolingDown,
		models.RetryOutcomeDisabled:    observability.RetryOutcomeDisabled,
	} {
		assert.Equal(t, string(domain), metricValue,
			"the metric label and the API's own outcome value must be the same string")
	}
}
