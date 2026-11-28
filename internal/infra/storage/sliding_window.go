package storage

import "time"

type slidingWindowStrategy struct {
	logs map[string][]time.Time
}

func newSlidingWindowStrategy() *slidingWindowStrategy {
	return &slidingWindowStrategy{
		logs: make(map[string][]time.Time),
	}
}

func (s *slidingWindowStrategy) record(key string, now time.Time, maxRequests, timeWindowSec int) (bool, int, int64) {
	window := secondsToDuration(timeWindowSec)
	windowStart := now.Add(-window)

	entries := pruneExpiredTimestamps(s.logs[key], windowStart)
	if len(entries) >= maxRequests {
		s.logs[key] = entries
		resetSec := slidingResetSeconds(entries[0], now, window)
		return false, 0, resetSec
	}

	entries = append(entries, now)
	s.logs[key] = entries

	resetSec := slidingResetSeconds(entries[0], now, window)
	return true, maxRequests - len(entries), resetSec
}

func (s *slidingWindowStrategy) delete(key string) {
	delete(s.logs, key)
}

func (s *slidingWindowStrategy) hasState(key string) bool {
	_, ok := s.logs[key]
	return ok
}

func pruneExpiredTimestamps(entries []time.Time, windowStart time.Time) []time.Time {
	idx := 0
	for idx < len(entries) && !entries[idx].After(windowStart) {
		idx++
	}
	if idx == 0 {
		return entries
	}
	n := copy(entries, entries[idx:])
	clear(entries[n:])
	return entries[:n]
}

func slidingResetSeconds(oldest, now time.Time, window time.Duration) int64 {
	remaining := oldest.Add(window).Sub(now)
	if remaining > window {
		remaining = window
	}
	return int64(durationToSeconds(remaining))
}
