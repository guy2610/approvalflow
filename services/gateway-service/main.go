package main

import (
	"net/http"

	"approvalflow/internal/platform/config"
	"approvalflow/internal/platform/health"
	"approvalflow/internal/platform/httpx"
	"approvalflow/internal/platform/logger"
)

const serviceName = "gateway-service"

func main() {
	log := logger.New(serviceName)

	port := config.GetEnv("PORT", "8080")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", health.Handler(serviceName))

	handler := httpx.CorrelationMiddleware(mux)

	addr := ":" + port
	log.Info("service starting", logger.Fields{"addr": addr})

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Error("service failed", logger.Fields{"error": err.Error()})
	}
}
