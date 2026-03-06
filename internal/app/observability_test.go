package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShutdownObservability(t *testing.T) {
	ctx := context.Background()

	t.Run("both nil returns nil", func(t *testing.T) {
		err := ShutdownObservability(ctx, nil, nil)
		assert.NoError(t, err)
	})
}
