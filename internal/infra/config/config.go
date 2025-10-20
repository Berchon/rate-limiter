package config

import (
	"fmt"
	"log"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

type Config struct {
	AppPort                 string `mapstructure:"APP_PORT"`
	RedisHost               string `mapstructure:"REDIS_HOST"`
	RedisPort               string `mapstructure:"REDIS_PORT"`
	RedisDB                 int    `mapstructure:"REDIS_DB"`
	RedisPassword           string `mapstructure:"REDIS_PASSWORD"`
	RateByIPTimeWindow      int    `mapstructure:"RATE_BY_IP_TIME_WINDOW"`
	RateByIPMaxRequests     int    `mapstructure:"RATE_BY_IP_MAX_REQUESTS"`
	RateByIPBlockedDuration int    `mapstructure:"RATE_BY_IP_BLOCKED_DURATION"`
	EnableTokenLimiting     bool   `mapstructure:"ENABLE_TOKEN_LIMITING"`
	DefaultTokenTTL         int    `mapstructure:"DEFAULT_TOKEN_TTL"`
}

func LoadConfig() (*Config, error) {
	if err := godotenv.Load("../../.env"); err != nil {
		log.Println("⚠️ No .env file found (probably running in Docker or production)")
	}

	// Defaults
	viper.SetDefault("APP_PORT", "8080")
	viper.SetDefault("REDIS_HOST", "localhost")
	viper.SetDefault("REDIS_PORT", "6379")
	viper.SetDefault("REDIS_DB", 0)
	viper.SetDefault("REDIS_PASSWORD", "")
	viper.SetDefault("RATE_BY_IP_TIME_WINDOW", 60)
	viper.SetDefault("RATE_BY_IP_MAX_REQUESTS", 10)
	viper.SetDefault("RATE_BY_IP_BLOCKED_DURATION", 300)
	viper.SetDefault("ENABLE_TOKEN_LIMITING", true)
	viper.SetDefault("DEFAULT_TOKEN_TTL", 3600)

	viper.AutomaticEnv()

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &cfg, nil
}
