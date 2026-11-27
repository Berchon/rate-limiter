// Package dto defines data transfer objects for API key HTTP payloads.
package dto

// CreateAPIKeyRequest represents the payload for creating a rate-limited API token.
type CreateAPIKeyRequest struct {
	TimeWindow      int `json:"time_window"`      // Window duration in seconds for counting requests
	MaxRequests     int `json:"max_requests"`     // Maximum number of requests allowed per window
	BlockedDuration int `json:"blocked_duration"` // Duration in seconds to block the key when exceeded
	TokenTTL        int `json:"token_ttl"`        // Token time-to-live in seconds
}

// CreateAPIKeyResponse represents the response returned after creating an API token.
type CreateAPIKeyResponse struct {
	Token           string `json:"token"`
	TimeWindow      int    `json:"time_window"`      // Window duration in seconds for counting requests
	MaxRequests     int    `json:"max_requests"`     // Maximum number of requests allowed per window
	BlockedDuration int    `json:"blocked_duration"` // Duration in seconds to block the key when exceeded
	CreatedAt       string `json:"created_at"`       // Creation timestamp in RFC3339 format
	ExpiresAt       string `json:"expires_at"`       // Expiration timestamp in RFC3339 format
}
