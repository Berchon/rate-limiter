package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/service"
)

type InMemoryStore struct {
	mu           sync.Mutex
	counters     map[string]int
	expirations  map[string]time.Time
	blocked      map[string]time.Time
	tokenConfigs map[string][]byte
}

func NewInMemoryStore() service.LimiterStore {
	return &InMemoryStore{
		counters:     make(map[string]int),
		expirations:  make(map[string]time.Time),
		blocked:      make(map[string]time.Time),
		tokenConfigs: make(map[string][]byte),
	}
}

func (s *InMemoryStore) SaveTokenConfig(_ context.Context, cfg *entity.TokenConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal token config: %w", err)
	}

	s.tokenConfigs[cfg.Token] = data

	go func(token string, ttl int) {
		time.Sleep(time.Duration(ttl) * time.Second)
		s.mu.Lock()
		delete(s.tokenConfigs, token)
		s.mu.Unlock()
	}(cfg.Token, cfg.TokenTTLSeconds)

	return nil
}

func (s *InMemoryStore) GetConfigByToken(_ context.Context, token string) (*entity.TokenConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, ok := s.tokenConfigs[token]
	if !ok {
		return nil, nil
	}

	var cfg entity.TokenConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal token config: %w", err)
	}

	return &cfg, nil
}

func (s *InMemoryStore) AddRequest(_ context.Context, key entity.LimiterKey, timestamp int64, maxRequests, timeWindowSec, blockedDuration int) (service.AddRequestResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	if blockedUntil, ok := s.blocked[key.String()]; ok && now.Before(blockedUntil) {
		remaining := int(blockedUntil.Sub(now).Seconds())
		return service.AddRequestResult{
			Allowed:           false,
			Remaining:         0,
			RetryAfterSeconds: remaining,
		}, nil
	}

	if exp, ok := s.expirations[key.String()]; !ok || now.After(exp) {
		s.counters[key.String()] = 0
		s.expirations[key.String()] = now.Add(time.Duration(timeWindowSec) * time.Second)
	}

	s.counters[key.String()]++

	if s.counters[key.String()] > maxRequests {
		s.blocked[key.String()] = now.Add(time.Duration(blockedDuration) * time.Second)
		return service.AddRequestResult{Allowed: false, Remaining: 0}, nil
	}

	remaining := maxRequests - s.counters[key.String()]
	return service.AddRequestResult{Allowed: true, Remaining: remaining}, nil
}

func (s *InMemoryStore) Block(_ context.Context, key entity.LimiterKey, durationSec int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blocked[key.String()] = time.Now().Add(time.Duration(durationSec) * time.Second)
	return nil
}

func (s *InMemoryStore) IsBlocked(_ context.Context, key entity.LimiterKey) (service.BlockStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if blockedUntil, ok := s.blocked[key.String()]; ok && time.Now().Before(blockedUntil) {
		return service.BlockStatus{Blocked: true, RemainingSeconds: int(blockedUntil.Sub(time.Now()).Seconds())}, nil
	}
	return service.BlockStatus{}, nil
}

func (s *InMemoryStore) ResetKey(_ context.Context, key entity.LimiterKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.counters, key.String())
	delete(s.expirations, key.String())
	delete(s.blocked, key.String())
	delete(s.tokenConfigs, key.String())
	return nil
}
