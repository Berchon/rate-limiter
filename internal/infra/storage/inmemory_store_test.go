package storage

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestStore(t *testing.T) (*InMemoryStore, *fakeClock) {
	t.Helper()
	clock := newFakeClock(time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC))
	return newInMemoryStoreWithClock(clock.Now), clock
}

func TestNewInMemoryStore(t *testing.T) {
	store := NewInMemoryStore()
	require.NotNil(t, store)

	res, err := store.AddRequest(context.Background(), *entity.NewLimiterKey("ip", "127.0.0.1"), 0, 5, 10, 30)
	require.NoError(t, err)
	assert.True(t, res.Allowed)
	assert.Equal(t, 4, res.Remaining)

	fallbackStore := newInMemoryStoreWithClock(nil)
	require.NotNil(t, fallbackStore)
	assert.False(t, fallbackStore.currentTime().IsZero())
}

func TestInMemoryStore_TokenConfig_LifecycleAndLazyExpiration(t *testing.T) {
	ctx := context.Background()

	t.Run("saves and retrieves using raw or prefixed token without mutating stored copy", func(t *testing.T) {
		store, _ := newTestStore(t)

		cfg := entity.NewTokenConfig("abc123", 10, 5, 30, 60)
		require.NoError(t, store.SaveTokenConfig(ctx, cfg))

		// Mutate caller copy to ensure store kept an isolated copy.
		cfg.MaxRequests = 999

		gotRaw, err := store.GetConfigByToken(ctx, "abc123")
		require.NoError(t, err)
		require.NotNil(t, gotRaw)
		assert.Equal(t, "abc123", gotRaw.Token)
		assert.Equal(t, 5, gotRaw.MaxRequests)
		assert.Equal(t, 60, gotRaw.TokenTTLSeconds)

		// Mutate returned copy and verify subsequent read via prefixed key is unaffected.
		gotRaw.MaxRequests = 123

		gotPrefixed, err := store.GetConfigByToken(ctx, "token:abc123")
		require.NoError(t, err)
		require.NotNil(t, gotPrefixed)
		assert.Equal(t, 5, gotPrefixed.MaxRequests)
	})

	t.Run("normalizes prefixed and whitespace-padded tokens on save and get", func(t *testing.T) {
		store, _ := newTestStore(t)

		cfg := entity.NewTokenConfig("  token:prefixed-1  ", 5, 3, 10, 30)
		require.NoError(t, store.SaveTokenConfig(ctx, cfg))

		gotByRaw, err := store.GetConfigByToken(ctx, "  prefixed-1  ")
		require.NoError(t, err)
		require.NotNil(t, gotByRaw)
		assert.Equal(t, 3, gotByRaw.MaxRequests)

		gotByPrefixed, err := store.GetConfigByToken(ctx, "  token:prefixed-1  ")
		require.NoError(t, err)
		require.NotNil(t, gotByPrefixed)
		assert.Equal(t, 3, gotByPrefixed.MaxRequests)
	})

	t.Run("overwrites existing token config and refreshes TTL", func(t *testing.T) {
		store, clock := newTestStore(t)

		initial := entity.NewTokenConfig("updatable", 10, 5, 30, 5)
		require.NoError(t, store.SaveTokenConfig(ctx, initial))

		clock.Advance(3 * time.Second)

		updated := entity.NewTokenConfig("updatable", 20, 50, 15, 30)
		require.NoError(t, store.SaveTokenConfig(ctx, updated))

		// Advance past original 5s TTL; updated 30s TTL must keep it active.
		clock.Advance(4 * time.Second)

		got, err := store.GetConfigByToken(ctx, "updatable")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, 50, got.MaxRequests)
		assert.Equal(t, 20, got.TimeWindowSeconds)
	})

	t.Run("lazily expires token at TTL boundary without leaking goroutines", func(t *testing.T) {
		store, clock := newTestStore(t)
		beforeGoroutines := runtime.NumGoroutine()

		for i := range 50 {
			cfg := entity.NewTokenConfig(fmt.Sprintf("token:expiring-key-%d", i), 10, 5, 30, 15)
			require.NoError(t, store.SaveTokenConfig(ctx, cfg))
		}

		// Saving 50 tokens must not spawn sleeping goroutines.
		assert.LessOrEqual(t, runtime.NumGoroutine(), beforeGoroutines+2)

		clock.Advance(14 * time.Second)
		got, err := store.GetConfigByToken(ctx, "expiring-key-0")
		require.NoError(t, err)
		require.NotNil(t, got)

		clock.Advance(1 * time.Second)
		got, err = store.GetConfigByToken(ctx, "expiring-key-0")
		require.NoError(t, err)
		assert.Nil(t, got)

		store.mu.Lock()
		_, stillPresent := store.tokenConfigs["token:expiring-key-0"]
		store.mu.Unlock()
		assert.False(t, stillPresent)
	})

	t.Run("returns nil for missing or empty token", func(t *testing.T) {
		store, _ := newTestStore(t)

		for _, token := range []string{"", "   ", "token:", "token:   ", "does-not-exist"} {
			got, err := store.GetConfigByToken(ctx, token)
			require.NoError(t, err)
			assert.Nil(t, got)
		}
	})
}

func TestInMemoryStore_SaveTokenConfig_ValidationErrors(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name string
		cfg  *entity.TokenConfig
	}{
		{name: "nil config", cfg: nil},
		{name: "empty token", cfg: &entity.TokenConfig{Token: "", TokenTTLSeconds: 10}},
		{name: "whitespace token", cfg: &entity.TokenConfig{Token: "   ", TokenTTLSeconds: 10}},
		{name: "prefix-only token", cfg: &entity.TokenConfig{Token: "token:  ", TokenTTLSeconds: 10}},
		{name: "zero TTL", cfg: &entity.TokenConfig{Token: "abc", TokenTTLSeconds: 0}},
		{name: "negative TTL", cfg: &entity.TokenConfig{Token: "abc", TokenTTLSeconds: -5}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := store.SaveTokenConfig(ctx, tc.cfg)
			require.Error(t, err)
		})
	}
}

func TestInMemoryStore_AddRequest_FlowWindowResetAndBlocking(t *testing.T) {
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
		_, counterStillPresent := store.counters[key.String()]
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

func TestInMemoryStore_AddRequest_ValidationErrors(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	validKey := *entity.NewLimiterKey("ip", "127.0.0.1")

	tests := []struct {
		name            string
		key             entity.LimiterKey
		maxRequests     int
		timeWindowSec   int
		blockedDuration int
	}{
		{name: "empty prefix", key: entity.LimiterKey{Prefix: "", Value: "127.0.0.1"}, maxRequests: 1, timeWindowSec: 1, blockedDuration: 1},
		{name: "empty value", key: entity.LimiterKey{Prefix: "ip", Value: "  "}, maxRequests: 1, timeWindowSec: 1, blockedDuration: 1},
		{name: "zero maxRequests", key: validKey, maxRequests: 0, timeWindowSec: 1, blockedDuration: 1},
		{name: "negative maxRequests", key: validKey, maxRequests: -1, timeWindowSec: 1, blockedDuration: 1},
		{name: "zero timeWindowSec", key: validKey, maxRequests: 1, timeWindowSec: 0, blockedDuration: 1},
		{name: "negative timeWindowSec", key: validKey, maxRequests: 1, timeWindowSec: -2, blockedDuration: 1},
		{name: "negative blockedDuration", key: validKey, maxRequests: 1, timeWindowSec: 1, blockedDuration: -1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.AddRequest(ctx, tc.key, 0, tc.maxRequests, tc.timeWindowSec, tc.blockedDuration)
			require.Error(t, err)
		})
	}
}

func TestInMemoryStore_BlockAndIsBlocked(t *testing.T) {
	store, clock := newTestStore(t)
	ctx := context.Background()
	key := *entity.NewLimiterKey("ip", "192.168.0.2")

	status, err := store.IsBlocked(ctx, key)
	require.NoError(t, err)
	assert.False(t, status.Blocked)
	assert.Equal(t, 0, status.RemainingSeconds)

	require.NoError(t, store.Block(ctx, key, 20))

	clock.Advance(3400 * time.Millisecond)
	status, err = store.IsBlocked(ctx, key)
	require.NoError(t, err)
	assert.True(t, status.Blocked)
	assert.Equal(t, 17, status.RemainingSeconds)

	// Sub-second remaining (500ms) must ceil to 1s.
	clock.Advance(16100 * time.Millisecond)
	status, err = store.IsBlocked(ctx, key)
	require.NoError(t, err)
	assert.True(t, status.Blocked)
	assert.Equal(t, 1, status.RemainingSeconds)

	// Exact expiration must lazily evict entry from s.blocked.
	clock.Advance(500 * time.Millisecond)
	status, err = store.IsBlocked(ctx, key)
	require.NoError(t, err)
	assert.False(t, status.Blocked)
	assert.Equal(t, 0, status.RemainingSeconds)

	store.mu.Lock()
	_, stillPresent := store.blocked[key.String()]
	store.mu.Unlock()
	assert.False(t, stillPresent)

	// Validation errors.
	require.Error(t, store.Block(ctx, entity.LimiterKey{}, 10))
	require.Error(t, store.Block(ctx, key, 0))
	require.Error(t, store.Block(ctx, key, -5))
	_, err = store.IsBlocked(ctx, entity.LimiterKey{Prefix: "ip", Value: ""})
	require.Error(t, err)
}

func TestInMemoryStore_ResetKey(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	tokenKey := *entity.NewLimiterKey("token", "my-api-token")

	cfg := entity.NewTokenConfig("my-api-token", 10, 5, 30, 60)
	require.NoError(t, store.SaveTokenConfig(ctx, cfg))

	_, err := store.AddRequest(ctx, tokenKey, 0, 5, 10, 30)
	require.NoError(t, err)
	require.NoError(t, store.Block(ctx, tokenKey, 30))

	require.NoError(t, store.ResetKey(ctx, tokenKey))

	gotCfg, err := store.GetConfigByToken(ctx, "my-api-token")
	require.NoError(t, err)
	assert.Nil(t, gotCfg)

	status, err := store.IsBlocked(ctx, tokenKey)
	require.NoError(t, err)
	assert.False(t, status.Blocked)

	res, err := store.AddRequest(ctx, tokenKey, 0, 5, 10, 30)
	require.NoError(t, err)
	assert.True(t, res.Allowed)
	assert.Equal(t, 4, res.Remaining)

	require.Error(t, store.ResetKey(ctx, entity.LimiterKey{Prefix: "", Value: "x"}))
}

func TestInMemoryStore_ContextCancellation(t *testing.T) {
	store, _ := newTestStore(t)
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	key := *entity.NewLimiterKey("ip", "127.0.0.1")
	cfg := entity.NewTokenConfig("tok", 10, 5, 30, 60)

	assert.ErrorIs(t, store.SaveTokenConfig(canceledCtx, cfg), context.Canceled)

	_, err := store.GetConfigByToken(canceledCtx, "tok")
	assert.ErrorIs(t, err, context.Canceled)

	_, err = store.AddRequest(canceledCtx, key, 0, 5, 10, 30)
	assert.ErrorIs(t, err, context.Canceled)

	assert.ErrorIs(t, store.Block(canceledCtx, key, 10), context.Canceled)

	_, err = store.IsBlocked(canceledCtx, key)
	assert.ErrorIs(t, err, context.Canceled)

	assert.ErrorIs(t, store.ResetKey(canceledCtx, key), context.Canceled)
}

func TestInMemoryStore_DurationToSecondsFloor(t *testing.T) {
	assert.Equal(t, 1, durationToSeconds(0))
	assert.Equal(t, 1, durationToSeconds(-time.Second))
	assert.Equal(t, 1, durationToSeconds(50*time.Millisecond))
	assert.Equal(t, 2, durationToSeconds(1001*time.Millisecond))
}

func TestInMemoryStore_ConcurrentAccess(t *testing.T) {
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

	// Mixed concurrent token and block operations to stress mutex protection under -race.
	tokenKey := *entity.NewLimiterKey("token", "concurrent-token")
	cfg := entity.NewTokenConfig("concurrent-token", 10, 5, 30, 60)

	var mixedWG sync.WaitGroup
	mixedWG.Add(30)
	for i := range 30 {
		go func(idx int) {
			defer mixedWG.Done()
			switch idx % 5 {
			case 0:
				_ = store.SaveTokenConfig(ctx, cfg)
			case 1:
				_, _ = store.GetConfigByToken(ctx, "concurrent-token")
			case 2:
				_ = store.Block(ctx, tokenKey, 15)
			case 3:
				_, _ = store.IsBlocked(ctx, tokenKey)
			case 4:
				_ = store.ResetKey(ctx, tokenKey)
			}
		}(i)
	}
	mixedWG.Wait()
}
