package redis

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	goredis "github.com/redis/go-redis/v9"
)

const logKeySuffix = ":log"

//go:embed scripts/sliding_window.lua
var slidingWindowLua string

var slidingWindowScript = goredis.NewScript(slidingWindowLua)

type slidingWindowStrategy struct {
	nonce string
	seq   atomic.Uint64
	now   func() time.Time
}

func newSlidingWindowStrategy() *slidingWindowStrategy {
	return &slidingWindowStrategy{
		nonce: generateNonce(),
		now:   time.Now,
	}
}

func (s *slidingWindowStrategy) addRequest(
	ctx context.Context,
	client *goredis.Client,
	key entity.LimiterKey,
	nowMs int64,
	maxRequests, timeWindowSec, blockedDuration int,
) ([]int64, error) {
	if nowMs <= 0 {
		nowMs = s.now().UnixMilli()
	}

	seq := s.seq.Add(1)
	member := strconv.FormatInt(nowMs, 10) + ":" + s.nonce + ":" + strconv.FormatUint(seq, 10)
	keys := []string{logKey(key), blockedKey(key)}

	return slidingWindowScript.Run(
		ctx,
		client,
		keys,
		maxRequests,
		timeWindowSec,
		blockedDuration,
		nowMs,
		member,
	).Int64Slice()
}

func generateNonce() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf)
}

func logKey(key entity.LimiterKey) string {
	return key.String() + logKeySuffix
}
