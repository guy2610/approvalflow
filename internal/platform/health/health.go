package health

import (
	"net/http"
	"time"

	"approvalflow/internal/platform/httpx"
)

type Response struct {
	Service       string `json:"service"`
	Status        string `json:"status"`
	TimeUTC       string `json:"time_utc"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

func Handler(service string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, Response{
			Service:       service,
			Status:        "ok",
			TimeUTC:       time.Now().UTC().Format(time.RFC3339),
			CorrelationID: httpx.CorrelationIDFromContext(r.Context()),
		})
	}
}
