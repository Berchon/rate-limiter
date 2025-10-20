package di

import (
	"fmt"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/usecase"
	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/Berchon/rate-limiter/internal/infra/http/handler"
	"github.com/Berchon/rate-limiter/internal/infra/http/middleware"
	redisStore "github.com/Berchon/rate-limiter/internal/infra/redis"
	redis "github.com/redis/go-redis/v9"
)

type Dependencies struct {
	GetHealthHandler    handler.GetHealthHandler
	GetPingHandler      handler.GetPingHandler
	CreateAPIKeyHandler handler.CreateAPIKeyHandler
	RateLimitMiddleware *middleware.RateLimitMiddleware
}

func BuildDependencies(cfg *config.Config) *Dependencies {
	// --- Storage / LimiterStore ---
	defaultTTL := time.Duration(cfg.DefaultTokenTTL) * time.Second

	// Alternativa InMemory
	// store := storage.NewInMemoryStore(defaultTTL)

	// Cria cliente Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort),
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	// Cria store Redis
	store := redisStore.NewRedisStore(redisClient, defaultTTL)

	// 	// --- UseCases ---
	rateLimiterUC := usecase.NewRateLimiterUseCase(
		store,
		cfg.RateByIPMaxRequests,
		cfg.RateByIPTimeWindow,
		cfg.RateByIPBlockedDuration,
	)

	// --- Handlers ---
	createApiKeyHandler := handler.NewCreateAPIKeyHandler(&rateLimiterUC)
	getHealthHandler := handler.NewGetHealthHandler()
	getPingHandler := handler.NewGetPingHandler()

	// Middleware de rate limit
	rateLimitMiddleware := middleware.NewRateLimitMiddleware(rateLimiterUC)

	return &Dependencies{
		GetHealthHandler:    getHealthHandler,
		GetPingHandler:      getPingHandler,
		CreateAPIKeyHandler: createApiKeyHandler,
		RateLimitMiddleware: &rateLimitMiddleware,
	}
}
