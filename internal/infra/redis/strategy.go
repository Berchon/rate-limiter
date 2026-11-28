package redis

import (
	"context"
	"fmt"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	"github.com/Berchon/rate-limiter/internal/business/service"
	goredis "github.com/redis/go-redis/v9"
)

// windowStrategy encapsulates a Redis-side atomic rate-limiting algorithm.
type windowStrategy interface {
	addRequest(
		ctx context.Context,
		client *goredis.Client,
		key entity.LimiterKey,
		nowMs int64,
		maxRequests, timeWindowSec, blockedDuration int,
	) ([]int64, error)
}

func newWindowStrategy(alg service.Algorithm) (windowStrategy, error) {
	switch alg {
	case "", service.AlgorithmFixedWindow:
		return fixedWindowStrategy{}, nil
	case service.AlgorithmSlidingWindow:
		return newSlidingWindowStrategy(), nil
	default:
		return nil, fmt.Errorf("unsupported rate-limit algorithm %q", alg)
	}
}
