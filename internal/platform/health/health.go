package health

import (
	"encoding/json"
	"net/http"
	"time"
)

type Response struct {
	Service string `json:"service"`
	Status  string `json:"status"`
	TimeUTC string `json:"time_utc"`
}

func Handler(service string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{
			Service: service,
			Status:  "ok",
			TimeUTC: time.Now().UTC().Format(time.RFC3339),
		})
	}
}
