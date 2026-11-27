// Package http provides the HTTP server lifecycle and transport wiring.
package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/Berchon/rate-limiter/internal/infra/di"
	"github.com/Berchon/rate-limiter/internal/infra/http/routes"
	"github.com/go-chi/chi"
)

const (
	defaultPort              = "8080"
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 10 * time.Second
	defaultWriteTimeout      = 15 * time.Second
	defaultIdleTimeout       = 60 * time.Second
)

// Server defines the HTTP server lifecycle operations.
type Server interface {
	Start() error
	Stop(ctx context.Context) error
	Addr() string
	Router() *chi.Mux
}

type server struct {
	srv    *http.Server
	router *chi.Mux
	logger *slog.Logger

	mu   sync.RWMutex
	addr string
}

// NewServer creates an HTTP server instance with explicit timeouts and registers application routes.
func NewServer(cfg *config.Config, deps *di.Dependencies, logger *slog.Logger) Server {
	if logger == nil {
		logger = slog.Default()
	}

	port := defaultPort
	readHeaderTimeout := defaultReadHeaderTimeout
	readTimeout := defaultReadTimeout
	writeTimeout := defaultWriteTimeout
	idleTimeout := defaultIdleTimeout

	if cfg != nil {
		if cfg.Port != "" {
			port = cfg.Port
		}
		if cfg.HTTPReadHeaderTimeout > 0 {
			readHeaderTimeout = cfg.HTTPReadHeaderTimeout
		}
		if cfg.HTTPReadTimeout > 0 {
			readTimeout = cfg.HTTPReadTimeout
		}
		if cfg.HTTPWriteTimeout > 0 {
			writeTimeout = cfg.HTTPWriteTimeout
		}
		if cfg.HTTPIdleTimeout > 0 {
			idleTimeout = cfg.HTTPIdleTimeout
		}
	}

	router := routes.RegisterRoutes(chi.NewRouter(), deps)
	addr := fmt.Sprintf(":%s", port)

	return &server{
		router: router,
		logger: logger,
		addr:   addr,
		srv: &http.Server{
			Addr:              addr,
			Handler:           router,
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
		},
	}
}

// Start binds the configured TCP address and serves HTTP requests until Stop is called.
func (s *server) Start() error {
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.srv.Addr, err)
	}

	boundAddr := ln.Addr().String()
	s.mu.Lock()
	s.addr = boundAddr
	s.mu.Unlock()

	s.logger.Info("http server started", "addr", boundAddr)

	if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving http requests: %w", err)
	}

	return nil
}

// Stop gracefully shuts down the HTTP server using the provided context.
func (s *server) Stop(ctx context.Context) error {
	s.logger.Info("shutting down http server", "addr", s.Addr())

	if err := s.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down http server: %w", err)
	}

	return nil
}

// Addr returns the server listen address (updated with the bound address once started).
func (s *server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.addr
}

// Router returns the configured chi.Mux router.
func (s *server) Router() *chi.Mux {
	return s.router
}
