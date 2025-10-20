package handler

import "net/http"

type GetHealthHandler interface {
	HttpHandler
}

type getHealthHandler struct {
	response *ResponseHandler
}

type status struct {
	Status string `json:"status"`
}

func NewGetHealthHandler() GetHealthHandler {
	response := NewResponseHandler()
	return &getHealthHandler{
		response: response,
	}
}

func (h *getHealthHandler) Handle(w http.ResponseWriter, r *http.Request) {
	status := status{Status: "Healthy"}
	h.response.RequestResponse(w, r, status, http.StatusOK)
}

type GetPingHandler interface {
	HttpHandler
}

type getPingHandler struct {
	response *ResponseHandler
}

func NewGetPingHandler() GetPingHandler {
	response := NewResponseHandler()
	return &getPingHandler{
		response: response,
	}
}

func (h *getPingHandler) Handle(w http.ResponseWriter, r *http.Request) {
	status := status{Status: "Pong"}
	h.response.RequestResponse(w, r, status, http.StatusOK)
}
