package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/go-chi/chi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeServer struct {
	startFn func() error
	stopFn  func(ctx context.Context) error
	addr    string
}

func (f *fakeServer) Start() error {
	if f.startFn != nil {
		return f.startFn()
	}
	return nil
}

func (f *fakeServer) Stop(ctx context.Context) error {
	if f.stopFn != nil {
		return f.stopFn(ctx)
	}
	return nil
}

func (f *fakeServer) Addr() string {
	return f.addr
}

func (f *fakeServer) Router() *chi.Mux {
	return nil
}

type fakeCloser struct {
	closeCount atomic.Int32
	err        error
}

func (f *fakeCloser) Close() error {
	f.closeCount.Add(1)
	return f.err
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewApp(t *testing.T) {
	t.Run("initializes successfully with memory storage", func(t *testing.T) {
		t.Setenv("PORT", "18080")
		t.Setenv("STORAGE_TYPE", config.StorageTypeMemory)

		application, err := NewApp()
		require.NoError(t, err)
		require.NotNil(t, application)

		require.NoError(t, application.Stop(context.Background()))
	})

	t.Run("returns error when configuration is invalid", func(t *testing.T) {
		t.Setenv("PORT", "invalid-port")

		application, err := NewAppWithLogger(newDiscardLogger())
		require.Error(t, err)
		assert.Nil(t, application)
		assert.Contains(t, err.Error(), "loading configuration")
	})

	t.Run("returns error when dependency wiring fails", func(t *testing.T) {
		t.Setenv("PORT", "18080")
		t.Setenv("STORAGE_TYPE", config.StorageTypeRedis)
		t.Setenv("REDIS_URL", "redis://localhost:6379/notanumber")

		application, err := NewAppWithLogger(newDiscardLogger())
		require.Error(t, err)
		assert.Nil(t, application)
		assert.Contains(t, err.Error(), "building dependencies")
	})

	t.Run("accepts nil logger and falls back to default", func(t *testing.T) {
		t.Setenv("PORT", "18080")
		t.Setenv("STORAGE_TYPE", config.StorageTypeMemory)

		application, err := NewAppWithLogger(nil)
		require.NoError(t, err)
		require.NotNil(t, application)
	})
}

func TestApp_Run(t *testing.T) {
	t.Run("gracefully shuts down server and closes dependencies on context cancellation", func(t *testing.T) {
		startedCh := make(chan struct{})
		stopCh := make(chan struct{})
		var stopOnce sync.Once

		srv := &fakeServer{
			startFn: func() error {
				close(startedCh)
				<-stopCh
				return nil
			},
			stopFn: func(context.Context) error {
				stopOnce.Do(func() { close(stopCh) })
				return nil
			},
		}
		closer := &fakeCloser{}
		application := NewAppWithComponents(&config.Config{Port: "8080"}, srv, closer, newDiscardLogger())

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			errCh <- application.Run(ctx)
		}()

		<-startedCh
		cancel()

		require.NoError(t, <-errCh)
		assert.Equal(t, int32(1), closer.closeCount.Load())
	})

	t.Run("returns error and closes resources when server fails to start", func(t *testing.T) {
		startErr := errors.New("address already in use")
		srv := &fakeServer{
			startFn: func() error {
				return startErr
			},
		}
		closer := &fakeCloser{}
		application := NewAppWithComponents(&config.Config{Port: "8080"}, srv, closer, newDiscardLogger())

		err := application.Run(context.Background())

		require.ErrorIs(t, err, startErr)
		assert.Equal(t, int32(1), closer.closeCount.Load())
	})

	t.Run("closes resources when server exits cleanly before context cancellation", func(t *testing.T) {
		srv := &fakeServer{
			startFn: func() error { return nil },
		}
		closer := &fakeCloser{}
		application := NewAppWithComponents(&config.Config{Port: "8080"}, srv, closer, nil)

		require.NoError(t, application.Run(context.Background()))
		assert.Equal(t, int32(1), closer.closeCount.Load())
	})

	t.Run("joins stop and closer errors during shutdown", func(t *testing.T) {
		startedCh := make(chan struct{})
		stopCh := make(chan struct{})
		stopErr := errors.New("shutdown deadline exceeded")
		closeErr := errors.New("redis close failed")

		srv := &fakeServer{
			startFn: func() error {
				close(startedCh)
				<-stopCh
				return nil
			},
			stopFn: func(context.Context) error {
				close(stopCh)
				return stopErr
			},
		}
		closer := &fakeCloser{err: closeErr}
		application := NewAppWithComponents(&config.Config{Port: "8080"}, srv, closer, newDiscardLogger())

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			errCh <- application.Run(ctx)
		}()

		<-startedCh
		cancel()

		err := <-errCh
		require.ErrorIs(t, err, stopErr)
		require.ErrorIs(t, err, closeErr)
		assert.Equal(t, int32(1), closer.closeCount.Load())
	})
}

func TestApp_Start_GracefulShutdownOnSIGTERM(t *testing.T) {
	startedCh := make(chan struct{})
	stopCh := make(chan struct{})
	var stopOnce sync.Once

	srv := &fakeServer{
		startFn: func() error {
			close(startedCh)
			<-stopCh
			return nil
		},
		stopFn: func(context.Context) error {
			stopOnce.Do(func() { close(stopCh) })
			return nil
		},
	}
	closer := &fakeCloser{}
	application := NewAppWithComponents(&config.Config{Port: "8080"}, srv, closer, newDiscardLogger())

	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Start()
	}()

	<-startedCh
	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGTERM))

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("application did not shut down after SIGTERM")
	}

	assert.Equal(t, int32(1), closer.closeCount.Load())
}

func TestApp_Stop_IdempotentClose(t *testing.T) {
	srv := &fakeServer{}
	closer := &fakeCloser{}
	application := NewAppWithComponents(nil, srv, closer, newDiscardLogger())

	require.NoError(t, application.Stop(context.Background()))
	require.NoError(t, application.Stop(context.Background()))
	assert.Equal(t, int32(1), closer.closeCount.Load())
}
