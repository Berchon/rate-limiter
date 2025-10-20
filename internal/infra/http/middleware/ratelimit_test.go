//go:generate mockery --dir=. --output=./mock --name=RateLimiterUseCase --structname=MockRateLimiterUseCase --outpkg=mock --filename=rate_limiter_use_case.go --disable-version-string

package middleware

import (
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	usecaseMock "github.com/Berchon/rate-limiter/internal/business/usecase/mock"
)

// === Helpers ===

func newTestMiddleware(allow bool, reason string, err error, expectedIP string, t *testing.T) RateLimitMiddleware {
	limiter := &usecaseMock.MockRateLimiterUseCase{}
	limiter.On("AllowRequest", expectedIP, mock.Anything, mock.Anything).Return(allow, reason, 0, 0, err)
	mw := NewRateLimitMiddleware(limiter)
	t.Cleanup(func() { limiter.AssertExpectations(t) })
	return mw
}

func newRequest(ip string, xForwardedFor ...string) *stdhttp.Request {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = ip
	if len(xForwardedFor) > 0 {
		req.Header.Set("X-Forwarded-For", xForwardedFor[0])
	}
	return req
}

func execute(mw RateLimitMiddleware, req *stdhttp.Request) (*httptest.ResponseRecorder, string) {
	rr := httptest.NewRecorder()
	mw.Handle(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusOK)
		w.Write([]byte("ok"))
	})).ServeHTTP(rr, req)
	body, _ := io.ReadAll(rr.Body)
	return rr, string(body)
}

// === Testes ===

func TestRateLimitMiddleware_Handle(t *testing.T) {

	t.Run("should call next handler when request is allowed", func(t *testing.T) {
		mw := newTestMiddleware(true, "allowed", nil, "1.2.3.4", t)
		req := newRequest("1.2.3.4")
		rr, body := execute(mw, req)

		assert.Equal(t, stdhttp.StatusOK, rr.Code)
		assert.Contains(t, body, "ok")
	})

	t.Run("should return 429 when request is blocked", func(t *testing.T) {
		mw := newTestMiddleware(false, "rate_limited", nil, "1.2.3.4", t)
		req := newRequest("1.2.3.4")
		rr, body := execute(mw, req)

		assert.Equal(t, stdhttp.StatusTooManyRequests, rr.Code)
		assert.Contains(t, body, `"error"`)
	})

	t.Run("should return 429 when limiter returns error", func(t *testing.T) {
		mw := newTestMiddleware(false, "internal_error", assert.AnError, "1.2.3.4", t)
		req := newRequest("1.2.3.4")
		rr, body := execute(mw, req)

		assert.Equal(t, stdhttp.StatusTooManyRequests, rr.Code)
		assert.Contains(t, body, `"error"`)
	})

	t.Run("should extract client IP from X-Forwarded-For when present", func(t *testing.T) {
		mw := newTestMiddleware(true, "allowed", nil, "5.6.7.8", t)
		req := newRequest("1.2.3.4", "5.6.7.8, 9.10.11.12")
		rr, body := execute(mw, req)

		assert.Equal(t, stdhttp.StatusOK, rr.Code)
		assert.Contains(t, body, "ok")
	})

	t.Run("should fallback to RemoteAddr when X-Forwarded-For is absent", func(t *testing.T) {
		mw := newTestMiddleware(true, "allowed", nil, "1.2.3.4", t)
		req := newRequest("1.2.3.4")
		rr, body := execute(mw, req)

		assert.Equal(t, stdhttp.StatusOK, rr.Code)
		assert.Contains(t, body, "ok")
	})

	t.Run("should handle malformed RemoteAddr gracefully", func(t *testing.T) {
		mw := newTestMiddleware(true, "allowed", nil, "malformed", t)
		req := newRequest("malformed")
		rr, body := execute(mw, req)

		assert.Equal(t, stdhttp.StatusOK, rr.Code)
		assert.Contains(t, body, "ok")
	})
}
