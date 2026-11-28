package di

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubCloser struct {
	err    error
	closed bool
}

func (s *stubCloser) Close() error {
	s.closed = true
	return s.err
}

func baseConfig(storageType string) *config.Config {
	return &config.Config{
		Port:                    "8080",
		StorageType:             storageType,
		RateLimitAlgorithm:      config.AlgorithmFixedWindow,
		RateByIPTimeWindow:      5,
		RateByIPMaxRequests:     2,
		RateByIPBlockedDuration: 30,
		EnableTokenLimiting:     true,
		DefaultTokenTTL:         60,
	}
}

func TestBuildDependencies(t *testing.T) {
	t.Run("returns error when config is nil", func(t *testing.T) {
		deps, err := BuildDependencies(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

		require.Error(t, err)
		assert.Nil(t, deps)
		assert.Contains(t, err.Error(), "config cannot be nil")
	})

	t.Run("builds dependencies with in-memory storage", func(t *testing.T) {
		cfg := baseConfig(config.StorageTypeMemory)

		deps, err := BuildDependencies(cfg, nil)
		require.NoError(t, err)
		require.NotNil(t, deps)
		assert.NotNil(t, deps.GetHealthHandler)
		assert.NotNil(t, deps.GetPingHandler)
		assert.NotNil(t, deps.CreateAPIKeyHandler)
		assert.NotNil(t, deps.RateLimitMiddleware)
		assert.NoError(t, deps.Close())

		// Smoke test wired CreateAPIKeyHandler backed by InMemoryStore.
		req := httptest.NewRequest(http.MethodPost, "/api-key", strings.NewReader(`{"time_window":5,"max_requests":3,"blocked_duration":10,"token_ttl":0}`))
		rec := httptest.NewRecorder()
		deps.CreateAPIKeyHandler.Handle(rec, req)
		assert.Equal(t, http.StatusCreated, rec.Code)
	})

	t.Run("builds dependencies with redis storage and closes client", func(t *testing.T) {
		mr, err := miniredis.Run()
		require.NoError(t, err)
		defer mr.Close()

		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		cfg := baseConfig(config.StorageTypeRedis)
		cfg.RedisURL = "redis://" + mr.Addr() + "/0"

		deps, err := BuildDependencies(cfg, logger)
		require.NoError(t, err)
		require.NotNil(t, deps)
		assert.NotNil(t, deps.closer)
		assert.Contains(t, logBuf.String(), "redis connection verified")

		// Smoke test wired handler writes to miniredis.
		req := httptest.NewRequest(http.MethodPost, "/api-key", strings.NewReader(`{"time_window":5,"max_requests":3,"blocked_duration":10,"token_ttl":60}`))
		rec := httptest.NewRecorder()
		deps.CreateAPIKeyHandler.Handle(rec, req)
		assert.Equal(t, http.StatusCreated, rec.Code)

		require.NoError(t, deps.Close())
	})

	t.Run("logs warning when redis ping fails at startup without failing build", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		closedAddr := ln.Addr().String()
		require.NoError(t, ln.Close())

		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		cfg := baseConfig(config.StorageTypeRedis)
		cfg.RedisURL = "redis://default:super-secret-pass@" + closedAddr + "/0"

		deps, err := BuildDependencies(cfg, logger)
		require.NoError(t, err)
		require.NotNil(t, deps)
		defer func() {
			require.NoError(t, deps.Close())
		}()

		assert.Contains(t, logBuf.String(), "redis ping failed during startup")
		assert.NotContains(t, logBuf.String(), "super-secret-pass")
	})

	t.Run("returns error on invalid REDIS_URL without leaking credentials", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		cfg := baseConfig(config.StorageTypeRedis)
		cfg.RedisURL = "http://user:top-secret-password@localhost:6379/0"

		deps, err := BuildDependencies(cfg, logger)

		require.Error(t, err)
		assert.Nil(t, deps)
		assert.Contains(t, err.Error(), "invalid REDIS_URL")
		assert.NotContains(t, err.Error(), "top-secret-password")
		assert.NotContains(t, logBuf.String(), "top-secret-password")
	})

	t.Run("wires FailOpen policy into RateLimitMiddleware on redis outage", func(t *testing.T) {
		tests := []struct {
			name       string
			algorithm  string
			failOpen   bool
			wantStatus int
		}{
			{name: "fixed_window fail-closed returns 503", algorithm: config.AlgorithmFixedWindow, failOpen: false, wantStatus: http.StatusServiceUnavailable},
			{name: "fixed_window fail-open allows request with 200", algorithm: config.AlgorithmFixedWindow, failOpen: true, wantStatus: http.StatusOK},
			{name: "sliding_window fail-closed returns 503", algorithm: config.AlgorithmSlidingWindow, failOpen: false, wantStatus: http.StatusServiceUnavailable},
			{name: "sliding_window fail-open allows request with 200", algorithm: config.AlgorithmSlidingWindow, failOpen: true, wantStatus: http.StatusOK},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				mr := miniredis.RunT(t)
				cfg := baseConfig(config.StorageTypeRedis)
				cfg.RedisURL = "redis://" + mr.Addr() + "/0"
				cfg.RateLimitAlgorithm = tc.algorithm
				cfg.FailOpen = tc.failOpen

				deps, err := BuildDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
				require.NoError(t, err)
				t.Cleanup(func() {
					require.NoError(t, deps.Close())
				})

				mr.Close()

				req := httptest.NewRequest(http.MethodGet, "/protected", nil)
				req.RemoteAddr = "192.0.2.10:12345"
				rec := httptest.NewRecorder()

				deps.RateLimitMiddleware.Handle(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				})).ServeHTTP(rec, req)

				assert.Equal(t, tc.wantStatus, rec.Code)
			})
		}
	})

	t.Run("wires sliding_window algorithm into memory and redis backends", func(t *testing.T) {
		mr := miniredis.RunT(t)

		tests := []struct {
			name        string
			storageType string
			redisURL    string
		}{
			{name: "memory", storageType: config.StorageTypeMemory},
			{name: "redis", storageType: config.StorageTypeRedis, redisURL: "redis://" + mr.Addr() + "/0"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				var logBuf bytes.Buffer
				logger := slog.New(slog.NewTextHandler(&logBuf, nil))

				cfg := baseConfig(tc.storageType)
				cfg.RedisURL = tc.redisURL
				cfg.RateLimitAlgorithm = config.AlgorithmSlidingWindow

				deps, err := BuildDependencies(cfg, logger)
				require.NoError(t, err)
				require.NotNil(t, deps)
				t.Cleanup(func() {
					require.NoError(t, deps.Close())
				})

				assert.Contains(t, logBuf.String(), "algorithm=sliding_window")

				handler := deps.RateLimitMiddleware.Handle(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				}))

				for i := range cfg.RateByIPMaxRequests {
					req := httptest.NewRequest(http.MethodGet, "/protected", nil)
					req.RemoteAddr = "198.51.100.10:12345"
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					assert.Equal(t, http.StatusOK, rec.Code, "request %d", i+1)
				}

				req := httptest.NewRequest(http.MethodGet, "/protected", nil)
				req.RemoteAddr = "198.51.100.10:12345"
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				assert.Equal(t, http.StatusTooManyRequests, rec.Code)
			})
		}
	})

	t.Run("defaults empty RATE_LIMIT_ALGORITHM to fixed_window", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		cfg := baseConfig(config.StorageTypeMemory)
		cfg.RateLimitAlgorithm = ""

		deps, err := BuildDependencies(cfg, logger)
		require.NoError(t, err)
		require.NotNil(t, deps)
		require.NoError(t, deps.Close())
		assert.Contains(t, logBuf.String(), "algorithm=fixed_window")
	})

	t.Run("returns error on unsupported RATE_LIMIT_ALGORITHM", func(t *testing.T) {
		cfg := baseConfig(config.StorageTypeMemory)
		cfg.RateLimitAlgorithm = "token_bucket"

		deps, err := BuildDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

		require.Error(t, err)
		assert.Nil(t, deps)
		assert.Contains(t, err.Error(), "unsupported rate-limit algorithm")
	})

	t.Run("returns error on unsupported STORAGE_TYPE", func(t *testing.T) {
		cfg := baseConfig("postgres")

		deps, err := BuildDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

		require.Error(t, err)
		assert.Nil(t, deps)
		assert.Contains(t, err.Error(), "unsupported storage type")
	})
}

func TestDependencies_Close(t *testing.T) {
	t.Run("handles nil dependencies receiver safely", func(t *testing.T) {
		var deps *Dependencies
		assert.NoError(t, deps.Close())
	})

	t.Run("wraps closer error", func(t *testing.T) {
		closer := &stubCloser{err: errors.New("socket close failure")}
		deps := &Dependencies{closer: closer}

		err := deps.Close()

		require.Error(t, err)
		assert.True(t, closer.closed)
		assert.Contains(t, err.Error(), "closing storage client")
		assert.Contains(t, err.Error(), "socket close failure")
	})
}
