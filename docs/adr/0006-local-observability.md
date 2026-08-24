# ADR 0006: Local observability model

## Status

Accepted

## Context

ApprovalFlow is a local-first event-driven system. The design prioritizes explainability, auditability, and operational clarity while remaining runnable with a simple Docker Compose setup.

Adding a full OpenTelemetry, Prometheus, and Grafana stack would improve production realism, but would also increase setup complexity and risk.

## Decision

ApprovalFlow uses lightweight local observability:

- Correlation IDs across gateway requests, service calls, events, logs, and audit records
- Structured service logs
- Business audit trail indexed by `correlation_id`
- `/healthz` endpoints
- End-to-end verification through `scripts/verify.sh`

The production observability gap is documented explicitly in `docs/operations/observability.md`.

## Consequences

The local environment remains easy to run and inspect.

A reviewer can trace important workflow transitions through audit events and logs without installing additional infrastructure.

This is not a replacement for production observability. A production version should add OpenTelemetry tracing, metrics, dashboards, centralized logging, and alerting.
