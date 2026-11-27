package http

import (
	"context"
	"io"
	"log/slog"
	stdhttp "net/http"
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/Berchon/rate-limiter/internal/infra/di"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDeps(t *testing.T) *di.Dependencies {
	t.Helper()

	cfg := &config.Config{
		Port:                    "0",
		StorageType:             config.StorageTypeMemory,
		RateByIPTimeWindow:      5,
		RateByIPMaxRequests:     5,
		RateByIPBlockedDuration: 10,
		EnableTokenLimiting:     true,
		DefaultTokenTTL:         60,
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps, err := di.BuildDependencies(cfg, logger)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, deps.Close())
	})

	return deps
}

func TestNewServer_ConfiguresTimeoutsAndDefaults(t *testing.T) {
	deps := newTestDeps(t)

	t.Run("applies configured timeouts and port", func(t *testing.T) {
		cfg := &config.Config{
			Port:                  "9090",
			HTTPReadHeaderTimeout: 3 * time.Second,
			HTTPReadTimeout:       7 * time.Second,
			HTTPWriteTimeout:      12 * time.Second,
			HTTPIdleTimeout:       45 * time.Second,
		}

		srv := NewServer(cfg, deps, slog.New(slog.NewTextHandler(io.Discard, nil)))
		concrete, ok := srv.(*server)
		require.True(t, ok)

		assert.Equal(t, ":9090", srv.Addr())
		assert.NotNil(t, srv.Router())
		assert.Equal(t, 3*time.Second, concrete.srv.ReadHeaderTimeout)
		assert.Equal(t, 7*time.Second, concrete.srv.ReadTimeout)
		assert.Equal(t, 12*time.Second, concrete.srv.WriteTimeout)
		assert.Equal(t, 45*time.Second, concrete.srv.IdleTimeout)
	})

	t.Run("falls back to safe defaults when config and logger are nil", func(t *testing.T) {
		srv := NewServer(nil, deps, nil)
		concrete, ok := srv.(*server)
		require.True(t, ok)

		assert.Equal(t, ":8080", srv.Addr())
		assert.Equal(t, defaultReadHeaderTimeout, concrete.srv.ReadHeaderTimeout)
		assert.Equal(t, defaultReadTimeout, concrete.srv.ReadTimeout)
		assert.Equal(t, defaultWriteTimeout, concrete.srv.WriteTimeout)
		assert.Equal(t, defaultIdleTimeout, concrete.srv.IdleTimeout)
	})
}

func TestServer_StartAndStopLifecycle(t *testing.T) {
	deps := newTestDeps(t)
	cfg := &config.Config{
		Port:                  "0",
		HTTPReadHeaderTimeout: 2 * time.Second,
		HTTPReadTimeout:       5 * time.Second,
		HTTPWriteTimeout:      5 * time.Second,
		HTTPIdleTimeout:       10 * time.Second,
	}

	srv := NewServer(cfg, deps, slog.New(slog.NewTextHandler(io.Discard, nil)))

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	require.Eventually(t, func() bool {
		return srv.Addr() != ":0"
	}, 2*time.Second, 10*time.Millisecond, "server did not bind to an ephemeral port")

	client := &stdhttp.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + srv.Addr() + "/health")
	require.NoError(t, err)
	defer func() {
		_ = resp.Body.Close()
	}()
	assert.Equal(t, stdhttp.StatusOK, resp.StatusCode)

	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	require.NoError(t, srv.Stop(stopCtx))
	require.NoError(t, <-errCh)
}

func TestServer_Start_InvalidAddress(t *testing.T) {
	deps := newTestDeps(t)
	cfg := &config.Config{
		Port: "99999",
	}

	srv := NewServer(cfg, deps, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := srv.Start()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listening on :99999")
}
