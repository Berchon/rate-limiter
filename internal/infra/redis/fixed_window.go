package redis

import (
	"context"
	_ "embed"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	goredis "github.com/redis/go-redis/v9"
)

const countKeySuffix = ":count"

//go:embed scripts/fixed_window.lua
var fixedWindowLua string

var fixedWindowScript = goredis.NewScript(fixedWindowLua)

type fixedWindowStrategy struct{}

func (fixedWindowStrategy) addRequest(
	ctx context.Context,
	client *goredis.Client,
	key entity.LimiterKey,
	_ int64,
	maxRequests, timeWindowSec, blockedDuration int,
) ([]int64, error) {
	keys := []string{countKey(key), blockedKey(key)}
	return fixedWindowScript.Run(ctx, client, keys, maxRequests, timeWindowSec, blockedDuration).Int64Slice()
}

func countKey(key entity.LimiterKey) string {
	return key.String() + countKeySuffix
}
