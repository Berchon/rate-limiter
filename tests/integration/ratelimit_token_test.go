package integration

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimitByToken(t *testing.T) {
	for _, mc := range strategyMatrix() {
		t.Run(mc.name, func(t *testing.T) {
			t.Run("token limits take precedence over IP limits", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
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
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
				token := env.createToken(t, 2, 3, 3, 30)

				exhaust(t, env, "192.0.2.21", token, 3)

				assertRateLimited(t, env.get(t, "192.0.2.22", token))
			})

			t.Run("blocked token does not block the IP", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
				token := env.createToken(t, 2, 2, 3, 30)

				exhaust(t, env, "192.0.2.23", token, 2)
				assertRateLimited(t, env.get(t, "192.0.2.23", token))

				assert.Equal(t, http.StatusOK, env.get(t, "192.0.2.23", "").Code)
			})

			t.Run("blocked IP does not block a valid token", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
				token := env.createToken(t, 5, 3, 3, 30)

				exhaust(t, env, "192.0.2.28", "", ipMaxRequests)
				assertRateLimited(t, env.get(t, "192.0.2.28", ""))

				rr := env.get(t, "192.0.2.28", token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "3", rr.Header().Get("X-RateLimit-Limit"))
				assert.Equal(t, "2", rr.Header().Get("X-RateLimit-Remaining"))
			})

			t.Run("normalizes whitespace and repeated token prefixes in API_KEY header", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
				token := env.createToken(t, 10, 2, 3, 30)

				rr := env.get(t, "192.0.2.29", "  "+token+"  ")
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "1", rr.Header().Get("X-RateLimit-Remaining"))

				rr = env.get(t, "192.0.2.29", "token:"+token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "0", rr.Header().Get("X-RateLimit-Remaining"))

				// Neither raw token nor repeated-prefix variations can bypass the active block.
				assertRateLimited(t, env.get(t, "192.0.2.29", token))
				assertRateLimited(t, env.get(t, "192.0.2.29", "token:token:"+token))
				assertRateLimited(t, env.get(t, "192.0.2.29", "  token: token: "+token+"  "))
			})

			t.Run("falls back to IP limits when the token is unknown", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))

				rr := env.get(t, "192.0.2.24", "invalid-token")
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, strconv.Itoa(ipMaxRequests), rr.Header().Get("X-RateLimit-Limit"))

				exhaust(t, env, "192.0.2.24", "invalid-token", ipMaxRequests-1)
				assertRateLimited(t, env.get(t, "192.0.2.24", "invalid-token"))
			})

			t.Run("falls back to IP limits when the token expires", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
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
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
				token := env.createToken(t, 10, 20, 3, 0)

				rr := env.get(t, "192.0.2.26", token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "20", rr.Header().Get("X-RateLimit-Limit"))
			})

			t.Run("ignores the token when token limiting is disabled", func(t *testing.T) {
				opts := defaultEnvOptions(mc.storageType, mc.algorithm)
				opts.enableTokenLimiting = false
				env := setupEnv(t, opts)
				token := env.createToken(t, 10, 50, 3, 30)

				rr := env.get(t, "192.0.2.27", token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, strconv.Itoa(ipMaxRequests), rr.Header().Get("X-RateLimit-Limit"))

				exhaust(t, env, "192.0.2.27", token, ipMaxRequests-1)
				assertRateLimited(t, env.get(t, "192.0.2.27", token))
			})

			t.Run("unblocks token cleanly when time window exceeds block duration", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
				token := env.createToken(t, 10, 2, 1, 30)

				exhaust(t, env, "192.0.2.32", token, 2)
				assertRateLimited(t, env.get(t, "192.0.2.32", token))

				env.advance(expiryMargin(1))

				rr := env.get(t, "192.0.2.32", token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "2", rr.Header().Get("X-RateLimit-Limit"))
				assert.Equal(t, "1", rr.Header().Get("X-RateLimit-Remaining"))
			})
		})
	}
}

func TestCreateAPIKeyValidation(t *testing.T) {
	validBody := map[string]int{"time_window": 5, "max_requests": 5, "blocked_duration": 5, "token_ttl": 10}

	for _, storageType := range allStorages() {
		t.Run(storageType, func(t *testing.T) {
			env := setupEnv(t, defaultEnvOptions(storageType, ""))

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
