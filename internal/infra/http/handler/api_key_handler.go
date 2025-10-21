package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/dto"
	"github.com/Berchon/rate-limiter/internal/business/usecase"
)

// CreateAPIKeyHandler interface para facilitar testes e mocks
type CreateAPIKeyHandler interface {
	HttpHandler
}

// apiKeyHandler struct concreta
type createApiKeyHandler struct {
	usecase  usecase.RateLimiterUseCase
	response *ResponseHandler
}

// NewCreateAPIKeyHandler construtor
func NewCreateAPIKeyHandler(uc usecase.RateLimiterUseCase) CreateAPIKeyHandler {
	return &createApiKeyHandler{
		usecase:  uc,
		response: NewResponseHandler(),
	}
}

// Handle trata o POST /api-key
func (h *createApiKeyHandler) Handle(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.response.RequestResponse(w, r, "invalid request body", http.StatusBadRequest)
		return
	}

	// validação simples
	if req.TimeWindow <= 0 || req.MaxRequests <= 0 || req.BlockedDuration <= 0 || req.TokenTTL <= 0 {
		h.response.RequestResponse(w, r, "invalid parameters", http.StatusBadRequest)
		return
	}

	// cria o token via usecase
	cfg, err := h.usecase.CreateToken(req.TimeWindow, req.MaxRequests, req.BlockedDuration, req.TokenTTL)
	if err != nil {
		h.response.RequestResponse(w, r, "failed to create token", http.StatusInternalServerError)
		return
	}

	// prepara resposta
	resp := dto.CreateAPIKeyResponse{
		Token:           cfg.Token,
		TimeWindow:      cfg.TimeWindowSeconds,
		MaxRequests:     cfg.MaxRequests,
		BlockedDuration: cfg.BlockedDurationSeconds,
		CreatedAt:       cfg.CreatedAt.Format(time.RFC3339),
		ExpiresAt:       cfg.CreatedAt.Add(time.Duration(cfg.TokenTTLSeconds) * time.Second).Format(time.RFC3339),
	}

	h.response.RequestResponse(w, r, resp, http.StatusCreated)
}
