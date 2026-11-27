package integration

import (
	"bytes"
	"encoding/json"
	"io"
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

	rateLimitMessage = "you have reached the maximum number of requests or actions allowed within a certain time frame"
)

// expiryMargin returns how far to advance time so that a TTL of ttlSec seconds has expired.
// Redis TTLs are fast-forwarded instantly; the in-memory store uses the real clock, so the
// margin is kept small (200ms) to keep the suite fast while still being deterministic.
func expiryMargin(ttlSec int) time.Duration {
	return time.Duration(ttlSec)*time.Second + 200*time.Millisecond
}

// testEnv bundles a fully wired application router and a way to move time forward
// for the selected storage backend.
type testEnv struct {
	router  http.Handler
	advance func(d time.Duration)
}

type backend struct {
	name  string
	redis bool
	setup func(t *testing.T, enableTokenLimiting bool) *testEnv
}

func backends() []backend {
	return []backend{
		{name: "redis", redis: true, setup: newRedisEnv},
		{name: "memory", setup: newMemoryEnv},
	}
}

// loadConfig exercises the real configuration loader using environment variables only.
// It relies on t.Setenv, so tests using it must never call t.Parallel.
func loadConfig(t *testing.T, storageType, redisURL string, enableTokenLimiting bool) *config.Config {
	t.Helper()

	t.Setenv("PORT", "8080")
	t.Setenv("STORAGE_TYPE", storageType)
	t.Setenv("REDIS_URL", redisURL)
	t.Setenv("RATE_BY_IP_TIME_WINDOW", strconv.Itoa(ipTimeWindowSec))
	t.Setenv("RATE_BY_IP_MAX_REQUESTS", strconv.Itoa(ipMaxRequests))
	t.Setenv("RATE_BY_IP_BLOCKED_DURATION", strconv.Itoa(ipBlockedDuration))
	t.Setenv("ENABLE_TOKEN_LIMITING", strconv.FormatBool(enableTokenLimiting))
	t.Setenv("DEFAULT_TOKEN_TTL", strconv.Itoa(defaultTokenTTL))

	cfg, err := config.LoadConfigWithLogger(discardLogger())
	require.NoError(t, err)

	return cfg
}

func buildEnv(t *testing.T, cfg *config.Config, advance func(time.Duration)) *testEnv {
	t.Helper()

	deps, err := di.BuildDependencies(cfg, discardLogger())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, deps.Close()) })

	return &testEnv{router: routes.RegisterRoutes(nil, deps), advance: advance}
}

func newRedisEnv(t *testing.T, enableTokenLimiting bool) *testEnv {
	t.Helper()

	mr := miniredis.RunT(t)
	cfg := loadConfig(t, config.StorageTypeRedis, "redis://"+mr.Addr()+"/0", enableTokenLimiting)

	return buildEnv(t, cfg, mr.FastForward)
}

func newMemoryEnv(t *testing.T, enableTokenLimiting bool) *testEnv {
	t.Helper()

	cfg := loadConfig(t, config.StorageTypeMemory, "", enableTokenLimiting)

	// The in-memory store uses the real clock, so time advances by sleeping.
	return buildEnv(t, cfg, time.Sleep)
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
	req.RemoteAddr = ip + ":12345"
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

	for i := 0; i < n; i++ {
		require.Equal(t, http.StatusOK, e.get(t, ip, apiKey).Code, "request %d must be allowed", i+1)
	}
}

func assertRateLimited(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()

	assert.Equal(t, http.StatusTooManyRequests, rr.Code)
	assert.JSONEq(t, `{"error":"`+rateLimitMessage+`"}`, rr.Body.String())
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
}

func TestRateLimitByIP(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Run("allows requests up to the limit", func(t *testing.T) {
				env := b.setup(t, true)
				exhaust(t, env, "192.0.2.1", "", ipMaxRequests)
			})

			t.Run("blocks the IP when the limit is exceeded", func(t *testing.T) {
				env := b.setup(t, true)
				exhaust(t, env, "192.0.2.2", "", ipMaxRequests)

				assertRateLimited(t, env.get(t, "192.0.2.2", ""))
				assertRateLimited(t, env.get(t, "192.0.2.2", ""))
			})

			t.Run("isolates different IPs", func(t *testing.T) {
				env := b.setup(t, true)
				exhaust(t, env, "192.0.2.3", "", ipMaxRequests)
				assertRateLimited(t, env.get(t, "192.0.2.3", ""))

				assert.Equal(t, http.StatusOK, env.get(t, "192.0.2.4", "").Code)
			})

			t.Run("prefers the first X-Forwarded-For address", func(t *testing.T) {
				env := b.setup(t, true)

				for i := 0; i < ipMaxRequests; i++ {
					req := httptest.NewRequest(http.MethodGet, "/protected", nil)
					req.RemoteAddr = "10.1.1.1:1000"
					req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.1.1.1")
					rr := httptest.NewRecorder()
					env.router.ServeHTTP(rr, req)
					require.Equal(t, http.StatusOK, rr.Code)
				}

				// The proxy address itself is still untouched.
				assert.Equal(t, http.StatusOK, env.get(t, "10.1.1.1", "").Code)
			})

			t.Run("unblocks the IP after the block duration expires", func(t *testing.T) {
				env := b.setup(t, true)
				exhaust(t, env, "192.0.2.5", "", ipMaxRequests)
				assertRateLimited(t, env.get(t, "192.0.2.5", ""))

				env.advance(expiryMargin(ipBlockedDuration))

				assert.Equal(t, http.StatusOK, env.get(t, "192.0.2.5", "").Code)
			})
		})
	}
}

func TestRateLimitHeaders(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Run("allowed responses expose remaining quota", func(t *testing.T) {
				env := b.setup(t, true)

				for i := 1; i <= ipMaxRequests; i++ {
					rr := env.get(t, "192.0.2.10", "")
					require.Equal(t, http.StatusOK, rr.Code)

					assert.Equal(t, strconv.Itoa(ipMaxRequests), rr.Header().Get("X-RateLimit-Limit"))
					assert.Equal(t, strconv.Itoa(ipMaxRequests-i), rr.Header().Get("X-RateLimit-Remaining"))

					reset, err := strconv.Atoi(rr.Header().Get("X-RateLimit-Reset"))
					require.NoError(t, err)
					assert.Positive(t, reset)
					assert.LessOrEqual(t, reset, ipTimeWindowSec)
					assert.Empty(t, rr.Header().Get("Retry-After"))
				}
			})

			t.Run("blocked responses expose Retry-After", func(t *testing.T) {
				env := b.setup(t, true)
				exhaust(t, env, "192.0.2.11", "", ipMaxRequests)

				for _, label := range []string{"first rejection", "subsequent rejection"} {
					rr := env.get(t, "192.0.2.11", "")
					assertRateLimited(t, rr)

					assert.Equal(t, strconv.Itoa(ipMaxRequests), rr.Header().Get("X-RateLimit-Limit"), label)
					assert.Equal(t, "0", rr.Header().Get("X-RateLimit-Remaining"), label)

					retryAfter, err := strconv.Atoi(rr.Header().Get("Retry-After"))
					require.NoError(t, err, label)
					assert.Positive(t, retryAfter, label)
					assert.LessOrEqual(t, retryAfter, ipBlockedDuration, label)
					assert.NotEmpty(t, rr.Header().Get("X-RateLimit-Reset"), label)
				}
			})
		})
	}
}

func TestRateLimitByToken(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Run("token limits take precedence over IP limits", func(t *testing.T) {
				env := b.setup(t, true)
				token := env.createToken(t, 2, 8, 3, 30)

				// 8 requests exceed the IP limit (5) but fit within the token limit (8).
				for i := 1; i <= 8; i++ {
					rr := env.get(t, "192.0.2.20", token)
					require.Equal(t, http.StatusOK, rr.Code, "request %d", i)
					assert.Equal(t, "8", rr.Header().Get("X-RateLimit-Limit"))
					assert.Equal(t, strconv.Itoa(8-i), rr.Header().Get("X-RateLimit-Remaining"))
				}

				rr := env.get(t, "192.0.2.20", token)
				assertRateLimited(t, rr)
				assert.NotEmpty(t, rr.Header().Get("Retry-After"))
			})

			t.Run("token quota is shared across IPs", func(t *testing.T) {
				env := b.setup(t, true)
				token := env.createToken(t, 2, 3, 3, 30)

				exhaust(t, env, "192.0.2.21", token, 3)

				assertRateLimited(t, env.get(t, "192.0.2.22", token))
			})

			t.Run("blocked token does not block the IP", func(t *testing.T) {
				env := b.setup(t, true)
				token := env.createToken(t, 2, 2, 3, 30)

				exhaust(t, env, "192.0.2.23", token, 2)
				assertRateLimited(t, env.get(t, "192.0.2.23", token))

				assert.Equal(t, http.StatusOK, env.get(t, "192.0.2.23", "").Code)
			})

			t.Run("falls back to IP limits when the token is unknown", func(t *testing.T) {
				env := b.setup(t, true)

				rr := env.get(t, "192.0.2.24", "invalid-token")
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, strconv.Itoa(ipMaxRequests), rr.Header().Get("X-RateLimit-Limit"))

				exhaust(t, env, "192.0.2.24", "invalid-token", ipMaxRequests-1)
				assertRateLimited(t, env.get(t, "192.0.2.24", "invalid-token"))
			})

			t.Run("falls back to IP limits when the token expires", func(t *testing.T) {
				env := b.setup(t, true)
				token := env.createToken(t, 10, 20, 3, 1)

				rr := env.get(t, "192.0.2.25", token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "20", rr.Header().Get("X-RateLimit-Limit"))

				env.advance(expiryMargin(1))

				rr = env.get(t, "192.0.2.25", token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, strconv.Itoa(ipMaxRequests), rr.Header().Get("X-RateLimit-Limit"))
			})

			t.Run("token_ttl zero uses the default token TTL", func(t *testing.T) {
				env := b.setup(t, true)
				token := env.createToken(t, 10, 20, 3, 0)

				rr := env.get(t, "192.0.2.26", token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "20", rr.Header().Get("X-RateLimit-Limit"))
			})

			t.Run("ignores the token when token limiting is disabled", func(t *testing.T) {
				env := b.setup(t, false)
				token := env.createToken(t, 10, 50, 3, 30)

				rr := env.get(t, "192.0.2.27", token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, strconv.Itoa(ipMaxRequests), rr.Header().Get("X-RateLimit-Limit"))

				exhaust(t, env, "192.0.2.27", token, ipMaxRequests-1)
				assertRateLimited(t, env.get(t, "192.0.2.27", token))
			})
		})
	}
}

func TestCreateAPIKeyValidation(t *testing.T) {
	validBody := map[string]int{"time_window": 5, "max_requests": 5, "blocked_duration": 5, "token_ttl": 10}

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			env := b.setup(t, true)

			tests := []struct {
				name   string
				mutate func(map[string]int)
			}{
				{name: "zero time window", mutate: func(m map[string]int) { m["time_window"] = 0 }},
				{name: "zero max requests", mutate: func(m map[string]int) { m["max_requests"] = 0 }},
				{name: "zero blocked duration", mutate: func(m map[string]int) { m["blocked_duration"] = 0 }},
				{name: "negative token ttl", mutate: func(m map[string]int) { m["token_ttl"] = -1 }},
			}

			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					body := map[string]int{}
					for k, v := range validBody {
						body[k] = v
					}
					tc.mutate(body)

					rr := env.do(t, http.MethodPost, "/api-key", "10.0.0.2", "", body)
					assert.Equal(t, http.StatusBadRequest, rr.Code)
				})
			}

			t.Run("malformed JSON body", func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, "/api-key", bytes.NewBufferString("{not-json"))
				rr := httptest.NewRecorder()
				env.router.ServeHTTP(rr, req)

				assert.Equal(t, http.StatusBadRequest, rr.Code)
			})
		})
	}
}

func TestHealthEndpoints(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			env := b.setup(t, true)

			for _, path := range []string{"/ping", "/health"} {
				t.Run(path, func(t *testing.T) {
					rr := env.do(t, http.MethodGet, path, "192.0.2.30", "", nil)
					assert.Equal(t, http.StatusOK, rr.Code)
				})
			}

			t.Run("health endpoints are not rate limited", func(t *testing.T) {
				for i := 0; i < ipMaxRequests*3; i++ {
					rr := env.do(t, http.MethodGet, "/ping", "192.0.2.31", "", nil)
					require.Equal(t, http.StatusOK, rr.Code)
				}
			})
		})
	}
}

func TestConcurrentBurst(t *testing.T) {
	const totalRequests = 50

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Run("exactly the configured limit is allowed per IP", func(t *testing.T) {
				env := b.setup(t, true)
				allowed, rejected := burst(t, env, totalRequests, "192.0.2.40", "")

				assert.Equal(t, ipMaxRequests, allowed)
				assert.Equal(t, totalRequests-ipMaxRequests, rejected)
			})

			t.Run("exactly the token limit is allowed per token", func(t *testing.T) {
				env := b.setup(t, true)
				token := env.createToken(t, 10, 12, 3, 30)

				allowed, rejected := burst(t, env, totalRequests, "192.0.2.41", token)

				assert.Equal(t, 12, allowed)
				assert.Equal(t, totalRequests-12, rejected)
			})
		})
	}
}

func burst(t *testing.T, env *testEnv, total int, ip, apiKey string) (allowed, rejected int) {
	t.Helper()

	codes := make(chan int, total)
	start := make(chan struct{})

	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.RemoteAddr = ip + ":12345"
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
