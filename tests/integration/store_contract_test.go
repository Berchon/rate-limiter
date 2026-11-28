package integration

import (
	"context"
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/service"
	"github.com/Berchon/rate-limiter/internal/business/usecase"
	redisStore "github.com/Berchon/rate-limiter/internal/infra/redis"
	"github.com/Berchon/rate-limiter/internal/infra/storage"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type storeFactory struct {
	name string
	new  func(t *testing.T) service.LimiterStore
}

func storeFactories() []storeFactory {
	return []storeFactory{
		{name: "redis/fixed_window", new: func(t *testing.T) service.LimiterStore {
			mr := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = client.Close() })
			return redisStore.NewStore(client)
		}},
		{name: "redis/sliding_window", new: func(t *testing.T) service.LimiterStore {
			mr := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = client.Close() })
			store, err := redisStore.NewStoreWithAlgorithm(client, service.AlgorithmSlidingWindow)
			require.NoError(t, err)
			return store
		}},
		{name: "memory/fixed_window", new: func(t *testing.T) service.LimiterStore {
			return storage.NewInMemoryStore()
		}},
		{name: "memory/sliding_window", new: func(t *testing.T) service.LimiterStore {
			store, err := storage.NewInMemoryStoreWithAlgorithm(service.AlgorithmSlidingWindow)
			require.NoError(t, err)
			return store
		}},
	}
}

func TestStoreContract(t *testing.T) {
	ctx := context.Background()
	key := *entity.NewLimiterKey("ip", "203.0.113.1")

	for _, f := range storeFactories() {
		t.Run(f.name, func(t *testing.T) {
			t.Run("token config round trip", func(t *testing.T) {
				store := f.new(t)
				cfg := entity.NewTokenConfig("round-trip", 5, 10, 3, 30)
				require.NoError(t, store.SaveTokenConfig(ctx, cfg))

				got, err := store.GetConfigByToken(ctx, "round-trip")
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, 10, got.MaxRequests)
				assert.Equal(t, 5*time.Second, got.TimeWindow())
				assert.Equal(t, 3*time.Second, got.BlockedDuration())
				assert.Positive(t, got.TTL())

				missing, err := store.GetConfigByToken(ctx, "missing")
				require.NoError(t, err)
				assert.Nil(t, missing)
			})

			t.Run("explicit block and reset", func(t *testing.T) {
				store := f.new(t)

				status, err := store.IsBlocked(ctx, key)
				require.NoError(t, err)
				assert.False(t, status.Blocked)

				require.NoError(t, store.Block(ctx, key, 30))

				status, err = store.IsBlocked(ctx, key)
				require.NoError(t, err)
				assert.True(t, status.Blocked)
				assert.Positive(t, status.RemainingSeconds)

				require.NoError(t, store.ResetKey(ctx, key))

				status, err = store.IsBlocked(ctx, key)
				require.NoError(t, err)
				assert.False(t, status.Blocked)
			})

			t.Run("rejects without blocking when blockedDuration is zero", func(t *testing.T) {
				store := f.new(t)
				nowMs := time.Now().UnixMilli()

				res, err := store.AddRequest(ctx, key, nowMs, 1, 5, 0)
				require.NoError(t, err)
				assert.True(t, res.Allowed)
				assert.Equal(t, 0, res.Remaining)

				res, err = store.AddRequest(ctx, key, nowMs+100, 1, 5, 0)
				require.NoError(t, err)
				assert.False(t, res.Allowed)
				assert.Equal(t, 0, res.Remaining)
				assert.Positive(t, res.RetryAfterSeconds)

				status, err := store.IsBlocked(ctx, key)
				require.NoError(t, err)
				assert.False(t, status.Blocked)
			})

			t.Run("rejects invalid input", func(t *testing.T) {
				store := f.new(t)
				emptyKey := entity.LimiterKey{}

				assert.Error(t, store.SaveTokenConfig(ctx, nil))
				assert.Error(t, store.SaveTokenConfig(ctx, entity.NewTokenConfig("", 5, 5, 5, 5)))
				assert.Error(t, store.SaveTokenConfig(ctx, entity.NewTokenConfig("zero-ttl", 5, 5, 5, 0)))
				assert.Error(t, store.Block(ctx, emptyKey, 5))
				assert.Error(t, store.Block(ctx, key, 0))
				assert.Error(t, store.ResetKey(ctx, emptyKey))

				_, err := store.IsBlocked(ctx, emptyKey)
				assert.Error(t, err)

				for _, args := range [][3]int{{0, 5, 5}, {5, 0, 5}, {5, 5, -1}} {
					_, err = store.AddRequest(ctx, key, time.Now().UnixMilli(), args[0], args[1], args[2])
					assert.Error(t, err, "args %v", args)
				}
				_, err = store.AddRequest(ctx, emptyKey, time.Now().UnixMilli(), 5, 5, 5)
				assert.Error(t, err)
			})

			t.Run("honors cancelled contexts", func(t *testing.T) {
				store := f.new(t)
				cancelled, cancel := context.WithCancel(ctx)
				cancel()

				_, err := store.AddRequest(cancelled, key, time.Now().UnixMilli(), 5, 5, 5)
				assert.Error(t, err)
			})
		})
	}
}

func TestStoreConstructorsRejectUnsupportedAlgorithm(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	_, err := redisStore.NewStoreWithAlgorithm(client, service.Algorithm("token_bucket"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported rate-limit algorithm")

	_, err = storage.NewInMemoryStoreWithAlgorithm(service.Algorithm("token_bucket"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported rate-limit algorithm")
}

func TestUseCaseIsBlocked(t *testing.T) {
	for _, f := range storeFactories() {
		t.Run(f.name, func(t *testing.T) {
			ctx := context.Background()
			store := f.new(t)
			uc := usecase.NewRateLimiterUseCase(store, usecase.RateLimiterConfig{
				IPMaxRequests: 1, IPTimeWindowSec: 5, IPBlockedDuration: 30, EnableTokenLimiting: true, DefaultTokenTTL: 30,
			}, discardLogger())

			now := time.Now()
			decision, err := uc.AllowRequest(ctx, "203.0.113.9", "", now)
			require.NoError(t, err)
			assert.True(t, decision.Allowed)

			decision, err = uc.AllowRequest(ctx, "203.0.113.9", "", now)
			require.NoError(t, err)
			assert.False(t, decision.Allowed)

			blocked, remaining, err := uc.IsBlocked(ctx, *entity.NewLimiterKey("ip", "203.0.113.9"))
			require.NoError(t, err)
			assert.True(t, blocked)
			assert.Positive(t, remaining)
		})
	}
}
