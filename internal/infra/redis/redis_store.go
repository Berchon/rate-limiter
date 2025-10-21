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
	client     *redis.Client
	defaultTTL time.Duration
}

func NewRedisStore(client *redis.Client, defaultTTL time.Duration) service.LimiterStore {
	return &RedisStore{
		client:     client,
		defaultTTL: defaultTTL,
	}
}

// Salva a configuração do token no Redis como JSON
func (r *RedisStore) SaveTokenConfig(cfg *entity.TokenConfig) error {
	ctx := context.Background()
	key := cfg.Token

	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal token config: %w", err)
	}

	ttl := time.Duration(cfg.TimeWindowSeconds) * time.Second
	if err := r.client.Set(ctx, key, data, ttl).Err(); err != nil {
		return err
	}
	return nil
}

// Recupera configuração do token
func (r *RedisStore) GetConfigByToken(key string) (*entity.TokenConfig, error) {
	ctx := context.Background()

	exists, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to check key existence: %w", err)
	}
	if exists == 0 {
		return nil, nil
	}

	data, err := r.client.Get(ctx, key).Result()
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

// Registra uma requisição, retorna se é permitida + restantes
func (r *RedisStore) AddRequest(key string, timestamp int64, maxRequests int, timeWindowSec int, blockedDuration int) (bool, int, error) {
	ctx := context.Background()

	blocked, remaining, err := r.IsBlocked(key)
	if err != nil {
		return false, 0, err
	}
	if blocked {
		return false, remaining, nil
	}

	// Usar chave de contagem separada ":count"
	countKey := key + ":count"
	count, err := r.client.Incr(ctx, countKey).Result()
	if err != nil {
		return false, 0, err
	}

	ttl, err := r.client.TTL(ctx, countKey).Result()
	if err != nil {
		return false, 0, err
	}
	if ttl < 0 {
		if err := r.client.Expire(ctx, countKey, time.Duration(timeWindowSec)*time.Second).Err(); err != nil {
			return false, 0, err
		}
	}

	if int(count) > maxRequests {
		if blockedDuration > 0 {
			if err := r.Block(key, blockedDuration); err != nil {
				return false, 0, err
			}
		}
		return false, 0, nil
	}

	return true, maxRequests - int(count), nil
}

// Bloqueia chave explicitamente por durationSec segundos
func (r *RedisStore) Block(key string, durationSec int) error {
	ctx := context.Background()
	return r.client.Set(ctx, key+":blocked", 1, time.Duration(durationSec)*time.Second).Err()
}

// Verifica se chave está bloqueada
func (r *RedisStore) IsBlocked(key string) (bool, int, error) {
	ctx := context.Background()
	ttl, err := r.client.TTL(ctx, key+":blocked").Result()
	if err != nil {
		return false, 0, err
	}
	if ttl > 0 {
		return true, int(ttl.Seconds()), nil
	}
	return false, 0, nil
}

// Reseta contador e bloqueio (útil para testes)
func (r *RedisStore) ResetKey(key string) error {
	ctx := context.Background()
	if err := r.client.Del(ctx, key+":count").Err(); err != nil {
		return fmt.Errorf("failed to reset count key: %w", err)
	}
	if err := r.client.Del(ctx, key+":blocked").Err(); err != nil {
		return fmt.Errorf("failed to reset blocked key: %w", err)
	}
	return nil
}
