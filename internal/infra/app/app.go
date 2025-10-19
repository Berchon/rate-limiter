package app

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/Berchon/rate-limiter/internal/infra/di"
)

type App interface {
	Start()
	Stop(ctx context.Context) error
}

type appImpl struct {
	cfg  *config.Config
	deps *di.Dependencies
}

// New cria a aplicação, carregando configuração e dependências
func New() App {
	// Carrega configuração
	cfgService := config.NewConfigService()
	cfg, err := cfgService.Load()
	if err != nil {
		log.Fatalf("❌ failed to load config: %v", err)
	}

	// Cria dependências
	deps := di.BuildDependencies(cfg)

	return &appImpl{
		cfg:  cfg,
		deps: deps,
	}
}

// Start inicia o servidor e aguarda sinais de shutdown
func (a *appImpl) Start() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	server := a.deps.Server

	// Inicia servidor
	go func() {
		log.Printf("🚀 Starting server on %s", server.Addr())
		if err := server.Start(); err != nil {
			if err.Error() != "http: Server closed" {
				log.Fatalf("💥 Server error: %v", err)
			}
		}
	}()

	select {
	case <-ctx.Done():
		log.Println("🛑 Graceful shutdown initiated (SIGINT or SIGTERM received).")
	case <-sigCh:
		log.Println("⚠️ Force shutdown (second signal received).")
		os.Exit(1)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := a.Stop(shutdownCtx); err != nil {
		log.Printf("⏳ Error during server shutdown: %v", err)
	} else {
		log.Println("✅ Server stopped successfully")
	}
}

// Stop realiza shutdown gracioso do servidor
func (a *appImpl) Stop(ctx context.Context) error {
	return a.deps.Server.Stop(ctx)
}
