package integration

import (
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlgorithmDifferentialBehavior(t *testing.T) {
	for _, storageType := range allStorages() {
		t.Run(storageType, func(t *testing.T) {
			t.Run("sliding_window prevents boundary bursts across rolling windows", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(storageType, config.AlgorithmSlidingWindow))
				token := env.createToken(t, 2, 2, 1, 30)

				// Request 1 at t=0 -> allowed (1 remaining).
				rr := env.get(t, "192.0.2.71", token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "1", rr.Header().Get("X-RateLimit-Remaining"))

				// Request 2 at t=1.1s -> allowed (0 remaining).
				time.Sleep(1100 * time.Millisecond)
				rr = env.get(t, "192.0.2.71", token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "0", rr.Header().Get("X-RateLimit-Remaining"))

				// At t=2.2s, Request 1 has slid out of the 2s window, but Request 2 (at 1.1s) is still active.
				time.Sleep(1100 * time.Millisecond)
				rr = env.get(t, "192.0.2.71", token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "0", rr.Header().Get("X-RateLimit-Remaining"))

				// Request 4 immediately at t=2.2s must be rejected (2 requests already in [0.2s, 2.2s]).
				assertRateLimited(t, env.get(t, "192.0.2.71", token))
			})

			t.Run("fixed_window resets full quota when discrete window rolls over", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(storageType, config.AlgorithmFixedWindow))
				token := env.createToken(t, 2, 2, 1, 30)

				// Request 1 at t=0 starts the 2s fixed window -> allowed (1 remaining).
				rr := env.get(t, "192.0.2.72", token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "1", rr.Header().Get("X-RateLimit-Remaining"))

				// Request 2 at t=1.1s consumes the remaining slot in the first fixed window.
				env.advance(1100 * time.Millisecond)
				rr = env.get(t, "192.0.2.72", token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "0", rr.Header().Get("X-RateLimit-Remaining"))

				// At t=2.2s, the discrete 2s window has expired, so a brand-new full quota (2) is available.
				env.advance(1100 * time.Millisecond)
				rr = env.get(t, "192.0.2.72", token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "1", rr.Header().Get("X-RateLimit-Remaining"))

				rr = env.get(t, "192.0.2.72", token)
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "0", rr.Header().Get("X-RateLimit-Remaining"))
			})
		})
	}
}

func TestAlgorithmMigrationOnSharedRedisWithoutFlush(t *testing.T) {
	t.Run("switching fixed_window to sliding_window avoids WRONGTYPE on existing keys", func(t *testing.T) {
		mr := miniredis.RunT(t)
		redisURL := "redis://" + mr.Addr() + "/0"

		fixedOpts := defaultEnvOptions(config.StorageTypeRedis, config.AlgorithmFixedWindow)
		fixedOpts.ipMaxRequests = 2
		fixedEnv := buildEnv(t, loadConfigWithOptions(t, redisURL, fixedOpts), mr, mr.FastForward)

		require.Equal(t, http.StatusOK, fixedEnv.get(t, "198.51.100.50", "").Code)
		require.True(t, mr.Exists("ip:198.51.100.50:count"))

		// Switch deployment to sliding_window against the exact same Redis without flushing.
		slidingOpts := defaultEnvOptions(config.StorageTypeRedis, config.AlgorithmSlidingWindow)
		slidingOpts.ipMaxRequests = 2
		slidingEnv := buildEnv(t, loadConfigWithOptions(t, redisURL, slidingOpts), mr, mr.FastForward)

		exhaust(t, slidingEnv, "198.51.100.50", "", 2)
		assertRateLimited(t, slidingEnv.get(t, "198.51.100.50", ""))
	})

	t.Run("switching sliding_window to fixed_window avoids WRONGTYPE on existing keys", func(t *testing.T) {
		mr := miniredis.RunT(t)
		redisURL := "redis://" + mr.Addr() + "/0"

		slidingOpts := defaultEnvOptions(config.StorageTypeRedis, config.AlgorithmSlidingWindow)
		slidingOpts.ipMaxRequests = 2
		slidingEnv := buildEnv(t, loadConfigWithOptions(t, redisURL, slidingOpts), mr, mr.FastForward)

		require.Equal(t, http.StatusOK, slidingEnv.get(t, "198.51.100.51", "").Code)
		require.True(t, mr.Exists("ip:198.51.100.51:log"))

		fixedOpts := defaultEnvOptions(config.StorageTypeRedis, config.AlgorithmFixedWindow)
		fixedOpts.ipMaxRequests = 2
		fixedEnv := buildEnv(t, loadConfigWithOptions(t, redisURL, fixedOpts), mr, mr.FastForward)

		exhaust(t, fixedEnv, "198.51.100.51", "", 2)
		assertRateLimited(t, fixedEnv.get(t, "198.51.100.51", ""))
	})

	t.Run("active block persists across algorithm switch on the same Redis", func(t *testing.T) {
		mr := miniredis.RunT(t)
		redisURL := "redis://" + mr.Addr() + "/0"

		fixedOpts := defaultEnvOptions(config.StorageTypeRedis, config.AlgorithmFixedWindow)
		fixedOpts.ipMaxRequests = 1
		fixedOpts.ipBlockedDuration = 5
		fixedEnv := buildEnv(t, loadConfigWithOptions(t, redisURL, fixedOpts), mr, mr.FastForward)

		require.Equal(t, http.StatusOK, fixedEnv.get(t, "198.51.100.52", "").Code)
		assertRateLimited(t, fixedEnv.get(t, "198.51.100.52", ""))

		slidingOpts := defaultEnvOptions(config.StorageTypeRedis, config.AlgorithmSlidingWindow)
		slidingOpts.ipMaxRequests = 1
		slidingOpts.ipBlockedDuration = 5
		slidingEnv := buildEnv(t, loadConfigWithOptions(t, redisURL, slidingOpts), mr, mr.FastForward)

		assertRateLimited(t, slidingEnv.get(t, "198.51.100.52", ""))

		mr.FastForward(expiryMargin(5))
		assert.Equal(t, http.StatusOK, slidingEnv.get(t, "198.51.100.52", "").Code)
	})
}

func TestNaturalWindowResetWithoutBlocking(t *testing.T) {
	for _, mc := range strategyMatrix() {
		t.Run(mc.name, func(t *testing.T) {
			env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))

			// Consume exact quota without exceeding it (no block triggered).
			exhaust(t, env, "192.0.2.80", "", ipMaxRequests)

			// Wait for the time window to expire naturally.
			env.advance(expiryMargin(ipTimeWindowSec))

			rr := env.get(t, "192.0.2.80", "")
			require.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, strconv.Itoa(ipMaxRequests), rr.Header().Get("X-RateLimit-Limit"))
			assert.Equal(t, strconv.Itoa(ipMaxRequests-1), rr.Header().Get("X-RateLimit-Remaining"))
		})
	}
}

func TestAlgorithmEnvNormalization(t *testing.T) {
	mr := miniredis.RunT(t)
	opts := defaultEnvOptions(config.StorageTypeRedis, "  SLIDING_WINDOW ")
	cfg := loadConfigWithOptions(t, "redis://"+mr.Addr()+"/0", opts)
	require.Equal(t, config.AlgorithmSlidingWindow, cfg.RateLimitAlgorithm)

	env := buildEnv(t, cfg, mr, mr.FastForward)
	require.Equal(t, http.StatusOK, env.get(t, "192.0.2.85", "").Code)
	assert.True(t, mr.Exists("ip:192.0.2.85:log"))
	assert.False(t, mr.Exists("ip:192.0.2.85:count"))
}

func TestConcurrentBurst(t *testing.T) {
	const totalRequests = 50

	for _, mc := range strategyMatrix() {
		t.Run(mc.name, func(t *testing.T) {
			t.Run("exactly the configured limit is allowed per IP", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
				allowed, rejected := burst(t, env, totalRequests, "192.0.2.40", "")

				assert.Equal(t, ipMaxRequests, allowed)
				assert.Equal(t, totalRequests-ipMaxRequests, rejected)
			})

			t.Run("exactly the token limit is allowed per token", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
				token := env.createToken(t, 10, 12, 3, 30)

				allowed, rejected := burst(t, env, totalRequests, "192.0.2.41", token)

				assert.Equal(t, 12, allowed)
				assert.Equal(t, totalRequests-12, rejected)
			})

			t.Run("distinct IPs remain isolated under concurrent load", func(t *testing.T) {
				env := setupEnv(t, defaultEnvOptions(mc.storageType, mc.algorithm))
				const perIP = 25

				var (
					wg                  sync.WaitGroup
					allowedA, rejectedA int
					allowedB, rejectedB int
				)

				wg.Add(2)
				go func() {
					defer wg.Done()
					allowedA, rejectedA = burst(t, env, perIP, "192.0.2.42", "")
				}()
				go func() {
					defer wg.Done()
					allowedB, rejectedB = burst(t, env, perIP, "192.0.2.43", "")
				}()
				wg.Wait()

				assert.Equal(t, ipMaxRequests, allowedA)
				assert.Equal(t, perIP-ipMaxRequests, rejectedA)
				assert.Equal(t, ipMaxRequests, allowedB)
				assert.Equal(t, perIP-ipMaxRequests, rejectedB)
			})
		})
	}
}
