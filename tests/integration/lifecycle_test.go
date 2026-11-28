package integration

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/infra/app"
	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplicationLifecycle(t *testing.T) {
	for _, mc := range strategyMatrix() {
		t.Run(mc.name, func(t *testing.T) {
			port := freePort(t)
			redisURL := ""
			if mc.storageType == config.StorageTypeRedis {
				mr := miniredis.RunT(t)
				redisURL = "redis://" + mr.Addr() + "/0"
			}
			loadConfigWithOptions(t, redisURL, defaultEnvOptions(mc.storageType, mc.algorithm))
			t.Setenv("PORT", port)

			application, err := app.NewAppWithLogger(discardLogger())
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- application.Run(ctx) }()

			base := "http://127.0.0.1:" + port
			waitForServer(t, base+"/health")

			for range ipMaxRequests {
				resp, err := http.Get(base + "/protected")
				require.NoError(t, err)
				_ = resp.Body.Close()
				require.Equal(t, http.StatusOK, resp.StatusCode)
			}

			resp, err := http.Get(base + "/protected")
			require.NoError(t, err)
			_ = resp.Body.Close()
			assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)

			cancel()

			select {
			case err := <-done:
				assert.NoError(t, err)
			case <-time.After(10 * time.Second):
				t.Fatal("application did not shut down gracefully")
			}

			client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
			_, err = client.Get(base + "/health")
			assert.Error(t, err, "server must stop accepting connections after shutdown")
		})
	}
}

func TestApplicationStartupFailures(t *testing.T) {
	tests := []struct {
		name    string
		wantErr string
		env     map[string]string
	}{
		{name: "invalid port", wantErr: "invalid PORT", env: map[string]string{"PORT": "70000"}},
		{name: "unsupported storage type", wantErr: "invalid STORAGE_TYPE", env: map[string]string{"STORAGE_TYPE": "postgres"}},
		{name: "unsupported rate limit algorithm", wantErr: "invalid RATE_LIMIT_ALGORITHM", env: map[string]string{"RATE_LIMIT_ALGORITHM": "token_bucket"}},
		{name: "malformed redis url", wantErr: "invalid REDIS_URL", env: map[string]string{"STORAGE_TYPE": "redis", "REDIS_URL": "http://localhost:6379"}},
		{name: "non-positive rate window", wantErr: "RATE_BY_IP_TIME_WINDOW", env: map[string]string{"RATE_BY_IP_TIME_WINDOW": "0"}},
		{name: "non-positive max requests", wantErr: "RATE_BY_IP_MAX_REQUESTS", env: map[string]string{"RATE_BY_IP_MAX_REQUESTS": "-1"}},
		{name: "non-positive blocked duration", wantErr: "RATE_BY_IP_BLOCKED_DURATION", env: map[string]string{"RATE_BY_IP_BLOCKED_DURATION": "0"}},
		{name: "non-positive default token ttl", wantErr: "DEFAULT_TOKEN_TTL", env: map[string]string{"DEFAULT_TOKEN_TTL": "0"}},
		{name: "malformed fail open bool", wantErr: "failed to unmarshal config", env: map[string]string{"FAIL_OPEN": "not-a-bool"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PORT", "8080")
			t.Setenv("STORAGE_TYPE", config.StorageTypeMemory)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			application, err := app.NewAppWithLogger(discardLogger())

			require.Error(t, err)
			assert.ErrorContains(t, err, tc.wantErr)
			assert.Nil(t, application)
		})
	}
}

func TestHealthEndpointsAndUnknownRoutes(t *testing.T) {
	for _, storageType := range allStorages() {
		t.Run(storageType, func(t *testing.T) {
			env := setupEnv(t, defaultEnvOptions(storageType, config.AlgorithmSlidingWindow))

			for _, path := range []string{"/ping", "/health"} {
				t.Run(path, func(t *testing.T) {
					rr := env.do(t, http.MethodGet, path, "192.0.2.30", "", nil)
					assert.Equal(t, http.StatusOK, rr.Code)
				})
			}

			t.Run("health endpoints are not rate limited", func(t *testing.T) {
				for range ipMaxRequests * 3 {
					rr := env.do(t, http.MethodGet, "/ping", "192.0.2.31", "", nil)
					require.Equal(t, http.StatusOK, rr.Code)
				}
			})

			t.Run("unknown routes and unsupported methods", func(t *testing.T) {
				rr := env.do(t, http.MethodGet, "/does-not-exist", "192.0.2.60", "", nil)
				assert.Equal(t, http.StatusNotFound, rr.Code)

				rr = httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodDelete, "/api-key", nil)
				env.router.ServeHTTP(rr, req)
				assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
			})
		})
	}
}

func TestDefaultConfigLoads(t *testing.T) {
	t.Setenv("STORAGE_TYPE", config.StorageTypeMemory)

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, config.StorageTypeMemory, cfg.StorageType)
	assert.Equal(t, config.AlgorithmFixedWindow, cfg.RateLimitAlgorithm)
	assert.False(t, cfg.FailOpen)
}

func TestApplicationFailsWhenPortIsBusy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	loadConfigWithOptions(t, "", defaultEnvOptions(config.StorageTypeMemory, config.AlgorithmFixedWindow))
	t.Setenv("PORT", strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))

	application, err := app.NewAppWithLogger(discardLogger())
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- application.Run(context.Background()) }()

	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("application kept running despite the busy port")
	}
}
