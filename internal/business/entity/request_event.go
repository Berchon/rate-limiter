package entity

import "time"

// RequestEvent representa um evento de requisição para logs ou testes
type RequestEvent struct {
	Key       *LimiterKey
	Timestamp time.Time
	Success   bool
}

// NewRequestEvent cria um novo RequestEvent
func NewRequestEvent(key *LimiterKey, success bool) *RequestEvent {
	return &RequestEvent{
		Key:       key,
		Timestamp: time.Now(),
		Success:   success,
	}
}
