package integration

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimitByIP(t *testing.T) {
	for _, mc := range strategyMatrix() {
		t.Run(mc.name, func(t *testing.T) {
			t.Run("allows requests up to the limit", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
				exhaust(t, env, "192.0.2.1", "", ipMaxRequests)
			})

			t.Run("blocks the IP when the limit is exceeded", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
				exhaust(t, env, "192.0.2.2", "", ipMaxRequests)

				assertRateLimited(t, env.get(t, "192.0.2.2", ""))
				assertRateLimited(t, env.get(t, "192.0.2.2", ""))
			})

			t.Run("isolates different IPs", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
				exhaust(t, env, "192.0.2.3", "", ipMaxRequests)
				assertRateLimited(t, env.get(t, "192.0.2.3", ""))

				assert.Equal(t, http.StatusOK, env.get(t, "192.0.2.4", "").Code)
			})

			t.Run("prefers the first X-Forwarded-For address and trims whitespace", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))

				for range ipMaxRequests {
					req := httptest.NewRequest(http.MethodGet, "/protected", nil)
					req.RemoteAddr = "10.1.1.1:1000"
					req.Header.Set("X-Forwarded-For", " 198.51.100.7 , 10.1.1.1 ")
					rr := httptest.NewRecorder()
					env.router.ServeHTTP(rr, req)
					require.Equal(t, http.StatusOK, rr.Code)
				}

				// Exceeding the limit for the forwarded client blocks it without affecting the proxy IP.
				req := httptest.NewRequest(http.MethodGet, "/protected", nil)
				req.RemoteAddr = "10.1.1.1:1000"
				req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.1.1.1")
				rr := httptest.NewRecorder()
				env.router.ServeHTTP(rr, req)
				assertRateLimited(t, rr)

				assert.Equal(t, http.StatusOK, env.get(t, "10.1.1.1", "").Code)
			})

			t.Run("handles IPv6 clients independently", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))

				exhaust(t, env, "::1", "", ipMaxRequests)
				assertRateLimited(t, env.get(t, "::1", ""))

				assert.Equal(t, http.StatusOK, env.get(t, "2001:db8::1", "").Code)
			})

			t.Run("unblocks the IP after the block duration expires", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
				exhaust(t, env, "192.0.2.5", "", ipMaxRequests)
				assertRateLimited(t, env.get(t, "192.0.2.5", ""))

				env.advance(expiryMargin(ipBlockedDuration))

				assert.Equal(t, http.StatusOK, env.get(t, "192.0.2.5", "").Code)
			})

			t.Run("prevents re-block loop when time window exceeds block duration", func(t *testing.T) {
				opts := defaultEnvOptions(mc.storageType, mc.algorithm)
				opts.ipTimeWindowSec = 10
				opts.ipMaxRequests = 2
				opts.ipBlockedDuration = 1
				env := setupEnv(t, opts)

				exhaust(t, env, "192.0.2.6", "", 2)
				assertRateLimited(t, env.get(t, "192.0.2.6", ""))

				// Advance past the 1s block while still inside the original 10s window.
				env.advance(expiryMargin(1))

				rr := env.get(t, "192.0.2.6", "")
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "2", rr.Header().Get("X-RateLimit-Limit"))
				assert.Equal(t, "1", rr.Header().Get("X-RateLimit-Remaining"))
			})
		})
	}
}

func TestRateLimitHeaders(t *testing.T) {
	for _, mc := range strategyMatrix() {
		t.Run(mc.name, func(t *testing.T) {
			t.Run("allowed responses expose remaining quota", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))

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
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
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
