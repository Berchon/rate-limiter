package entity_test

import (
	"testing"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLimiterKeyAndString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		prefix   string
		value    string
		expected string
	}{
		{
			name:     "IPv4 key",
			prefix:   "ip",
			value:    "192.168.0.1",
			expected: "ip:192.168.0.1",
		},
		{
			name:     "IPv6 key",
			prefix:   "ip",
			value:    "::1",
			expected: "ip:::1",
		},
		{
			name:     "token key",
			prefix:   "token",
			value:    "abc123",
			expected: "token:abc123",
		},
		{
			name:     "empty prefix and value",
			prefix:   "",
			value:    "",
			expected: ":",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key := entity.NewLimiterKey(tc.prefix, tc.value)
			require.NotNil(t, key)
			assert.Equal(t, tc.prefix, key.Prefix)
			assert.Equal(t, tc.value, key.Value)
			assert.Equal(t, tc.expected, key.String())
		})
	}
}
