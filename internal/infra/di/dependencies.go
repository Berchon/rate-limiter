package di

import (
	"github.com/Berchon/rate-limiter/internal/infra/config"
	httpInfra "github.com/Berchon/rate-limiter/internal/infra/http"
)

// Dependencies agrupa as instâncias compartilhadas da aplicação
type Dependencies struct {
	Server httpInfra.Server
	// RateLimiterUC usecase.RateLimiterUseCase
	// futuramente: logger, storage, outros usecases
}

// BuildDependencies cria e retorna todas as dependências da aplicação
func BuildDependencies(cfg *config.Config) *Dependencies {
	// --- HTTP Server ---
	server := httpInfra.NewServer(":" + cfg.AppPort)

	// --- Services ---
	// rateLimiterService := service.NewRateLimiterService(cfg.RateLimitRPS, cfg.BurstCapacity)

	// --- UseCases ---
	// rateLimiterUC := usecase.NewRateLimiterUseCase(rateLimiterService)

	return &Dependencies{
		Server: server,
		// RateLimiterUC: rateLimiterUC,
	}
}
