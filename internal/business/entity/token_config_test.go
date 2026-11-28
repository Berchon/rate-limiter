package entity_test

import (
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTokenConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		token           string
		timeWindow      int
		maxRequests     int
		blockedDuration int
		tokenTTL        int
	}{
		{
			name:            "standard token config",
			token:           "tok-123",
			timeWindow:      60,
			maxRequests:     100,
			blockedDuration: 300,
			tokenTTL:        3600,
		},
		{
			name:            "zero blocked duration",
			token:           "tok-456",
			timeWindow:      10,
			maxRequests:     5,
			blockedDuration: 0,
			tokenTTL:        120,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			before := time.Now()
			cfg := entity.NewTokenConfig(tc.token, tc.timeWindow, tc.maxRequests, tc.blockedDuration, tc.tokenTTL)
			after := time.Now()

			require.NotNil(t, cfg)
			assert.Equal(t, tc.token, cfg.Token)
			assert.Equal(t, tc.timeWindow, cfg.TimeWindowSeconds)
			assert.Equal(t, tc.maxRequests, cfg.MaxRequests)
			assert.Equal(t, tc.blockedDuration, cfg.BlockedDurationSeconds)
			assert.Equal(t, tc.tokenTTL, cfg.TokenTTLSeconds)

			assert.False(t, cfg.CreatedAt.Before(before))
			assert.False(t, cfg.CreatedAt.After(after))

			expectedExpiresAt := cfg.CreatedAt.Add(time.Duration(tc.tokenTTL) * time.Second)
			assert.Equal(t, expectedExpiresAt, cfg.ExpiresAt)
		})
	}
}

func TestTokenConfigDurations(t *testing.T) {
	t.Parallel()

	t.Run("BlockedDuration and TimeWindow", func(t *testing.T) {
		t.Parallel()

		cfg := &entity.TokenConfig{
			TimeWindowSeconds:      30,
			BlockedDurationSeconds: 120,
		}

		assert.Equal(t, 30*time.Second, cfg.TimeWindow())
		assert.Equal(t, 120*time.Second, cfg.BlockedDuration())
	})

	t.Run("TTL returns positive duration for future expiration", func(t *testing.T) {
		t.Parallel()

		cfg := &entity.TokenConfig{
			ExpiresAt: time.Now().Add(10 * time.Second),
		}

		ttl := cfg.TTL()
		assert.Greater(t, ttl, 8*time.Second)
		assert.LessOrEqual(t, ttl, 10*time.Second)
	})

	t.Run("TTL returns negative duration for past expiration", func(t *testing.T) {
		t.Parallel()

		cfg := &entity.TokenConfig{
			ExpiresAt: time.Now().Add(-5 * time.Second),
		}

		assert.Less(t, cfg.TTL(), time.Duration(0))
	})
}

func TestNormalizeTokenID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "raw token", input: "abc123", want: "abc123"},
		{name: "surrounding whitespace", input: "  abc123  ", want: "abc123"},
		{name: "single prefix", input: "token:abc123", want: "abc123"},
		{name: "double prefix", input: "token:token:abc123", want: "abc123"},
		{name: "spaced repeated prefixes", input: "  token: token: abc123  ", want: "abc123"},
		{name: "empty string", input: "", want: ""},
		{name: "whitespace only", input: "   ", want: ""},
		{name: "prefix only", input: "token:", want: ""},
		{name: "repeated prefixes only", input: "  token: token:  ", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := entity.NormalizeTokenID(tc.input)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, got, entity.NormalizeTokenID(got), "NormalizeTokenID must be idempotent")
		})
	}
}
