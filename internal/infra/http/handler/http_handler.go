// Package handler provides HTTP handlers for health, ping, and API key management.
package handler

import "net/http"

// HTTPHandler defines the common contract for application HTTP handlers.
type HTTPHandler interface {
	Handle(http.ResponseWriter, *http.Request)
}
