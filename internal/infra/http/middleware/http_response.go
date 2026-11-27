package middleware

import (
	"encoding/json"
	"net/http"
)

const rateLimitExceededMessage = "you have reached the maximum number of requests or actions allowed within a certain time frame"

// WriteRateLimitExceeded sends an HTTP 429 Too Many Requests response with the standard JSON error body.
func WriteRateLimitExceeded(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)

	resp := map[string]string{
		"error": rateLimitExceededMessage,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return
	}
}
