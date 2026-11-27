package routes

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Berchon/rate-limiter/internal/business/dto"
	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/Berchon/rate-limiter/internal/infra/di"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDependencies(t *testing.T, maxRequests int) *di.Dependencies {
	t.Helper()

	cfg := &config.Config{
		Port:                    "8080",
		StorageType:             config.StorageTypeMemory,
		RateByIPTimeWindow:      5,
		RateByIPMaxRequests:     maxRequests,
		RateByIPBlockedDuration: 30,
		EnableTokenLimiting:     true,
		DefaultTokenTTL:         60,
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps, err := di.BuildDependencies(cfg, logger)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, deps.Close())
	})

	return deps
}

type failingResponseWriter struct {
	header     http.Header
	statusCode int
}

func (w *failingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

func (w *failingResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestRegisterRoutes(t *testing.T) {
	t.Run("registers public endpoints and creates router when nil", func(t *testing.T) {
		deps := newTestDependencies(t, 2)
		router := RegisterRoutes(nil, deps)
		require.NotNil(t, router)

		tests := []struct {
			name           string
			method         string
			path           string
			expectedStatus int
			expectedField  string
			expectedValue  string
		}{
			{
				name:           "GET /ping",
				method:         http.MethodGet,
				path:           "/ping",
				expectedStatus: http.StatusOK,
				expectedField:  "status",
				expectedValue:  "Pong",
			},
			{
				name:           "GET /health",
				method:         http.MethodGet,
				path:           "/health",
				expectedStatus: http.StatusOK,
				expectedField:  "status",
				expectedValue:  "Healthy",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				req := httptest.NewRequest(tc.method, tc.path, nil)
				rec := httptest.NewRecorder()

				router.ServeHTTP(rec, req)

				assert.Equal(t, tc.expectedStatus, rec.Code)
				var payload map[string]string
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
				assert.Equal(t, tc.expectedValue, payload[tc.expectedField])
			})
		}
	})

	t.Run("registers POST /api-key", func(t *testing.T) {
		deps := newTestDependencies(t, 2)
		router := RegisterRoutes(nil, deps)

		body := `{"time_window":10,"max_requests":5,"blocked_duration":30,"token_ttl":0}`
		req := httptest.NewRequest(http.MethodPost, "/api-key", strings.NewReader(body))
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusCreated, rec.Code)
		var resp dto.CreateAPIKeyResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp.Token)
		assert.Equal(t, 5, resp.MaxRequests)
	})

	t.Run("protects GET /protected with rate-limit middleware", func(t *testing.T) {
		deps := newTestDependencies(t, 1)
		router := RegisterRoutes(nil, deps)

		req1 := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req1.RemoteAddr = "192.0.2.10:12345"
		rec1 := httptest.NewRecorder()
		router.ServeHTTP(rec1, req1)

		assert.Equal(t, http.StatusOK, rec1.Code)
		assert.Equal(t, "protected content", rec1.Body.String())
		assert.Equal(t, "1", rec1.Header().Get("X-RateLimit-Limit"))
		assert.Equal(t, "0", rec1.Header().Get("X-RateLimit-Remaining"))
		assert.NotEmpty(t, rec1.Header().Get("X-RateLimit-Reset"))

		req2 := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req2.RemoteAddr = "192.0.2.10:12345"
		rec2 := httptest.NewRecorder()
		router.ServeHTTP(rec2, req2)

		assert.Equal(t, http.StatusTooManyRequests, rec2.Code)
		assert.NotEmpty(t, rec2.Header().Get("Retry-After"))
	})

	t.Run("handles write error on GET /protected without panicking", func(t *testing.T) {
		deps := newTestDependencies(t, 5)
		router := RegisterRoutes(nil, deps)

		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.RemoteAddr = "192.0.2.20:12345"
		fw := &failingResponseWriter{}

		router.ServeHTTP(fw, req)

		assert.Equal(t, http.StatusOK, fw.statusCode)
	})
}
