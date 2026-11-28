package config_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_DefaultsAndOverrides(t *testing.T) {
	t.Run("loads defaults when environment variables are not set", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		cfg, err := config.LoadConfigWithLogger(logger)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		assert.Equal(t, "8080", cfg.Port)
		assert.Equal(t, config.StorageTypeRedis, cfg.StorageType)
		assert.Equal(t, config.AlgorithmFixedWindow, cfg.RateLimitAlgorithm)
		assert.Equal(t, "redis://localhost:6379/0", cfg.RedisURL)
		assert.Equal(t, 5, cfg.RateByIPTimeWindow)
		assert.Equal(t, 2, cfg.RateByIPMaxRequests)
		assert.Equal(t, 60, cfg.RateByIPBlockedDuration)
		assert.True(t, cfg.EnableTokenLimiting)
		assert.Equal(t, 40, cfg.DefaultTokenTTL)
		assert.False(t, cfg.FailOpen)
		assert.Equal(t, 5*time.Second, cfg.HTTPReadHeaderTimeout)
		assert.Equal(t, 10*time.Second, cfg.HTTPReadTimeout)
		assert.Equal(t, 15*time.Second, cfg.HTTPWriteTimeout)
		assert.Equal(t, 60*time.Second, cfg.HTTPIdleTimeout)
		assert.Contains(t, buf.String(), "configuration loaded")
		assert.Contains(t, buf.String(), "rate_limit_algorithm=fixed_window")
		assert.Contains(t, buf.String(), "fail_open=false")
	})

	t.Run("overrides defaults from environment variables and masks redis credentials in logs", func(t *testing.T) {
		t.Setenv("PORT", "9090")
		t.Setenv("STORAGE_TYPE", "  MEMORY ")
		t.Setenv("RATE_LIMIT_ALGORITHM", "  SLIDING_WINDOW ")
		testPass := "super-secret-value"
		t.Setenv("REDIS_URL", fmt.Sprintf("rediss://default:%s@redis.example.com:6379/0", testPass))
		t.Setenv("RATE_BY_IP_TIME_WINDOW", "10")
		t.Setenv("RATE_BY_IP_MAX_REQUESTS", "100")
		t.Setenv("RATE_BY_IP_BLOCKED_DURATION", "120")
		t.Setenv("ENABLE_TOKEN_LIMITING", "false")
		t.Setenv("DEFAULT_TOKEN_TTL", "300")
		t.Setenv("FAIL_OPEN", "true")
		t.Setenv("HTTP_READ_HEADER_TIMEOUT", "3s")
		t.Setenv("HTTP_READ_TIMEOUT", "8s")
		t.Setenv("HTTP_WRITE_TIMEOUT", "12s")
		t.Setenv("HTTP_IDLE_TIMEOUT", "45s")

		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		cfg, err := config.LoadConfigWithLogger(logger)
		require.NoError(t, err)

		assert.Equal(t, "9090", cfg.Port)
		assert.Equal(t, config.StorageTypeMemory, cfg.StorageType)
		assert.Equal(t, config.AlgorithmSlidingWindow, cfg.RateLimitAlgorithm)
		assert.Equal(t, 10, cfg.RateByIPTimeWindow)
		assert.Equal(t, 100, cfg.RateByIPMaxRequests)
		assert.Equal(t, 120, cfg.RateByIPBlockedDuration)
		assert.False(t, cfg.EnableTokenLimiting)
		assert.Equal(t, 300, cfg.DefaultTokenTTL)
		assert.True(t, cfg.FailOpen)
		assert.Equal(t, 3*time.Second, cfg.HTTPReadHeaderTimeout)
		assert.Equal(t, 8*time.Second, cfg.HTTPReadTimeout)
		assert.Equal(t, 12*time.Second, cfg.HTTPWriteTimeout)
		assert.Equal(t, 45*time.Second, cfg.HTTPIdleTimeout)

		logOutput := buf.String()
		assert.NotContains(t, logOutput, testPass)
		assert.Contains(t, logOutput, "rediss://redacted@redis.example.com:6379/0")
		assert.Contains(t, logOutput, "rate_limit_algorithm=sliding_window")
		assert.Contains(t, logOutput, "fail_open=true")
	})

	t.Run("LoadConfig works with nil logger fallback via LoadConfig", func(t *testing.T) {
		t.Setenv("STORAGE_TYPE", "memory")
		cfg, err := config.LoadConfig()
		require.NoError(t, err)
		assert.Equal(t, config.StorageTypeMemory, cfg.StorageType)
	})

	t.Run("LoadConfigWithLogger handles nil logger directly without panicking", func(t *testing.T) {
		t.Setenv("STORAGE_TYPE", "memory")
		cfg, err := config.LoadConfigWithLogger(nil)
		require.NoError(t, err)
		assert.Equal(t, config.StorageTypeMemory, cfg.StorageType)
	})

	t.Run("loads from .env file and gives precedence to OS environment variables", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Chdir(tmpDir)

		envContent := "PORT=7070\nSTORAGE_TYPE=memory\nRATE_LIMIT_ALGORITHM=fixed_window\nFAIL_OPEN=false\nRATE_BY_IP_MAX_REQUESTS=50\n"
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".env"), []byte(envContent), 0o600))

		// Simulate PaaS/Docker environment variables overriding .env values.
		t.Setenv("PORT", "10000")
		t.Setenv("RATE_LIMIT_ALGORITHM", "sliding_window")
		t.Setenv("FAIL_OPEN", "true")
		t.Cleanup(func() {
			_ = os.Unsetenv("STORAGE_TYPE")
			_ = os.Unsetenv("RATE_BY_IP_MAX_REQUESTS")
		})

		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		cfg, err := config.LoadConfigWithLogger(logger)
		require.NoError(t, err)

		assert.Equal(t, "10000", cfg.Port)
		assert.Equal(t, config.StorageTypeMemory, cfg.StorageType)
		assert.Equal(t, config.AlgorithmSlidingWindow, cfg.RateLimitAlgorithm)
		assert.True(t, cfg.FailOpen)
		assert.Equal(t, 50, cfg.RateByIPMaxRequests)
		assert.NotContains(t, buf.String(), "no .env file found")
	})

	t.Run("LoadConfigWithLogger returns error on invalid config", func(t *testing.T) {
		t.Setenv("STORAGE_TYPE", "postgres")
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		cfg, err := config.LoadConfigWithLogger(logger)
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "invalid STORAGE_TYPE")
	})

	t.Run("LoadConfigWithLogger returns error when env var has malformed type", func(t *testing.T) {
		t.Setenv("RATE_BY_IP_TIME_WINDOW", "5s")
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		cfg, err := config.LoadConfigWithLogger(logger)
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "failed to unmarshal config")
	})

	t.Run("LoadConfigWithLogger returns error when FAIL_OPEN has malformed boolean", func(t *testing.T) {
		t.Setenv("FAIL_OPEN", "not-a-bool")
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		cfg, err := config.LoadConfigWithLogger(logger)
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "failed to unmarshal config")
	})

	t.Run("LoadConfigWithLogger returns error on invalid RATE_LIMIT_ALGORITHM", func(t *testing.T) {
		t.Setenv("RATE_LIMIT_ALGORITHM", "leaky_bucket")
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		cfg, err := config.LoadConfigWithLogger(logger)
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "invalid RATE_LIMIT_ALGORITHM")
	})
}

func TestConfig_Validate(t *testing.T) {
	validConfig := func() *config.Config {
		return &config.Config{
			Port:                    "8080",
			StorageType:             config.StorageTypeRedis,
			RateLimitAlgorithm:      config.AlgorithmFixedWindow,
			RedisURL:                "redis://localhost:6379/0",
			RateByIPTimeWindow:      5,
			RateByIPMaxRequests:     2,
			RateByIPBlockedDuration: 60,
			EnableTokenLimiting:     true,
			DefaultTokenTTL:         40,
			HTTPReadHeaderTimeout:   5 * time.Second,
			HTTPReadTimeout:         10 * time.Second,
			HTTPWriteTimeout:        15 * time.Second,
			HTTPIdleTimeout:         60 * time.Second,
		}
	}

	tests := []struct {
		name      string
		mutate    func(*config.Config)
		wantErr   string
		nilConfig bool
	}{
		{
			name:    "valid redis config",
			mutate:  func(_ *config.Config) {},
			wantErr: "",
		},
		{
			name: "valid production TLS redis config with query params",
			mutate: func(c *config.Config) {
				c.Port = "10000"
				c.RedisURL = fmt.Sprintf("rediss://default:%s@upstash.io:6379/0?dial_timeout=5s", "tls-pass")
			},
			wantErr: "",
		},
		{
			name: "valid memory config without redis url",
			mutate: func(c *config.Config) {
				c.StorageType = config.StorageTypeMemory
				c.RedisURL = ""
			},
			wantErr: "",
		},
		{
			name:      "nil config",
			nilConfig: true,
			wantErr:   "config cannot be nil",
		},
		{
			name:    "empty port",
			mutate:  func(c *config.Config) { c.Port = "" },
			wantErr: "invalid PORT",
		},
		{
			name:    "zero port",
			mutate:  func(c *config.Config) { c.Port = "0" },
			wantErr: "invalid PORT",
		},
		{
			name:    "non-numeric port",
			mutate:  func(c *config.Config) { c.Port = "abc" },
			wantErr: "invalid PORT",
		},
		{
			name:    "out of range port",
			mutate:  func(c *config.Config) { c.Port = "65536" },
			wantErr: "invalid PORT",
		},
		{
			name:    "invalid storage type",
			mutate:  func(c *config.Config) { c.StorageType = "sqlite" },
			wantErr: "invalid STORAGE_TYPE",
		},
		{
			name:    "valid sliding window algorithm",
			mutate:  func(c *config.Config) { c.RateLimitAlgorithm = config.AlgorithmSlidingWindow },
			wantErr: "",
		},
		{
			name:    "empty rate limit algorithm",
			mutate:  func(c *config.Config) { c.RateLimitAlgorithm = "" },
			wantErr: "invalid RATE_LIMIT_ALGORITHM",
		},
		{
			name:    "invalid rate limit algorithm",
			mutate:  func(c *config.Config) { c.RateLimitAlgorithm = "token_bucket" },
			wantErr: "invalid RATE_LIMIT_ALGORITHM",
		},
		{
			name:    "empty redis url when storage is redis",
			mutate:  func(c *config.Config) { c.RedisURL = "" },
			wantErr: "invalid REDIS_URL",
		},
		{
			name:    "malformed redis url syntax",
			mutate:  func(c *config.Config) { c.RedisURL = "://bad-url" },
			wantErr: "invalid REDIS_URL",
		},
		{
			name:    "invalid redis url scheme",
			mutate:  func(c *config.Config) { c.RedisURL = "http://localhost:6379/0" },
			wantErr: "invalid REDIS_URL scheme",
		},
		{
			name:    "redis url missing host",
			mutate:  func(c *config.Config) { c.RedisURL = "redis:///0" },
			wantErr: "host cannot be empty",
		},
		{
			name:    "non-positive rate by ip time window",
			mutate:  func(c *config.Config) { c.RateByIPTimeWindow = 0 },
			wantErr: "invalid RATE_BY_IP_TIME_WINDOW",
		},
		{
			name:    "non-positive rate by ip max requests",
			mutate:  func(c *config.Config) { c.RateByIPMaxRequests = -1 },
			wantErr: "invalid RATE_BY_IP_MAX_REQUESTS",
		},
		{
			name:    "non-positive rate by ip blocked duration",
			mutate:  func(c *config.Config) { c.RateByIPBlockedDuration = 0 },
			wantErr: "invalid RATE_BY_IP_BLOCKED_DURATION",
		},
		{
			name:    "non-positive default token ttl",
			mutate:  func(c *config.Config) { c.DefaultTokenTTL = 0 },
			wantErr: "invalid DEFAULT_TOKEN_TTL",
		},
		{
			name:    "non-positive http read header timeout",
			mutate:  func(c *config.Config) { c.HTTPReadHeaderTimeout = 0 },
			wantErr: "invalid HTTP_READ_HEADER_TIMEOUT",
		},
		{
			name:    "non-positive http read timeout",
			mutate:  func(c *config.Config) { c.HTTPReadTimeout = 0 },
			wantErr: "invalid HTTP_READ_TIMEOUT",
		},
		{
			name:    "non-positive http write timeout",
			mutate:  func(c *config.Config) { c.HTTPWriteTimeout = 0 },
			wantErr: "invalid HTTP_WRITE_TIMEOUT",
		},
		{
			name:    "non-positive http idle timeout",
			mutate:  func(c *config.Config) { c.HTTPIdleTimeout = 0 },
			wantErr: "invalid HTTP_IDLE_TIMEOUT",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var cfg *config.Config
			if !tc.nilConfig {
				cfg = validConfig()
				tc.mutate(cfg)
			}

			err := cfg.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestMaskRedisURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "empty string",
			raw:  "",
			want: "",
		},
		{
			name: "url without credentials",
			raw:  "redis://localhost:6379/0",
			want: "redis://localhost:6379/0",
		},
		{
			name: "url with user and password",
			raw:  fmt.Sprintf("rediss://default:%s@upstash.io:6379/0", "my-test-pass"),
			want: "rediss://redacted@upstash.io:6379/0",
		},
		{
			name: "url with password only",
			raw:  fmt.Sprintf("redis://:%s@localhost:6379/0", "my-test-pass"),
			want: "redis://redacted@localhost:6379/0",
		},
		{
			name: "invalid url",
			raw:  "://invalid-url",
			want: "<invalid>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, config.MaskRedisURL(tc.raw))
		})
	}
}
