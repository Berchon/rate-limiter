package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Berchon/rate-limiter/internal/business/entity"
	usecaseMock "github.com/Berchon/rate-limiter/internal/business/usecase/mock"
)

const (
	expectedExceededError    = "you have reached the maximum number of requests or actions allowed within a certain time frame"
	expectedUnavailableError = "service temporarily unavailable"
)

func executeMiddlewareRequest(mw RateLimitMiddleware, req *stdhttp.Request) (*httptest.ResponseRecorder, string, bool) {
	rr := httptest.NewRecorder()
	nextCalled := false
	mw.Handle(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		nextCalled = true
		w.WriteHeader(stdhttp.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})).ServeHTTP(rr, req)

	body, _ := io.ReadAll(rr.Body)
	return rr, string(body), nextCalled
}

func TestRateLimitMiddleware_Handle(t *testing.T) {
	type contextKey string
	const traceKey contextKey = "trace_id"

	tests := []struct {
		name               string
		remoteAddr         string
		xForwardedFor      string
		apiKey             string
		expectedIP         string
		expectedToken      string
		failOpen           bool
		decision           *entity.RateLimitDecision
		limiterErr         error
		wantStatus         int
		wantNextCalled     bool
		wantBody           string
		wantErrorMessage   string
		wantLogLevel       string
		wantLogContains    string
		wantLimitHeader    string
		wantRemainHeader   string
		wantResetHeader    string
		wantRetryAfter     string
		wantRetryAfterSeen bool
		nilLogger          bool
	}{
		{
			name:             "allows request and sets standard rate-limit headers",
			remoteAddr:       "1.2.3.4:12345",
			apiKey:           "secret-token-123",
			expectedIP:       "1.2.3.4",
			expectedToken:    "secret-token-123",
			decision:         &entity.RateLimitDecision{Allowed: true, Limit: 100, Remaining: 99, ResetSeconds: 60, Reason: "allowed"},
			wantStatus:       stdhttp.StatusOK,
			wantNextCalled:   true,
			wantBody:         "ok",
			wantLimitHeader:  "100",
			wantRemainHeader: "99",
			wantResetHeader:  "60",
		},
		{
			name:               "blocks request with 429, rate-limit headers, Retry-After, and exact JSON error",
			remoteAddr:         "1.2.3.4:12345",
			expectedIP:         "1.2.3.4",
			decision:           &entity.RateLimitDecision{Allowed: false, Limit: 10, Remaining: 0, ResetSeconds: 30, RetryAfterSeconds: 300, Reason: "rate limit exceeded"},
			wantStatus:         stdhttp.StatusTooManyRequests,
			wantErrorMessage:   expectedExceededError,
			wantLimitHeader:    "10",
			wantRemainHeader:   "0",
			wantResetHeader:    "30",
			wantRetryAfter:     "300",
			wantRetryAfterSeen: true,
		},
		{
			name:               "blocks request without Retry-After when RetryAfterSeconds is zero",
			remoteAddr:         "1.2.3.4:12345",
			expectedIP:         "1.2.3.4",
			decision:           &entity.RateLimitDecision{Allowed: false, Limit: 10, Remaining: 0, ResetSeconds: 15, RetryAfterSeconds: 0, Reason: "blocked"},
			wantStatus:         stdhttp.StatusTooManyRequests,
			wantErrorMessage:   expectedExceededError,
			wantLimitHeader:    "10",
			wantRemainHeader:   "0",
			wantResetHeader:    "15",
			wantRetryAfterSeen: false,
		},
		{
			name:             "returns 503 fail-closed when limiter returns error",
			remoteAddr:       "1.2.3.4:12345",
			expectedIP:       "1.2.3.4",
			limiterErr:       assert.AnError,
			wantStatus:       stdhttp.StatusServiceUnavailable,
			wantErrorMessage: expectedUnavailableError,
			wantLogLevel:     "ERROR",
			wantLogContains:  "rate limit evaluation failed",
		},
		{
			name:             "returns 503 fail-closed when limiter returns nil decision",
			remoteAddr:       "1.2.3.4:12345",
			expectedIP:       "1.2.3.4",
			decision:         nil,
			wantStatus:       stdhttp.StatusServiceUnavailable,
			wantErrorMessage: expectedUnavailableError,
			wantLogLevel:     "ERROR",
			wantLogContains:  "rate limit decision is nil",
		},
		{
			name:            "allows request when fail-open is enabled and limiter returns error",
			remoteAddr:      "1.2.3.4:12345",
			apiKey:          "secret-token-fail-open",
			expectedIP:      "1.2.3.4",
			expectedToken:   "secret-token-fail-open",
			failOpen:        true,
			limiterErr:      assert.AnError,
			wantStatus:      stdhttp.StatusOK,
			wantNextCalled:  true,
			wantBody:        "ok",
			wantLogLevel:    "WARN",
			wantLogContains: "rate limiter unavailable, allowing request (fail-open)",
		},
		{
			name:            "allows request when fail-open is enabled and limiter returns nil decision",
			remoteAddr:      "1.2.3.4:12345",
			expectedIP:      "1.2.3.4",
			failOpen:        true,
			decision:        nil,
			wantStatus:      stdhttp.StatusOK,
			wantNextCalled:  true,
			wantBody:        "ok",
			wantLogLevel:    "WARN",
			wantLogContains: "rate limiter unavailable, allowing request (fail-open)",
		},
		{
			name:             "extracts and trims first valid IP from X-Forwarded-For",
			remoteAddr:       "10.0.0.1:8080",
			xForwardedFor:    "  5.6.7.8  , 9.10.11.12",
			expectedIP:       "5.6.7.8",
			decision:         &entity.RateLimitDecision{Allowed: true, Limit: 5, Remaining: 4, ResetSeconds: 1},
			wantStatus:       stdhttp.StatusOK,
			wantNextCalled:   true,
			wantBody:         "ok",
			wantLimitHeader:  "5",
			wantRemainHeader: "4",
			wantResetHeader:  "1",
		},
		{
			name:             "falls back to RemoteAddr when X-Forwarded-For is invalid",
			remoteAddr:       "10.0.0.1:8080",
			xForwardedFor:    "not-an-ip, 9.10.11.12",
			expectedIP:       "10.0.0.1",
			decision:         &entity.RateLimitDecision{Allowed: true, Limit: 5, Remaining: 4, ResetSeconds: 1},
			wantStatus:       stdhttp.StatusOK,
			wantNextCalled:   true,
			wantBody:         "ok",
			wantLimitHeader:  "5",
			wantRemainHeader: "4",
			wantResetHeader:  "1",
		},
		{
			name:             "extracts IPv6 address from RemoteAddr with port",
			remoteAddr:       "[::1]:54321",
			expectedIP:       "::1",
			decision:         &entity.RateLimitDecision{Allowed: true, Limit: 5, Remaining: 4, ResetSeconds: 1},
			wantStatus:       stdhttp.StatusOK,
			wantNextCalled:   true,
			wantBody:         "ok",
			wantLimitHeader:  "5",
			wantRemainHeader: "4",
			wantResetHeader:  "1",
		},
		{
			name:             "handles RemoteAddr without port gracefully",
			remoteAddr:       "malformed",
			expectedIP:       "malformed",
			decision:         &entity.RateLimitDecision{Allowed: true, Limit: 5, Remaining: 4, ResetSeconds: 1},
			wantStatus:       stdhttp.StatusOK,
			wantNextCalled:   true,
			wantBody:         "ok",
			wantLimitHeader:  "5",
			wantRemainHeader: "4",
			wantResetHeader:  "1",
			nilLogger:        true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var logBuf bytes.Buffer
			var logger *slog.Logger
			if !tc.nilLogger {
				logger = slog.New(slog.NewJSONHandler(&logBuf, nil))
			}

			limiter := usecaseMock.NewMockRateLimiterUseCase(t)
			limiter.EXPECT().
				AllowRequest(
					mock.MatchedBy(func(ctx context.Context) bool {
						return ctx.Value(traceKey) == "req-123"
					}),
					tc.expectedIP,
					tc.expectedToken,
					mock.Anything,
				).
				Return(tc.decision, tc.limiterErr)

			mw := NewRateLimitMiddleware(limiter, tc.failOpen, logger)

			req := httptest.NewRequest(stdhttp.MethodGet, "/protected", nil)
			req = req.WithContext(context.WithValue(req.Context(), traceKey, "req-123"))
			req.RemoteAddr = tc.remoteAddr
			if tc.xForwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tc.xForwardedFor)
			}
			if tc.apiKey != "" {
				req.Header.Set("API_KEY", tc.apiKey)
			}

			rr, body, nextCalled := executeMiddlewareRequest(mw, req)

			require.Equal(t, tc.wantStatus, rr.Code)
			assert.Equal(t, tc.wantNextCalled, nextCalled)

			if tc.wantBody != "" {
				assert.Equal(t, tc.wantBody, body)
			}

			if tc.wantErrorMessage != "" {
				assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
				var payload map[string]string
				require.NoError(t, json.Unmarshal([]byte(body), &payload))
				assert.Equal(t, tc.wantErrorMessage, payload["error"])
			}

			if tc.wantLogLevel != "" && !tc.nilLogger {
				assert.Contains(t, logBuf.String(), `"level":"`+tc.wantLogLevel+`"`)
			}

			if tc.wantLogContains != "" && !tc.nilLogger {
				assert.Contains(t, logBuf.String(), tc.wantLogContains)
			}

			assert.Equal(t, tc.wantLimitHeader, rr.Header().Get("X-RateLimit-Limit"))
			assert.Equal(t, tc.wantRemainHeader, rr.Header().Get("X-RateLimit-Remaining"))
			assert.Equal(t, tc.wantResetHeader, rr.Header().Get("X-RateLimit-Reset"))

			if tc.wantRetryAfterSeen {
				assert.Equal(t, tc.wantRetryAfter, rr.Header().Get("Retry-After"))
			} else {
				assert.Empty(t, rr.Header().Get("Retry-After"))
			}

			if tc.apiKey != "" && !tc.nilLogger {
				assert.NotContains(t, logBuf.String(), tc.apiKey)
			}
		})
	}
}
