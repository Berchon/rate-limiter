package redis

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_FixedWindow_AddRequest(t *testing.T) {
	t.Run("allows requests up to limit and resets after time window", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		ctx := context.Background()
		key := *entity.NewLimiterKey("ip", "192.168.1.10")

		for i := 1; i <= 3; i++ {
			res, err := store.AddRequest(ctx, key, time.Now().UnixMilli(), 3, 5, 10)
			require.NoError(t, err)
			assert.True(t, res.Allowed)
			assert.Equal(t, 3-i, res.Remaining)
			assert.GreaterOrEqual(t, res.ResetSeconds, int64(1))
			assert.LessOrEqual(t, res.ResetSeconds, int64(5))
			assert.Equal(t, 0, res.RetryAfterSeconds)
		}

		mr.FastForward(6 * time.Second)

		res, err := store.AddRequest(ctx, key, time.Now().UnixMilli(), 3, 5, 10)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 2, res.Remaining)
	})

	t.Run("isolates counters and blocks across distinct keys", func(t *testing.T) {
		_, _, store := newTestStore(t)
		ctx := context.Background()
		ipA := *entity.NewLimiterKey("ip", "192.168.1.1")
		ipB := *entity.NewLimiterKey("ip", "192.168.1.2")
		tokenKey := *entity.NewLimiterKey("token", "192.168.1.1")

		// Exhaust and block ipA.
		res, err := store.AddRequest(ctx, ipA, 0, 1, 10, 15)
		require.NoError(t, err)
		assert.True(t, res.Allowed)

		res, err = store.AddRequest(ctx, ipA, 0, 1, 10, 15)
		require.NoError(t, err)
		assert.False(t, res.Allowed)

		// ipB and tokenKey with same value must remain unaffected.
		res, err = store.AddRequest(ctx, ipB, 0, 1, 10, 15)
		require.NoError(t, err)
		assert.True(t, res.Allowed)

		res, err = store.AddRequest(ctx, tokenKey, 0, 1, 10, 15)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
	})

	t.Run("blocks key when limit exceeded and unblocks cleanly when window exceeds block duration", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		ctx := context.Background()
		key := *entity.NewLimiterKey("ip", "192.168.1.20")

		// Use window (30s) longer than block duration (5s) to ensure counter cleanup prevents re-block loop.
		for i := 0; i < 2; i++ {
			res, err := store.AddRequest(ctx, key, 0, 2, 30, 5)
			require.NoError(t, err)
			assert.True(t, res.Allowed)
		}

		// 3rd request exceeds limit and triggers block.
		res, err := store.AddRequest(ctx, key, 0, 2, 30, 5)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(5), res.ResetSeconds)
		assert.Equal(t, 5, res.RetryAfterSeconds)
		assert.True(t, mr.Exists(blockedKey(key)))
		assert.False(t, mr.Exists(countKey(key)))

		// Still blocked with sub-second remaining time (4.5s elapsed -> 500ms left -> RetryAfter=1s).
		mr.FastForward(4500 * time.Millisecond)
		res, err = store.AddRequest(ctx, key, 0, 2, 30, 5)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, 1, res.RetryAfterSeconds)
		assert.Equal(t, int64(1), res.ResetSeconds)

		// After block expires, requests are allowed again with a fresh window.
		mr.FastForward(1 * time.Second)
		res, err = store.AddRequest(ctx, key, 0, 2, 30, 5)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 1, res.Remaining)
	})

	t.Run("keeps key blocked beyond time window when block duration exceeds window", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		ctx := context.Background()
		key := *entity.NewLimiterKey("ip", "192.168.1.25")

		// window=5s, block=15s
		res, err := store.AddRequest(ctx, key, 0, 1, 5, 15)
		require.NoError(t, err)
		assert.True(t, res.Allowed)

		res, err = store.AddRequest(ctx, key, 0, 1, 5, 15)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 15, res.RetryAfterSeconds)

		// Past the 5s window, key must remain blocked (~9s left).
		mr.FastForward(6 * time.Second)
		res, err = store.AddRequest(ctx, key, 0, 1, 5, 15)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 9, res.RetryAfterSeconds)
		assert.Equal(t, int64(9), res.ResetSeconds)

		// Past the 15s block duration, key is unblocked.
		mr.FastForward(10 * time.Second)
		res, err = store.AddRequest(ctx, key, 0, 1, 5, 15)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
	})

	t.Run("rejects for remaining window without block key when blockedDuration is zero", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		ctx := context.Background()
		key := *entity.NewLimiterKey("token", "tok-no-block")

		res, err := store.AddRequest(ctx, key, 0, 1, 5, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)

		res, err = store.AddRequest(ctx, key, 0, 1, 5, 0)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.GreaterOrEqual(t, res.RetryAfterSeconds, 1)
		assert.LessOrEqual(t, res.RetryAfterSeconds, 5)
		assert.False(t, mr.Exists(blockedKey(key)))

		mr.FastForward(6 * time.Second)
		res, err = store.AddRequest(ctx, key, 0, 1, 5, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
	})

	t.Run("heals orphan counter key without TTL", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		ctx := context.Background()
		key := *entity.NewLimiterKey("ip", "10.0.0.1")

		require.NoError(t, mr.Set(countKey(key), "1"))
		assert.Equal(t, time.Duration(0), mr.TTL(countKey(key)))

		res, err := store.AddRequest(ctx, key, 0, 5, 10, 5)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 3, res.Remaining)
		assert.Equal(t, int64(10), res.ResetSeconds)
		assert.Greater(t, mr.TTL(countKey(key)), time.Duration(0))
	})

	t.Run("blocks and cleans up orphan counter key already at maxRequests", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		ctx := context.Background()
		key := *entity.NewLimiterKey("ip", "10.0.0.4")

		require.NoError(t, mr.Set(countKey(key), "5"))
		assert.Equal(t, time.Duration(0), mr.TTL(countKey(key)))

		res, err := store.AddRequest(ctx, key, 0, 5, 10, 8)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(8), res.ResetSeconds)
		assert.Equal(t, 8, res.RetryAfterSeconds)
		assert.True(t, mr.Exists(blockedKey(key)))
		assert.False(t, mr.Exists(countKey(key)))
	})

	t.Run("heals orphan blocked key without TTL when blockedDuration is positive", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		ctx := context.Background()
		key := *entity.NewLimiterKey("ip", "10.0.0.2")

		require.NoError(t, mr.Set(blockedKey(key), "1"))
		assert.Equal(t, time.Duration(0), mr.TTL(blockedKey(key)))

		res, err := store.AddRequest(ctx, key, 0, 5, 10, 8)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(8), res.ResetSeconds)
		assert.Equal(t, 8, res.RetryAfterSeconds)
		assert.Greater(t, mr.TTL(blockedKey(key)), time.Duration(0))
	})

	t.Run("removes stale orphan blocked key when blockedDuration is zero", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		ctx := context.Background()
		key := *entity.NewLimiterKey("ip", "10.0.0.3")

		require.NoError(t, mr.Set(blockedKey(key), "1"))
		assert.Equal(t, time.Duration(0), mr.TTL(blockedKey(key)))

		res, err := store.AddRequest(ctx, key, 0, 5, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 4, res.Remaining)
		assert.False(t, mr.Exists(blockedKey(key)))
	})

	t.Run("returns validation errors on invalid arguments", func(t *testing.T) {
		_, _, store := newTestStore(t)
		ctx := context.Background()
		validKey := *entity.NewLimiterKey("ip", "127.0.0.1")

		cases := []struct {
			name            string
			key             entity.LimiterKey
			maxRequests     int
			timeWindowSec   int
			blockedDuration int
		}{
			{name: "empty prefix", key: entity.LimiterKey{Prefix: "", Value: "127.0.0.1"}, maxRequests: 5, timeWindowSec: 10, blockedDuration: 5},
			{name: "whitespace prefix", key: entity.LimiterKey{Prefix: "   ", Value: "127.0.0.1"}, maxRequests: 5, timeWindowSec: 10, blockedDuration: 5},
			{name: "empty value", key: entity.LimiterKey{Prefix: "ip", Value: ""}, maxRequests: 5, timeWindowSec: 10, blockedDuration: 5},
			{name: "whitespace value", key: entity.LimiterKey{Prefix: "ip", Value: "   "}, maxRequests: 5, timeWindowSec: 10, blockedDuration: 5},
			{name: "zero maxRequests", key: validKey, maxRequests: 0, timeWindowSec: 10, blockedDuration: 5},
			{name: "negative maxRequests", key: validKey, maxRequests: -1, timeWindowSec: 10, blockedDuration: 5},
			{name: "zero timeWindowSec", key: validKey, maxRequests: 5, timeWindowSec: 0, blockedDuration: 5},
			{name: "negative timeWindowSec", key: validKey, maxRequests: 5, timeWindowSec: -5, blockedDuration: 5},
			{name: "negative blockedDuration", key: validKey, maxRequests: 5, timeWindowSec: 10, blockedDuration: -1},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := store.AddRequest(ctx, tc.key, 0, tc.maxRequests, tc.timeWindowSec, tc.blockedDuration)
				require.Error(t, err)
			})
		}
	})

	t.Run("propagates context cancellation and redis errors", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		key := *entity.NewLimiterKey("ip", "127.0.0.2")

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := store.AddRequest(canceledCtx, key, 0, 5, 10, 5)
		require.ErrorIs(t, err, context.Canceled)

		mr.Close()
		_, err = store.AddRequest(context.Background(), key, 0, 5, 10, 5)
		require.Error(t, err)
	})
}

func TestStore_FixedWindow_Concurrency(t *testing.T) {
	mr, _, store := newTestStore(t)
	ctx := context.Background()
	key := *entity.NewLimiterKey("ip", "192.168.100.1")

	const (
		totalRequests = 25
		maxRequests   = 5
	)

	var (
		wg      sync.WaitGroup
		allowed atomic.Int32
		denied  atomic.Int32
	)
	errCh := make(chan error, totalRequests)

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := store.AddRequest(ctx, key, 0, maxRequests, 10, 5)
			if err != nil {
				errCh <- err
				return
			}
			if res.Allowed {
				allowed.Add(1)
			} else {
				denied.Add(1)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	assert.Equal(t, int32(maxRequests), allowed.Load())
	assert.Equal(t, int32(totalRequests-maxRequests), denied.Load())
	assert.True(t, mr.Exists(blockedKey(key)))
	assert.False(t, mr.Exists(countKey(key)))
}
