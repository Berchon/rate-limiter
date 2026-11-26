// Package app manages the application startup and graceful shutdown lifecycle.
package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Berchon/rate-limiter/internal/infra/config"
	"github.com/Berchon/rate-limiter/internal/infra/di"
	httpInfra "github.com/Berchon/rate-limiter/internal/infra/http"
)

// App defines the application lifecycle.
type App interface {
	Start()
	Stop(ctx context.Context) error
}

type app struct {
	cfg    *config.Config
	server httpInfra.Server
}

// NewApp creates and wires a new application instance.
func NewApp() App {
	// Carrega configuração
	// cfgService := config.NewConfigService()
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("❌ failed to load config: %v", err)
	}

	// 1️⃣ Constrói dependências
	deps := di.BuildDependencies(cfg)

	// 2️⃣ Cria o servidor com config e dependências
	srv := httpInfra.NewServer(cfg, deps)

	return &app{
		cfg:    cfg,
		server: srv,
	}
}

// Start inicia o servidor e aguarda sinais de shutdown
func (a *app) Start() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// Inicia servidor
	go func() {
		log.Printf("🚀 Starting server on %s", a.cfg.Port)
		if err := a.server.Start(); err != nil {
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

func (a *app) Stop(ctx context.Context) error {
	fmt.Println("Stopping application...")
	return a.server.Stop(ctx)
}
