// Package middleware provides HTTP middlewares for rate limiting and response formatting.
package middleware

import (
	"log/slog"
	"net"
	stdhttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/usecase"
)

const apiKeyHeader = "API_KEY"

// RateLimitMiddleware defines the contract for the HTTP rate-limiting middleware.
type RateLimitMiddleware interface {
	Handle(next stdhttp.Handler) stdhttp.Handler
}

type rateLimitMiddleware struct {
	limiter usecase.RateLimiterUseCase
	logger  *slog.Logger
}

// NewRateLimitMiddleware creates a new RateLimitMiddleware with the given use case and logger.
func NewRateLimitMiddleware(limiter usecase.RateLimiterUseCase, logger *slog.Logger) RateLimitMiddleware {
	if logger == nil {
		logger = slog.Default()
	}

	return &rateLimitMiddleware{
		limiter: limiter,
		logger:  logger,
	}
}

// Handle enforces rate limiting on incoming HTTP requests and emits standard rate-limit headers.
func (m *rateLimitMiddleware) Handle(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		ip := extractClientIP(r)
		token := r.Header.Get(apiKeyHeader)

		decision, err := m.limiter.AllowRequest(r.Context(), ip, token, time.Now())
		if err != nil {
			m.logger.ErrorContext(r.Context(), "rate limit evaluation failed",
				"ip", ip,
				"has_token", token != "",
				"error", err,
			)
			WriteRateLimitExceeded(w)
			return
		}

		if decision == nil {
			m.logger.ErrorContext(r.Context(), "rate limit decision is nil",
				"ip", ip,
				"has_token", token != "",
			)
			WriteRateLimitExceeded(w)
			return
		}

		setRateLimitHeaders(w, decision)

		if !decision.Allowed {
			if decision.RetryAfterSeconds > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(decision.RetryAfterSeconds))
			}

			m.logger.WarnContext(r.Context(), "request rate limited",
				"ip", ip,
				"has_token", token != "",
				"reason", decision.Reason,
				"retry_after_sec", decision.RetryAfterSeconds,
			)
			WriteRateLimitExceeded(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func setRateLimitHeaders(w stdhttp.ResponseWriter, decision *entity.RateLimitDecision) {
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(decision.Limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(decision.Remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(decision.ResetSeconds, 10))
}

// extractClientIP resolves the client IP address from X-Forwarded-For or falls back to RemoteAddr.
func extractClientIP(r *stdhttp.Request) string {
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		parts := strings.Split(xff, ",")
		clientIP := strings.TrimSpace(parts[0])
		if net.ParseIP(clientIP) != nil {
			return clientIP
		}
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
