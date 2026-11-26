package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Berchon/rate-limiter/internal/business/usecase"
	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/Berchon/rate-limiter/internal/infra/http/handler"
	"github.com/Berchon/rate-limiter/internal/infra/http/middleware"
	redisStore "github.com/Berchon/rate-limiter/internal/infra/redis"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setup(t *testing.T) (*miniredis.Miniredis, http.Handler, http.Handler) {
	os.Setenv("RATE_BY_IP_MAX_REQUESTS", "5")
	os.Setenv("RATE_BY_IP_TIME_WINDOW", "2")
	os.Setenv("RATE_BY_IP_BLOCKED_DURATION", "3")

	t.Helper()

	mr, err := miniredis.Run()
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	cfg, err := config.LoadConfig()
	require.NoError(t, err)

	store := redisStore.NewStore(client)

	rlUseCase := usecase.NewRateLimiterUseCase(
		store,
		cfg.RateByIPMaxRequests,
		cfg.RateByIPTimeWindow,
		cfg.RateByIPBlockedDuration,
	)

	rateLimiter := middleware.NewRateLimitMiddleware(rlUseCase)
	createApiKeyHandler := handler.NewCreateAPIKeyHandler(rlUseCase)
	protectedHandler := rateLimiter.Handle(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	createTokenHandler := http.HandlerFunc(createApiKeyHandler.Handle)

	return mr, protectedHandler, createTokenHandler
}

func CreateToken(t *testing.T, h http.Handler, timeWindow, maxRequests, blockedDuration, tokenTTL int) string {
	body := map[string]interface{}{
		"time_window":      timeWindow,
		"max_requests":     maxRequests,
		"blocked_duration": blockedDuration,
		"token_ttl":        tokenTTL,
	}

	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", "/api-key", bytes.NewBuffer(jsonBody))
	if err != nil {
		panic(err)
	}

	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusCreated, rr.Code)

	var result map[string]interface{}
	err = json.Unmarshal(rr.Body.Bytes(), &result)
	if err != nil {
		assert.Fail(t, "Failed to parse response JSON")
	}

	return result["token"].(string)
}

func TestRateLimitWithoutToken(t *testing.T) {
	mr, middle, _ := setup(t)
	defer mr.Close()

	t.Run("deve permitir acessar o endpoint quando o limite de max_requests não for excedido", func(t *testing.T) {
		ip := "127.0.0.1"

		req, err := http.NewRequest("GET", "/protected", nil)
		req.RemoteAddr = ip
		require.NoError(t, err)

		for i := 0; i < 5; i++ {
			rr := httptest.NewRecorder()
			middle.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code)
		}
	})

	t.Run("deve bloquear o IP quando o limite de max_requests for excedido", func(t *testing.T) {
		ip := "127.0.0.2"

		req, err := http.NewRequest("GET", "/protected", nil)
		req.RemoteAddr = ip
		require.NoError(t, err)

		for i := 0; i < 5; i++ {
			rr := httptest.NewRecorder()
			middle.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code)
		}

		rr := httptest.NewRecorder()
		middle.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusTooManyRequests, rr.Code)
	})
}

func TestRateLimitWithToken(t *testing.T) {
	mr, middle, h := setup(t)
	defer mr.Close()
	t.Run("deve permitir acessar o endpoint quando o limite de max_requests não for excedido", func(t *testing.T) {
		ip := "127.0.0.3"

		token := CreateToken(t, h, 2, 5, 5, 10)

		req, err := http.NewRequest("GET", "/protected", nil)
		require.NoError(t, err)
		req.RemoteAddr = ip
		req.Header.Set("API_KEY", token)

		for i := 0; i < 5; i++ {
			rr := httptest.NewRecorder()
			middle.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code)
		}
	})

	t.Run("deve bloquear o IP quando o limite de max_requests for excedido", func(t *testing.T) {
		ip := "127.0.0.4"

		token := CreateToken(t, h, 2, 5, 5, 10)

		req, err := http.NewRequest("GET", "/protected", nil)
		require.NoError(t, err)
		req.RemoteAddr = ip
		req.Header.Set("API_KEY", token)

		for i := 0; i < 5; i++ {
			rr := httptest.NewRecorder()
			middle.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code)
		}

		rr := httptest.NewRecorder()
		middle.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusTooManyRequests, rr.Code)
	})
}

func TestRateLimitInvalidToken(t *testing.T) {
	mr, middle, _ := setup(t)
	defer mr.Close()
	t.Run("deve permitir acessar o endpoint quando o token é inválido e o limite de request do ip não for excedido", func(t *testing.T) {
		ip := "127.0.0.5"

		token := "invalid-token"

		req, err := http.NewRequest("GET", "/protected", nil)
		require.NoError(t, err)
		req.RemoteAddr = ip
		req.Header.Set("API_KEY", token)

		rr := httptest.NewRecorder()
		middle.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})
}
