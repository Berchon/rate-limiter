package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/service"
	"github.com/redis/go-redis/v9"
)

type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(client *redis.Client) service.LimiterStore {
	return &RedisStore{
		client: client,
	}
}

// ---------------- Configuração do token ----------------
func (r *RedisStore) SaveTokenConfig(ctx context.Context, cfg *entity.TokenConfig) error {
	// key := *entity.NewLimiterKey("token", cfg.Token) // cfg.Token já está no formato `token:<TOKEN>``

	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal token config: %w", err)
	}

	ttl := time.Duration(cfg.TokenTTLSeconds) * time.Second
	if err := r.client.Set(ctx, cfg.Token, data, ttl).Err(); err != nil {
		return err
	}
	return nil
}

func (r *RedisStore) GetConfigByToken(ctx context.Context, token string) (*entity.TokenConfig, error) {
	key := *entity.NewLimiterKey("token", token)

	data, err := r.client.Get(ctx, key.String()).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get token config: %w", err)
	}

	var cfg entity.TokenConfig
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal token config: %w", err)
	}
	return &cfg, nil
}

// ---------------- Contador de requisições ----------------
func (r *RedisStore) AddRequest(ctx context.Context, key entity.LimiterKey, timestamp int64, maxRequests, timeWindowSec, blockedDuration int) (service.AddRequestResult, error) {
	status, err := r.IsBlocked(ctx, key)
	if err != nil {
		return service.AddRequestResult{}, err
	}
	if status.Blocked {
		return service.AddRequestResult{
			Allowed:           false,
			Remaining:         0,
			RetryAfterSeconds: status.RemainingSeconds,
		}, nil
	}

	countKey := key.String() + ":count"
	count, err := r.client.Incr(ctx, countKey).Result()
	if err != nil {
		return service.AddRequestResult{}, err
	}

	ttl, err := r.client.TTL(ctx, countKey).Result()
	if err != nil {
		return service.AddRequestResult{}, err
	}
	if ttl < 0 {
		if err := r.client.Expire(ctx, countKey, time.Duration(timeWindowSec)*time.Second).Err(); err != nil {
			return service.AddRequestResult{}, err
		}
	}

	if int(count) > maxRequests {
		return service.AddRequestResult{Allowed: false, Remaining: 0}, nil
	}

	return service.AddRequestResult{Allowed: true, Remaining: maxRequests - int(count)}, nil
}

// ---------------- Bloqueio ----------------
func (r *RedisStore) Block(ctx context.Context, key entity.LimiterKey, durationSec int) error {
	blockedKey := key.String() + ":blocked"
	return r.client.Set(ctx, blockedKey, 1, time.Duration(durationSec)*time.Second).Err()
}

func (r *RedisStore) IsBlocked(ctx context.Context, key entity.LimiterKey) (service.BlockStatus, error) {
	blockedKey := key.String() + ":blocked"

	ttl, err := r.client.TTL(ctx, blockedKey).Result()
	if err != nil {
		return service.BlockStatus{}, err
	}
	if ttl > 0 {
		return service.BlockStatus{Blocked: true, RemainingSeconds: int(ttl.Seconds())}, nil
	}
	return service.BlockStatus{}, nil
}

// ---------------- Reset (para testes) ----------------
func (r *RedisStore) ResetKey(ctx context.Context, key entity.LimiterKey) error {
	if err := r.client.Del(ctx, key.String()+":count").Err(); err != nil {
		return fmt.Errorf("failed to reset count key: %w", err)
	}
	if err := r.client.Del(ctx, key.String()+":blocked").Err(); err != nil {
		return fmt.Errorf("failed to reset blocked key: %w", err)
	}
	if err := r.client.Del(ctx, key.String()).Err(); err != nil {
		return fmt.Errorf("failed to reset config key: %w", err)
	}
	return nil
}
