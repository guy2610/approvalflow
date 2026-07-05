package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

type contextKey string

const (
	CorrelationIDHeader = "X-Correlation-Id"
	correlationIDKey    = contextKey("correlation_id")
)

func CorrelationIDFromContext(ctx context.Context) string {
	value, ok := ctx.Value(correlationIDKey).(string)
	if !ok {
		return ""
	}
	return value
}

func CorrelationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := r.Header.Get(CorrelationIDHeader)
		if correlationID == "" {
			correlationID = newCorrelationID()
		}

		w.Header().Set(CorrelationIDHeader, correlationID)

		ctx := context.WithValue(r.Context(), correlationIDKey, correlationID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newCorrelationID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "correlation-id-unavailable"
	}
	return hex.EncodeToString(buf[:])
}
