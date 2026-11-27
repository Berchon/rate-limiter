// Package main is the entry point for the rate-limiter HTTP service.
package main

import (
	"log/slog"
	"os"

	"github.com/Berchon/rate-limiter/internal/infra/app"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	application, err := app.NewAppWithLogger(logger)
	if err != nil {
		logger.Error("failed to initialize application", "error", err)
		os.Exit(1)
	}

	if err := application.Start(); err != nil {
		logger.Error("application exited with error", "error", err)
		os.Exit(1)
	}
}
