package handler

import (
	"log/slog"
	"net/http"
)

// GetHealthHandler handles health check requests.
type GetHealthHandler interface {
	HTTPHandler
}

type getHealthHandler struct {
	response *ResponseHandler
}

type statusResponse struct {
	Status string `json:"status"`
}

// NewGetHealthHandler creates a new GetHealthHandler with the provided logger.
func NewGetHealthHandler(logger *slog.Logger) GetHealthHandler {
	return &getHealthHandler{
		response: NewResponseHandler(logger),
	}
}

// Handle responds with the service health status.
func (h *getHealthHandler) Handle(w http.ResponseWriter, r *http.Request) {
	h.response.RequestResponse(w, r, statusResponse{Status: "Healthy"}, http.StatusOK)
}

// GetPingHandler handles ping liveness requests.
type GetPingHandler interface {
	HTTPHandler
}

type getPingHandler struct {
	response *ResponseHandler
}

// NewGetPingHandler creates a new GetPingHandler with the provided logger.
func NewGetPingHandler(logger *slog.Logger) GetPingHandler {
	return &getPingHandler{
		response: NewResponseHandler(logger),
	}
}

// Handle responds with a Pong status payload.
func (h *getPingHandler) Handle(w http.ResponseWriter, r *http.Request) {
	h.response.RequestResponse(w, r, statusResponse{Status: "Pong"}, http.StatusOK)
}
