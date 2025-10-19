package storage

import (
	"sync"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/service"
)

// InMemoryStore mantém contadores e bloqueios na memória
type InMemoryStore struct {
	mu         sync.Mutex
	counters   map[string]int
	blocked    map[string]time.Time
	defaultTTL time.Duration
}

// NewInMemoryStore cria uma nova instância de InMemoryStore
func NewInMemoryStore(defaultTTL time.Duration) service.LimiterStore {
	return &InMemoryStore{
		counters:   make(map[string]int),
		blocked:    make(map[string]time.Time),
		defaultTTL: defaultTTL,
	}
}

// AddRequest registra uma requisição e retorna se é permitida + quantas restam
func (s *InMemoryStore) AddRequest(key string, timestamp int64) (bool, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if blockedUntil, ok := s.blocked[key]; ok && time.Now().Before(blockedUntil) {
		remaining := int(blockedUntil.Sub(time.Now()).Seconds())
		return false, remaining, nil
	}

	s.counters[key]++
	remaining := int(s.defaultTTL.Seconds()) - s.counters[key]
	allowed := remaining >= 0

	// Configura reset automático
	go func() {
		time.Sleep(s.defaultTTL)
		s.ResetKey(key)
	}()

	return allowed, remaining, nil
}

// GetConfigByToken retorna nil (não gerenciado por InMemory)
func (s *InMemoryStore) GetConfigByToken(token string) (*entity.TokenConfig, error) {
	return nil, nil
}

// Block bloqueia explicitamente a chave
func (s *InMemoryStore) Block(key string, durationSec int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blocked[key] = time.Now().Add(time.Duration(durationSec) * time.Second)
	return nil
}

// IsBlocked verifica se a chave está bloqueada
func (s *InMemoryStore) IsBlocked(key string) (bool, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if blockedUntil, ok := s.blocked[key]; ok && time.Now().Before(blockedUntil) {
		return true, int(blockedUntil.Sub(time.Now()).Seconds()), nil
	}
	return false, 0, nil
}

// ResetKey reseta contador e bloqueio da chave
func (s *InMemoryStore) ResetKey(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.counters, key)
	delete(s.blocked, key)
	return nil
}
