package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/infra/app"
	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisOutageFailurePolicy(t *testing.T) {
	for _, alg := range allAlgorithms() {
		t.Run(alg, func(t *testing.T) {
			policies := []struct {
				name     string
				failOpen bool
			}{
				{name: "fail_closed", failOpen: false},
				{name: "fail_open", failOpen: true},
			}

			for _, policy := range policies {
				t.Run(policy.name, func(t *testing.T) {
					t.Run("IP request during Redis outage", func(t *testing.T) {
						opts := defaultEnvOptions(config.StorageTypeRedis, alg)
						opts.failOpen = policy.failOpen
						env := setupEnv(t, opts)

						require.Equal(t, http.StatusOK, env.get(t, "192.0.2.50", "").Code)
						env.mr.Close()

						rr := env.get(t, "192.0.2.50", "")
						if policy.failOpen {
							assert.Equal(t, http.StatusOK, rr.Code)
							assert.Equal(t, "protected content", rr.Body.String())
							assert.Empty(t, rr.Header().Get("X-RateLimit-Limit"))
							assert.Empty(t, rr.Header().Get("X-RateLimit-Remaining"))
							assert.Empty(t, rr.Header().Get("X-RateLimit-Reset"))
							assert.Empty(t, rr.Header().Get("Retry-After"))
						} else {
							assertServiceUnavailable(t, rr)
						}

						// Token creation always fails with 500 when persistence is down; health stays 200.
						rr = env.do(t, http.MethodPost, "/api-key", "192.0.2.50", "", map[string]int{
							"time_window": 5, "max_requests": 5, "blocked_duration": 5, "token_ttl": 5,
						})
						assert.Equal(t, http.StatusInternalServerError, rr.Code)
						assert.Equal(t, http.StatusOK, env.do(t, http.MethodGet, "/health", "192.0.2.50", "", nil).Code)
					})

					t.Run("API_KEY request during Redis outage", func(t *testing.T) {
						opts := defaultEnvOptions(config.StorageTypeRedis, alg)
						opts.failOpen = policy.failOpen
						env := setupEnv(t, opts)

						token := env.createToken(t, 5, 10, 5, 30)
						require.Equal(t, http.StatusOK, env.get(t, "192.0.2.51", token).Code)
						env.mr.Close()

						// Error occurs during token lookup (GetConfigByToken) before AddRequest.
						rr := env.get(t, "192.0.2.51", token)
						if policy.failOpen {
							assert.Equal(t, http.StatusOK, rr.Code)
							assert.Equal(t, "protected content", rr.Body.String())
							assert.Empty(t, rr.Header().Get("X-RateLimit-Limit"))
						} else {
							assertServiceUnavailable(t, rr)
						}
					})
				})
			}
		})
	}
}

func TestApplicationStartsWhenRedisIsUnreachable(t *testing.T) {
	for _, alg := range allAlgorithms() {
		t.Run(alg, func(t *testing.T) {
			cases := []struct {
				name       string
				failOpen   bool
				wantStatus int
			}{
				{name: "fail_closed returns 503", failOpen: false, wantStatus: http.StatusServiceUnavailable},
				{name: "fail_open returns 200", failOpen: true, wantStatus: http.StatusOK},
			}

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					closedRedisPort := freePort(t)
					appPort := freePort(t)

					opts := defaultEnvOptions(config.StorageTypeRedis, alg)
					opts.failOpen = tc.failOpen
					loadConfigWithOptions(t, "redis://127.0.0.1:"+closedRedisPort+"/0", opts)
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
					assert.Equal(t, tc.wantStatus, resp.StatusCode)

					cancel()

					select {
					case err := <-done:
						assert.NoError(t, err)
					case <-time.After(10 * time.Second):
						t.Fatal("application did not shut down gracefully")
					}
				})
			}
		})
	}
}

func TestFailOpenStillEnforcesRateLimitsWhenHealthy(t *testing.T) {
	for _, mc := range strategyMatrix() {
		t.Run(mc.name, func(t *testing.T) {
			opts := defaultEnvOptions(mc.storageType, mc.algorithm)
			opts.failOpen = true
			env := setupEnv(t, opts)

			// IP limit still returns 429 even with FAIL_OPEN=true.
			exhaust(t, env, "192.0.2.55", "", ipMaxRequests)
			assertRateLimited(t, env.get(t, "192.0.2.55", ""))

			// Token limit still returns 429 even with FAIL_OPEN=true.
			token := env.createToken(t, 5, 2, 3, 30)
			exhaust(t, env, "192.0.2.56", token, 2)
			assertRateLimited(t, env.get(t, "192.0.2.56", token))
		})
	}
}
