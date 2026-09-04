package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/formbricks/hub/internal/repository"
)

// TestPendingQueryJobKindsMatchTheArgsTypes pins the kind literals the pending-set query's
// in-flight exclusion filters on to the kinds the args types actually declare. The repository
// cannot import service, so it spells the kinds out — and a repo-side kind that drifted would make
// the exclusion silently match nothing, reintroducing the double-enqueue it exists to prevent.
func TestPendingQueryJobKindsMatchTheArgsTypes(t *testing.T) {
	assert.Equal(t, repository.FeedbackSentimentJobKind, FeedbackSentimentArgs{}.Kind())
	assert.Equal(t, repository.FeedbackEmotionsJobKind, FeedbackEmotionsArgs{}.Kind())
	assert.Equal(t, repository.FeedbackTranslationJobKind, FeedbackTranslationArgs{}.Kind())
}
