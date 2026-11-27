package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/dto"
	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/usecase"
)

const maxAPIKeyRequestBodyBytes = 1 << 20 // 1 MiB

// CreateAPIKeyHandler handles HTTP requests for generating rate-limited API keys.
type CreateAPIKeyHandler interface {
	HTTPHandler
}

type createAPIKeyHandler struct {
	usecase  usecase.RateLimiterUseCase
	response *ResponseHandler
	logger   *slog.Logger
}

// NewCreateAPIKeyHandler creates a new CreateAPIKeyHandler with the given use case and logger.
func NewCreateAPIKeyHandler(uc usecase.RateLimiterUseCase, logger *slog.Logger) CreateAPIKeyHandler {
	if logger == nil {
		logger = slog.Default()
	}

	return &createAPIKeyHandler{
		usecase:  uc,
		response: NewResponseHandler(logger),
		logger:   logger,
	}
}

// Handle processes POST /api-key requests, validating rate-limit parameters and returning the generated token.
func (h *createAPIKeyHandler) Handle(w http.ResponseWriter, r *http.Request) {
	req, err := decodeCreateAPIKeyRequest(r)
	if err != nil {
		h.writeError(w, r, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := validateCreateAPIKeyRequest(req); err != nil {
		h.writeError(w, r, "invalid parameters", http.StatusBadRequest)
		return
	}

	tokenCfg, err := h.createToken(r.Context(), req)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "failed to create api token", "error", err)
		h.writeError(w, r, "failed to create token", http.StatusInternalServerError)
		return
	}

	h.response.RequestResponse(w, r, toCreateAPIKeyResponse(tokenCfg), http.StatusCreated)
}

func (h *createAPIKeyHandler) createToken(ctx context.Context, req dto.CreateAPIKeyRequest) (*entity.TokenConfig, error) {
	if h.usecase == nil {
		return nil, errors.New("rate limiter use case is nil")
	}

	return h.usecase.CreateToken(ctx, req.TimeWindow, req.MaxRequests, req.BlockedDuration, req.TokenTTL)
}

func (h *createAPIKeyHandler) writeError(w http.ResponseWriter, r *http.Request, message string, statusCode int) {
	h.response.RequestResponse(w, r, errorResponse{Error: message}, statusCode)
}

func decodeCreateAPIKeyRequest(r *http.Request) (dto.CreateAPIKeyRequest, error) {
	if r.Body == nil {
		return dto.CreateAPIKeyRequest{}, errors.New("request body is nil")
	}

	var req dto.CreateAPIKeyRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAPIKeyRequestBodyBytes)).Decode(&req); err != nil {
		return dto.CreateAPIKeyRequest{}, err
	}

	return req, nil
}

func validateCreateAPIKeyRequest(req dto.CreateAPIKeyRequest) error {
	if req.TimeWindow <= 0 || req.MaxRequests <= 0 || req.BlockedDuration <= 0 || req.TokenTTL < 0 {
		return errors.New("invalid rate limit parameters")
	}

	return nil
}

func toCreateAPIKeyResponse(cfg *entity.TokenConfig) dto.CreateAPIKeyResponse {
	return dto.CreateAPIKeyResponse{
		Token:           cfg.Token,
		TimeWindow:      cfg.TimeWindowSeconds,
		MaxRequests:     cfg.MaxRequests,
		BlockedDuration: cfg.BlockedDurationSeconds,
		CreatedAt:       cfg.CreatedAt.Format(time.RFC3339),
		ExpiresAt:       resolveTokenExpiration(cfg).Format(time.RFC3339),
	}
}

func resolveTokenExpiration(cfg *entity.TokenConfig) time.Time {
	if !cfg.ExpiresAt.IsZero() {
		return cfg.ExpiresAt
	}

	return cfg.CreatedAt.Add(time.Duration(cfg.TokenTTLSeconds) * time.Second)
}
