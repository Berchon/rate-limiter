package entity

import "fmt"

// LimiterKey representa uma chave unificada para o rate limiter
// Pode ser baseada em IP ou token, por exemplo: "ip:192.168.0.1" ou "token:abc123"
type LimiterKey struct {
	Prefix string
	Value  string
}

// NewLimiterKey cria uma nova LimiterKey com prefixo e valor
func NewLimiterKey(prefix, value string) *LimiterKey {
	return &LimiterKey{
		Prefix: prefix,
		Value:  value,
	}
}

// String retorna a representação única da chave (ex.: "ip:192.168.0.1")
func (k *LimiterKey) String() string {
	return fmt.Sprintf("%s:%s", k.Prefix, k.Value)
}
