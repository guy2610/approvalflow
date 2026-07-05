package main

import (
	"io"
	"net/http"
	"strings"

	"approvalflow/internal/platform/config"
	daprclient "approvalflow/internal/platform/dapr"
	"approvalflow/internal/platform/health"
	"approvalflow/internal/platform/httpx"
	"approvalflow/internal/platform/logger"
)

const serviceName = "gateway-service"

type server struct {
	log  *logger.Logger
	dapr *daprclient.Client
}

func main() {
	log := logger.New(serviceName)
	port := config.GetEnv("PORT", "8080")

	srv := &server{
		log:  log,
		dapr: daprclient.NewFromEnv(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", health.Handler(serviceName))
	mux.HandleFunc("/submissions", srv.handleSubmissions)
	mux.HandleFunc("/submissions/", srv.handleSubmissionByID)
	mux.HandleFunc("/approvals", srv.handleApprovals)
	mux.HandleFunc("/approvals/", srv.handleApprovalAction)
	mux.HandleFunc("/audit/", srv.handleAuditTrail)

	handler := httpx.CorrelationMiddleware(mux)

	addr := ":" + port
	log.Info("service starting", logger.Fields{"addr": addr})

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Error("service failed", logger.Fields{"error": err.Error()})
	}
}

func (s *server) handleSubmissions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "failed to read request body")
		return
	}
	defer r.Body.Close()

	status, raw, err := s.dapr.InvokeRaw(r.Context(), "submission-service", "submissions", http.MethodPost, body)
	if err != nil {
		s.log.Error("submission invocation failed", logger.Fields{
			"error":          err.Error(),
			"correlation_id": httpx.CorrelationIDFromContext(r.Context()),
		})

		if status == 0 {
			httpx.WriteError(w, r, http.StatusBadGateway, "submission service unavailable")
			return
		}
	}

	writeRawJSON(w, status, raw)
}

func (s *server) handleSubmissionByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	trackingID := strings.TrimPrefix(r.URL.Path, "/submissions/")
	if trackingID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "missing tracking id")
		return
	}

	status, raw, err := s.dapr.InvokeRaw(r.Context(), "submission-service", "submissions/"+trackingID, http.MethodGet, nil)
	if err != nil {
		s.log.Error("submission status invocation failed", logger.Fields{
			"error":          err.Error(),
			"correlation_id": httpx.CorrelationIDFromContext(r.Context()),
		})

		if status == 0 {
			httpx.WriteError(w, r, http.StatusBadGateway, "submission service unavailable")
			return
		}
	}

	writeRawJSON(w, status, raw)
}

func (s *server) handleApprovals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	status, raw, err := s.dapr.InvokeRawPassthrough(r.Context(), "approval-service", "approvals", http.MethodGet, nil)
	if err != nil {
		s.log.Error("approval service unavailable", logger.Fields{
			"error":          err.Error(),
			"correlation_id": httpx.CorrelationIDFromContext(r.Context()),
		})
		httpx.WriteError(w, r, http.StatusBadGateway, "approval service unavailable")
		return
	}

	writeRawJSON(w, status, raw)
}

func (s *server) handleApprovalAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/approvals/")
	if path == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "missing approval action path")
		return
	}

	if r.Method != http.MethodPost {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "failed to read request body")
		return
	}
	defer r.Body.Close()

	status, raw, err := s.dapr.InvokeRawPassthrough(r.Context(), "approval-service", "approvals/"+path, http.MethodPost, body)
	if err != nil {
		s.log.Error("approval service unavailable", logger.Fields{
			"error":          err.Error(),
			"correlation_id": httpx.CorrelationIDFromContext(r.Context()),
		})
		httpx.WriteError(w, r, http.StatusBadGateway, "approval service unavailable")
		return
	}

	writeRawJSON(w, status, raw)
}

func (s *server) handleAuditTrail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/audit/")
	path = strings.Trim(path, "/")
	if path == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "missing correlation id")
		return
	}

	if r.Method != http.MethodGet {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	status, raw, err := s.dapr.InvokeRawPassthrough(r.Context(), "audit-service", "audit/"+path, http.MethodGet, nil)
	if err != nil {
		s.log.Error("audit service unavailable", logger.Fields{
			"error":          err.Error(),
			"correlation_id": httpx.CorrelationIDFromContext(r.Context()),
		})
		httpx.WriteError(w, r, http.StatusBadGateway, "audit service unavailable")
		return
	}

	writeRawJSON(w, status, raw)
}

func writeRawJSON(w http.ResponseWriter, status int, raw []byte) {
	if status == 0 {
		status = http.StatusBadGateway
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if len(raw) > 0 {
		_, _ = w.Write(raw)
		return
	}

	_, _ = w.Write([]byte("{}"))
}
