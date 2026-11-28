// Package redis implements the rate-limiter persistence store backed by Redis.
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/service"
	goredis "github.com/redis/go-redis/v9"
)

const (
	tokenKeyPrefix   = "token"
	blockedKeySuffix = ":blocked"
)

var _ service.LimiterStore = (*Store)(nil)

// Store is a Redis-backed implementation of service.LimiterStore.
type Store struct {
	client   *goredis.Client
	strategy windowStrategy
}

// NewStore creates a new Store backed by the provided Redis client using the fixed-window algorithm.
func NewStore(client *goredis.Client) *Store {
	return &Store{
		client:   client,
		strategy: fixedWindowStrategy{},
	}
}

// NewStoreWithAlgorithm creates a new Store backed by the provided Redis client and rate-limiting algorithm.
func NewStoreWithAlgorithm(client *goredis.Client, alg service.Algorithm) (*Store, error) {
	strategy, err := newWindowStrategy(alg)
	if err != nil {
		return nil, err
	}

	return &Store{
		client:   client,
		strategy: strategy,
	}, nil
}

// SaveTokenConfig persists a token configuration in Redis with its configured TTL.
func (r *Store) SaveTokenConfig(ctx context.Context, cfg *entity.TokenConfig) error {
	if err := r.validateClient(); err != nil {
		return fmt.Errorf("saving token config: %w", err)
	}

	rawToken, err := validateTokenConfig(cfg)
	if err != nil {
		return fmt.Errorf("saving token config: %w", err)
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling token config: %w", err)
	}

	ttl := time.Duration(cfg.TokenTTLSeconds) * time.Second
	if err := r.client.Set(ctx, tokenConfigKey(rawToken), data, ttl).Err(); err != nil {
		return fmt.Errorf("saving token config: %w", err)
	}
	return nil
}

// GetConfigByToken retrieves a token configuration from Redis, returning nil if absent or expired.
func (r *Store) GetConfigByToken(ctx context.Context, token string) (*entity.TokenConfig, error) {
	if err := r.validateClient(); err != nil {
		return nil, fmt.Errorf("getting token config: %w", err)
	}

	rawToken := normalizeTokenID(token)
	if rawToken == "" {
		return nil, nil
	}

	data, err := r.client.Get(ctx, tokenConfigKey(rawToken)).Result()
	if errors.Is(err, goredis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting token config: %w", err)
	}

	var cfg entity.TokenConfig
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling token config: %w", err)
	}
	return &cfg, nil
}

// AddRequest atomically evaluates block state, updates the active rate-limit window,
// and applies a temporary block if maxRequests is exceeded.
func (r *Store) AddRequest(
	ctx context.Context,
	key entity.LimiterKey,
	timestamp int64,
	maxRequests, timeWindowSec, blockedDuration int,
) (service.AddRequestResult, error) {
	if err := r.validateClientAndKey(key); err != nil {
		return service.AddRequestResult{}, fmt.Errorf("adding request: %w", err)
	}
	if err := validateAddRequestArgs(maxRequests, timeWindowSec, blockedDuration); err != nil {
		return service.AddRequestResult{}, fmt.Errorf("adding request: %w", err)
	}

	strategy := r.activeStrategy()
	res, err := strategy.addRequest(ctx, r.client, key, timestamp, maxRequests, timeWindowSec, blockedDuration)
	if err != nil {
		return service.AddRequestResult{}, fmt.Errorf("executing rate-limit script for %s: %w", key.String(), err)
	}

	return parseAddRequestResult(key, res)
}

// Block explicitly blocks the given key for durationSec seconds.
func (r *Store) Block(ctx context.Context, key entity.LimiterKey, durationSec int) error {
	if err := r.validateClientAndKey(key); err != nil {
		return fmt.Errorf("blocking key: %w", err)
	}
	if durationSec <= 0 {
		return fmt.Errorf("blocking key %s: duration must be positive, got %d", key.String(), durationSec)
	}

	ttl := time.Duration(durationSec) * time.Second
	if err := r.client.Set(ctx, blockedKey(key), 1, ttl).Err(); err != nil {
		return fmt.Errorf("blocking key %s: %w", key.String(), err)
	}
	return nil
}

// IsBlocked checks whether the given key is currently blocked and returns its remaining block duration.
func (r *Store) IsBlocked(ctx context.Context, key entity.LimiterKey) (service.BlockStatus, error) {
	if err := r.validateClientAndKey(key); err != nil {
		return service.BlockStatus{}, fmt.Errorf("checking block status: %w", err)
	}

	pttl, err := r.client.PTTL(ctx, blockedKey(key)).Result()
	if err != nil {
		return service.BlockStatus{}, fmt.Errorf("checking block status for %s: %w", key.String(), err)
	}
	if pttl == -1 {
		return service.BlockStatus{
			Blocked:          true,
			RemainingSeconds: 0,
		}, nil
	}
	if pttl > 0 {
		return service.BlockStatus{
			Blocked:          true,
			RemainingSeconds: int(math.Ceil(pttl.Seconds())),
		}, nil
	}
	return service.BlockStatus{}, nil
}

// ResetKey removes the rate-limit window, block state, and stored configuration keys for the given key.
func (r *Store) ResetKey(ctx context.Context, key entity.LimiterKey) error {
	if err := r.validateClientAndKey(key); err != nil {
		return fmt.Errorf("resetting key: %w", err)
	}

	if err := r.client.Del(ctx, countKey(key), logKey(key), blockedKey(key), key.String()).Err(); err != nil {
		return fmt.Errorf("resetting key %s: %w", key.String(), err)
	}
	return nil
}

func (r *Store) activeStrategy() windowStrategy {
	if r.strategy == nil {
		return fixedWindowStrategy{}
	}
	return r.strategy
}

func (r *Store) validateClient() error {
	if r == nil || r.client == nil {
		return errors.New("redis client cannot be nil")
	}
	return nil
}

func (r *Store) validateClientAndKey(key entity.LimiterKey) error {
	if err := r.validateClient(); err != nil {
		return err
	}
	return validateLimiterKey(key)
}

func validateLimiterKey(key entity.LimiterKey) error {
	if strings.TrimSpace(key.Prefix) == "" || strings.TrimSpace(key.Value) == "" {
		return errors.New("limiter key prefix and value must be non-empty")
	}
	return nil
}

func validateTokenConfig(cfg *entity.TokenConfig) (string, error) {
	if cfg == nil {
		return "", errors.New("config cannot be nil")
	}

	rawToken := normalizeTokenID(cfg.Token)
	if rawToken == "" {
		return "", errors.New("token cannot be empty")
	}
	if cfg.TokenTTLSeconds <= 0 {
		return "", fmt.Errorf("token TTL must be positive, got %d", cfg.TokenTTLSeconds)
	}
	return rawToken, nil
}

func validateAddRequestArgs(maxRequests, timeWindowSec, blockedDuration int) error {
	if maxRequests <= 0 {
		return fmt.Errorf("maxRequests must be positive, got %d", maxRequests)
	}
	if timeWindowSec <= 0 {
		return fmt.Errorf("timeWindowSec must be positive, got %d", timeWindowSec)
	}
	if blockedDuration < 0 {
		return fmt.Errorf("blockedDuration cannot be negative, got %d", blockedDuration)
	}
	return nil
}

func parseAddRequestResult(key entity.LimiterKey, res []int64) (service.AddRequestResult, error) {
	if len(res) != 4 {
		return service.AddRequestResult{}, fmt.Errorf("executing rate-limit script for %s: unexpected result length %d", key.String(), len(res))
	}

	return service.AddRequestResult{
		Allowed:           res[0] == 1,
		Remaining:         int(res[1]),
		ResetSeconds:      res[2],
		RetryAfterSeconds: int(res[3]),
	}, nil
}

func normalizeTokenID(token string) string {
	return entity.NormalizeTokenID(token)
}

func tokenConfigKey(rawToken string) string {
	return entity.NewLimiterKey(tokenKeyPrefix, rawToken).String()
}

func blockedKey(key entity.LimiterKey) string {
	return key.String() + blockedKeySuffix
}
