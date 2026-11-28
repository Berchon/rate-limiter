package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/Berchon/rate-limiter/internal/infra/di"
	"github.com/Berchon/rate-limiter/internal/infra/http/routes"
	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	ipMaxRequests     = 5
	ipTimeWindowSec   = 2
	ipBlockedDuration = 1
	defaultTokenTTL   = 40

	rateLimitMessage          = "you have reached the maximum number of requests or actions allowed within a certain time frame"
	serviceUnavailableMessage = "service temporarily unavailable"
)

// expiryMargin returns how far to advance time so that a TTL of ttlSec seconds has expired.
// Redis TTLs are fast-forwarded instantly; the in-memory store uses the real clock, so a small
// safety margin (200ms) keeps real-clock assertions deterministic.
func expiryMargin(ttlSec int) time.Duration {
	return time.Duration(ttlSec)*time.Second + 200*time.Millisecond
}

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

// matrixCase pairs a storage backend with a rate-limiting algorithm.
type matrixCase struct {
	name        string
	storageType string
	algorithm   string
}

func strategyMatrix() []matrixCase {
	return []matrixCase{
		{name: "redis/fixed_window", storageType: config.StorageTypeRedis, algorithm: config.AlgorithmFixedWindow},
		{name: "redis/sliding_window", storageType: config.StorageTypeRedis, algorithm: config.AlgorithmSlidingWindow},
		{name: "memory/fixed_window", storageType: config.StorageTypeMemory, algorithm: config.AlgorithmFixedWindow},
		{name: "memory/sliding_window", storageType: config.StorageTypeMemory, algorithm: config.AlgorithmSlidingWindow},
	}
}

func allAlgorithms() []string {
	return []string{config.AlgorithmFixedWindow, config.AlgorithmSlidingWindow}
}

func allStorages() []string {
	return []string{config.StorageTypeRedis, config.StorageTypeMemory}
}

type envOptions struct {
	storageType         string
	algorithm           string
	enableTokenLimiting bool
	failOpen            bool
	ipMaxRequests       int
	ipTimeWindowSec     int
	ipBlockedDuration   int
}

func defaultEnvOptions(storageType, algorithm string) envOptions {
	return envOptions{
		storageType:         storageType,
		algorithm:           algorithm,
		enableTokenLimiting: true,
		failOpen:            false,
		ipMaxRequests:       ipMaxRequests,
		ipTimeWindowSec:     ipTimeWindowSec,
		ipBlockedDuration:   ipBlockedDuration,
	}
}

// testEnv bundles a fully wired application router, optional miniredis handle,
// and a clock-advancement function appropriate for the storage backend.
type testEnv struct {
	router  http.Handler
	mr      *miniredis.Miniredis
	advance func(d time.Duration)
}

// loadConfigWithOptions exercises the real configuration loader using environment variables only.
// It relies on t.Setenv, so tests using it must never call t.Parallel.
func loadConfigWithOptions(t *testing.T, redisURL string, opts envOptions) *config.Config {
	t.Helper()

	if opts.algorithm == "" {
		opts.algorithm = config.AlgorithmFixedWindow
	}
	if opts.ipMaxRequests <= 0 {
		opts.ipMaxRequests = ipMaxRequests
	}
	if opts.ipTimeWindowSec <= 0 {
		opts.ipTimeWindowSec = ipTimeWindowSec
	}
	if opts.ipBlockedDuration <= 0 {
		opts.ipBlockedDuration = ipBlockedDuration
	}

	t.Setenv("PORT", "8080")
	t.Setenv("STORAGE_TYPE", opts.storageType)
	t.Setenv("RATE_LIMIT_ALGORITHM", opts.algorithm)
	t.Setenv("REDIS_URL", redisURL)
	t.Setenv("RATE_BY_IP_TIME_WINDOW", strconv.Itoa(opts.ipTimeWindowSec))
	t.Setenv("RATE_BY_IP_MAX_REQUESTS", strconv.Itoa(opts.ipMaxRequests))
	t.Setenv("RATE_BY_IP_BLOCKED_DURATION", strconv.Itoa(opts.ipBlockedDuration))
	t.Setenv("ENABLE_TOKEN_LIMITING", strconv.FormatBool(opts.enableTokenLimiting))
	t.Setenv("DEFAULT_TOKEN_TTL", strconv.Itoa(defaultTokenTTL))
	t.Setenv("FAIL_OPEN", strconv.FormatBool(opts.failOpen))

	cfg, err := config.LoadConfigWithLogger(discardLogger())
	require.NoError(t, err)

	return cfg
}

func buildEnv(t *testing.T, cfg *config.Config, mr *miniredis.Miniredis, advance func(time.Duration)) *testEnv {
	t.Helper()

	deps, err := di.BuildDependencies(cfg, discardLogger())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, deps.Close()) })

	return &testEnv{
		router:  routes.RegisterRoutes(nil, deps),
		mr:      mr,
		advance: advance,
	}
}

func setupEnv(t *testing.T, opts envOptions) *testEnv {
	t.Helper()

	if opts.storageType == config.StorageTypeRedis {
		mr := miniredis.RunT(t)
		cfg := loadConfigWithOptions(t, "redis://"+mr.Addr()+"/0", opts)
		return buildEnv(t, cfg, mr, mr.FastForward)
	}

	cfg := loadConfigWithOptions(t, "", opts)
	return buildEnv(t, cfg, nil, time.Sleep)
}

func (e *testEnv) do(t *testing.T, method, path, ip, apiKey string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(payload)
	}

	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = net.JoinHostPort(ip, "12345")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if apiKey != "" {
		req.Header.Set("API_KEY", apiKey)
	}

	rr := httptest.NewRecorder()
	e.router.ServeHTTP(rr, req)

	return rr
}

func (e *testEnv) get(t *testing.T, ip, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(t, http.MethodGet, "/protected", ip, apiKey, nil)
}

func (e *testEnv) createToken(t *testing.T, timeWindow, maxRequests, blockedDuration, tokenTTL int) string {
	t.Helper()

	rr := e.do(t, http.MethodPost, "/api-key", "10.0.0.1", "", map[string]int{
		"time_window":      timeWindow,
		"max_requests":     maxRequests,
		"blocked_duration": blockedDuration,
		"token_ttl":        tokenTTL,
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	var result map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &result))

	token, ok := result["token"].(string)
	require.True(t, ok, "response must contain a token")
	require.NotEmpty(t, token)

	return token
}

func exhaust(t *testing.T, e *testEnv, ip, apiKey string, n int) {
	t.Helper()

	for i := range n {
		require.Equal(t, http.StatusOK, e.get(t, ip, apiKey).Code, "request %d must be allowed", i+1)
	}
}

func assertRateLimited(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()

	assert.Equal(t, http.StatusTooManyRequests, rr.Code)
	assert.JSONEq(t, `{"error":"`+rateLimitMessage+`"}`, rr.Body.String())
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
}

func assertServiceUnavailable(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()

	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
	assert.JSONEq(t, `{"error":"`+serviceUnavailableMessage+`"}`, rr.Body.String())
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.Empty(t, rr.Header().Get("X-RateLimit-Limit"))
	assert.Empty(t, rr.Header().Get("X-RateLimit-Remaining"))
	assert.Empty(t, rr.Header().Get("X-RateLimit-Reset"))
	assert.Empty(t, rr.Header().Get("Retry-After"))
}

func burst(t *testing.T, env *testEnv, total int, ip, apiKey string) (allowed, rejected int) {
	t.Helper()

	codes := make(chan int, total)
	start := make(chan struct{})

	var wg sync.WaitGroup
	for range total {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.RemoteAddr = net.JoinHostPort(ip, "12345")
			if apiKey != "" {
				req.Header.Set("API_KEY", apiKey)
			}
			rr := httptest.NewRecorder()
			env.router.ServeHTTP(rr, req)
			codes <- rr.Code
		}()
	}

	close(start)
	wg.Wait()
	close(codes)

	for code := range codes {
		switch code {
		case http.StatusOK:
			allowed++
		case http.StatusTooManyRequests:
			rejected++
		default:
			t.Errorf("unexpected status code %d", code)
		}
	}

	return allowed, rejected
}
