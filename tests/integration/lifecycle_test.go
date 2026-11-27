package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
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

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func freePort(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

func waitForServer(t *testing.T, url string) {
	t.Helper()

	require.Eventually(t, func() bool {
		resp, err := http.Get(url)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 25*time.Millisecond, "server did not become ready")
}

func TestApplicationLifecycle(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			port := freePort(t)
			storage, redisURL := config.StorageTypeMemory, ""
			if b.redis {
				mr := miniredis.RunT(t)
				storage, redisURL = config.StorageTypeRedis, "redis://"+mr.Addr()+"/0"
			}
			loadConfig(t, storage, redisURL, true)
			t.Setenv("PORT", port)

			application, err := app.NewAppWithLogger(discardLogger())
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- application.Run(ctx) }()

			base := "http://127.0.0.1:" + port
			waitForServer(t, base+"/health")

			for i := 0; i < ipMaxRequests; i++ {
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
		{name: "malformed redis url", wantErr: "invalid REDIS_URL", env: map[string]string{"STORAGE_TYPE": "redis", "REDIS_URL": "http://localhost:6379"}},
		{name: "non-positive rate window", wantErr: "RATE_BY_IP_TIME_WINDOW", env: map[string]string{"RATE_BY_IP_TIME_WINDOW": "0"}},
		{name: "non-positive max requests", wantErr: "RATE_BY_IP_MAX_REQUESTS", env: map[string]string{"RATE_BY_IP_MAX_REQUESTS": "-1"}},
		{name: "non-positive blocked duration", wantErr: "RATE_BY_IP_BLOCKED_DURATION", env: map[string]string{"RATE_BY_IP_BLOCKED_DURATION": "0"}},
		{name: "non-positive default token ttl", wantErr: "DEFAULT_TOKEN_TTL", env: map[string]string{"DEFAULT_TOKEN_TTL": "0"}},
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

func TestApplicationStartsWhenRedisIsUnreachable(t *testing.T) {
	closedRedisPort := freePort(t)
	appPort := freePort(t)
	loadConfig(t, config.StorageTypeRedis, "redis://127.0.0.1:"+closedRedisPort+"/0", true)
	t.Setenv("PORT", appPort)

	application, err := app.NewAppWithLogger(discardLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()

	base := "http://127.0.0.1:" + appPort
	waitForServer(t, base+"/health")

	resp, err := http.Get(base + "/protected")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("application did not shut down gracefully")
	}
}

func TestRedisOutageFailsClosed(t *testing.T) {
	mr := miniredis.RunT(t)
	cfg := loadConfig(t, config.StorageTypeRedis, "redis://"+mr.Addr()+"/0", true)
	require.False(t, cfg.FailOpen)

	env := buildEnv(t, cfg, mr.FastForward)
	require.Equal(t, http.StatusOK, env.get(t, "192.0.2.50", "").Code)

	mr.Close()

	rr := env.get(t, "192.0.2.50", "")
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.Empty(t, rr.Header().Get("X-RateLimit-Limit"))
	assert.Empty(t, rr.Header().Get("X-RateLimit-Remaining"))
	assert.Empty(t, rr.Header().Get("X-RateLimit-Reset"))
	assert.Empty(t, rr.Header().Get("Retry-After"))

	var payload map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &payload))
	assert.Equal(t, "service temporarily unavailable", payload["error"])

	rr = env.do(t, http.MethodPost, "/api-key", "192.0.2.50", "", map[string]int{
		"time_window": 5, "max_requests": 5, "blocked_duration": 5, "token_ttl": 5,
	})
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestRedisOutageFailsOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	t.Setenv("FAIL_OPEN", "true")
	cfg := loadConfig(t, config.StorageTypeRedis, "redis://"+mr.Addr()+"/0", true)
	require.True(t, cfg.FailOpen)

	env := buildEnv(t, cfg, mr.FastForward)
	require.Equal(t, http.StatusOK, env.get(t, "192.0.2.51", "").Code)

	mr.Close()

	rr := env.get(t, "192.0.2.51", "")
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "protected content", rr.Body.String())
	assert.Empty(t, rr.Header().Get("X-RateLimit-Limit"))
	assert.Empty(t, rr.Header().Get("X-RateLimit-Remaining"))
	assert.Empty(t, rr.Header().Get("X-RateLimit-Reset"))
	assert.Empty(t, rr.Header().Get("Retry-After"))

	rr = env.do(t, http.MethodPost, "/api-key", "192.0.2.51", "", map[string]int{
		"time_window": 5, "max_requests": 5, "blocked_duration": 5, "token_ttl": 5,
	})
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestUnknownRoutesReturnNotFound(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			env := b.setup(t, true)

			rr := env.do(t, http.MethodGet, "/does-not-exist", "192.0.2.60", "", nil)
			assert.Equal(t, http.StatusNotFound, rr.Code)

			rr = httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, "/api-key", nil)
			env.router.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
		})
	}
}
