// Package config loads and exposes application configuration from environment variables.
package config

import (
	"fmt"
	"log"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Config holds the application runtime configuration.
type Config struct {
	Port                    string `mapstructure:"PORT"`
	RedisURL                string `mapstructure:"REDIS_URL"`
	RateByIPTimeWindow      int    `mapstructure:"RATE_BY_IP_TIME_WINDOW"`
	RateByIPMaxRequests     int    `mapstructure:"RATE_BY_IP_MAX_REQUESTS"`
	RateByIPBlockedDuration int    `mapstructure:"RATE_BY_IP_BLOCKED_DURATION"`
	EnableTokenLimiting     bool   `mapstructure:"ENABLE_TOKEN_LIMITING"`
	DefaultTokenTTL         int    `mapstructure:"DEFAULT_TOKEN_TTL"`
}

// LoadConfig loads configuration from .env and environment variables.
func LoadConfig() (*Config, error) {
	if err := godotenv.Load(".env"); err != nil {
		log.Println("No .env file found, reading configuration from environment variables")
	}

	// Defaults aligned with .env.example
	viper.SetDefault("PORT", "8080")
	viper.SetDefault("REDIS_URL", "redis://localhost:6379/0")
	viper.SetDefault("RATE_BY_IP_TIME_WINDOW", 5)
	viper.SetDefault("RATE_BY_IP_MAX_REQUESTS", 2)
	viper.SetDefault("RATE_BY_IP_BLOCKED_DURATION", 60)
	viper.SetDefault("ENABLE_TOKEN_LIMITING", true)
	viper.SetDefault("DEFAULT_TOKEN_TTL", 40)

	viper.AutomaticEnv()

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	log.Printf("Configuration loaded (port=%s)", cfg.Port)

	return &cfg, nil
}
