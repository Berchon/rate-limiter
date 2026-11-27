package middleware

import (
	"fmt"
	"net"
	stdhttp "net/http"
	"strings"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/usecase"
)

// RateLimitMiddleware define o contrato do middleware de rate limiting.
type RateLimitMiddleware interface {
	Handle(next stdhttp.Handler) stdhttp.Handler
}

// rateLimitMiddleware é a implementação concreta.
type rateLimitMiddleware struct {
	limiter usecase.RateLimiterUseCase
	// cfg     *config.Config
}

// NewRateLimitMiddleware cria uma nova instância do middleware.
func NewRateLimitMiddleware(limiter usecase.RateLimiterUseCase) RateLimitMiddleware {
	return &rateLimitMiddleware{
		limiter: limiter,
		// cfg:     cfg,
	}
}

// Handle aplica a lógica de rate limiting.
func (m *rateLimitMiddleware) Handle(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		ip := extractClientIP(r)
		token := r.Header.Get("API_KEY")
		fmt.Println("RateLimitMiddleware: IP =", ip, "Token =", token)

		decision, err := m.limiter.AllowRequest(r.Context(), ip, token, time.Now())
		if err != nil || decision == nil || !decision.Allowed {
			WriteRateLimitExceeded(w)
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
