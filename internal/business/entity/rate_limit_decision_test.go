package entity_test

import (
	"testing"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/stretchr/testify/assert"
)

func TestRateLimitDecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		decision entity.RateLimitDecision
	}{
		{
			name: "allowed request",
			decision: entity.RateLimitDecision{
				Allowed:           true,
				Limit:             100,
				Remaining:         99,
				ResetSeconds:      60,
				RetryAfterSeconds: 0,
				Reason:            "",
			},
		},
		{
			name: "blocked request",
			decision: entity.RateLimitDecision{
				Allowed:           false,
				Limit:             10,
				Remaining:         0,
				ResetSeconds:      0,
				RetryAfterSeconds: 300,
				Reason:            "rate limit exceeded",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.decision.Allowed, tc.decision.RetryAfterSeconds == 0)
			if tc.decision.Allowed {
				assert.Empty(t, tc.decision.Reason)
				assert.GreaterOrEqual(t, tc.decision.Remaining, 0)
			} else {
				assert.NotEmpty(t, tc.decision.Reason)
				assert.Equal(t, 0, tc.decision.Remaining)
			}
		})
	}
}
