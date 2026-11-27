// Package di wires application dependencies across infrastructure and business layers.
package di

import (
	"context"
	"log"

	"github.com/Berchon/rate-limiter/internal/business/usecase"
	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/Berchon/rate-limiter/internal/infra/http/handler"
	"github.com/Berchon/rate-limiter/internal/infra/http/middleware"
	redisStore "github.com/Berchon/rate-limiter/internal/infra/redis"
	redis "github.com/redis/go-redis/v9"
)

// Dependencies holds the wired application handlers and middleware.
type Dependencies struct {
	GetHealthHandler    handler.GetHealthHandler
	GetPingHandler      handler.GetPingHandler
	CreateAPIKeyHandler handler.CreateAPIKeyHandler
	RateLimitMiddleware middleware.RateLimitMiddleware
}

// BuildDependencies initializes storage, use cases, handlers, and middleware.
func BuildDependencies(cfg *config.Config) *Dependencies {
	// --- Storage / LimiterStore ---
	// defaultTTL := time.Duration(cfg.DefaultTokenTTL) * time.Second

	// Alternativa InMemory
	// store := storage.NewInMemoryStore(defaultTTL)

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Fatal("invalid REDIS_URL: check scheme (redis:// or rediss://) and URI format")
	}

	log.Printf("Connecting to Redis at %s", redisOpts.Addr)
	redisClient := redis.NewClient(redisOpts)

	// Cria store Redis
	store := redisStore.NewStore(redisClient)
	pong, err := redisClient.Ping(context.Background()).Result()
	log.Printf("Redis test ping: %s (err=%v)", pong, err)

	// --- UseCases ---
	rateLimiterUC := usecase.NewRateLimiterUseCase(
		store,
		usecase.RateLimiterConfig{
			IPMaxRequests:       cfg.RateByIPMaxRequests,
			IPTimeWindowSec:     cfg.RateByIPTimeWindow,
			IPBlockedDuration:   cfg.RateByIPBlockedDuration,
			EnableTokenLimiting: cfg.EnableTokenLimiting,
			DefaultTokenTTL:     cfg.DefaultTokenTTL,
		},
		nil,
	)

	// --- Handlers ---
	createAPIKeyHandler := handler.NewCreateAPIKeyHandler(rateLimiterUC)
	getHealthHandler := handler.NewGetHealthHandler()
	getPingHandler := handler.NewGetPingHandler()

	// Middleware de rate limit
	rateLimitMiddleware := middleware.NewRateLimitMiddleware(rateLimiterUC)

	return &Dependencies{
		GetHealthHandler:    getHealthHandler,
		GetPingHandler:      getPingHandler,
		CreateAPIKeyHandler: createAPIKeyHandler,
		RateLimitMiddleware: rateLimitMiddleware,
	}
}
