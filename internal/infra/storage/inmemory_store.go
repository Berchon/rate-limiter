// Package storage implements an in-memory rate-limiter persistence store.
package storage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/service"
)

const tokenKeyPrefix = "token"

var _ service.LimiterStore = (*InMemoryStore)(nil)

type counterEntry struct {
	count     int
	expiresAt time.Time
}

type tokenConfigEntry struct {
	cfg       entity.TokenConfig
	expiresAt time.Time
}

// InMemoryStore is a concurrency-safe, in-memory implementation of service.LimiterStore
// that uses lazy expiration instead of background goroutines.
type InMemoryStore struct {
	mu           sync.Mutex
	now          func() time.Time
	counters     map[string]counterEntry
	blocked      map[string]time.Time
	tokenConfigs map[string]tokenConfigEntry
}

// NewInMemoryStore creates a new InMemoryStore backed by the system clock.
func NewInMemoryStore() *InMemoryStore {
	return newInMemoryStoreWithClock(time.Now)
}

func newInMemoryStoreWithClock(now func() time.Time) *InMemoryStore {
	if now == nil {
		now = time.Now
	}
	return &InMemoryStore{
		now:          now,
		counters:     make(map[string]counterEntry),
		blocked:      make(map[string]time.Time),
		tokenConfigs: make(map[string]tokenConfigEntry),
	}
}

// SaveTokenConfig persists a token configuration in memory with lazy TTL expiration.
func (s *InMemoryStore) SaveTokenConfig(ctx context.Context, cfg *entity.TokenConfig) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("saving token config: %w", err)
	}

	rawToken, err := validateTokenConfig(cfg)
	if err != nil {
		return fmt.Errorf("saving token config: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.tokenConfigs[tokenConfigKey(rawToken)] = tokenConfigEntry{
		cfg:       *cfg,
		expiresAt: s.currentTime().Add(secondsToDuration(cfg.TokenTTLSeconds)),
	}
	return nil
}

// GetConfigByToken retrieves a token configuration from memory, returning nil if absent or expired.
func (s *InMemoryStore) GetConfigByToken(ctx context.Context, token string) (*entity.TokenConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("getting token config: %w", err)
	}

	rawToken := normalizeTokenID(token)
	if rawToken == "" {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.activeTokenConfigLocked(tokenConfigKey(rawToken), s.currentTime()), nil
}

// AddRequest atomically evaluates block state, increments the request counter within the window,
// and applies a temporary block if maxRequests is exceeded.
func (s *InMemoryStore) AddRequest(
	ctx context.Context,
	key entity.LimiterKey,
	_ int64,
	maxRequests, timeWindowSec, blockedDuration int,
) (service.AddRequestResult, error) {
	if err := validateContextAndKey(ctx, key); err != nil {
		return service.AddRequestResult{}, fmt.Errorf("adding request: %w", err)
	}
	if err := validateAddRequestArgs(maxRequests, timeWindowSec, blockedDuration); err != nil {
		return service.AddRequestResult{}, fmt.Errorf("adding request: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.currentTime()
	k := key.String()

	if remaining, blocked := s.activeBlockRemainingLocked(k, now); blocked {
		return rejectedResult(remaining), nil
	}

	entry, resetSec := s.incrementCounterLocked(k, now, timeWindowSec)
	if entry.count > maxRequests {
		return s.blockOrRejectLocked(k, now, blockedDuration, resetSec), nil
	}

	return allowedResult(maxRequests-entry.count, resetSec), nil
}

// Block explicitly blocks the given key for durationSec seconds.
func (s *InMemoryStore) Block(ctx context.Context, key entity.LimiterKey, durationSec int) error {
	if err := validateContextAndKey(ctx, key); err != nil {
		return fmt.Errorf("blocking key: %w", err)
	}
	if durationSec <= 0 {
		return fmt.Errorf("blocking key %s: duration must be positive, got %d", key.String(), durationSec)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.setBlockLocked(key.String(), s.currentTime(), durationSec)
	return nil
}

// IsBlocked checks whether the given key is currently blocked and returns its remaining block duration.
func (s *InMemoryStore) IsBlocked(ctx context.Context, key entity.LimiterKey) (service.BlockStatus, error) {
	if err := validateContextAndKey(ctx, key); err != nil {
		return service.BlockStatus{}, fmt.Errorf("checking block status: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if remaining, blocked := s.activeBlockRemainingLocked(key.String(), s.currentTime()); blocked {
		return service.BlockStatus{
			Blocked:          true,
			RemainingSeconds: remaining,
		}, nil
	}
	return service.BlockStatus{}, nil
}

// ResetKey removes the counter, block state, and stored configuration for the given key.
func (s *InMemoryStore) ResetKey(ctx context.Context, key entity.LimiterKey) error {
	if err := validateContextAndKey(ctx, key); err != nil {
		return fmt.Errorf("resetting key: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	k := key.String()
	delete(s.counters, k)
	delete(s.blocked, k)
	delete(s.tokenConfigs, k)
	return nil
}

func (s *InMemoryStore) currentTime() time.Time {
	return s.now()
}

func (s *InMemoryStore) activeTokenConfigLocked(key string, now time.Time) *entity.TokenConfig {
	entry, ok := s.tokenConfigs[key]
	if !ok {
		return nil
	}
	if !now.Before(entry.expiresAt) {
		delete(s.tokenConfigs, key)
		return nil
	}

	cfg := entry.cfg
	return &cfg
}

func (s *InMemoryStore) activeBlockRemainingLocked(key string, now time.Time) (int, bool) {
	blockedUntil, ok := s.blocked[key]
	if !ok {
		return 0, false
	}
	if now.Before(blockedUntil) {
		return durationToSeconds(blockedUntil.Sub(now)), true
	}

	delete(s.blocked, key)
	return 0, false
}

func (s *InMemoryStore) setBlockLocked(key string, now time.Time, durationSec int) {
	s.blocked[key] = now.Add(secondsToDuration(durationSec))
}

func (s *InMemoryStore) incrementCounterLocked(key string, now time.Time, timeWindowSec int) (counterEntry, int64) {
	entry, ok := s.counters[key]
	if !ok || !now.Before(entry.expiresAt) {
		entry = counterEntry{
			count:     0,
			expiresAt: now.Add(secondsToDuration(timeWindowSec)),
		}
	}

	entry.count++
	s.counters[key] = entry

	resetSec := int64(durationToSeconds(entry.expiresAt.Sub(now)))
	return entry, resetSec
}

func (s *InMemoryStore) blockOrRejectLocked(key string, now time.Time, blockedDuration int, resetSec int64) service.AddRequestResult {
	if blockedDuration > 0 {
		s.setBlockLocked(key, now, blockedDuration)
		delete(s.counters, key)
		return rejectedResult(blockedDuration)
	}
	return rejectedResult(int(resetSec))
}

func allowedResult(remaining int, resetSec int64) service.AddRequestResult {
	return service.AddRequestResult{
		Allowed:           true,
		Remaining:         remaining,
		ResetSeconds:      resetSec,
		RetryAfterSeconds: 0,
	}
}

func rejectedResult(retryAfterSec int) service.AddRequestResult {
	return service.AddRequestResult{
		Allowed:           false,
		Remaining:         0,
		ResetSeconds:      int64(retryAfterSec),
		RetryAfterSeconds: retryAfterSec,
	}
}

func secondsToDuration(sec int) time.Duration {
	return time.Duration(sec) * time.Second
}

func durationToSeconds(d time.Duration) int {
	sec := int(math.Ceil(d.Seconds()))
	if sec < 1 {
		return 1
	}
	return sec
}

func validateContextAndKey(ctx context.Context, key entity.LimiterKey) error {
	if err := ctx.Err(); err != nil {
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

func normalizeTokenID(token string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(token), tokenKeyPrefix+":"))
}

func tokenConfigKey(rawToken string) string {
	return entity.NewLimiterKey(tokenKeyPrefix, rawToken).String()
}
