//go:generate mockery --dir=. --output=./mock --name=RateLimiterUseCase --structname=MockRateLimiterUseCase --outpkg=mock --filename=rate_limiter_use_case.go --disable-version-string

// Package usecase orchestrates rate-limiting policies and API token lifecycle rules.
package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/service"
)

const (
	keyPrefixIP       = "ip"
	keyPrefixToken    = "token"
	tokenByteLength   = 16
	reasonBlocked     = "blocked"
	reasonLimitExceed = "rate limit exceeded"
)

// ErrNilStore is returned when the use case is invoked without a configured LimiterStore.
var ErrNilStore = errors.New("limiter store cannot be nil")

// RateLimiterUseCase defines the application contract for rate-limit evaluation and token management.
type RateLimiterUseCase interface {
	AllowRequest(ctx context.Context, ip, token string, now time.Time) (*entity.RateLimitDecision, error)
	IsBlocked(ctx context.Context, key entity.LimiterKey) (bool, int, error)
	CreateToken(ctx context.Context, timeWindow, maxRequests, blockedDuration, tokenTTL int) (*entity.TokenConfig, error)
}

// RateLimiterConfig holds the IP fallback limits and token settings used by RateLimiterUseCase.
type RateLimiterConfig struct {
	IPMaxRequests       int
	IPTimeWindowSec     int
	IPBlockedDuration   int
	EnableTokenLimiting bool
	DefaultTokenTTL     int
}

type resolvedPolicy struct {
	key entity.LimiterKey
	cfg *entity.TokenConfig
}

type rateLimiterUseCase struct {
	store  service.LimiterStore
	cfg    RateLimiterConfig
	logger *slog.Logger
}

// NewRateLimiterUseCase creates a new RateLimiterUseCase with the given store, configuration, and logger.
func NewRateLimiterUseCase(store service.LimiterStore, cfg RateLimiterConfig, logger *slog.Logger) RateLimiterUseCase {
	if logger == nil {
		logger = slog.Default()
	}

	return &rateLimiterUseCase{
		store:  store,
		cfg:    cfg,
		logger: logger,
	}
}

// CreateToken generates a new API token and persists its rate-limit policy in the store.
// When tokenTTL is less than or equal to zero, the configured DefaultTokenTTL is applied.
func (uc *rateLimiterUseCase) CreateToken(
	ctx context.Context,
	timeWindow, maxRequests, blockedDuration, tokenTTL int,
) (*entity.TokenConfig, error) {
	if uc.store == nil {
		return nil, fmt.Errorf("creating token: %w", ErrNilStore)
	}

	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("generating token: %w", err)
	}

	effectiveTTL := uc.resolveTokenTTL(tokenTTL)
	cfg := entity.NewTokenConfig(token, timeWindow, maxRequests, blockedDuration, effectiveTTL)

	if err := uc.store.SaveTokenConfig(ctx, cfg); err != nil {
		return nil, fmt.Errorf("saving token config: %w", err)
	}

	uc.logger.Info("api token created",
		"time_window_sec", cfg.TimeWindowSeconds,
		"max_requests", cfg.MaxRequests,
		"blocked_duration_sec", cfg.BlockedDurationSeconds,
		"token_ttl_sec", cfg.TokenTTLSeconds,
	)

	return cfg, nil
}

// AllowRequest evaluates whether an incoming request from ip/token is allowed under the active policy.
func (uc *rateLimiterUseCase) AllowRequest(
	ctx context.Context,
	ip, token string,
	now time.Time,
) (*entity.RateLimitDecision, error) {
	if uc.store == nil {
		return nil, fmt.Errorf("evaluating rate limit: %w", ErrNilStore)
	}

	policy, err := uc.resolvePolicy(ctx, ip, token)
	if err != nil {
		return nil, err
	}

	if decision, err := uc.checkBlocked(ctx, policy); err != nil || decision != nil {
		return decision, err
	}

	result, err := uc.store.AddRequest(
		ctx,
		policy.key,
		now.UnixMilli(),
		policy.cfg.MaxRequests,
		policy.cfg.TimeWindowSeconds,
		policy.cfg.BlockedDurationSeconds,
	)
	if err != nil {
		return nil, fmt.Errorf("recording request: %w", err)
	}

	return uc.buildDecision(ctx, policy, result)
}

// IsBlocked reports whether the given key is currently blocked and the remaining block duration in seconds.
func (uc *rateLimiterUseCase) IsBlocked(ctx context.Context, key entity.LimiterKey) (bool, int, error) {
	if uc.store == nil {
		return false, 0, fmt.Errorf("checking block status: %w", ErrNilStore)
	}

	status, err := uc.store.IsBlocked(ctx, key)
	if err != nil {
		return false, 0, fmt.Errorf("checking block status: %w", err)
	}

	return status.Blocked, status.RemainingSeconds, nil
}

func (uc *rateLimiterUseCase) resolvePolicy(ctx context.Context, ip, token string) (resolvedPolicy, error) {
	if uc.cfg.EnableTokenLimiting && token != "" {
		policy, found, err := uc.resolveTokenPolicy(ctx, token)
		if err != nil {
			return resolvedPolicy{}, err
		}
		if found {
			return policy, nil
		}

		uc.logger.Debug("token not found or expired, falling back to ip policy", "ip", ip)
	}

	return uc.ipPolicy(ip), nil
}

func (uc *rateLimiterUseCase) resolveTokenPolicy(ctx context.Context, token string) (resolvedPolicy, bool, error) {
	cfg, err := uc.store.GetConfigByToken(ctx, token)
	if err != nil {
		return resolvedPolicy{}, false, fmt.Errorf("fetching token config: %w", err)
	}
	if cfg == nil {
		return resolvedPolicy{}, false, nil
	}

	uc.logger.Debug("rate limit policy resolved",
		"key_type", keyPrefixToken,
		"max_requests", cfg.MaxRequests,
		"time_window_sec", cfg.TimeWindowSeconds,
	)

	return resolvedPolicy{
		key: *entity.NewLimiterKey(keyPrefixToken, token),
		cfg: cfg,
	}, true, nil
}

func (uc *rateLimiterUseCase) ipPolicy(ip string) resolvedPolicy {
	cfg := &entity.TokenConfig{
		MaxRequests:            uc.cfg.IPMaxRequests,
		TimeWindowSeconds:      uc.cfg.IPTimeWindowSec,
		BlockedDurationSeconds: uc.cfg.IPBlockedDuration,
	}

	uc.logger.Debug("rate limit policy resolved",
		"key_type", keyPrefixIP,
		"ip", ip,
		"max_requests", cfg.MaxRequests,
		"time_window_sec", cfg.TimeWindowSeconds,
	)

	return resolvedPolicy{
		key: *entity.NewLimiterKey(keyPrefixIP, ip),
		cfg: cfg,
	}
}

func (uc *rateLimiterUseCase) checkBlocked(ctx context.Context, policy resolvedPolicy) (*entity.RateLimitDecision, error) {
	status, err := uc.store.IsBlocked(ctx, policy.key)
	if err != nil {
		return nil, fmt.Errorf("checking block status: %w", err)
	}
	if !status.Blocked {
		return nil, nil
	}

	uc.logger.Debug("request rejected: key is blocked",
		"key_type", policy.key.Prefix,
		"retry_after_sec", status.RemainingSeconds,
	)

	return &entity.RateLimitDecision{
		Allowed:           false,
		Limit:             policy.cfg.MaxRequests,
		Remaining:         0,
		ResetSeconds:      int64(status.RemainingSeconds),
		RetryAfterSeconds: status.RemainingSeconds,
		Reason:            reasonBlocked,
	}, nil
}

func (uc *rateLimiterUseCase) buildDecision(
	ctx context.Context,
	policy resolvedPolicy,
	result service.AddRequestResult,
) (*entity.RateLimitDecision, error) {
	if result.Allowed {
		uc.logger.Debug("request allowed",
			"key_type", policy.key.Prefix,
			"remaining", result.Remaining,
			"reset_sec", result.ResetSeconds,
		)

		return &entity.RateLimitDecision{
			Allowed:           true,
			Limit:             policy.cfg.MaxRequests,
			Remaining:         result.Remaining,
			ResetSeconds:      result.ResetSeconds,
			RetryAfterSeconds: 0,
			Reason:            "",
		}, nil
	}

	retryAfter, resetSec, err := uc.ensureBlocked(ctx, policy, result)
	if err != nil {
		return nil, err
	}

	uc.logger.Debug("request rejected: rate limit exceeded",
		"key_type", policy.key.Prefix,
		"retry_after_sec", retryAfter,
	)

	return &entity.RateLimitDecision{
		Allowed:           false,
		Limit:             policy.cfg.MaxRequests,
		Remaining:         0,
		ResetSeconds:      resetSec,
		RetryAfterSeconds: retryAfter,
		Reason:            reasonLimitExceed,
	}, nil
}

func (uc *rateLimiterUseCase) ensureBlocked(
	ctx context.Context,
	policy resolvedPolicy,
	result service.AddRequestResult,
) (int, int64, error) {
	if result.RetryAfterSeconds > 0 {
		resetSec := result.ResetSeconds
		if resetSec <= 0 {
			resetSec = int64(result.RetryAfterSeconds)
		}
		return result.RetryAfterSeconds, resetSec, nil
	}

	if policy.cfg.BlockedDurationSeconds > 0 {
		if err := uc.store.Block(ctx, policy.key, policy.cfg.BlockedDurationSeconds); err != nil {
			return 0, 0, fmt.Errorf("applying block: %w", err)
		}
		return policy.cfg.BlockedDurationSeconds, int64(policy.cfg.BlockedDurationSeconds), nil
	}

	return 0, result.ResetSeconds, nil
}

func (uc *rateLimiterUseCase) resolveTokenTTL(requestedTTL int) int {
	if requestedTTL <= 0 {
		return uc.cfg.DefaultTokenTTL
	}
	return requestedTTL
}

func generateToken() (string, error) {
	tokenBytes := make([]byte, tokenByteLength)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(tokenBytes), nil
}
