//go:generate mockery --dir=. --output=./mock --name=RateLimiterUseCase --structname=MockRateLimiterUseCase --outpkg=mock --filename=rate_limiter_use_case.go --disable-version-string
package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/service"
)

// RateLimiterUseCase define a interface do limiter
type RateLimiterUseCase interface {
	AllowRequest(ip string, token string, now time.Time) (allowed bool, reason string, remaining int, retryAfterSec int, err error)
	IsBlocked(key entity.LimiterKey) (blocked bool, remainingSec int, err error)
	CreateToken(timeWindow, maxRequests, blockedDuration, tokenTTL int) (*entity.TokenConfig, error)
}

type rateLimiterUseCase struct {
	store       service.LimiterStore
	ipMaxReq    int
	ipWindowSec int
	ipBlockSec  int
}

func NewRateLimiterUseCase(store service.LimiterStore, ipMaxReq, ipWindowSec, ipBlockSec int) RateLimiterUseCase {
	return &rateLimiterUseCase{
		store:       store,
		ipMaxReq:    ipMaxReq,
		ipWindowSec: ipWindowSec,
		ipBlockSec:  ipBlockSec,
	}
}

// Cria token e salva no Redis com TTL baseado no TokenTTL
func (uc *rateLimiterUseCase) CreateToken(timeWindow, maxRequests, blockedDuration, tokenTTL int) (*entity.TokenConfig, error) {
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(tokenBytes)

	key := *entity.NewLimiterKey("token", token)
	cfg := &entity.TokenConfig{
		Token:                  key.String(),
		TimeWindowSeconds:      timeWindow,
		MaxRequests:            maxRequests,
		BlockedDurationSeconds: blockedDuration,
		TokenTTLSeconds:        tokenTTL,
		CreatedAt:              time.Now(),
	}

	if err := uc.store.SaveTokenConfig(context.Background(), cfg); err != nil {
		return nil, err
	}

	cfg.Token = token
	return cfg, nil
}

func (uc *rateLimiterUseCase) AllowRequest(ip string, token string, now time.Time) (bool, string, int, int, error) {
	ctx := context.Background()
	var key entity.LimiterKey
	var cfg *entity.TokenConfig
	var err error

	timestamp := now.UnixMilli()

	if token != "" {
		cfg, err = uc.store.GetConfigByToken(ctx, token)
		if err != nil {
			return false, "error fetching token config", 0, 0, err
		}
		if cfg != nil {
			key = *entity.NewLimiterKey("token", token)
			fmt.Printf("[RateLimiter] Token found: %s | MaxReq: %d | Window: %ds\n", token, cfg.MaxRequests, cfg.TimeWindowSeconds)
		}
		// se cfg == nil, token expirou, mas podemos continuar usando o IP
	}

	if key.Value == "" {
		key = *entity.NewLimiterKey("ip", ip)
		cfg = &entity.TokenConfig{
			MaxRequests:            uc.ipMaxReq,
			TimeWindowSeconds:      uc.ipWindowSec,
			BlockedDurationSeconds: uc.ipBlockSec,
		}
		fmt.Printf("[RateLimiter] Using IP %s | MaxReq: %d | Window: %ds\n", ip, cfg.MaxRequests, cfg.TimeWindowSeconds)
	}

	blockStatus, err := uc.store.IsBlocked(ctx, key)
	if err != nil {
		return false, "error checking blocked", 0, 0, err
	}
	if blockStatus.Blocked {
		fmt.Printf("[RateLimiter] Blocked: %s | Retry after: %ds\n", key.String(), blockStatus.RemainingSeconds)
		return false, "blocked", 0, blockStatus.RemainingSeconds, nil
	}

	result, err := uc.store.AddRequest(ctx, key, timestamp, cfg.MaxRequests, cfg.TimeWindowSeconds, cfg.BlockedDurationSeconds)
	if err != nil {
		return false, "error adding request", 0, 0, err
	}

	if !result.Allowed && cfg.BlockedDurationSeconds > 0 {
		if err := uc.store.Block(ctx, key, cfg.BlockedDurationSeconds); err != nil {
			return false, "error applying block", 0, 0, err
		}
		fmt.Printf("[RateLimiter] Limit exceeded: %s | Blocking for %ds\n", key.String(), cfg.BlockedDurationSeconds)
		return false, "rate limit exceeded", 0, cfg.BlockedDurationSeconds, nil
	}

	fmt.Printf("[RateLimiter] Allowed: %s | Remaining: %d\n", key.String(), result.Remaining)
	return true, "", result.Remaining, 0, nil
}

func (uc *rateLimiterUseCase) IsBlocked(key entity.LimiterKey) (bool, int, error) {
	status, err := uc.store.IsBlocked(context.Background(), key)
	return status.Blocked, status.RemainingSeconds, err
}
