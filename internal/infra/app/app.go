// Package app manages the application startup and graceful shutdown lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/Berchon/rate-limiter/internal/infra/di"
	httpInfra "github.com/Berchon/rate-limiter/internal/infra/http"
)

const defaultShutdownTimeout = 10 * time.Second

// App defines the application lifecycle operations.
type App interface {
	Start() error
	Run(ctx context.Context) error
	Stop(ctx context.Context) error
}

type app struct {
	cfg             *config.Config
	server          httpInfra.Server
	closer          io.Closer
	logger          *slog.Logger
	shutdownTimeout time.Duration

	closeOnce sync.Once
	closeErr  error
}

// NewApp loads configuration, wires dependencies, and returns an initialized App instance.
func NewApp() (App, error) {
	return NewAppWithLogger(slog.Default())
}

// NewAppWithLogger loads configuration, wires dependencies with the provided logger, and returns an App instance.
func NewAppWithLogger(logger *slog.Logger) (App, error) {
	if logger == nil {
		logger = slog.Default()
	}

	cfg, err := config.LoadConfigWithLogger(logger)
	if err != nil {
		return nil, fmt.Errorf("loading configuration: %w", err)
	}

	deps, err := di.BuildDependencies(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("building dependencies: %w", err)
	}

	srv := httpInfra.NewServer(cfg, deps, logger)

	return NewAppWithComponents(cfg, srv, deps, logger), nil
}

// NewAppWithComponents constructs an App from explicit components, facilitating dependency injection and testing.
func NewAppWithComponents(cfg *config.Config, srv httpInfra.Server, closer io.Closer, logger *slog.Logger) App {
	if logger == nil {
		logger = slog.Default()
	}

	return &app{
		cfg:             cfg,
		server:          srv,
		closer:          closer,
		logger:          logger,
		shutdownTimeout: defaultShutdownTimeout,
	}
}

// Start runs the application until SIGINT or SIGTERM is received and performs a graceful shutdown.
func (a *app) Start() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return a.Run(ctx)
}

// Run starts the HTTP server and blocks until ctx is canceled or the server exits.
func (a *app) Run(ctx context.Context) error {
	serverErrCh := a.startServerAsync()

	select {
	case err := <-serverErrCh:
		return a.handleEarlyServerExit(err)
	case <-ctx.Done():
		return a.shutdownGracefully(serverErrCh)
	}
}

// Stop gracefully shuts down the HTTP server and closes external dependencies.
func (a *app) Stop(ctx context.Context) error {
	a.logger.Info("stopping application")

	srvErr := a.server.Stop(ctx)
	closeErr := a.closeResources()

	return errors.Join(srvErr, closeErr)
}

func (a *app) startServerAsync() <-chan error {
	errCh := make(chan error, 1)

	go func() {
		a.logger.Info("starting application", "port", a.configuredPort())
		errCh <- a.server.Start()
	}()

	return errCh
}

func (a *app) handleEarlyServerExit(serverErr error) error {
	if serverErr != nil {
		a.logger.Error("http server stopped with error", "error", serverErr)
	}

	return errors.Join(serverErr, a.closeResources())
}

func (a *app) shutdownGracefully(serverErrCh <-chan error) error {
	a.logger.Info("graceful shutdown initiated")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
	defer cancel()

	stopErr := a.Stop(shutdownCtx)
	serverErr := <-serverErrCh

	a.logShutdownOutcome(stopErr, serverErr)

	return errors.Join(stopErr, serverErr)
}

func (a *app) logShutdownOutcome(stopErr, serverErr error) {
	if stopErr != nil || serverErr != nil {
		a.logger.Error("application shutdown completed with errors",
			"stop_error", stopErr,
			"server_error", serverErr,
		)
		return
	}

	a.logger.Info("application stopped successfully")
}

func (a *app) configuredPort() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Port
}

func (a *app) closeResources() error {
	if a.closer == nil {
		return nil
	}

	a.closeOnce.Do(func() {
		if err := a.closer.Close(); err != nil {
			a.closeErr = fmt.Errorf("closing application dependencies: %w", err)
		}
	})

	return a.closeErr
}
