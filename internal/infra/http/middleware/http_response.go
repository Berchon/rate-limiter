package middleware

import (
	"encoding/json"
	"net/http"
)

// WriteRateLimitExceeded envia uma resposta HTTP 429 com corpo JSON padrão
func WriteRateLimitExceeded(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)

	resp := map[string]string{
		"error": "you have reached the maximum number of requests or actions allowed within a certain time frame",
	}

	json.NewEncoder(w).Encode(resp)
}
