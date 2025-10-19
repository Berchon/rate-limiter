package middleware

import (
	"net"
	stdhttp "net/http"
	"strings"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/usecase"
	"github.com/Berchon/rate-limiter/internal/infra/config"
	infrahttp "github.com/Berchon/rate-limiter/internal/infra/http"
)

// RateLimitMiddleware define o contrato do middleware de rate limiting.
type RateLimitMiddleware interface {
	Handle(next stdhttp.Handler) stdhttp.Handler
}

// rateLimitMiddleware é a implementação concreta.
type rateLimitMiddleware struct {
	limiter usecase.RateLimiterUseCase
	cfg     *config.Config
}

// NewRateLimitMiddleware cria uma nova instância do middleware.
func NewRateLimitMiddleware(limiter usecase.RateLimiterUseCase, cfg *config.Config) RateLimitMiddleware {
	return &rateLimitMiddleware{
		limiter: limiter,
		cfg:     cfg,
	}
}

// Handle aplica a lógica de rate limiting.
func (m *rateLimitMiddleware) Handle(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		ip := extractClientIP(r)
		token := r.Header.Get("API_KEY")

		allowed, _, _, _, err := m.limiter.AllowRequest(ip, token, time.Now())
		if err != nil || !allowed {
			infrahttp.WriteRateLimitExceeded(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// extractClientIP extrai o IP do cliente, considerando proxies e fallback.
func extractClientIP(r *stdhttp.Request) string {
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		parts := strings.Split(xff, ",")
		clientIP := strings.TrimSpace(parts[0])
		if net.ParseIP(clientIP) != nil {
			return clientIP
		}
	}

	// fallback para RemoteAddr (host:port)
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
