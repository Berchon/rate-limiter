// Package config loads, validates, and exposes application configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

const (
	// StorageTypeRedis selects Redis as the rate-limiter storage backend.
	StorageTypeRedis = "redis"
	// StorageTypeMemory selects in-memory storage as the rate-limiter backend.
	StorageTypeMemory = "memory"
)

// Config holds the application runtime configuration.
type Config struct {
	Port                    string        `mapstructure:"PORT"`
	StorageType             string        `mapstructure:"STORAGE_TYPE"`
	RedisURL                string        `mapstructure:"REDIS_URL"`
	RateByIPTimeWindow      int           `mapstructure:"RATE_BY_IP_TIME_WINDOW"`
	RateByIPMaxRequests     int           `mapstructure:"RATE_BY_IP_MAX_REQUESTS"`
	RateByIPBlockedDuration int           `mapstructure:"RATE_BY_IP_BLOCKED_DURATION"`
	EnableTokenLimiting     bool          `mapstructure:"ENABLE_TOKEN_LIMITING"`
	DefaultTokenTTL         int           `mapstructure:"DEFAULT_TOKEN_TTL"`
	HTTPReadHeaderTimeout   time.Duration `mapstructure:"HTTP_READ_HEADER_TIMEOUT"`
	HTTPReadTimeout         time.Duration `mapstructure:"HTTP_READ_TIMEOUT"`
	HTTPWriteTimeout        time.Duration `mapstructure:"HTTP_WRITE_TIMEOUT"`
	HTTPIdleTimeout         time.Duration `mapstructure:"HTTP_IDLE_TIMEOUT"`
}

// LoadConfig loads configuration from .env and environment variables using the default slog logger.
func LoadConfig() (*Config, error) {
	return LoadConfigWithLogger(slog.Default())
}

// LoadConfigWithLogger loads and validates configuration using the provided structured logger.
func LoadConfigWithLogger(logger *slog.Logger) (*Config, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if err := godotenv.Load(".env"); err != nil {
		logger.Info("no .env file found, reading configuration from environment variables")
	}

	v := viper.New()

	// Defaults aligned with .env.example and safe HTTP server timeouts.
	v.SetDefault("PORT", "8080")
	v.SetDefault("STORAGE_TYPE", StorageTypeRedis)
	v.SetDefault("REDIS_URL", "redis://localhost:6379/0")
	v.SetDefault("RATE_BY_IP_TIME_WINDOW", 5)
	v.SetDefault("RATE_BY_IP_MAX_REQUESTS", 2)
	v.SetDefault("RATE_BY_IP_BLOCKED_DURATION", 60)
	v.SetDefault("ENABLE_TOKEN_LIMITING", true)
	v.SetDefault("DEFAULT_TOKEN_TTL", 40)
	v.SetDefault("HTTP_READ_HEADER_TIMEOUT", 5*time.Second)
	v.SetDefault("HTTP_READ_TIMEOUT", 10*time.Second)
	v.SetDefault("HTTP_WRITE_TIMEOUT", 15*time.Second)
	v.SetDefault("HTTP_IDLE_TIMEOUT", 60*time.Second)

	v.AutomaticEnv()

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	cfg.Port = strings.TrimSpace(cfg.Port)
	cfg.StorageType = strings.ToLower(strings.TrimSpace(cfg.StorageType))
	cfg.RedisURL = strings.TrimSpace(cfg.RedisURL)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	logger.Info("configuration loaded",
		slog.String("port", cfg.Port),
		slog.String("storage_type", cfg.StorageType),
		slog.String("redis_url", MaskRedisURL(cfg.RedisURL)),
		slog.Int("rate_by_ip_time_window", cfg.RateByIPTimeWindow),
		slog.Int("rate_by_ip_max_requests", cfg.RateByIPMaxRequests),
		slog.Int("rate_by_ip_blocked_duration", cfg.RateByIPBlockedDuration),
		slog.Bool("enable_token_limiting", cfg.EnableTokenLimiting),
		slog.Int("default_token_ttl", cfg.DefaultTokenTTL),
		slog.Duration("http_read_header_timeout", cfg.HTTPReadHeaderTimeout),
		slog.Duration("http_read_timeout", cfg.HTTPReadTimeout),
		slog.Duration("http_write_timeout", cfg.HTTPWriteTimeout),
		slog.Duration("http_idle_timeout", cfg.HTTPIdleTimeout),
	)

	return &cfg, nil
}

// Validate checks that all required configuration invariants hold.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config cannot be nil")
	}

	portNum, err := strconv.Atoi(c.Port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return fmt.Errorf("invalid PORT %q: must be an integer between 1 and 65535", c.Port)
	}

	switch c.StorageType {
	case StorageTypeRedis, StorageTypeMemory:
	default:
		return fmt.Errorf("invalid STORAGE_TYPE %q: must be %q or %q", c.StorageType, StorageTypeRedis, StorageTypeMemory)
	}

	if c.StorageType == StorageTypeRedis {
		if err := validateRedisURL(c.RedisURL); err != nil {
			return err
		}
	}

	if c.RateByIPTimeWindow <= 0 {
		return fmt.Errorf("invalid RATE_BY_IP_TIME_WINDOW %d: must be positive", c.RateByIPTimeWindow)
	}
	if c.RateByIPMaxRequests <= 0 {
		return fmt.Errorf("invalid RATE_BY_IP_MAX_REQUESTS %d: must be positive", c.RateByIPMaxRequests)
	}
	if c.RateByIPBlockedDuration <= 0 {
		return fmt.Errorf("invalid RATE_BY_IP_BLOCKED_DURATION %d: must be positive", c.RateByIPBlockedDuration)
	}
	if c.DefaultTokenTTL <= 0 {
		return fmt.Errorf("invalid DEFAULT_TOKEN_TTL %d: must be positive", c.DefaultTokenTTL)
	}

	if c.HTTPReadHeaderTimeout <= 0 {
		return fmt.Errorf("invalid HTTP_READ_HEADER_TIMEOUT %s: must be positive", c.HTTPReadHeaderTimeout)
	}
	if c.HTTPReadTimeout <= 0 {
		return fmt.Errorf("invalid HTTP_READ_TIMEOUT %s: must be positive", c.HTTPReadTimeout)
	}
	if c.HTTPWriteTimeout <= 0 {
		return fmt.Errorf("invalid HTTP_WRITE_TIMEOUT %s: must be positive", c.HTTPWriteTimeout)
	}
	if c.HTTPIdleTimeout <= 0 {
		return fmt.Errorf("invalid HTTP_IDLE_TIMEOUT %s: must be positive", c.HTTPIdleTimeout)
	}

	return nil
}

// MaskRedisURL redacts credentials from a Redis connection URL for safe logging.
func MaskRedisURL(rawURL string) string {
	if strings.TrimSpace(rawURL) == "" {
		return ""
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid>"
	}

	if u.User != nil {
		u.User = url.User("redacted")
	}

	return u.String()
}

func validateRedisURL(rawURL string) error {
	if rawURL == "" {
		return errors.New("invalid REDIS_URL: cannot be empty when STORAGE_TYPE is redis")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid REDIS_URL: %w", err)
	}

	if u.Scheme != "redis" && u.Scheme != "rediss" {
		return fmt.Errorf("invalid REDIS_URL scheme %q: must be redis or rediss", u.Scheme)
	}

	if u.Host == "" {
		return errors.New("invalid REDIS_URL: host cannot be empty")
	}

	return nil
}
