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

func newSlidingTestStore(t *testing.T) (*InMemoryStore, *fakeClock) {
	t.Helper()
	clock := newFakeClock(time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC))
	return newInMemoryStore(newSlidingWindowStrategy(), clock.Now), clock
}

func TestInMemoryStore_SlidingWindow_AddRequest(t *testing.T) {
	ctx := context.Background()

	t.Run("prevents boundary bursts across rolling window", func(t *testing.T) {
		store, clock := newSlidingTestStore(t)
		key := *entity.NewLimiterKey("ip", "192.168.10.1")

		// Window = 10s, max = 2, no explicit block.
		res, err := store.AddRequest(ctx, key, 0, 2, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 1, res.Remaining)
		assert.Equal(t, int64(10), res.ResetSeconds)
		assert.Equal(t, 0, res.RetryAfterSeconds)

		// Second request at t = +9.0s.
		clock.Advance(9 * time.Second)
		res, err = store.AddRequest(ctx, key, 0, 2, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(1), res.ResetSeconds)

		// Third request at t = +9.5s is rejected; oldest (t = 0) expires in 500ms -> 1s.
		clock.Advance(500 * time.Millisecond)
		res, err = store.AddRequest(ctx, key, 0, 2, 10, 0)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(1), res.ResetSeconds)
		assert.Equal(t, 1, res.RetryAfterSeconds)

		// At t = +10.0s, request from t = 0 slides out while t = +9.0s remains active.
		clock.Advance(500 * time.Millisecond)
		res, err = store.AddRequest(ctx, key, 0, 2, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(9), res.ResetSeconds)

		// At t = +10.5s, both t = +9.0s and t = +10.0s are inside the rolling window -> denied.
		clock.Advance(500 * time.Millisecond)
		res, err = store.AddRequest(ctx, key, 0, 2, 10, 0)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(9), res.ResetSeconds)
		assert.Equal(t, 9, res.RetryAfterSeconds)

		// At t = +19.0s, request from t = +9.0s slides out -> allowed again.
		clock.Advance(8500 * time.Millisecond)
		res, err = store.AddRequest(ctx, key, 0, 2, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(1), res.ResetSeconds)
	})

	t.Run("blocks key when limit exceeded and clears log state to avoid re-block loop", func(t *testing.T) {
		store, clock := newSlidingTestStore(t)
		key := *entity.NewLimiterKey("ip", "192.168.10.2")

		// Window (30s) longer than block (5s).
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
		hasLog := store.strategy.hasState(key.String())
		store.mu.Unlock()
		assert.False(t, hasLog)

		// 500ms left must ceil to 1s.
		clock.Advance(4500 * time.Millisecond)
		res, err = store.AddRequest(ctx, key, 0, 2, 30, 5)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 1, res.RetryAfterSeconds)

		// After block expires, key starts fresh without re-blocking.
		clock.Advance(500 * time.Millisecond)
		res, err = store.AddRequest(ctx, key, 0, 2, 30, 5)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 1, res.Remaining)
		assert.Equal(t, int64(30), res.ResetSeconds)
	})

	t.Run("isolates logs and blocks across distinct keys", func(t *testing.T) {
		store, _ := newSlidingTestStore(t)
		ipA := *entity.NewLimiterKey("ip", "192.168.10.11")
		ipB := *entity.NewLimiterKey("ip", "192.168.10.12")
		tokenKey := *entity.NewLimiterKey("token", "192.168.10.11")

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
		store, clock := newSlidingTestStore(t)
		key := *entity.NewLimiterKey("ip", "192.168.10.13")

		// window=5s, block=15s
		res, err := store.AddRequest(ctx, key, 0, 1, 5, 15)
		require.NoError(t, err)
		assert.True(t, res.Allowed)

		res, err = store.AddRequest(ctx, key, 0, 1, 5, 15)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 15, res.RetryAfterSeconds)

		// Past the 5s window, key must remain blocked (9s left).
		clock.Advance(6 * time.Second)
		res, err = store.AddRequest(ctx, key, 0, 1, 5, 15)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 9, res.RetryAfterSeconds)
		assert.Equal(t, int64(9), res.ResetSeconds)

		// Past the 15s block duration, key is unblocked.
		clock.Advance(9 * time.Second)
		res, err = store.AddRequest(ctx, key, 0, 1, 5, 15)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
	})

	t.Run("rejected requests with zero blockedDuration do not pollute the sliding window", func(t *testing.T) {
		store, clock := newSlidingTestStore(t)
		key := *entity.NewLimiterKey("ip", "192.168.10.14")

		// Window=10s, max=1, blockedDuration=0.
		res, err := store.AddRequest(ctx, key, 0, 1, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)

		// Repeated retries must be denied without extending the window.
		clock.Advance(5 * time.Second)
		for expectedRetry := 5; expectedRetry >= 1; expectedRetry-- {
			res, err = store.AddRequest(ctx, key, 0, 1, 10, 0)
			require.NoError(t, err)
			assert.False(t, res.Allowed)
			assert.Equal(t, expectedRetry, res.RetryAfterSeconds)
			clock.Advance(1 * time.Second)
		}

		// At t=10s, the single allowed request at t=0 has aged out so the next request succeeds.
		res, err = store.AddRequest(ctx, key, 0, 1, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(10), res.ResetSeconds)
	})

	t.Run("resets quota and compacts slice after all entries age out", func(t *testing.T) {
		store, clock := newSlidingTestStore(t)
		key := *entity.NewLimiterKey("ip", "192.168.10.15")

		res, err := store.AddRequest(ctx, key, 0, 2, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)

		clock.Advance(2 * time.Second)
		res, err = store.AddRequest(ctx, key, 0, 2, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)

		// Advance past the 10s window so all prior entries are pruned.
		clock.Advance(13 * time.Second)
		res, err = store.AddRequest(ctx, key, 0, 2, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 1, res.Remaining)
		assert.Equal(t, int64(10), res.ResetSeconds)

		sw, ok := store.strategy.(*slidingWindowStrategy)
		require.True(t, ok)
		store.mu.Lock()
		logLen := len(sw.logs[key.String()])
		store.mu.Unlock()
		assert.Equal(t, 1, logLen)
	})

	t.Run("ResetKey clears sliding window log, block state, and token config", func(t *testing.T) {
		store, _ := newSlidingTestStore(t)
		key := *entity.NewLimiterKey("token", "sliding-reset")

		cfg := entity.NewTokenConfig("sliding-reset", 10, 5, 30, 60)
		require.NoError(t, store.SaveTokenConfig(ctx, cfg))

		_, err := store.AddRequest(ctx, key, 0, 5, 10, 30)
		require.NoError(t, err)
		require.NoError(t, store.Block(ctx, key, 30))

		store.mu.Lock()
		assert.True(t, store.strategy.hasState(key.String()))
		store.mu.Unlock()

		require.NoError(t, store.ResetKey(ctx, key))

		store.mu.Lock()
		assert.False(t, store.strategy.hasState(key.String()))
		store.mu.Unlock()

		res, err := store.AddRequest(ctx, key, 0, 5, 10, 30)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 4, res.Remaining)
	})

	t.Run("clamps reset seconds to window when oldest entry is ahead of now", func(t *testing.T) {
		now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
		oldest := now.Add(5 * time.Second)

		assert.Equal(t, int64(10), slidingResetSeconds(oldest, now, 10*time.Second))
	})
}

func TestInMemoryStore_SlidingWindow_ConcurrentAccess(t *testing.T) {
	store, _ := newSlidingTestStore(t)
	ctx := context.Background()
	key := *entity.NewLimiterKey("ip", "203.0.113.15")

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
