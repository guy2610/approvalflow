# ApprovalFlow Observability

## Summary

ApprovalFlow uses lightweight local-demo observability:

- Correlation IDs propagated through gateway and service calls
- Structured service logs
- Business audit trail by `correlation_id`
- Health endpoints for every service
- End-to-end verification through `scripts/verify.sh`

This is intentionally local-demo observability. A production deployment would normally add OpenTelemetry traces, metrics, dashboards, alerting, and centralized log storage.

## Correlation IDs

The gateway accepts an optional request header:

````http
X-Correlation-Id: demo-123
````

If the client does not provide one, the gateway creates a correlation id.

The correlation id connects:

- Submission request
- Decision result
- Approval queue event
- Human approval action
- Payment result
- Audit events
- Service logs

Example:

````bash
curl -i \
  -H "Content-Type: application/json" \
  -H "X-Correlation-Id: demo-observability-1001" \
  --data @/tmp/invoice.json \
  http://localhost:8080/submissions
````

## Audit trail

The audit service consumes `audit.event` messages and indexes them by correlation id.

Fetch audit events through the gateway:

````bash
curl -sS \
  -H "X-Demo-Role: auditor" \
  -H "X-Correlation-Id: demo-audit-read" \
  http://localhost:8080/audit/demo-observability-1001 | jq .
````

Important audit actions include:

| Action | Meaning |
|---|---|
| `submission_accepted` | Submission was accepted and a tracking id was created. |
| `duplicate_submission_detected` | Duplicate invoice was detected and the existing tracking id was returned. |
| `decision_produced` | Deterministic router produced a decision. |
| `approval_required_published` | Decision service paused the workflow for human review. |
| `approval_item_queued` | Approval service persisted a pending approval item. |
| `human_approved` | Human approver approved the item. |
| `human_rejected` | Human approver rejected the item. |
| `info_requested` | Human approver requested more information. |
| `payment_requested_published` | Payment processing was requested. |
| `payment_succeeded` | Payment simulation succeeded. |
| `payment_failed_compensated` | Payment simulation failed and compensation state was applied. |
| `duplicate_payment_ignored` | Duplicate payment request was ignored using idempotency state. |

## Logs

All Go services use the shared logger package and include useful fields such as:

- `service`
- `tracking_id`
- `invoice_id`
- `correlation_id`
- `event_id`
- `action`
- `error`

Useful local log commands:

````bash
docker compose logs -f gateway-service
docker compose logs -f decision-service
docker compose logs -f approval-service
docker compose logs -f payment-service
docker compose logs -f audit-service
````

Search by correlation id:

````bash
docker compose logs | grep demo-observability-1001
````

## Health checks

Each service exposes:

````http
GET /healthz
````

The gateway health endpoint is externally available:

````bash
curl -i http://localhost:8080/healthz
````

Internal service health endpoints are reachable inside Docker Compose and through service logs.

## Verification harness

The main observability and safety smoke test is:

````bash
./scripts/verify.sh
````

The script starts a clean local environment and verifies the verification scenarios:

- Auto approval
- Human review and resume
- Duplicate invoice detection
- Payment failure and compensation
- Prompt-injection / forced-agent-approval blocking
- Audit event presence

A successful run ends with:

````text
ALL VERIFICATION CHECKS PASSED
````

## Production gaps

The local demo does not include:

- Full OpenTelemetry tracing
- Prometheus metrics
- Grafana dashboards
- Centralized log aggregation
- Alerting
- Distributed rate limiting
- Production-grade audit database

Recommended production additions:

- Add OpenTelemetry SDK instrumentation to gateway and services.
- Export traces to an OTLP collector.
- Add Prometheus counters for submissions, decisions, approvals, payments, failures, and rate-limit rejections.
- Add dashboards for queue depth, decision routes, payment outcomes, error rate, and latency.
- Store audit events in an append-only transactional database table.
