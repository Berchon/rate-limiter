package usecase

import (
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/service"
)

// RateLimiterUseCase define a interface do limiter
type RateLimiterUseCase interface {
	AllowRequest(ip string, token string, now time.Time) (allowed bool, reason string, remaining int, retryAfterSec int, err error)
	IsBlocked(key entity.LimiterKey) (blocked bool, remainingSec int, err error)
}

// rateLimiterUseCase é a implementação do RateLimiterUseCase
type rateLimiterUseCase struct {
	store       service.LimiterStore
	ipMaxReq    int
	ipWindowSec int
	ipBlockSec  int
}

// NewRateLimiterUseCase cria uma nova instância do RateLimiterUseCase
func NewRateLimiterUseCase(store service.LimiterStore, ipMaxReq, ipWindowSec, ipBlockSec int) RateLimiterUseCase {
	return &rateLimiterUseCase{
		store:       store,
		ipMaxReq:    ipMaxReq,
		ipWindowSec: ipWindowSec,
		ipBlockSec:  ipBlockSec,
	}
}

// AllowRequest avalia se a requisição deve ser permitida
func (uc *rateLimiterUseCase) AllowRequest(ip string, token string, now time.Time) (bool, string, int, int, error) {
	var key entity.LimiterKey
	var cfg *entity.TokenConfig
	var err error

	timestamp := now.UnixMilli()

	// 1. Prioridade token
	if token != "" {
		cfg, err = uc.store.GetConfigByToken(token)
		if err != nil {
			return false, "error fetching token config", 0, 0, err
		}
		if cfg != nil {
			key = *entity.NewLimiterKey("token", token)
		}
	}

	// 2. Caso não tenha token ou config não encontrada → usa IP
	if key.Value == "" {
		key = *entity.NewLimiterKey("ip", ip)
		cfg = &entity.TokenConfig{
			MaxRequests:            uc.ipMaxReq,
			TimeWindowSeconds:      uc.ipWindowSec,
			BlockedDurationSeconds: uc.ipBlockSec,
		}
	}

	// 3. Verifica bloqueio
	blocked, remainingSec, err := uc.store.IsBlocked(key.String())
	if err != nil {
		return false, "error checking blocked", 0, 0, err
	}
	if blocked {
		return false, "blocked", 0, remainingSec, nil
	}

	// 4. Adiciona requisição no store
	allowed, remaining, err := uc.store.AddRequest(key.String(), timestamp)
	if err != nil {
		return false, "error adding request", 0, 0, err
	}

	// 5. Se excedeu → aplica bloqueio
	if !allowed {
		if cfg.BlockedDurationSeconds > 0 {
			if err := uc.store.Block(key.String(), cfg.BlockedDurationSeconds); err != nil {
				return false, "error applying block", 0, 0, err
			}
		}
		return false, "rate limit exceeded", 0, cfg.BlockedDurationSeconds, nil
	}

	return true, "", remaining, 0, nil
}

// IsBlocked retorna se a chave está bloqueada e o tempo restante
func (uc *rateLimiterUseCase) IsBlocked(key entity.LimiterKey) (bool, int, error) {
	return uc.store.IsBlocked(key.String())
}
