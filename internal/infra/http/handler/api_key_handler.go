package handler

import (
	"encoding/json"
	"net/http"

	"github.com/Berchon/rate-limiter/internal/business/dto"
	"github.com/Berchon/rate-limiter/internal/business/usecase"
)

// APIKeyHandler interface para facilitar testes e mocks
type CreateAPIKeyHandler interface {
	HttpHandler
}

// apiKeyHandler struct concreta
type createApiKeyHandler struct {
	usecase  *usecase.RateLimiterUseCase
	response *ResponseHandler
}

// NewAPIKeyHandler construtor
func NewCreateAPIKeyHandler(usecase *usecase.RateLimiterUseCase) CreateAPIKeyHandler {
	response := NewResponseHandler()
	return &createApiKeyHandler{
		usecase:  usecase,
		response: response,
	}
}

// Create é o handler do POST /api-key
func (h *createApiKeyHandler) Handle(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.response.RequestResponse(w, r, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validar campos mínimos
	if req.TimeWindow <= 0 || req.MaxRequests <= 0 || req.BlockedDuration < 0 {
		h.response.RequestResponse(w, r, "invalid parameters", http.StatusBadRequest)
		return
	}

	// Chama o UseCase para criar o token
	// token, err := h.useCase.CreateToken(req.TimeWindow, req.MaxRequests, req.BlockedDuration, time.Now())
	// if err != nil {
	// 	http.Error(w, "failed to create token", http.StatusInternalServerError)
	// 	h.response.RequestResponse(w, r, err, err.StatusInternalServerError)
	// }

	// resp := dto.CreateAPIKeyResponse{
	// 	Token: token,
	// }

	h.response.RequestResponse(w, r, nil, http.StatusCreated)
}
