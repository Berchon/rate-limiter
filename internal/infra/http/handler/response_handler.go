package handler

import (
	"encoding/json"
	"net/http"
)

type ResponseHandler struct{}

func NewResponseHandler() *ResponseHandler {
	return &ResponseHandler{}
}

func (responseHandler *ResponseHandler) RequestResponse(w http.ResponseWriter, r *http.Request, body interface{}, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(body)
}
