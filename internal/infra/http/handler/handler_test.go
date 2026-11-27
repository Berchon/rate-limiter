package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/business/dto"
	"github.com/Berchon/rate-limiter/internal/business/entity"
	usecasemock "github.com/Berchon/rate-limiter/internal/business/usecase/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGetHealthHandler_Handle(t *testing.T) {
	tests := []struct {
		name   string
		logger *slog.Logger
	}{
		{name: "with custom logger", logger: newDiscardLogger()},
		{name: "with nil logger falls back to default", logger: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewGetHealthHandler(tc.logger)
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			rec := httptest.NewRecorder()

			h.Handle(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

			var resp statusResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			assert.Equal(t, "Healthy", resp.Status)
		})
	}
}

func TestGetPingHandler_Handle(t *testing.T) {
	tests := []struct {
		name   string
		logger *slog.Logger
	}{
		{name: "with custom logger", logger: newDiscardLogger()},
		{name: "with nil logger falls back to default", logger: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewGetPingHandler(tc.logger)
			req := httptest.NewRequest(http.MethodGet, "/ping", nil)
			rec := httptest.NewRecorder()

			h.Handle(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

			var resp statusResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			assert.Equal(t, "Pong", resp.Status)
		})
	}
}

func TestResponseHandler_RequestResponse_EncodeError(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	rh := NewResponseHandler(logger)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	// Functions cannot be JSON-encoded, triggering the encode error path.
	rh.RequestResponse(rec, req, func() {}, http.StatusOK)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, logBuf.String(), "failed to encode json response")
}

func TestCreateAPIKeyHandler_Handle(t *testing.T) {
	fixedNow := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

	t.Run("creates token successfully with explicit token_ttl", func(t *testing.T) {
		const expectedKey = "sample-key-123"
		ucMock := usecasemock.NewMockRateLimiterUseCase(t)
		ucMock.EXPECT().
			CreateToken(mock.Anything, 10, 5, 30, 3600).
			Return(&entity.TokenConfig{
				Token:                  expectedKey,
				TimeWindowSeconds:      10,
				MaxRequests:            5,
				BlockedDurationSeconds: 30,
				TokenTTLSeconds:        3600,
				CreatedAt:              fixedNow,
				ExpiresAt:              fixedNow.Add(3600 * time.Second),
			}, nil).
			Once()

		h := NewCreateAPIKeyHandler(ucMock, newDiscardLogger())
		body := `{"time_window":10,"max_requests":5,"blocked_duration":30,"token_ttl":3600}`
		req := httptest.NewRequest(http.MethodPost, "/api-key", strings.NewReader(body))
		rec := httptest.NewRecorder()

		h.Handle(rec, req)

		require.Equal(t, http.StatusCreated, rec.Code)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		var resp dto.CreateAPIKeyResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, expectedKey, resp.Token)
		assert.Equal(t, 10, resp.TimeWindow)
		assert.Equal(t, 5, resp.MaxRequests)
		assert.Equal(t, 30, resp.BlockedDuration)
		assert.Equal(t, fixedNow.Format(time.RFC3339), resp.CreatedAt)
		assert.Equal(t, fixedNow.Add(3600*time.Second).Format(time.RFC3339), resp.ExpiresAt)
	})

	t.Run("allows optional token_ttl zero and falls back to calculated expiration when ExpiresAt is zero", func(t *testing.T) {
		const expectedKey = "sample-key-default"
		ucMock := usecasemock.NewMockRateLimiterUseCase(t)
		ucMock.EXPECT().
			CreateToken(mock.Anything, 5, 2, 60, 0).
			Return(&entity.TokenConfig{
				Token:                  expectedKey,
				TimeWindowSeconds:      5,
				MaxRequests:            2,
				BlockedDurationSeconds: 60,
				TokenTTLSeconds:        40,
				CreatedAt:              fixedNow,
			}, nil).
			Once()

		h := NewCreateAPIKeyHandler(ucMock, nil)
		body := `{"time_window":5,"max_requests":2,"blocked_duration":60}`
		req := httptest.NewRequest(http.MethodPost, "/api-key", strings.NewReader(body))
		rec := httptest.NewRecorder()

		h.Handle(rec, req)

		require.Equal(t, http.StatusCreated, rec.Code)

		var resp dto.CreateAPIKeyResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, expectedKey, resp.Token)
		assert.Equal(t, fixedNow.Add(40*time.Second).Format(time.RFC3339), resp.ExpiresAt)
	})

	t.Run("rejects malformed or missing request body", func(t *testing.T) {
		tests := []struct {
			name string
			body io.Reader
		}{
			{name: "nil body", body: nil},
			{name: "empty body", body: strings.NewReader("")},
			{name: "invalid json", body: strings.NewReader("{bad-json}")},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				ucMock := usecasemock.NewMockRateLimiterUseCase(t)
				h := NewCreateAPIKeyHandler(ucMock, newDiscardLogger())

				req := httptest.NewRequest(http.MethodPost, "/api-key", tc.body)
				if tc.body == nil {
					req.Body = nil
				}
				rec := httptest.NewRecorder()

				h.Handle(rec, req)

				assert.Equal(t, http.StatusBadRequest, rec.Code)
				var resp errorResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				assert.Equal(t, "invalid request body", resp.Error)
			})
		}
	})

	t.Run("rejects invalid rate-limit parameters", func(t *testing.T) {
		tests := []struct {
			name string
			body string
		}{
			{name: "zero time_window", body: `{"time_window":0,"max_requests":5,"blocked_duration":10,"token_ttl":60}`},
			{name: "negative time_window", body: `{"time_window":-1,"max_requests":5,"blocked_duration":10,"token_ttl":60}`},
			{name: "zero max_requests", body: `{"time_window":5,"max_requests":0,"blocked_duration":10,"token_ttl":60}`},
			{name: "negative max_requests", body: `{"time_window":5,"max_requests":-3,"blocked_duration":10,"token_ttl":60}`},
			{name: "zero blocked_duration", body: `{"time_window":5,"max_requests":5,"blocked_duration":0,"token_ttl":60}`},
			{name: "negative blocked_duration", body: `{"time_window":5,"max_requests":5,"blocked_duration":-10,"token_ttl":60}`},
			{name: "negative token_ttl", body: `{"time_window":5,"max_requests":5,"blocked_duration":10,"token_ttl":-1}`},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				ucMock := usecasemock.NewMockRateLimiterUseCase(t)
				h := NewCreateAPIKeyHandler(ucMock, newDiscardLogger())

				req := httptest.NewRequest(http.MethodPost, "/api-key", strings.NewReader(tc.body))
				rec := httptest.NewRecorder()

				h.Handle(rec, req)

				assert.Equal(t, http.StatusBadRequest, rec.Code)
				var resp errorResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				assert.Equal(t, "invalid parameters", resp.Error)
			})
		}
	})

	t.Run("returns 500 when use case fails", func(t *testing.T) {
		ucMock := usecasemock.NewMockRateLimiterUseCase(t)
		ucMock.EXPECT().
			CreateToken(mock.Anything, 5, 2, 10, 60).
			Return(nil, errors.New("storage failure")).
			Once()

		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))
		h := NewCreateAPIKeyHandler(ucMock, logger)

		body := `{"time_window":5,"max_requests":2,"blocked_duration":10,"token_ttl":60}`
		req := httptest.NewRequest(http.MethodPost, "/api-key", strings.NewReader(body))
		rec := httptest.NewRecorder()

		h.Handle(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		var resp errorResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, "failed to create token", resp.Error)
		assert.Contains(t, logBuf.String(), "failed to create api token")
	})

	t.Run("returns 500 when use case is nil", func(t *testing.T) {
		h := NewCreateAPIKeyHandler(nil, newDiscardLogger())

		body := `{"time_window":5,"max_requests":2,"blocked_duration":10,"token_ttl":0}`
		req := httptest.NewRequest(http.MethodPost, "/api-key", strings.NewReader(body))
		rec := httptest.NewRecorder()

		h.Handle(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		var resp errorResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, "failed to create token", resp.Error)
	})
}
