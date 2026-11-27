package entity

// RateLimitDecision represents the outcome of a rate-limit evaluation.
type RateLimitDecision struct {
	Allowed           bool
	Limit             int
	Remaining         int
	ResetSeconds      int64
	RetryAfterSeconds int
	Reason            string
}
