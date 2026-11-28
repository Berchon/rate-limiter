package storage

import "time"

type counterEntry struct {
	count     int
	expiresAt time.Time
}

type fixedWindowStrategy struct {
	counters map[string]counterEntry
}

func newFixedWindowStrategy() *fixedWindowStrategy {
	return &fixedWindowStrategy{
		counters: make(map[string]counterEntry),
	}
}

func (s *fixedWindowStrategy) record(key string, now time.Time, maxRequests, timeWindowSec int) (bool, int, int64) {
	entry, ok := s.counters[key]
	if !ok || !now.Before(entry.expiresAt) {
		entry = counterEntry{
			count:     0,
			expiresAt: now.Add(secondsToDuration(timeWindowSec)),
		}
	}

	entry.count++
	s.counters[key] = entry

	resetSec := int64(durationToSeconds(entry.expiresAt.Sub(now)))
	if entry.count > maxRequests {
		return false, 0, resetSec
	}
	return true, maxRequests - entry.count, resetSec
}

func (s *fixedWindowStrategy) delete(key string) {
	delete(s.counters, key)
}

func (s *fixedWindowStrategy) hasState(key string) bool {
	_, ok := s.counters[key]
	return ok
}
