package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/service"
	"github.com/redis/go-redis/v9"
)

// RedisStore é a implementação de LimiterStore usando Redis
type RedisStore struct {
	client     *redis.Client
	defaultTTL time.Duration
}

// NewRedisStore cria uma nova instância de RedisStore
func NewRedisStore(client *redis.Client, defaultTTL time.Duration) service.LimiterStore {
	return &RedisStore{
		client:     client,
		defaultTTL: defaultTTL,
	}
}

// AddRequest registra uma requisição e retorna se é permitida + quantas restam
func (r *RedisStore) AddRequest(key string, timestamp int64, maxRequests int, timeWindowSec int, blockedDuration int) (bool, int, error) {
	ctx := context.Background()

	// 1. Verifica se o IP ou token está bloqueado
	blocked, remaining, err := r.IsBlocked(key)
	if err != nil {
		return false, 0, err
	}
	if blocked {
		return false, remaining, nil
	}

	// 2. Incrementa o contador e garante TTL da janela
	count, err := r.client.Incr(ctx, key).Result()
	if err != nil {
		return false, 0, err
	}

	// Aplica TTL se for a primeira vez
	ttl, err := r.client.TTL(ctx, key).Result()
	if err != nil {
		return false, 0, err
	}
	if ttl < 0 {
		if err := r.client.Expire(ctx, key, time.Duration(timeWindowSec)*time.Second).Err(); err != nil {
			return false, 0, err
		}
	}

	// 3. Verifica se ultrapassou o limite
	if int(count) > maxRequests {
		// Bloqueia o IP
		if blockedDuration > 0 {
			if err := r.Block(key, blockedDuration); err != nil {
				return false, 0, err
			}
		}
		return false, 0, nil
	}

	remaining = maxRequests - int(count)
	return true, remaining, nil
}

// GetConfigByToken retorna a configuração do token
func (r *RedisStore) GetConfigByToken(token string) (*entity.TokenConfig, error) {
	return nil, nil
}

// Block bloqueia explicitamente a chave por durationSec segundos
func (r *RedisStore) Block(key string, durationSec int) error {
	ctx := context.Background()
	return r.client.Set(ctx, key+":blocked", 1, time.Duration(durationSec)*time.Second).Err()
}

// IsBlocked verifica se a chave está bloqueada
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

// ResetKey reseta o contador da chave (útil para testes)
func (r *RedisStore) ResetKey(key string) error {
	ctx := context.Background()
	if err := r.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to reset key: %w", err)
	}
	if err := r.client.Del(ctx, key+":blocked").Err(); err != nil {
		return fmt.Errorf("failed to reset blocked key: %w", err)
	}
	return nil
}
