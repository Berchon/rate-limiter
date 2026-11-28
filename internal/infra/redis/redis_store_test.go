package redis

import (
	"context"
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/service"
	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) (*miniredis.Miniredis, *goredis.Client, *Store) {
	t.Helper()

	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{
		Addr: mr.Addr(),
	})
	t.Cleanup(func() {
		_ = client.Close()
	})

	return mr, client, NewStore(client)
}

func TestStore_NilClientAndReceiver(t *testing.T) {
	ctx := context.Background()
	key := *entity.NewLimiterKey("ip", "127.0.0.1")
	cfg := entity.NewTokenConfig("tok", 10, 5, 10, 60)

	cases := []struct {
		name  string
		store *Store
	}{
		{name: "nil client", store: NewStore(nil)},
		{name: "nil store receiver", store: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, tc.store.SaveTokenConfig(ctx, cfg), "redis client cannot be nil")
			_, err := tc.store.GetConfigByToken(ctx, "tok")
			require.ErrorContains(t, err, "redis client cannot be nil")
			_, err = tc.store.AddRequest(ctx, key, 0, 5, 10, 5)
			require.ErrorContains(t, err, "redis client cannot be nil")
			require.ErrorContains(t, tc.store.Block(ctx, key, 5), "redis client cannot be nil")
			_, err = tc.store.IsBlocked(ctx, key)
			require.ErrorContains(t, err, "redis client cannot be nil")
			require.ErrorContains(t, tc.store.ResetKey(ctx, key), "redis client cannot be nil")
		})
	}
}

func TestParseAddRequestResult(t *testing.T) {
	key := *entity.NewLimiterKey("ip", "127.0.0.1")

	cases := []struct {
		name      string
		raw       []int64
		want      service.AddRequestResult
		wantError bool
	}{
		{
			name: "allowed result",
			raw:  []int64{1, 4, 10, 0},
			want: service.AddRequestResult{Allowed: true, Remaining: 4, ResetSeconds: 10, RetryAfterSeconds: 0},
		},
		{
			name: "denied result",
			raw:  []int64{0, 0, 15, 15},
			want: service.AddRequestResult{Allowed: false, Remaining: 0, ResetSeconds: 15, RetryAfterSeconds: 15},
		},
		{
			name:      "empty slice",
			raw:       []int64{},
			wantError: true,
		},
		{
			name:      "too few elements",
			raw:       []int64{1, 2, 3},
			wantError: true,
		},
		{
			name:      "too many elements",
			raw:       []int64{1, 2, 3, 4, 5},
			wantError: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAddRequestResult(key, tc.raw)
			if tc.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unexpected result length")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestStore_SaveAndGetTokenConfig(t *testing.T) {
	t.Run("saves and retrieves token with raw ID", func(t *testing.T) {
		_, _, store := newTestStore(t)
		ctx := context.Background()

		cfg := entity.NewTokenConfig("abc123", 10, 5, 30, 60)
		require.NoError(t, store.SaveTokenConfig(ctx, cfg))

		got, err := store.GetConfigByToken(ctx, "abc123")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, cfg.Token, got.Token)
		assert.Equal(t, cfg.MaxRequests, got.MaxRequests)
		assert.Equal(t, cfg.TimeWindowSeconds, got.TimeWindowSeconds)
		assert.Equal(t, cfg.BlockedDurationSeconds, got.BlockedDurationSeconds)
		assert.Equal(t, cfg.TokenTTLSeconds, got.TokenTTLSeconds)
	})

	t.Run("overwrites existing token config and refreshes TTL", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		ctx := context.Background()

		initial := entity.NewTokenConfig("updatable", 10, 5, 30, 5)
		require.NoError(t, store.SaveTokenConfig(ctx, initial))

		mr.FastForward(3 * time.Second)

		updated := entity.NewTokenConfig("updatable", 20, 50, 15, 30)
		require.NoError(t, store.SaveTokenConfig(ctx, updated))

		mr.FastForward(4 * time.Second)

		got, err := store.GetConfigByToken(ctx, "updatable")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, 50, got.MaxRequests)
		assert.Equal(t, 20, got.TimeWindowSeconds)
	})

	t.Run("normalizes prefixed and whitespace-padded tokens on save and get", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		ctx := context.Background()

		cfg := entity.NewTokenConfig("  token:prefixed-1  ", 5, 3, 10, 30)
		require.NoError(t, store.SaveTokenConfig(ctx, cfg))
		assert.True(t, mr.Exists("token:prefixed-1"))

		gotByRaw, err := store.GetConfigByToken(ctx, "  prefixed-1  ")
		require.NoError(t, err)
		require.NotNil(t, gotByRaw)
		assert.Equal(t, 3, gotByRaw.MaxRequests)

		gotByPrefixed, err := store.GetConfigByToken(ctx, "token:prefixed-1")
		require.NoError(t, err)
		require.NotNil(t, gotByPrefixed)
		assert.Equal(t, 3, gotByPrefixed.MaxRequests)
	})

	t.Run("returns nil when token does not exist or normalizes to empty", func(t *testing.T) {
		_, _, store := newTestStore(t)
		ctx := context.Background()

		for _, token := range []string{"non-existent", "", "   ", "token:", "  token:  "} {
			got, err := store.GetConfigByToken(ctx, token)
			require.NoError(t, err)
			assert.Nil(t, got)
		}
	})

	t.Run("expires token config after TTL", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		ctx := context.Background()

		cfg := entity.NewTokenConfig("ephemeral", 10, 5, 30, 5)
		require.NoError(t, store.SaveTokenConfig(ctx, cfg))

		mr.FastForward(6 * time.Second)

		got, err := store.GetConfigByToken(ctx, "ephemeral")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("returns validation errors on invalid token config", func(t *testing.T) {
		_, _, store := newTestStore(t)
		ctx := context.Background()

		cases := []struct {
			name string
			cfg  *entity.TokenConfig
		}{
			{name: "nil config", cfg: nil},
			{name: "empty token", cfg: entity.NewTokenConfig("", 10, 5, 10, 60)},
			{name: "whitespace token", cfg: entity.NewTokenConfig("   ", 10, 5, 10, 60)},
			{name: "prefix only token", cfg: entity.NewTokenConfig("token:", 10, 5, 10, 60)},
			{name: "prefix with whitespace token", cfg: entity.NewTokenConfig("  token:  ", 10, 5, 10, 60)},
			{name: "zero ttl", cfg: entity.NewTokenConfig("tok1", 10, 5, 10, 0)},
			{name: "negative ttl", cfg: entity.NewTokenConfig("tok1", 10, 5, 10, -5)},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := store.SaveTokenConfig(ctx, tc.cfg)
				require.Error(t, err)
			})
		}
	})

	t.Run("returns error on corrupted JSON payload", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		ctx := context.Background()

		require.NoError(t, mr.Set("token:corrupted", "{not-valid-json"))

		got, err := store.GetConfigByToken(ctx, "corrupted")
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "unmarshaling token config")
	})

	t.Run("propagates context cancellation and redis errors", func(t *testing.T) {
		mr, _, store := newTestStore(t)

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		cfg := entity.NewTokenConfig("tok-ctx", 10, 5, 10, 60)
		err := store.SaveTokenConfig(canceledCtx, cfg)
		require.ErrorIs(t, err, context.Canceled)

		_, err = store.GetConfigByToken(canceledCtx, "tok-ctx")
		require.ErrorIs(t, err, context.Canceled)

		mr.Close()
		err = store.SaveTokenConfig(context.Background(), cfg)
		require.Error(t, err)
		_, err = store.GetConfigByToken(context.Background(), "tok-ctx")
		require.Error(t, err)
	})
}

func TestStore_BlockAndIsBlocked(t *testing.T) {
	t.Run("blocks key and reports remaining time until expiration", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		ctx := context.Background()
		key := *entity.NewLimiterKey("ip", "10.10.0.5")

		status, err := store.IsBlocked(ctx, key)
		require.NoError(t, err)
		assert.False(t, status.Blocked)
		assert.Equal(t, 0, status.RemainingSeconds)

		require.NoError(t, store.Block(ctx, key, 5))

		status, err = store.IsBlocked(ctx, key)
		require.NoError(t, err)
		assert.True(t, status.Blocked)
		assert.Equal(t, 5, status.RemainingSeconds)

		mr.FastForward(4500 * time.Millisecond)
		status, err = store.IsBlocked(ctx, key)
		require.NoError(t, err)
		assert.True(t, status.Blocked)
		assert.Equal(t, 1, status.RemainingSeconds)

		mr.FastForward(1 * time.Second)
		status, err = store.IsBlocked(ctx, key)
		require.NoError(t, err)
		assert.False(t, status.Blocked)
		assert.Equal(t, 0, status.RemainingSeconds)
	})

	t.Run("reports blocked when orphan blocked key has no TTL", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		ctx := context.Background()
		key := *entity.NewLimiterKey("ip", "10.10.0.9")

		require.NoError(t, mr.Set(blockedKey(key), "1"))

		status, err := store.IsBlocked(ctx, key)
		require.NoError(t, err)
		assert.True(t, status.Blocked)
		assert.Equal(t, 0, status.RemainingSeconds)
	})

	t.Run("returns validation errors on invalid inputs", func(t *testing.T) {
		_, _, store := newTestStore(t)
		ctx := context.Background()
		validKey := *entity.NewLimiterKey("ip", "10.10.0.6")

		require.Error(t, store.Block(ctx, entity.LimiterKey{}, 5))
		require.Error(t, store.Block(ctx, validKey, 0))
		require.Error(t, store.Block(ctx, validKey, -3))

		_, err := store.IsBlocked(ctx, entity.LimiterKey{})
		require.Error(t, err)
	})

	t.Run("propagates context cancellation and redis errors", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		key := *entity.NewLimiterKey("ip", "10.10.0.7")

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		require.ErrorIs(t, store.Block(canceledCtx, key, 5), context.Canceled)
		_, err := store.IsBlocked(canceledCtx, key)
		require.ErrorIs(t, err, context.Canceled)

		mr.Close()
		require.Error(t, store.Block(context.Background(), key, 5))
		_, err = store.IsBlocked(context.Background(), key)
		require.Error(t, err)
	})
}

func TestStore_ResetKey(t *testing.T) {
	t.Run("deletes count, blocked, and token config keys", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		ctx := context.Background()
		key := *entity.NewLimiterKey("token", "reset-me")

		cfg := entity.NewTokenConfig("reset-me", 10, 5, 10, 60)
		require.NoError(t, store.SaveTokenConfig(ctx, cfg))
		_, err := store.AddRequest(ctx, key, 0, 5, 10, 10)
		require.NoError(t, err)
		require.NoError(t, store.Block(ctx, key, 10))

		require.True(t, mr.Exists(key.String()))
		require.True(t, mr.Exists(countKey(key)))
		require.True(t, mr.Exists(blockedKey(key)))

		require.NoError(t, store.ResetKey(ctx, key))

		assert.False(t, mr.Exists(key.String()))
		assert.False(t, mr.Exists(countKey(key)))
		assert.False(t, mr.Exists(blockedKey(key)))
	})

	t.Run("is idempotent when key does not exist", func(t *testing.T) {
		_, _, store := newTestStore(t)
		key := *entity.NewLimiterKey("ip", "10.10.0.99")

		require.NoError(t, store.ResetKey(context.Background(), key))
	})

	t.Run("returns validation, context cancellation, and redis errors", func(t *testing.T) {
		mr, _, store := newTestStore(t)
		key := *entity.NewLimiterKey("ip", "10.10.0.8")

		require.Error(t, store.ResetKey(context.Background(), entity.LimiterKey{}))

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, store.ResetKey(canceledCtx, key), context.Canceled)

		mr.Close()
		require.Error(t, store.ResetKey(context.Background(), key))
	})
}
