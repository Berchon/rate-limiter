package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubLimiterStore struct {
	saveTokenConfigFn  func(ctx context.Context, cfg *entity.TokenConfig) error
	addRequestFn       func(ctx context.Context, key entity.LimiterKey, timestamp int64, maxRequests, timeWindowSec, blockedDuration int) (service.AddRequestResult, error)
	getConfigByTokenFn func(ctx context.Context, token string) (*entity.TokenConfig, error)
	blockFn            func(ctx context.Context, key entity.LimiterKey, durationSec int) error
	isBlockedFn        func(ctx context.Context, key entity.LimiterKey) (service.BlockStatus, error)
	resetKeyFn         func(ctx context.Context, key entity.LimiterKey) error
}

func (s *stubLimiterStore) SaveTokenConfig(ctx context.Context, cfg *entity.TokenConfig) error {
	if s.saveTokenConfigFn != nil {
		return s.saveTokenConfigFn(ctx, cfg)
	}
	return nil
}

func (s *stubLimiterStore) AddRequest(
	ctx context.Context,
	key entity.LimiterKey,
	timestamp int64,
	maxRequests, timeWindowSec, blockedDuration int,
) (service.AddRequestResult, error) {
	if s.addRequestFn != nil {
		return s.addRequestFn(ctx, key, timestamp, maxRequests, timeWindowSec, blockedDuration)
	}
	return service.AddRequestResult{Allowed: true}, nil
}

func (s *stubLimiterStore) GetConfigByToken(ctx context.Context, token string) (*entity.TokenConfig, error) {
	if s.getConfigByTokenFn != nil {
		return s.getConfigByTokenFn(ctx, token)
	}
	return nil, nil
}

func (s *stubLimiterStore) Block(ctx context.Context, key entity.LimiterKey, durationSec int) error {
	if s.blockFn != nil {
		return s.blockFn(ctx, key, durationSec)
	}
	return nil
}

func (s *stubLimiterStore) IsBlocked(ctx context.Context, key entity.LimiterKey) (service.BlockStatus, error) {
	if s.isBlockedFn != nil {
		return s.isBlockedFn(ctx, key)
	}
	return service.BlockStatus{}, nil
}

func (s *stubLimiterStore) ResetKey(ctx context.Context, key entity.LimiterKey) error {
	if s.resetKeyFn != nil {
		return s.resetKeyFn(ctx, key)
	}
	return nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func defaultTestConfig() RateLimiterConfig {
	return RateLimiterConfig{
		IPMaxRequests:       10,
		IPTimeWindowSec:     60,
		IPBlockedDuration:   300,
		EnableTokenLimiting: true,
		DefaultTokenTTL:     3600,
	}
}

func TestCreateToken(t *testing.T) {
	storeErr := errors.New("redis unavailable")

	tests := []struct {
		name            string
		storeNil        bool
		requestedTTL    int
		saveErr         error
		wantTTL         int
		wantErrContains string
		wantErrTarget   error
	}{
		{
			name:         "uses explicit positive token TTL",
			requestedTTL: 600,
			wantTTL:      600,
		},
		{
			name:         "falls back to default token TTL when zero",
			requestedTTL: 0,
			wantTTL:      3600,
		},
		{
			name:         "falls back to default token TTL when negative",
			requestedTTL: -10,
			wantTTL:      3600,
		},
		{
			name:            "wraps error when store fails to save token",
			requestedTTL:    600,
			saveErr:         storeErr,
			wantErrContains: "saving token config",
			wantErrTarget:   storeErr,
		},
		{
			name:            "returns ErrNilStore when store is nil",
			storeNil:        true,
			requestedTTL:    600,
			wantErrContains: "creating token",
			wantErrTarget:   ErrNilStore,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var savedCfg *entity.TokenConfig
			var capturedCtx context.Context

			var store service.LimiterStore
			if !tc.storeNil {
				store = &stubLimiterStore{
					saveTokenConfigFn: func(ctx context.Context, cfg *entity.TokenConfig) error {
						capturedCtx = ctx
						savedCfg = cfg
						return tc.saveErr
					},
				}
			}

			uc := NewRateLimiterUseCase(store, defaultTestConfig(), discardLogger())

			type ctxKey string
			ctx := context.WithValue(context.Background(), ctxKey("req"), "trace-1")

			cfg, err := uc.CreateToken(ctx, 10, 50, 120, tc.requestedTTL)
			if tc.wantErrTarget != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErrTarget)
				assert.ErrorContains(t, err, tc.wantErrContains)
				assert.Nil(t, cfg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, cfg)
			assert.Equal(t, ctx, capturedCtx)
			assert.Len(t, cfg.Token, tokenByteLength*2)
			assert.Equal(t, 10, cfg.TimeWindowSeconds)
			assert.Equal(t, 50, cfg.MaxRequests)
			assert.Equal(t, 120, cfg.BlockedDurationSeconds)
			assert.Equal(t, tc.wantTTL, cfg.TokenTTLSeconds)
			assert.Equal(t, cfg.CreatedAt.Add(time.Duration(tc.wantTTL)*time.Second), cfg.ExpiresAt)
			assert.Equal(t, cfg, savedCfg)
		})
	}
}

func TestAllowRequest(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	storeErr := errors.New("storage failure")

	tokenCfg := &entity.TokenConfig{
		Token:                  "secret-token-value",
		TimeWindowSeconds:      30,
		MaxRequests:            100,
		BlockedDurationSeconds: 180,
		TokenTTLSeconds:        3600,
	}

	tests := []struct {
		name            string
		cfg             RateLimiterConfig
		store           service.LimiterStore
		ip              string
		token           string
		wantDecision    *entity.RateLimitDecision
		wantErrTarget   error
		wantErrContains string
	}{
		{
			name:  "allows request under IP policy when no token is provided",
			cfg:   defaultTestConfig(),
			ip:    "192.168.1.10",
			token: "",
			store: &stubLimiterStore{
				addRequestFn: func(_ context.Context, key entity.LimiterKey, ts int64, maxReq, winSec, blockSec int) (service.AddRequestResult, error) {
					assert.Equal(t, "ip:192.168.1.10", key.String())
					assert.Equal(t, now.UnixMilli(), ts)
					assert.Equal(t, 10, maxReq)
					assert.Equal(t, 60, winSec)
					assert.Equal(t, 300, blockSec)
					return service.AddRequestResult{
						Allowed:      true,
						Remaining:    9,
						ResetSeconds: 60,
					}, nil
				},
			},
			wantDecision: &entity.RateLimitDecision{
				Allowed:           true,
				Limit:             10,
				Remaining:         9,
				ResetSeconds:      60,
				RetryAfterSeconds: 0,
				Reason:            "",
			},
		},
		{
			name: "ignores API token and applies IP policy when EnableTokenLimiting is false",
			cfg: RateLimiterConfig{
				IPMaxRequests:       5,
				IPTimeWindowSec:     10,
				IPBlockedDuration:   20,
				EnableTokenLimiting: false,
				DefaultTokenTTL:     3600,
			},
			ip:    "10.0.0.1",
			token: "secret-token-value",
			store: &stubLimiterStore{
				getConfigByTokenFn: func(_ context.Context, _ string) (*entity.TokenConfig, error) {
					t.Fatal("GetConfigByToken should not be called when token limiting is disabled")
					return nil, nil
				},
				addRequestFn: func(_ context.Context, key entity.LimiterKey, _ int64, maxReq, winSec, blockSec int) (service.AddRequestResult, error) {
					assert.Equal(t, "ip:10.0.0.1", key.String())
					assert.Equal(t, 5, maxReq)
					assert.Equal(t, 10, winSec)
					assert.Equal(t, 20, blockSec)
					return service.AddRequestResult{
						Allowed:      true,
						Remaining:    4,
						ResetSeconds: 10,
					}, nil
				},
			},
			wantDecision: &entity.RateLimitDecision{
				Allowed:           true,
				Limit:             5,
				Remaining:         4,
				ResetSeconds:      10,
				RetryAfterSeconds: 0,
				Reason:            "",
			},
		},
		{
			name:  "applies token policy when EnableTokenLimiting is true and token exists",
			cfg:   defaultTestConfig(),
			ip:    "10.0.0.2",
			token: "secret-token-value",
			store: &stubLimiterStore{
				getConfigByTokenFn: func(_ context.Context, token string) (*entity.TokenConfig, error) {
					assert.Equal(t, "secret-token-value", token)
					return tokenCfg, nil
				},
				addRequestFn: func(_ context.Context, key entity.LimiterKey, _ int64, maxReq, winSec, blockSec int) (service.AddRequestResult, error) {
					assert.Equal(t, "token:secret-token-value", key.String())
					assert.Equal(t, 100, maxReq)
					assert.Equal(t, 30, winSec)
					assert.Equal(t, 180, blockSec)
					return service.AddRequestResult{
						Allowed:      true,
						Remaining:    99,
						ResetSeconds: 30,
					}, nil
				},
			},
			wantDecision: &entity.RateLimitDecision{
				Allowed:           true,
				Limit:             100,
				Remaining:         99,
				ResetSeconds:      30,
				RetryAfterSeconds: 0,
				Reason:            "",
			},
		},
		{
			name:  "falls back to IP policy when token is not found or expired",
			cfg:   defaultTestConfig(),
			ip:    "10.0.0.3",
			token: "expired-token",
			store: &stubLimiterStore{
				getConfigByTokenFn: func(_ context.Context, _ string) (*entity.TokenConfig, error) {
					return nil, nil
				},
				addRequestFn: func(_ context.Context, key entity.LimiterKey, _ int64, maxReq, _, _ int) (service.AddRequestResult, error) {
					assert.Equal(t, "ip:10.0.0.3", key.String())
					assert.Equal(t, 10, maxReq)
					return service.AddRequestResult{
						Allowed:      true,
						Remaining:    9,
						ResetSeconds: 60,
					}, nil
				},
			},
			wantDecision: &entity.RateLimitDecision{
				Allowed:      true,
				Limit:        10,
				Remaining:    9,
				ResetSeconds: 60,
			},
		},
		{
			name:  "rejects immediately when key is already blocked",
			cfg:   defaultTestConfig(),
			ip:    "10.0.0.4",
			token: "",
			store: &stubLimiterStore{
				isBlockedFn: func(_ context.Context, key entity.LimiterKey) (service.BlockStatus, error) {
					assert.Equal(t, "ip:10.0.0.4", key.String())
					return service.BlockStatus{
						Blocked:          true,
						RemainingSeconds: 45,
					}, nil
				},
				addRequestFn: func(_ context.Context, _ entity.LimiterKey, _ int64, _, _, _ int) (service.AddRequestResult, error) {
					t.Fatal("AddRequest should not be called when key is already blocked")
					return service.AddRequestResult{}, nil
				},
			},
			wantDecision: &entity.RateLimitDecision{
				Allowed:           false,
				Limit:             10,
				Remaining:         0,
				ResetSeconds:      45,
				RetryAfterSeconds: 45,
				Reason:            reasonBlocked,
			},
		},
		{
			name:  "rejects when atomic store reports rate limit exceeded with retry-after",
			cfg:   defaultTestConfig(),
			ip:    "10.0.0.5",
			token: "",
			store: &stubLimiterStore{
				addRequestFn: func(_ context.Context, _ entity.LimiterKey, _ int64, _, _, _ int) (service.AddRequestResult, error) {
					return service.AddRequestResult{
						Allowed:           false,
						Remaining:         0,
						ResetSeconds:      300,
						RetryAfterSeconds: 300,
					}, nil
				},
				blockFn: func(_ context.Context, _ entity.LimiterKey, _ int) error {
					t.Fatal("Block should not be called again when atomic store already returned RetryAfterSeconds")
					return nil
				},
			},
			wantDecision: &entity.RateLimitDecision{
				Allowed:           false,
				Limit:             10,
				Remaining:         0,
				ResetSeconds:      300,
				RetryAfterSeconds: 300,
				Reason:            reasonLimitExceed,
			},
		},
		{
			name:  "defaults ResetSeconds to RetryAfterSeconds when store omits ResetSeconds",
			cfg:   defaultTestConfig(),
			ip:    "10.0.0.6",
			token: "",
			store: &stubLimiterStore{
				addRequestFn: func(_ context.Context, _ entity.LimiterKey, _ int64, _, _, _ int) (service.AddRequestResult, error) {
					return service.AddRequestResult{
						Allowed:           false,
						Remaining:         0,
						ResetSeconds:      0,
						RetryAfterSeconds: 120,
					}, nil
				},
			},
			wantDecision: &entity.RateLimitDecision{
				Allowed:           false,
				Limit:             10,
				Remaining:         0,
				ResetSeconds:      120,
				RetryAfterSeconds: 120,
				Reason:            reasonLimitExceed,
			},
		},
		{
			name:  "calls Block when non-atomic store rejects without RetryAfterSeconds",
			cfg:   defaultTestConfig(),
			ip:    "10.0.0.7",
			token: "",
			store: &stubLimiterStore{
				addRequestFn: func(_ context.Context, _ entity.LimiterKey, _ int64, _, _, _ int) (service.AddRequestResult, error) {
					return service.AddRequestResult{
						Allowed:           false,
						Remaining:         0,
						ResetSeconds:      60,
						RetryAfterSeconds: 0,
					}, nil
				},
				blockFn: func(_ context.Context, key entity.LimiterKey, durationSec int) error {
					assert.Equal(t, "ip:10.0.0.7", key.String())
					assert.Equal(t, 300, durationSec)
					return nil
				},
			},
			wantDecision: &entity.RateLimitDecision{
				Allowed:           false,
				Limit:             10,
				Remaining:         0,
				ResetSeconds:      300,
				RetryAfterSeconds: 300,
				Reason:            reasonLimitExceed,
			},
		},
		{
			name: "does not call Block when BlockedDurationSeconds is zero",
			cfg: RateLimiterConfig{
				IPMaxRequests:       10,
				IPTimeWindowSec:     60,
				IPBlockedDuration:   0,
				EnableTokenLimiting: true,
				DefaultTokenTTL:     3600,
			},
			ip:    "10.0.0.8",
			token: "",
			store: &stubLimiterStore{
				addRequestFn: func(_ context.Context, _ entity.LimiterKey, _ int64, _, _, _ int) (service.AddRequestResult, error) {
					return service.AddRequestResult{
						Allowed:           false,
						Remaining:         0,
						ResetSeconds:      15,
						RetryAfterSeconds: 0,
					}, nil
				},
				blockFn: func(_ context.Context, _ entity.LimiterKey, _ int) error {
					t.Fatal("Block should not be called when BlockedDurationSeconds is 0")
					return nil
				},
			},
			wantDecision: &entity.RateLimitDecision{
				Allowed:           false,
				Limit:             10,
				Remaining:         0,
				ResetSeconds:      15,
				RetryAfterSeconds: 0,
				Reason:            reasonLimitExceed,
			},
		},
		{
			name:  "wraps error when GetConfigByToken fails",
			cfg:   defaultTestConfig(),
			ip:    "10.0.0.9",
			token: "some-token",
			store: &stubLimiterStore{
				getConfigByTokenFn: func(_ context.Context, _ string) (*entity.TokenConfig, error) {
					return nil, storeErr
				},
			},
			wantErrTarget:   storeErr,
			wantErrContains: "fetching token config",
		},
		{
			name:  "wraps error when IsBlocked fails",
			cfg:   defaultTestConfig(),
			ip:    "10.0.0.10",
			token: "",
			store: &stubLimiterStore{
				isBlockedFn: func(_ context.Context, _ entity.LimiterKey) (service.BlockStatus, error) {
					return service.BlockStatus{}, storeErr
				},
			},
			wantErrTarget:   storeErr,
			wantErrContains: "checking block status",
		},
		{
			name:  "wraps error when AddRequest fails",
			cfg:   defaultTestConfig(),
			ip:    "10.0.0.11",
			token: "",
			store: &stubLimiterStore{
				addRequestFn: func(_ context.Context, _ entity.LimiterKey, _ int64, _, _, _ int) (service.AddRequestResult, error) {
					return service.AddRequestResult{}, storeErr
				},
			},
			wantErrTarget:   storeErr,
			wantErrContains: "recording request",
		},
		{
			name:  "wraps error when fallback Block call fails",
			cfg:   defaultTestConfig(),
			ip:    "10.0.0.12",
			token: "",
			store: &stubLimiterStore{
				addRequestFn: func(_ context.Context, _ entity.LimiterKey, _ int64, _, _, _ int) (service.AddRequestResult, error) {
					return service.AddRequestResult{Allowed: false}, nil
				},
				blockFn: func(_ context.Context, _ entity.LimiterKey, _ int) error {
					return storeErr
				},
			},
			wantErrTarget:   storeErr,
			wantErrContains: "applying block",
		},
		{
			name:            "returns ErrNilStore when store is nil",
			cfg:             defaultTestConfig(),
			ip:              "10.0.0.13",
			token:           "",
			store:           nil,
			wantErrTarget:   ErrNilStore,
			wantErrContains: "evaluating rate limit",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uc := NewRateLimiterUseCase(tc.store, tc.cfg, discardLogger())

			decision, err := uc.AllowRequest(context.Background(), tc.ip, tc.token, now)
			if tc.wantErrTarget != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErrTarget)
				assert.ErrorContains(t, err, tc.wantErrContains)
				assert.Nil(t, decision)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantDecision, decision)
		})
	}
}

func TestAllowRequest_DoesNotLogRawTokenAndHandlesNilLogger(t *testing.T) {
	t.Run("never logs raw API token in structured debug logs", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

		secretToken := "super-secret-api-key-123456"
		store := &stubLimiterStore{
			getConfigByTokenFn: func(_ context.Context, _ string) (*entity.TokenConfig, error) {
				return &entity.TokenConfig{
					Token:                  secretToken,
					TimeWindowSeconds:      10,
					MaxRequests:            5,
					BlockedDurationSeconds: 60,
				}, nil
			},
			addRequestFn: func(_ context.Context, _ entity.LimiterKey, _ int64, _, _, _ int) (service.AddRequestResult, error) {
				return service.AddRequestResult{Allowed: true, Remaining: 4, ResetSeconds: 10}, nil
			},
		}

		uc := NewRateLimiterUseCase(store, defaultTestConfig(), logger)
		decision, err := uc.AllowRequest(context.Background(), "127.0.0.1", secretToken, time.Now())
		require.NoError(t, err)
		require.True(t, decision.Allowed)
		assert.NotContains(t, buf.String(), secretToken)
	})

	t.Run("falls back to slog.Default when logger is nil", func(t *testing.T) {
		store := &stubLimiterStore{
			addRequestFn: func(_ context.Context, _ entity.LimiterKey, _ int64, _, _, _ int) (service.AddRequestResult, error) {
				return service.AddRequestResult{Allowed: true, Remaining: 9, ResetSeconds: 60}, nil
			},
		}

		uc := NewRateLimiterUseCase(store, defaultTestConfig(), nil)
		decision, err := uc.AllowRequest(context.Background(), "127.0.0.1", "", time.Now())
		require.NoError(t, err)
		assert.True(t, decision.Allowed)
	})
}

func TestIsBlocked(t *testing.T) {
	storeErr := errors.New("connection lost")
	key := *entity.NewLimiterKey("ip", "127.0.0.1")

	tests := []struct {
		name            string
		store           service.LimiterStore
		wantBlocked     bool
		wantRemaining   int
		wantErrTarget   error
		wantErrContains string
	}{
		{
			name: "returns blocked status and remaining seconds",
			store: &stubLimiterStore{
				isBlockedFn: func(_ context.Context, gotKey entity.LimiterKey) (service.BlockStatus, error) {
					assert.Equal(t, key, gotKey)
					return service.BlockStatus{Blocked: true, RemainingSeconds: 25}, nil
				},
			},
			wantBlocked:   true,
			wantRemaining: 25,
		},
		{
			name: "returns not blocked when key has no active block",
			store: &stubLimiterStore{
				isBlockedFn: func(_ context.Context, _ entity.LimiterKey) (service.BlockStatus, error) {
					return service.BlockStatus{Blocked: false, RemainingSeconds: 0}, nil
				},
			},
			wantBlocked:   false,
			wantRemaining: 0,
		},
		{
			name: "wraps store error",
			store: &stubLimiterStore{
				isBlockedFn: func(_ context.Context, _ entity.LimiterKey) (service.BlockStatus, error) {
					return service.BlockStatus{}, storeErr
				},
			},
			wantErrTarget:   storeErr,
			wantErrContains: "checking block status",
		},
		{
			name:            "returns ErrNilStore when store is nil",
			store:           nil,
			wantErrTarget:   ErrNilStore,
			wantErrContains: "checking block status",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uc := NewRateLimiterUseCase(tc.store, defaultTestConfig(), discardLogger())

			blocked, remaining, err := uc.IsBlocked(context.Background(), key)
			if tc.wantErrTarget != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErrTarget)
				assert.ErrorContains(t, err, tc.wantErrContains)
				assert.False(t, blocked)
				assert.Zero(t, remaining)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantBlocked, blocked)
			assert.Equal(t, tc.wantRemaining, remaining)
		})
	}
}

func TestAllowRequest_TokenNormalization(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	tokenCfg := &entity.TokenConfig{
		Token:                  "secret-token-value",
		MaxRequests:            100,
		TimeWindowSeconds:      10,
		BlockedDurationSeconds: 60,
		TokenTTLSeconds:        3600,
	}

	t.Run("normalizes whitespace and repeated token prefixes before lookup and key construction", func(t *testing.T) {
		for _, rawHeader := range []string{
			"  secret-token-value  ",
			"  token:secret-token-value  ",
			"token:token:secret-token-value",
			"  token: token: secret-token-value  ",
		} {
			var lookedUpToken string
			var recordedKey entity.LimiterKey

			store := &stubLimiterStore{
				getConfigByTokenFn: func(_ context.Context, token string) (*entity.TokenConfig, error) {
					lookedUpToken = token
					return tokenCfg, nil
				},
				addRequestFn: func(_ context.Context, key entity.LimiterKey, _ int64, _, _, _ int) (service.AddRequestResult, error) {
					recordedKey = key
					return service.AddRequestResult{Allowed: true, Remaining: 99, ResetSeconds: 10}, nil
				},
			}

			uc := NewRateLimiterUseCase(store, defaultTestConfig(), discardLogger())
			decision, err := uc.AllowRequest(context.Background(), "192.0.2.1", rawHeader, now)
			require.NoError(t, err, "header=%q", rawHeader)
			assert.True(t, decision.Allowed, "header=%q", rawHeader)
			assert.Equal(t, "secret-token-value", lookedUpToken, "header=%q", rawHeader)
			assert.Equal(t, *entity.NewLimiterKey("token", "secret-token-value"), recordedKey, "header=%q", rawHeader)
		}
	})

	t.Run("falls back to IP without querying store when token normalizes to empty", func(t *testing.T) {
		for _, rawHeader := range []string{"   ", "  token:  ", "  token: token:  "} {
			lookupCalled := false
			var recordedKey entity.LimiterKey

			store := &stubLimiterStore{
				getConfigByTokenFn: func(_ context.Context, _ string) (*entity.TokenConfig, error) {
					lookupCalled = true
					return tokenCfg, nil
				},
				addRequestFn: func(_ context.Context, key entity.LimiterKey, _ int64, _, _, _ int) (service.AddRequestResult, error) {
					recordedKey = key
					return service.AddRequestResult{Allowed: true, Remaining: 9, ResetSeconds: 60}, nil
				},
			}

			uc := NewRateLimiterUseCase(store, defaultTestConfig(), discardLogger())
			decision, err := uc.AllowRequest(context.Background(), "192.0.2.2", rawHeader, now)
			require.NoError(t, err, "header=%q", rawHeader)
			assert.True(t, decision.Allowed, "header=%q", rawHeader)
			assert.False(t, lookupCalled, "header=%q", rawHeader)
			assert.Equal(t, *entity.NewLimiterKey("ip", "192.0.2.2"), recordedKey, "header=%q", rawHeader)
		}
	})
}
