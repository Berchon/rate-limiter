// Package di wires application dependencies across infrastructure and business layers.
package di

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/service"
	"github.com/Berchon/rate-limiter/internal/business/usecase"
	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/Berchon/rate-limiter/internal/infra/http/handler"
	"github.com/Berchon/rate-limiter/internal/infra/http/middleware"
	redisStore "github.com/Berchon/rate-limiter/internal/infra/redis"
	"github.com/Berchon/rate-limiter/internal/infra/storage"
	redis "github.com/redis/go-redis/v9"
)

const redisPingTimeout = 5 * time.Second

// Dependencies holds the wired application handlers, middleware, and resource closers.
type Dependencies struct {
	GetHealthHandler    handler.GetHealthHandler
	GetPingHandler      handler.GetPingHandler
	CreateAPIKeyHandler handler.CreateAPIKeyHandler
	RateLimitMiddleware middleware.RateLimitMiddleware

	closer io.Closer
}

// BuildDependencies initializes storage according to STORAGE_TYPE and RATE_LIMIT_ALGORITHM,
// along with use cases, handlers, and middleware.
func BuildDependencies(cfg *config.Config, logger *slog.Logger) (*Dependencies, error) {
	logger = resolveLogger(logger)

	if cfg == nil {
		return nil, errors.New("config cannot be nil")
	}

	store, closer, err := buildStore(context.Background(), cfg, logger)
	if err != nil {
		return nil, err
	}

	rateLimiterUC := buildRateLimiterUseCase(store, cfg, logger)

	return wireHTTPDependencies(rateLimiterUC, cfg.FailOpen, closer, logger), nil
}

// Close releases external resources owned by the dependency container, such as the Redis client.
func (d *Dependencies) Close() error {
	if d == nil || d.closer == nil {
		return nil
	}

	if err := d.closer.Close(); err != nil {
		return fmt.Errorf("closing storage client: %w", err)
	}

	return nil
}

func resolveLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default()
	}
	return logger
}

func buildRateLimiterUseCase(store service.LimiterStore, cfg *config.Config, logger *slog.Logger) usecase.RateLimiterUseCase {
	return usecase.NewRateLimiterUseCase(
		store,
		usecase.RateLimiterConfig{
			IPMaxRequests:       cfg.RateByIPMaxRequests,
			IPTimeWindowSec:     cfg.RateByIPTimeWindow,
			IPBlockedDuration:   cfg.RateByIPBlockedDuration,
			EnableTokenLimiting: cfg.EnableTokenLimiting,
			DefaultTokenTTL:     cfg.DefaultTokenTTL,
		},
		logger,
	)
}

func wireHTTPDependencies(rateLimiterUC usecase.RateLimiterUseCase, failOpen bool, closer io.Closer, logger *slog.Logger) *Dependencies {
	return &Dependencies{
		GetHealthHandler:    handler.NewGetHealthHandler(logger),
		GetPingHandler:      handler.NewGetPingHandler(logger),
		CreateAPIKeyHandler: handler.NewCreateAPIKeyHandler(rateLimiterUC, logger),
		RateLimitMiddleware: middleware.NewRateLimitMiddleware(rateLimiterUC, failOpen, logger),
		closer:              closer,
	}
}

func buildStore(ctx context.Context, cfg *config.Config, logger *slog.Logger) (service.LimiterStore, io.Closer, error) {
	alg, err := resolveAlgorithm(cfg.RateLimitAlgorithm)
	if err != nil {
		return nil, nil, err
	}

	switch cfg.StorageType {
	case config.StorageTypeMemory:
		return buildInMemoryStore(alg, logger)
	case config.StorageTypeRedis:
		return buildRedisStore(ctx, cfg.RedisURL, alg, logger)
	default:
		return nil, nil, fmt.Errorf("unsupported storage type %q", cfg.StorageType)
	}
}

func resolveAlgorithm(raw string) (service.Algorithm, error) {
	switch raw {
	case "", config.AlgorithmFixedWindow:
		return service.AlgorithmFixedWindow, nil
	case config.AlgorithmSlidingWindow:
		return service.AlgorithmSlidingWindow, nil
	default:
		return "", fmt.Errorf("unsupported rate-limit algorithm %q", raw)
	}
}

func buildInMemoryStore(alg service.Algorithm, logger *slog.Logger) (service.LimiterStore, io.Closer, error) {
	logger.Info("using in-memory storage backend", "algorithm", string(alg))
	store, err := storage.NewInMemoryStoreWithAlgorithm(alg)
	if err != nil {
		return nil, nil, err
	}
	return store, nil, nil
}

func buildRedisStore(ctx context.Context, rawRedisURL string, alg service.Algorithm, logger *slog.Logger) (service.LimiterStore, io.Closer, error) {
	redisOpts, err := redis.ParseURL(rawRedisURL)
	if err != nil {
		return nil, nil, errors.New("invalid REDIS_URL: check scheme (redis:// or rediss://) and URI format")
	}

	maskedURL := config.MaskRedisURL(rawRedisURL)
	logger.Info("connecting to redis storage backend",
		"redis_url", maskedURL,
		"addr", redisOpts.Addr,
		"algorithm", string(alg),
	)

	redisClient := redis.NewClient(redisOpts)
	verifyRedisConnection(ctx, redisClient, maskedURL, redisOpts.Addr, logger)

	store, err := redisStore.NewStoreWithAlgorithm(redisClient, alg)
	if err != nil {
		_ = redisClient.Close()
		return nil, nil, err
	}

	return store, redisClient, nil
}

func verifyRedisConnection(ctx context.Context, client *redis.Client, maskedURL, addr string, logger *slog.Logger) {
	pingCtx, cancel := context.WithTimeout(ctx, redisPingTimeout)
	defer cancel()

	pong, err := client.Ping(pingCtx).Result()
	if err != nil {
		logger.Warn("redis ping failed during startup",
			"redis_url", maskedURL,
			"addr", addr,
			"error", err,
		)
		return
	}

	logger.Info("redis connection verified",
		"redis_url", maskedURL,
		"addr", addr,
		"ping", pong,
	)
}
