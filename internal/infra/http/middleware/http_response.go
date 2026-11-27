package middleware

import (
	"encoding/json"
	"net/http"
)

const (
	rateLimitExceededMessage  = "you have reached the maximum number of requests or actions allowed within a certain time frame"
	serviceUnavailableMessage = "service temporarily unavailable"
)

// WriteRateLimitExceeded sends an HTTP 429 Too Many Requests response with the standard JSON error body.
func WriteRateLimitExceeded(w http.ResponseWriter) {
	writeJSONError(w, http.StatusTooManyRequests, rateLimitExceededMessage)
}

// WriteServiceUnavailable sends an HTTP 503 Service Unavailable response with a JSON error body.
func WriteServiceUnavailable(w http.ResponseWriter) {
	writeJSONError(w, http.StatusServiceUnavailable, serviceUnavailableMessage)
}

func writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	resp := map[string]string{
		"error": message,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return
	}
}
