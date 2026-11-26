package entity

import "time"

// TokenConfig represents the rate-limit configuration for an API token.
type TokenConfig struct {
	Token                  string    // Generated token identifier
	TimeWindowSeconds      int       // Sliding/fixed window duration in seconds
	MaxRequests            int       // Maximum requests allowed within the window
	BlockedDurationSeconds int       // Block duration in seconds after exceeding the limit
	TokenTTLSeconds        int       // Token time-to-live in seconds
	CreatedAt              time.Time // Creation timestamp
	ExpiresAt              time.Time // Expiration timestamp
}

// NewTokenConfig creates a new TokenConfig instance with computed creation and expiration timestamps.
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

// BlockedDuration returns the block duration as a time.Duration.
func (t *TokenConfig) BlockedDuration() time.Duration {
	return time.Duration(t.BlockedDurationSeconds) * time.Second
}

// TimeWindow returns the rate-limit window as a time.Duration.
func (t *TokenConfig) TimeWindow() time.Duration {
	return time.Duration(t.TimeWindowSeconds) * time.Second
}

// TTL returns the remaining lifetime of the token until ExpiresAt.
func (t *TokenConfig) TTL() time.Duration {
	return time.Until(t.ExpiresAt)
}
