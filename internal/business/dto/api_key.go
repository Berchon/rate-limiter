package dto

// CreateAPIKeyRequest representa o payload para criar um token de rate limiter.
type CreateAPIKeyRequest struct {
	TimeWindow      int `json:"time_window"`      // tempo em segundos considerado para calcular o limite de requisições
	MaxRequests     int `json:"max_requests"`     // número máximo de requisições
	BlockedDuration int `json:"blocked_duration"` // duração do bloqueio em segundos
	TokenTTL        int `json:"token_ttl"`        // tempo de vida do token em segundos
}

// CreateAPIKeyResponse representa a resposta com o token gerado.
type CreateAPIKeyResponse struct {
	Token           string `json:"token"`
	TimeWindow      int    `json:"time_window"`      // tempo em segundos considerado para calcular o limite de requisições
	MaxRequests     int    `json:"max_requests"`     // número máximo de requisições
	BlockedDuration int    `json:"blocked_duration"` // duração do bloqueio em segundos
	CreatedAt       string `json:"created_at"`       // timestamp de criação em RFC3339
	ExpiresAt       string `json:"expires_at"`       // timestamp com o horário de expiração do token em RFC3339
}
