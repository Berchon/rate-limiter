// Package http provides the HTTP server lifecycle and transport wiring.
package http

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/Berchon/rate-limiter/internal/infra/di"
	"github.com/Berchon/rate-limiter/internal/infra/http/routes"
	"github.com/go-chi/chi"
)

// Server defines the HTTP server lifecycle operations.
type Server interface {
	Start() error
	Stop(ctx context.Context) error
	Addr() string
	Router() *chi.Mux
}

type server struct {
	cfg    *config.Config
	srv    *http.Server
	deps   *di.Dependencies
	router *chi.Mux
}

// NewServer cria uma instância de servidor HTTP e registra as rotas
func NewServer(cfg *config.Config, deps *di.Dependencies) Server {
	r := chi.NewRouter()
	router := routes.RegisterRoutes(r, deps)

	return &server{
		cfg:    cfg,
		deps:   deps,
		router: router,
		srv: &http.Server{
			Addr:              fmt.Sprintf(":%s", cfg.Port),
			Handler:           r,
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

// Start inicia o servidor em uma goroutine
func (s *server) Start() error {
	fmt.Printf("Server started at :%s\n", s.Addr())
	// return s.srv.ListenAndServe()
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("💥 HTTP server error: %v\n", err)
		}
	}()
	return nil
}

// Stop realiza shutdown gracioso do servidor
func (s *server) Stop(ctx context.Context) error {
	fmt.Println("Shutting down server...")
	return s.srv.Shutdown(ctx)
}

// Addr retorna o endereço do servidor
func (s *server) Addr() string {
	return s.srv.Addr
}

// Router retorna o roteador chi.Mux
func (s *server) Router() *chi.Mux {
	return s.router
}
