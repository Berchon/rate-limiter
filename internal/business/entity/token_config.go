package entity

import "time"

// TokenConfig representa a configuração de limite de requisições por token
type TokenConfig struct {
	Token                  string
	TimeWindowSeconds      int
	MaxRequests            int
	BlockedDurationSeconds int
	CreatedAt              time.Time
}

// NewTokenConfig cria uma nova instância de TokenConfig
func NewTokenConfig(token string, timeWindow, maxRequests, blockedDuration int) *TokenConfig {
	return &TokenConfig{
		Token:                  token,
		TimeWindowSeconds:      timeWindow,
		MaxRequests:            maxRequests,
		BlockedDurationSeconds: blockedDuration,
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
