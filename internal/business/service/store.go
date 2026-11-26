package service

import (
	"context"

	"github.com/Berchon/rate-limiter/internal/business/entity"
)

// AddRequestResult represents the typed outcome of recording a request in the store.
type AddRequestResult struct {
	Allowed           bool
	Remaining         int
	ResetSeconds      int64
	RetryAfterSeconds int
}

// BlockStatus represents whether a key is currently blocked and its remaining block duration.
type BlockStatus struct {
	Blocked          bool
	RemainingSeconds int
}

// LimiterStore defines the persistence contract for the rate limiter.
type LimiterStore interface {
	// SaveTokenConfig persists a token configuration with its configured TTL.
	SaveTokenConfig(ctx context.Context, cfg *entity.TokenConfig) error

	// AddRequest records a request for the key and returns the typed rate-limit check result.
	AddRequest(ctx context.Context, key entity.LimiterKey, timestamp int64, maxRequests, timeWindowSec, blockedDuration int) (AddRequestResult, error)

	// GetConfigByToken returns the token configuration, or nil if it does not exist or has expired.
	GetConfigByToken(ctx context.Context, token string) (*entity.TokenConfig, error)

	// Block explicitly blocks the key for durationSec seconds.
	Block(ctx context.Context, key entity.LimiterKey, durationSec int) error

	// IsBlocked checks whether the key is currently blocked and returns the remaining block time.
	IsBlocked(ctx context.Context, key entity.LimiterKey) (BlockStatus, error)

	// ResetKey clears counters, block state, and stored configuration for the key.
	ResetKey(ctx context.Context, key entity.LimiterKey) error
}
