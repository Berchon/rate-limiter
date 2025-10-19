package config

import (
	"fmt"
	"log"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

type Config struct {
	AppPort                 string
	RedisHost               string
	RedisPort               string
	RedisDB                 int
	RedisPassword           string
	RateByIPTimeWindow      int
	RateByIPMaxRequests     int
	RateByIPBlockedDuration int
	EnableTokenLimiting     bool
	DefaultTokenTTL         int
}

type ConfigService interface {
	Load() (*Config, error)
}

type configService struct{}

func NewConfigService() ConfigService {
	return &configService{}
}

func (c *configService) Load() (*Config, error) {
	if err := godotenv.Load(".env"); err != nil {
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
