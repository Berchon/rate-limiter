package entity

import "fmt"

// LimiterKey represents a unified identifier for rate limiting.
// It can be based on an IP address or a token, e.g., "ip:192.168.0.1" or "token:abc123".
type LimiterKey struct {
	Prefix string
	Value  string
}

// NewLimiterKey creates a new LimiterKey with the given prefix and value.
func NewLimiterKey(prefix, value string) *LimiterKey {
	return &LimiterKey{
		Prefix: prefix,
		Value:  value,
	}
}

// String returns the canonical string representation of the key (e.g., "ip:192.168.0.1").
func (k *LimiterKey) String() string {
	return fmt.Sprintf("%s:%s", k.Prefix, k.Value)
}
