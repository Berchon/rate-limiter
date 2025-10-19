package http

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server interface {
	Start() error
	Stop(ctx context.Context) error
	Addr() string
	Router() *chi.Mux
}

type serverImpl struct {
	addr   string
	server *http.Server
	router *chi.Mux
}

// NewServer cria uma instância de servidor HTTP com chi e middlewares básicos
func NewServer(addr string) Server {
	r := chi.NewRouter()

	// Middlewares
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Rota de healthcheck
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	return &serverImpl{
		addr:   addr,
		server: srv,
		router: r,
	}
}

// Start inicia o servidor em uma goroutine
func (s *serverImpl) Start() error {
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("💥 HTTP server error: %v\n", err)
		}
	}()
	return nil
}

// Stop realiza shutdown gracioso do servidor
func (s *serverImpl) Stop(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// Addr retorna o endereço do servidor
func (s *serverImpl) Addr() string {
	return s.addr
}

// Router retorna o mux chi, permitindo adicionar middlewares ou rotas adicionais
func (s *serverImpl) Router() *chi.Mux {
	return s.router
}
