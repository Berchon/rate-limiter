package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type errorResponse struct {
	Error string `json:"error"`
}

// ResponseHandler encodes JSON HTTP responses consistently across handlers.
type ResponseHandler struct {
	logger *slog.Logger
}

// NewResponseHandler creates a new ResponseHandler with the given structured logger.
func NewResponseHandler(logger *slog.Logger) *ResponseHandler {
	if logger == nil {
		logger = slog.Default()
	}

	return &ResponseHandler{logger: logger}
}

// RequestResponse writes a JSON payload with the given HTTP status code.
func (h *ResponseHandler) RequestResponse(w http.ResponseWriter, r *http.Request, body any, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.ErrorContext(r.Context(), "failed to encode json response", "error", err)
	}
}
