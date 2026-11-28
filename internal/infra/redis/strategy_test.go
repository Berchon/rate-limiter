package redis

import (
	"context"
	"testing"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/service"
	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStoreWithAlgorithm(t *testing.T) {
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	tests := []struct {
		name    string
		alg     service.Algorithm
		wantErr bool
	}{
		{name: "empty defaults to fixed window", alg: ""},
		{name: "fixed window", alg: service.AlgorithmFixedWindow},
		{name: "sliding window", alg: service.AlgorithmSlidingWindow},
		{name: "unsupported algorithm", alg: service.Algorithm("token_bucket"), wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewStoreWithAlgorithm(client, tc.alg)
			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, store)
				assert.Contains(t, err.Error(), "unsupported rate-limit algorithm")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, store)
		})
	}

	t.Run("zero-value strategy falls back to fixed window", func(t *testing.T) {
		store := &Store{client: client}
		res, err := store.AddRequest(context.Background(), *entity.NewLimiterKey("ip", "127.0.0.1"), 0, 2, 10, 5)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 1, res.Remaining)
	})
}
