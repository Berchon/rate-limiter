package service

import "github.com/Berchon/rate-limiter/internal/business/entity"

// LimiterStore define a interface de persistência para o rate limiter
type LimiterStore interface {
	// AddRequest registra uma requisição e retorna se é permitida, quantas restam e possível erro
	// Recebe maxRequests, timeWindowSec e blockedDuration para controle correto do rate limiter
	AddRequest(key string, timestamp int64, maxRequests int, timeWindowSec int, blockedDuration int) (allowed bool, remaining int, err error)

	// GetConfigByToken retorna a configuração do token, ou nil se não existir
	GetConfigByToken(token string) (*entity.TokenConfig, error)

	// Block bloqueia explicitamente a chave por durationSec segundos
	Block(key string, durationSec int) error

	// IsBlocked verifica se a chave está bloqueada e quanto tempo resta
	IsBlocked(key string) (blocked bool, remainingBlockSeconds int, err error)

	// ResetKey reseta o contador da chave (útil para testes)
	ResetKey(key string) error
}

// LimiterStoreConfig contém dependências/configurações da implementação concreta
type LimiterStoreConfig struct {
	// Por exemplo: Redis host/port, default TTL, etc.
}

// NewLimiterStore constrói a implementação concreta de LimiterStore
// (por enquanto retorna nil, será implementado no RedisStore ou InMemoryStore)
func NewLimiterStore(cfg *LimiterStoreConfig) LimiterStore {
	return nil
}
