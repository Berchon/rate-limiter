package dto

// CreateAPIKeyRequest representa o payload para criar um token de rate limiter.
type CreateAPIKeyRequest struct {
	TimeWindow      int `json:"time_window"`      // em segundos
	MaxRequests     int `json:"max_requests"`     // número máximo de requisições
	BlockedDuration int `json:"blocked_duration"` // duração do bloqueio em segundos
}

// CreateAPIKeyResponse representa a resposta com o token gerado.
type CreateAPIKeyResponse struct {
	Token string `json:"token"`
}
