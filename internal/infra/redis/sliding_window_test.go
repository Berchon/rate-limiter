package redis

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/service"
	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSlidingTestStore(t *testing.T) (*miniredis.Miniredis, *goredis.Client, *Store) {
	t.Helper()

	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{
		Addr: mr.Addr(),
	})
	t.Cleanup(func() {
		_ = client.Close()
	})

	store, err := NewStoreWithAlgorithm(client, service.AlgorithmSlidingWindow)
	require.NoError(t, err)
	return mr, client, store
}

func TestStore_SlidingWindow_AddRequest(t *testing.T) {
	ctx := context.Background()
	baseMs := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC).UnixMilli()

	t.Run("prevents boundary bursts across rolling window", func(t *testing.T) {
		_, _, store := newSlidingTestStore(t)
		key := *entity.NewLimiterKey("ip", "192.168.10.1")

		// Window = 10s, max = 2, no explicit block.
		res, err := store.AddRequest(ctx, key, baseMs, 2, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 1, res.Remaining)
		assert.Equal(t, int64(10), res.ResetSeconds)
		assert.Equal(t, 0, res.RetryAfterSeconds)

		// Second request near the end of the first window (t = +9.0s).
		res, err = store.AddRequest(ctx, key, baseMs+9000, 2, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(1), res.ResetSeconds)

		// Third request at t = +9.5s is rejected; oldest (t = 0) expires in 500ms -> 1s.
		res, err = store.AddRequest(ctx, key, baseMs+9500, 2, 10, 0)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(1), res.ResetSeconds)
		assert.Equal(t, 1, res.RetryAfterSeconds)

		// At t = +10.0s, request from t = 0 slides out, leaving request from t = +9.0s.
		res, err = store.AddRequest(ctx, key, baseMs+10000, 2, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(9), res.ResetSeconds)

		// At t = +10.5s, both t = +9.0s and t = +10.0s are inside the rolling window -> denied.
		res, err = store.AddRequest(ctx, key, baseMs+10500, 2, 10, 0)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(9), res.ResetSeconds)
		assert.Equal(t, 9, res.RetryAfterSeconds)

		// At t = +19.0s, request from t = +9.0s slides out -> allowed again.
		res, err = store.AddRequest(ctx, key, baseMs+19000, 2, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(1), res.ResetSeconds)
	})

	t.Run("blocks key when limit exceeded and clears log key to avoid re-block loop", func(t *testing.T) {
		mr, _, store := newSlidingTestStore(t)
		key := *entity.NewLimiterKey("ip", "192.168.10.2")

		// Window (30s) longer than block (5s).
		for i := range 2 {
			res, err := store.AddRequest(ctx, key, baseMs+int64(i*100), 2, 30, 5)
			require.NoError(t, err)
			assert.True(t, res.Allowed)
		}

		res, err := store.AddRequest(ctx, key, baseMs+200, 2, 30, 5)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(5), res.ResetSeconds)
		assert.Equal(t, 5, res.RetryAfterSeconds)
		assert.True(t, mr.Exists(blockedKey(key)))
		assert.False(t, mr.Exists(logKey(key)))

		// Still blocked after 4.5s -> 500ms left -> 1s retry-after.
		mr.FastForward(4500 * time.Millisecond)
		res, err = store.AddRequest(ctx, key, baseMs+4700, 2, 30, 5)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 1, res.RetryAfterSeconds)

		// After block expires (still inside original 30s window), key starts fresh.
		mr.FastForward(1 * time.Second)
		res, err = store.AddRequest(ctx, key, baseMs+5700, 2, 30, 5)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 1, res.Remaining)
	})

	t.Run("isolates logs and blocks across distinct keys", func(t *testing.T) {
		_, _, store := newSlidingTestStore(t)
		ipA := *entity.NewLimiterKey("ip", "192.168.10.11")
		ipB := *entity.NewLimiterKey("ip", "192.168.10.12")
		tokenKey := *entity.NewLimiterKey("token", "192.168.10.11")

		res, err := store.AddRequest(ctx, ipA, baseMs, 1, 10, 15)
		require.NoError(t, err)
		assert.True(t, res.Allowed)

		res, err = store.AddRequest(ctx, ipA, baseMs+100, 1, 10, 15)
		require.NoError(t, err)
		assert.False(t, res.Allowed)

		res, err = store.AddRequest(ctx, ipB, baseMs+100, 1, 10, 15)
		require.NoError(t, err)
		assert.True(t, res.Allowed)

		res, err = store.AddRequest(ctx, tokenKey, baseMs+100, 1, 10, 15)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
	})

	t.Run("keeps key blocked beyond time window when block duration exceeds window", func(t *testing.T) {
		mr, _, store := newSlidingTestStore(t)
		key := *entity.NewLimiterKey("ip", "192.168.10.13")

		// window=5s, block=15s
		res, err := store.AddRequest(ctx, key, baseMs, 1, 5, 15)
		require.NoError(t, err)
		assert.True(t, res.Allowed)

		res, err = store.AddRequest(ctx, key, baseMs+100, 1, 5, 15)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 15, res.RetryAfterSeconds)

		// Past the 5s window, key must remain blocked (~9s left).
		mr.FastForward(6 * time.Second)
		res, err = store.AddRequest(ctx, key, baseMs+6100, 1, 5, 15)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, 9, res.RetryAfterSeconds)
		assert.Equal(t, int64(9), res.ResetSeconds)

		// Past the 15s block duration, key is unblocked.
		mr.FastForward(10 * time.Second)
		res, err = store.AddRequest(ctx, key, baseMs+16100, 1, 5, 15)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
	})

	t.Run("rejected requests with zero blockedDuration do not pollute the sliding window", func(t *testing.T) {
		_, _, store := newSlidingTestStore(t)
		key := *entity.NewLimiterKey("ip", "192.168.10.14")

		// Window=10s, max=1, blockedDuration=0.
		res, err := store.AddRequest(ctx, key, baseMs, 1, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)

		// Repeated retries must be denied without extending the window.
		for sec := int64(5); sec <= 9; sec++ {
			res, err = store.AddRequest(ctx, key, baseMs+sec*1000, 1, 10, 0)
			require.NoError(t, err)
			assert.False(t, res.Allowed)
			assert.Equal(t, int(10-sec), res.RetryAfterSeconds)
		}

		// Once the single allowed request at t=0 ages out at t=10s, the next request succeeds.
		res, err = store.AddRequest(ctx, key, baseMs+10000, 1, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)
		assert.Equal(t, int64(10), res.ResetSeconds)
	})

	t.Run("resets quota completely after all entries age out", func(t *testing.T) {
		_, _, store := newSlidingTestStore(t)
		key := *entity.NewLimiterKey("ip", "192.168.10.15")

		res, err := store.AddRequest(ctx, key, baseMs, 2, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 1, res.Remaining)

		res, err = store.AddRequest(ctx, key, baseMs+2000, 2, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 0, res.Remaining)

		// At t=+15s, both entries have expired from the 10s window.
		res, err = store.AddRequest(ctx, key, baseMs+15000, 2, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 1, res.Remaining)
		assert.Equal(t, int64(10), res.ResetSeconds)
	})

	t.Run("heals orphan log and blocked keys without TTL", func(t *testing.T) {
		mr, _, store := newSlidingTestStore(t)

		t.Run("orphan log key below capacity is healed with PEXPIRE", func(t *testing.T) {
			key := *entity.NewLimiterKey("ip", "10.20.0.10")
			_, err := mr.ZAdd(logKey(key), float64(baseMs), "orphan-member")
			require.NoError(t, err)
			assert.Equal(t, time.Duration(0), mr.TTL(logKey(key)))

			res, err := store.AddRequest(ctx, key, baseMs+1000, 3, 10, 5)
			require.NoError(t, err)
			assert.True(t, res.Allowed)
			assert.Equal(t, 1, res.Remaining)
			assert.Greater(t, mr.TTL(logKey(key)), time.Duration(0))
		})

		t.Run("orphan log key at capacity with positive blockedDuration is deleted on block", func(t *testing.T) {
			key := *entity.NewLimiterKey("ip", "10.20.0.11")
			_, err := mr.ZAdd(logKey(key), float64(baseMs), "orphan-member")
			require.NoError(t, err)
			assert.Equal(t, time.Duration(0), mr.TTL(logKey(key)))

			res, err := store.AddRequest(ctx, key, baseMs+1000, 1, 10, 8)
			require.NoError(t, err)
			assert.False(t, res.Allowed)
			assert.Equal(t, 8, res.RetryAfterSeconds)
			assert.True(t, mr.Exists(blockedKey(key)))
			assert.False(t, mr.Exists(logKey(key)))
		})

		t.Run("orphan log key at capacity with zero blockedDuration", func(t *testing.T) {
			key := *entity.NewLimiterKey("ip", "10.20.0.1")
			_, err := mr.ZAdd(logKey(key), float64(baseMs), "orphan-member")
			require.NoError(t, err)
			assert.Equal(t, time.Duration(0), mr.TTL(logKey(key)))

			res, err := store.AddRequest(ctx, key, baseMs+1000, 1, 10, 0)
			require.NoError(t, err)
			assert.False(t, res.Allowed)
			assert.Equal(t, 9, res.RetryAfterSeconds)
			assert.Greater(t, mr.TTL(logKey(key)), time.Duration(0))
		})

		t.Run("orphan blocked key healed when blockedDuration is positive", func(t *testing.T) {
			key := *entity.NewLimiterKey("ip", "10.20.0.2")
			require.NoError(t, mr.Set(blockedKey(key), "1"))

			res, err := store.AddRequest(ctx, key, baseMs, 5, 10, 8)
			require.NoError(t, err)
			assert.False(t, res.Allowed)
			assert.Equal(t, 8, res.RetryAfterSeconds)
			assert.Greater(t, mr.TTL(blockedKey(key)), time.Duration(0))
		})

		t.Run("orphan blocked key removed when blockedDuration is zero", func(t *testing.T) {
			key := *entity.NewLimiterKey("ip", "10.20.0.3")
			require.NoError(t, mr.Set(blockedKey(key), "1"))

			res, err := store.AddRequest(ctx, key, baseMs, 5, 10, 0)
			require.NoError(t, err)
			assert.True(t, res.Allowed)
			assert.Equal(t, 4, res.Remaining)
			assert.False(t, mr.Exists(blockedKey(key)))
		})
	})

	t.Run("falls back to current wall clock when timestamp is non-positive", func(t *testing.T) {
		_, _, store := newSlidingTestStore(t)
		key := *entity.NewLimiterKey("ip", "10.20.0.4")

		res, err := store.AddRequest(ctx, key, 0, 2, 10, 5)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 1, res.Remaining)
		assert.Equal(t, int64(10), res.ResetSeconds)
	})

	t.Run("clamps reset seconds to timeWindowSec under clock skew", func(t *testing.T) {
		_, _, store := newSlidingTestStore(t)
		key := *entity.NewLimiterKey("ip", "10.20.0.5")

		// First request fills capacity (maxRequests=1) with a timestamp ahead of the second request,
		// so the rejected second request exercises the remainingMs > windowMs clamp in Lua.
		res, err := store.AddRequest(ctx, key, baseMs+5000, 1, 10, 0)
		require.NoError(t, err)
		assert.True(t, res.Allowed)

		res, err = store.AddRequest(ctx, key, baseMs+1000, 1, 10, 0)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, int64(10), res.ResetSeconds)
		assert.Equal(t, 10, res.RetryAfterSeconds)
	})

	t.Run("ResetKey deletes sliding window log, blocked, and token config keys", func(t *testing.T) {
		mr, _, store := newSlidingTestStore(t)
		key := *entity.NewLimiterKey("token", "sliding-reset")

		cfg := entity.NewTokenConfig("sliding-reset", 10, 5, 10, 60)
		require.NoError(t, store.SaveTokenConfig(ctx, cfg))

		_, err := store.AddRequest(ctx, key, baseMs, 5, 10, 10)
		require.NoError(t, err)
		require.NoError(t, store.Block(ctx, key, 10))

		require.True(t, mr.Exists(key.String()))
		require.True(t, mr.Exists(logKey(key)))
		require.True(t, mr.Exists(blockedKey(key)))

		require.NoError(t, store.ResetKey(ctx, key))

		assert.False(t, mr.Exists(key.String()))
		assert.False(t, mr.Exists(logKey(key)))
		assert.False(t, mr.Exists(blockedKey(key)))
	})

	t.Run("propagates context cancellation and redis errors", func(t *testing.T) {
		mr, _, store := newSlidingTestStore(t)
		key := *entity.NewLimiterKey("ip", "10.20.0.6")

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := store.AddRequest(canceledCtx, key, baseMs, 5, 10, 5)
		require.ErrorIs(t, err, context.Canceled)

		mr.Close()
		_, err = store.AddRequest(context.Background(), key, baseMs, 5, 10, 5)
		require.Error(t, err)
	})
}

func TestStore_SlidingWindow_Concurrency(t *testing.T) {
	mr, _, store := newSlidingTestStore(t)
	ctx := context.Background()
	key := *entity.NewLimiterKey("ip", "192.168.100.2")
	sameTimestampMs := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC).UnixMilli()

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

	// All goroutines share the exact same millisecond timestamp to verify unique ZSET member generation.
	for range totalRequests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := store.AddRequest(ctx, key, sameTimestampMs, maxRequests, 10, 5)
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
	assert.False(t, mr.Exists(logKey(key)))
}
