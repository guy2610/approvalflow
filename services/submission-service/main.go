package main

import (
	"log"
	"net/http"
	"os"

	"approvalflow/internal/platform/health"
)

const serviceName = "submission-service"

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", health.Handler(serviceName))

	addr := ":" + port
	log.Printf("%s listening on %s", serviceName, addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("%s failed: %v", serviceName, err)
	}
}
