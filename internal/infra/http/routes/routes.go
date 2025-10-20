package routes

import (
	"net/http"

	"github.com/Berchon/rate-limiter/internal/infra/di"
	"github.com/go-chi/chi"
)

// RegisterRoutes cria o roteamento completo da aplicação
func RegisterRoutes(router *chi.Mux, handlers *di.Dependencies) *chi.Mux {
	router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	router.Get("/ping", handlers.GetPingHandler.Handle)

	router.Get("/health", handlers.GetHealthHandler.Handle)

	router.Post("/api-key", handlers.CreateAPIKeyHandler.Handle)

	// router.Group(func(r chi.Router) {
	// 	r.Use(handlers.RateLimitMiddleware.Handle)

	// 	// Aqui você adiciona outras rotas que precisam de proteção
	// 	// r.Get("/protected", protectedHandler)
	// })
	return router
}
