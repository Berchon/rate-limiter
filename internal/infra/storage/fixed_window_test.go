package storage

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

func TestInMemoryStore_FixedWindow_AddRequest(t *testing.T) {
	ctx := context.Background()

	t.Run("allows up to maxRequests with sub-second ceiling reset and resets after window", func(t *testing.T) {
		store, clock := newTestStore(t)
		key := *entity.NewLimiterKey("ip", "192.168.1.10")

		res1, err := store.AddRequest(ctx, key, 0, 2, 10, 30)
		require.NoError(t, err)
		assert.True(t, res1.Allowed)
		assert.Equal(t, 1, res1.Remaining)
		assert.Equal(t, int64(10), res1.ResetSeconds)
		assert.Equal(t, 0, res1.RetryAfterSeconds)

		clock.Advance(1500 * time.Millisecond)

		res2, err := store.AddRequest(ctx, key, 0, 2, 10, 30)
		require.NoError(t, err)
		assert.True(t, res2.Allowed)
		assert.Equal(t, 0, res2.Remaining)
		assert.Equal(t, int64(9), res2.ResetSeconds)
		assert.Equal(t, 0, res2.RetryAfterSeconds)

		clock.Advance(8500 * time.Millisecond)

		res3, err := store.AddRequest(ctx, key, 0, 2, 10, 30)
		require.NoError(t, err)
		assert.True(t, res3.Allowed)
		assert.Equal(t, 1, res3.Remaining)
		assert.Equal(t, int64(10), res3.ResetSeconds)
	})

	t.Run("isolates counters and blocks across distinct keys", func(t *testing.T) {
		store, _ := newTestStore(t)
		ipA := *entity.NewLimiterKey("ip", "192.168.1.1")
		ipB := *entity.NewLimiterKey("ip", "192.168.1.2")
		tokenKey := *entity.NewLimiterKey("token", "192.168.1.1")

		res, err := store.AddRequest(ctx, ipA, 0, 1, 10, 15)
		require.NoError(t, err)
		assert.True(t, res.Allowed)

		res, err = store.AddRequest(ctx, ipA, 0, 1, 10, 15)
		require.NoError(t, err)
		assert.False(t, res.Allowed)

		res, err = store.AddRequest(ctx, ipB, 0, 1, 10, 15)
		require.NoError(t, err)
		assert.True(t, res.Allowed)

		res, err = store.AddRequest(ctx, tokenKey, 0, 1, 10, 15)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
	})

	t.Run("keeps key blocked beyond time window when block duration exceeds window", func(t *testing.T) {
		store, clock := newTestStore(t)
		key := *entity.NewLimiterKey("ip", "192.168.1.25")

		res1, err := store.AddRequest(ctx, key, 0, 1, 5, 15)
		require.NoError(t, err)
		assert.True(t, res1.Allowed)

		res2, err := store.AddRequest(ctx, key, 0, 1, 5, 15)
		require.NoError(t, err)
		assert.False(t, res2.Allowed)
		assert.Equal(t, 0, res2.Remaining)
		assert.Equal(t, int64(15), res2.ResetSeconds)
		assert.Equal(t, 15, res2.RetryAfterSeconds)

		// Past the 5s window, key must remain blocked (9s left).
		clock.Advance(6 * time.Second)
		res3, err := store.AddRequest(ctx, key, 0, 1, 5, 15)
		require.NoError(t, err)
		assert.False(t, res3.Allowed)
		assert.Equal(t, int64(9), res3.ResetSeconds)
		assert.Equal(t, 9, res3.RetryAfterSeconds)

		// Past the 15s block, expired block is evicted lazily and request is allowed.
		clock.Advance(9 * time.Second)
		res4, err := store.AddRequest(ctx, key, 0, 1, 5, 15)
		require.NoError(t, err)
		assert.True(t, res4.Allowed)
		assert.Equal(t, 0, res4.Remaining)

		store.mu.Lock()
		_, stillBlocked := store.blocked[key.String()]
		store.mu.Unlock()
		assert.False(t, stillBlocked)
	})

	t.Run("unblocks cleanly when window exceeds block duration and rounds sub-second block to 1s", func(t *testing.T) {
		store, clock := newTestStore(t)
		key := *entity.NewLimiterKey("ip", "192.168.1.20")

		// Window (30s) longer than block duration (5s) verifies counter cleanup prevents re-block loop.
		for range 2 {
			res, err := store.AddRequest(ctx, key, 0, 2, 30, 5)
			require.NoError(t, err)
			assert.True(t, res.Allowed)
		}

		res, err := store.AddRequest(ctx, key, 0, 2, 30, 5)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, int64(5), res.ResetSeconds)
		assert.Equal(t, 5, res.RetryAfterSeconds)

		store.mu.Lock()
		counterStillPresent := store.strategy.hasState(key.String())
		store.mu.Unlock()
		assert.False(t, counterStillPresent)

		// 500ms remaining must ceil to 1s.
		clock.Advance(4500 * time.Millisecond)
		res, err = store.AddRequest(ctx, key, 0, 2, 30, 5)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, int64(1), res.ResetSeconds)
		assert.Equal(t, 1, res.RetryAfterSeconds)

		// After block expires (still inside original 30s window), fresh window must allow requests.
		clock.Advance(500 * time.Millisecond)
		res, err = store.AddRequest(ctx, key, 0, 2, 30, 5)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 1, res.Remaining)
		assert.Equal(t, int64(30), res.ResetSeconds)
	})

	t.Run("rejects without setting block when blockedDuration is zero", func(t *testing.T) {
		store, clock := newTestStore(t)
		noBlockKey := *entity.NewLimiterKey("ip", "10.0.0.1")

		res1, err := store.AddRequest(ctx, noBlockKey, 0, 1, 5, 0)
		require.NoError(t, err)
		assert.True(t, res1.Allowed)
		assert.Equal(t, 0, res1.Remaining)

		clock.Advance(1200 * time.Millisecond)

		res2, err := store.AddRequest(ctx, noBlockKey, 0, 1, 5, 0)
		require.NoError(t, err)
		assert.False(t, res2.Allowed)
		assert.Equal(t, 0, res2.Remaining)
		assert.Equal(t, int64(4), res2.ResetSeconds)
		assert.Equal(t, 4, res2.RetryAfterSeconds)

		status, err := store.IsBlocked(ctx, noBlockKey)
		require.NoError(t, err)
		assert.False(t, status.Blocked)

		clock.Advance(3800 * time.Millisecond)

		res3, err := store.AddRequest(ctx, noBlockKey, 0, 1, 5, 0)
		require.NoError(t, err)
		assert.True(t, res3.Allowed)
	})
}

func TestInMemoryStore_FixedWindow_ConcurrentAccess(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	key := *entity.NewLimiterKey("ip", "203.0.113.5")

	const (
		goroutines  = 50
		maxRequests = 10
	)

	var (
		allowedCount atomic.Int32
		blockedCount atomic.Int32
		wg           sync.WaitGroup
	)

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			res, err := store.AddRequest(ctx, key, 0, maxRequests, 60, 30)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if res.Allowed {
				allowedCount.Add(1)
			} else {
				blockedCount.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(maxRequests), allowedCount.Load())
	assert.Equal(t, int32(goroutines-maxRequests), blockedCount.Load())
}
