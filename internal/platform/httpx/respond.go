package httpx

import (
	"encoding/json"
	"net/http"
)

type ErrorResponse struct {
	Error         string `json:"error"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, message string) {
	WriteJSON(w, status, ErrorResponse{
		Error:         message,
		CorrelationID: CorrelationIDFromContext(r.Context()),
	})
}
