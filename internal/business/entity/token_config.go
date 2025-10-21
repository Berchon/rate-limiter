package entity

import "time"

// TokenConfig representa a configuração de limite de requisições por token
type TokenConfig struct {
	Token                  string    // token gerado
	TimeWindowSeconds      int       // janela de tempo para contar requisições
	MaxRequests            int       // número máximo de requisições dentro da janela
	BlockedDurationSeconds int       // tempo que o token/IP fica bloqueado se exceder limite
	TokenTTLSeconds        int       // tempo de vida do token
	CreatedAt              time.Time // timestamp de criação
	ExpiresAt              time.Time // timestamp de expiração real
}

// NewTokenConfig cria uma nova instância de TokenConfig
func NewTokenConfig(token string, timeWindow, maxRequests, blockedDuration, tokenTTL int) *TokenConfig {
	now := time.Now()
	return &TokenConfig{
		Token:                  token,
		TimeWindowSeconds:      timeWindow,
		MaxRequests:            maxRequests,
		BlockedDurationSeconds: blockedDuration,
		TokenTTLSeconds:        tokenTTL,
		CreatedAt:              now,
		ExpiresAt:              now.Add(time.Duration(tokenTTL) * time.Second),
	}
}

// BlockedDuration retorna o tempo de bloqueio como time.Duration
func (t *TokenConfig) BlockedDuration() time.Duration {
	return time.Duration(t.BlockedDurationSeconds) * time.Second
}

// TimeWindow retorna a janela de tempo como time.Duration
func (t *TokenConfig) TimeWindow() time.Duration {
	return time.Duration(t.TimeWindowSeconds) * time.Second
}

// TTL retorna o tempo de vida restante do token
func (t *TokenConfig) TTL() time.Duration {
	return time.Until(t.ExpiresAt)
}
