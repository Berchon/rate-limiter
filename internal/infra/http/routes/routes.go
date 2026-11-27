// Package routes wires HTTP endpoints and middlewares onto the chi router.
package routes

import (
	"log/slog"
	"net/http"

	"github.com/Berchon/rate-limiter/internal/infra/di"
	"github.com/go-chi/chi"
)

// RegisterRoutes registers all public and rate-limited HTTP routes on the provided router.
func RegisterRoutes(router *chi.Mux, deps *di.Dependencies) *chi.Mux {
	if router == nil {
		router = chi.NewRouter()
	}

	router.Get("/ping", deps.GetPingHandler.Handle)
	router.Get("/health", deps.GetHealthHandler.Handle)
	router.Post("/api-key", deps.CreateAPIKeyHandler.Handle)

	router.Group(func(r chi.Router) {
		r.Use(deps.RateLimitMiddleware.Handle)

		r.Get("/protected", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("protected content")); err != nil {
				slog.Default().ErrorContext(r.Context(), "failed to write protected response", "error", err)
			}
		})
	})

	return router
}
