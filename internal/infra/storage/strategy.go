package storage

import (
	"fmt"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/service"
)

// windowStrategy encapsulates a rate-limiting window algorithm for InMemoryStore.
// All methods are called while InMemoryStore.mu is held.
type windowStrategy interface {
	record(key string, now time.Time, maxRequests, timeWindowSec int) (allowed bool, remaining int, resetSec int64)
	delete(key string)
	hasState(key string) bool
}

func newWindowStrategy(alg service.Algorithm) (windowStrategy, error) {
	switch alg {
	case "", service.AlgorithmFixedWindow:
		return newFixedWindowStrategy(), nil
	case service.AlgorithmSlidingWindow:
		return newSlidingWindowStrategy(), nil
	default:
		return nil, fmt.Errorf("unsupported rate-limit algorithm %q", alg)
	}
}
