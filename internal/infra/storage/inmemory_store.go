package storage

// import (
// 	"fmt"
// 	"sync"
// 	"time"

// 	"github.com/Berchon/rate-limiter/internal/business/entity"
// 	"github.com/Berchon/rate-limiter/internal/business/service"
// )

// // InMemoryStore mantém contadores e bloqueios na memória
// type InMemoryStore struct {
// 	mu          sync.Mutex
// 	counters    map[string]int
// 	expirations map[string]time.Time
// 	blocked     map[string]time.Time
// }

// // NewInMemoryStore cria uma nova instância de InMemoryStore
// func NewInMemoryStore() service.LimiterStore {
// 	return &InMemoryStore{
// 		counters:    make(map[string]int),
// 		expirations: make(map[string]time.Time),
// 		blocked:     make(map[string]time.Time),
// 	}
// }

// // SaveTokenConfig salva a configuração do token no Redis com TTL
// func (s *InMemoryStore) SaveTokenConfig(cfg *entity.TokenConfig) error {
// 	// TODO: Implementar
// 	return nil
// }

// // AddRequest registra uma requisição e retorna se é permitida + quantas restam
// func (s *InMemoryStore) AddRequest(key string, timestamp int64, maxRequests int, timeWindowSec int, blockedDuration int) (bool, int, error) {
// 	fmt.Println("AddRequest called in InMemoryStore")
// 	s.mu.Lock()
// 	defer s.mu.Unlock()

// 	now := time.Now()

// 	// 1. Verifica bloqueio ativo
// 	if blockedUntil, ok := s.blocked[key]; ok && now.Before(blockedUntil) {
// 		remaining := int(blockedUntil.Sub(now).Seconds())
// 		return false, remaining, nil
// 	}

// 	// 2. Limpa contador se a janela expirou
// 	if exp, ok := s.expirations[key]; !ok || now.After(exp) {
// 		s.counters[key] = 0
// 		s.expirations[key] = now.Add(time.Duration(timeWindowSec) * time.Second)
// 	}

// 	// 3. Incrementa contador
// 	s.counters[key]++

// 	// 4. Verifica se ultrapassou o limite
// 	if s.counters[key] > maxRequests {
// 		s.blocked[key] = now.Add(time.Duration(blockedDuration) * time.Second)
// 		return false, 0, nil
// 	}

// 	remaining := maxRequests - s.counters[key]
// 	return true, remaining, nil
// }

// // GetConfigByToken retorna nil (não gerenciado por InMemory)
// func (s *InMemoryStore) GetConfigByToken(token string) (*entity.TokenConfig, error) {
// 	return nil, nil
// }

// // Block bloqueia explicitamente a chave
// func (s *InMemoryStore) Block(key string, durationSec int) error {
// 	s.mu.Lock()
// 	defer s.mu.Unlock()
// 	s.blocked[key] = time.Now().Add(time.Duration(durationSec) * time.Second)
// 	return nil
// }

// // IsBlocked verifica se a chave está bloqueada
// func (s *InMemoryStore) IsBlocked(key string) (bool, int, error) {
// 	s.mu.Lock()
// 	defer s.mu.Unlock()
// 	if blockedUntil, ok := s.blocked[key]; ok && time.Now().Before(blockedUntil) {
// 		return true, int(blockedUntil.Sub(time.Now()).Seconds()), nil
// 	}
// 	return false, 0, nil
// }

// // ResetKey reseta contador e bloqueio da chave
// func (s *InMemoryStore) ResetKey(key string) error {
// 	s.mu.Lock()
// 	defer s.mu.Unlock()
// 	delete(s.counters, key)
// 	delete(s.expirations, key)
// 	delete(s.blocked, key)
// 	return nil
// }
