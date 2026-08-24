# ApprovalFlow Project Capabilities

## Purpose

This document summarizes ApprovalFlow's implemented capabilities, verification evidence, local-runtime boundaries, and current engineering limitations.

It shows:

- what is implemented,
- what is demonstrated by `./scripts/verify.sh`,
- what is implemented only as a local demo,
- what remains a documented production limitation.

## Status legend

| Status | Meaning |
|---|---|
| Implemented | Implemented and demonstrated locally. |
| Partial implementation | Implemented locally, but not at production scale or maturity. |
| Known limitation | Outside the current implementation and documented explicitly. |
| Not implemented | Not part of the current implementation. |

---

# 1. Product capabilities

| Capability | Status | Evidence |
|---|---:|---|
| AI-assisted invoice and expense approval platform | Implemented | Multi-service workflow with submission, decision, agent, approval, payment, and audit services. |
| Submit requests asynchronously | Implemented | `POST /submissions` returns `202 Accepted` and a `tracking_id`. |
| Track request status | Implemented | `GET /submissions/{tracking_id}`. |
| AI/agent recommendation | Implemented | `agent-service` returns recommendation, confidence, and cited policy rules. |
| Deterministic router is final authority | Implemented | `decision-service` uses `internal/policy`; the agent cannot directly approve, reject, or pay. |
| Human-in-the-loop review | Implemented | `approval-service`, `GET /approvals`, and approval action endpoints. |
| Payment after approval | Implemented | `payment-service` consumes `payment.requested`. |
| Complete audit trail | Implemented | `audit-service`, `GET /audit/{correlation_id}`. |
| Prove no auto-approval above configured limit | Implemented | Router hard-stop, safety report, and config-driven autonomy ceiling. |

---

# 2. Core engineering capabilities

## Repository and project structure

| Capability | Status | Evidence |
|---|---:|---|
| Single repository containing services, docs, scripts, and fixtures | Implemented | Go services, Python agent, Dapr/Compose files, `data/`, `docs/`, `scripts/`. |

## Local runtime and Docker Compose

| Capability | Status | Evidence |
|---|---:|---|
| Local runnable system | Implemented | `docker compose up --build -d`. |
| Dependencies included | Implemented | Redis, Postgres, Dapr sidecars. |
| One-command verification | Implemented | `./scripts/verify.sh`. |

## Dapr integration

| Dapr capability | Status | Evidence |
|---|---:|---|
| Service invocation | Implemented | Gateway/services call each other through Dapr. |
| Pub/Sub | Implemented | `submission.received`, `approval.required`, `payment.requested`, `audit.event`. |
| State | Implemented | Submission, duplicate, decision, approval, payment, audit, and autonomy budget state. |
| Secrets | Implemented | `payment-service` loads `payment-provider-token` from the local `localsecrets` Dapr secret store at startup. |

## API gateway

| Capability | Status | Evidence |
|---|---:|---|
| Gateway routes user-facing APIs | Implemented | Submissions, approvals, audit, analytics, notifications, and UI routes. |
| Correlation id propagation | Implemented | `X-Correlation-Id` middleware and structured logs. |
| Demo role checks | Implemented locally | Gateway enforces separate roles for approvals, audit, analytics, and notifications; `verify.sh` includes AuthZ smoke tests. |

## Asynchronous workflow

| Capability | Status | Evidence |
|---|---:|---|
| Submission returns before final decision | Implemented | `POST /submissions` returns `202`. |
| Decision is event-driven | Implemented | `decision-service` consumes `submission.received`. |
| Human review pauses workflow durably | Implemented | Approval item stored in Dapr state. |
| Workflow resumes after approval | Implemented | Human approval publishes `payment.requested`. |

## Gateway rate limiting

| Capability | Status | Evidence |
|---|---:|---|
| Gateway rate limiting | Implemented | Local in-memory limiter. |
| Distributed production limiter | Known limitation | Would require Redis/API-gateway-level distributed limiting. |

## Browser UI

| Capability | Status | Evidence |
|---|---:|---|
| Local workflow UI | Implemented | Gateway serves a browser console for invoice submission, workflow status, human approvals, request-info, revision-aware resubmission, audit lookup, and analytics. |
| Independent approval selection | Implemented | Users can select any approval item explicitly; pending, waiting-for-submitter, and completed states are presented separately. |
| Durable request-info resume in UI | Implemented | Submitters can provide notes, receipt status, or attendees through `POST /submissions/{tracking_id}/additional-info`; the UI follows the revised workflow automatically. |
| Friendly response display | Implemented | The UI presents structured workflow summaries while preserving the full raw JSON response in a collapsible section. |
| Automatic workflow refresh | Implemented | Submission polling refreshes asynchronous status, approval state, and controller analytics as workflows progress. |
| UI smoke verification | Implemented | `verify.sh` checks `GET /` returns `200`. |
| Controller dashboard | Implemented | UI displays throughput, auto/human routing, approved money, rejection, and payment-failure metrics from `/analytics/summary`. |
| Static portfolio demo | Implemented | `docs/` contains a clearly labelled browser-only simulation that can be served locally and does not claim to run backend services. |

## Payment workflow and compensation

| Capability | Status | Evidence |
|---|---:|---|
| Payment request after approval | Implemented | `payment.requested`. |
| Simulated payment success | Implemented | Submission reaches `PAID`. |
| Simulated payment failure | Implemented | `INV-1012` reaches `PAYMENT_FAILED`. |
| Compensation evidence | Implemented | `payment_failed_compensated` audit event. |
| Durable terminal notification channel | Implemented | `GET /notifications/{tracking_id}` and idempotent acknowledgement endpoint; `verify.sh` proves `PAID` and `PAYMENT_FAILED` notifications. |
| Real payment provider compensation | Known limitation | No external provider in local demo. |

## Idempotency and state guards

| Capability | Status | Evidence |
|---|---:|---|
| Duplicate submissions do not start a second workflow | Implemented | `INV-1007`, duplicate response and duplicate audit. |
| Human approval actions are not repeatable after final action | Implemented | Approval item status guard. |
| Payment events are idempotent | Implemented | `payment:{tracking_id}` state key. |
| Payment state guard | Implemented | Payment service validates submission status before payment effect. |
| Approval state guard | Implemented | Approval service validates submission status before human action. |

## Human review and workflow resumption

| Capability | Status | Evidence |
|---|---:|---|
| Human review item is durable | Implemented | `approval:{tracking_id}` state entry. |
| Approval queue visible | Implemented | `GET /approvals`. |
| Approve/reject/request-info actions | Implemented | Approval API. |
| Resume to payment after approval | Implemented | `INV-1003` verify path. |

## Autonomy safety invariant

| Capability | Status | Evidence |
|---|---:|---|
| Configured max auto-approval amount | Implemented | `data/policy-config.json`, `max_auto_approve_usd`. |
| Router prevents auto-approval above ceiling | Implemented | Deterministic policy hard-stop. |
| Safety report | Implemented | `reports/autonomy-safety-report.md`. |
| Cumulative small-invoice guardrail | Implemented | Daily submitter/vendor exposure limits and `AUTONOMY-BUDGET`. |

## Runtime policy configuration

| Capability | Status | Evidence |
|---|---:|---|
| Router thresholds are not hardcoded | Implemented | `data/policy-config.json`; router receives config object. |
| Config validation | Implemented | Policy config loader validation tests. |
| Verification-specific config | Implemented | `data/policy-config-verify.json` used by `scripts/verify.sh`. |
| Controller changes thresholds without redeploy | Implemented locally | `decision-service` reloads the mounted policy config for every decision; `verify.sh` proves runtime threshold changes with `INV-1022`. |

## Structured logging and correlation IDs

| Capability | Status | Evidence |
|---|---:|---|
| Structured service logs | Implemented | Shared logger package. |
| Correlation id propagation | Implemented | Gateway and services propagate/log correlation id. |
| Audit lookup by correlation id | Implemented | `GET /audit/{correlation_id}`. |

## Service health and HTTP reliability

| Capability | Status | Evidence |
|---|---:|---|
| Shared platform packages | Implemented | `internal/platform/config`, `httpx`, `logger`, `health`, `dapr`. |
| Health endpoints | Implemented | `/healthz`. |
| Clean error responses | Implemented | `httpx.WriteError`. |
| Domain model separation | Implemented | `internal/domain`. |

## Agent provider abstraction and failure handling

| Capability | Status | Evidence |
|---|---:|---|
| Agent isolated behind service boundary | Implemented | `agent-service` is called through Dapr. |
| Agent is advisory only | Implemented | Agent cannot directly approve/pay. |
| Agent failure fails safely | Implemented | Router remains deterministic safety boundary. |
| Multiple recommendation providers | Implemented locally | Local deterministic policy-retrieval provider and optional Gemini provider share a common response contract; Gemini failures fall back safely to local evaluation. |

## CI, testing, and project documentation

| Capability | Status | Evidence |
|---|---:|---|
| Unit tests | Implemented | `go test ./...`. |
| Verification harness | Implemented | `./scripts/verify.sh`. |
| CI workflow | Implemented | `.github/workflows/ci.yml` runs Go tests, shell syntax checks, OpenAPI linting, and Docker Compose validation. |
| Docker build workflow | Implemented | `.github/workflows/docker-build.yml` validates and builds all Compose images. |
| Static demo workflow | Implemented | `.github/workflows/pages.yml` validates the browser-only demo JavaScript and required files under `docs/`; public deployment is intentionally disabled. |
| README | Implemented | Setup, architecture, local UI, static demo boundary, verification, release, and documentation links. |
| OpenAPI documentation | Implemented | `docs/openapi.yaml` documents all public gateway endpoints and is linted through Makefile and CI. |

---

## Browser UI deployment model

The gateway-served UI is part of the complete local Docker Compose runtime and
communicates with the real gateway APIs.

The static site under `docs/` is a separate portfolio simulation that can be
served locally. It demonstrates the same primary workflow scenarios using
browser-local state, but does not run Go services, the Python agent, Dapr, Redis,
PostgreSQL, authorization checks, durable state, event delivery, or payment
idempotency.

Public GitHub Pages deployment is intentionally not enabled because the
repository was originally developed privately.

# 3. Supporting engineering capabilities

## Authorization model

| Capability | Status | Evidence |
|---|---:|---|
| Role separation | Implemented locally | Approvals use `approver`/`admin`, audit uses `auditor`/`admin`, analytics uses `controller`/`admin`, and notifications use `submitter`/`controller`/`admin`. |
| Production-grade AuthN/AuthZ | Known limitation | JWT/OIDC and service-to-service auth are not implemented. |

## Build and deployment automation

| Capability | Status | Evidence |
|---|---:|---|
| Docker build validation | Implemented | `.github/workflows/docker-build.yml` validates Compose and builds all service images on pushes and pull requests. |
| Static site assets | Implemented | The portfolio landing page and browser-only workflow simulation are included under `docs/` and can be served locally. |
| Static demo validation workflow | Implemented | `.github/workflows/pages.yml` validates JavaScript syntax and required static files without attempting deployment. |
| Public static deployment | Known limitation | The static demo is prepared for GitHub Pages deployment from the public repository. |
| Kubernetes reference deployment | Implemented | Reference manifests are provided under `k8s/`. |
| Backend continuous deployment | Post-release | No live microservice deployment target; registry publishing, health-gated rollout, rollback, secrets integration, and external environment validation are planned in `docs/POST-RELEASE-ROADMAP.md`. |

## Distributed reliability patterns

| Capability | Status | Evidence |
|---|---:|---|
| Boundary-specific idempotency | Implemented locally | Duplicate submissions, approval retries, payment processing, notifications, audit records, and autonomy-budget updates use stable state keys or revision-aware guards. |
| Approval concurrency and stale-event safety | Implemented | Dapr ETags protect conflicting approval actions; revision and source-event metadata reject duplicate and stale approval events. |
| Throttling | Implemented locally | Gateway local in-memory limiter. |
| Transactional outbox and shared durable inbox | Post-release | Not implemented in `v1.0`; planned with dead-letter and replay tooling in `docs/POST-RELEASE-ROADMAP.md`. |
| Distributed rate limiting | Post-release | Multi-replica shared quotas are not implemented. |

## Observability

| Capability | Status | Evidence |
|---|---:|---|
| Structured logs | Implemented | Correlation ids in logs. |
| Audit trail | Implemented | Audit service. |
| Analytics and health endpoints | Implemented | Controller summary, dashboard, and `/healthz` endpoints. |
| Runbook | Implemented | `docs/operations/runbook.md`. |
| OpenTelemetry, metrics backend, and dashboards | Post-release | A production telemetry stack is planned in `docs/POST-RELEASE-ROADMAP.md`. |

## Policy retrieval and grounding

| Capability | Status | Evidence |
|---|---:|---|
| Policy rule citations | Implemented | Agent returns cited policy rules. |
| Local policy retrieval | Implemented | Agent loads local `data/policy.md`. |
| Full vector RAG | Known limitation | Not implemented as a production vector/RAG stack. |

## Multi-layer testing

| Capability | Status | Evidence |
|---|---:|---|
| Unit tests | Implemented | Policy/config/router/platform tests. |
| Acceptance tests | Implemented | `scripts/verify.sh`. |
| E2E scenarios | Implemented | Verification scenarios plus safety scenarios. |

---

# 4. Extended capabilities

## Evaluation harness and reports

| Capability | Status | Evidence |
|---|---:|---|
| Deterministic evaluation harness | Implemented | `scripts/verify.sh` validates product verification scenarios, safety boundaries, notification delivery, analytics, runtime configuration, and authorization. |
| Consolidated evaluation report | Implemented | `reports/evaluation-report.md` documents automated, race-enabled, concurrency, stale-event, recovery, and HTTP-hardening evidence. |
| Safety report | Implemented | `reports/autonomy-safety-report.md`. |
| Large statistical eval dataset | Not implemented | Could be added later. |

## MCP integration

| Capability | Status | Evidence |
|---|---:|---|
| MCP server | Post-release | Not implemented in `v1.0`; a gateway-backed read-only MCP service is planned first in `docs/POST-RELEASE-ROADMAP.md`. |

## Kubernetes reference deployment

| Capability | Status | Evidence |
|---|---:|---|
| Kubernetes manifests | Implemented | Reference manifests under `k8s/` include services, Deployments, Dapr annotations, policy ConfigMap, and Secret placeholders. |

---

# 5. Verification scenarios

Run:

````bash
./scripts/verify.sh
````

The verification harness starts the stack with:

````text
POLICY_CONFIG_PATH=data/policy-config-verify.json
````

This keeps the cumulative-budget scenario short and deterministic while leaving the default demo policy in:

````text
data/policy-config.json
````

| Journey | Fixture | Expected result | Status |
|---|---|---|---:|
| Auto approval and payment | `INV-1001` | `PAID` | Implemented |
| Second auto-approved item | `INV-1002` | `PAID` | Implemented |
| Human review and resume | `INV-1003` | Human review, approval, then `PAID` | Implemented |
| Duplicate submission | `INV-1007` | Duplicate response and no second payment | Implemented |
| Payment failure and compensation | `INV-1012` | `PAYMENT_FAILED` and compensation audit | Implemented |
| Durable terminal notifications | `INV-1001` / `INV-1012` | `PAID` and `PAYMENT_FAILED` notifications; acknowledgement persists | Implemented |
| Prompt injection cannot bypass router | `INV-1013` | Human review despite agent recommending approval | Implemented |
| Split-invoice cumulative budget guardrail | `INV-1020` / `INV-1021` | First `PAID`; second `HUMAN_REVIEW_REQUIRED` with `AUTONOMY-BUDGET` | Implemented |
| Runtime policy config reload | `INV-1022` | Verify lowers `max_auto_approve_usd` at runtime; next invoice becomes `HUMAN_REVIEW_REQUIRED` | Implemented |
| UI and local role authorization smoke tests | `/`, approvals, audit, analytics, notifications | UI returns `200`; protected routes reject missing roles and accept permitted roles | Implemented |
| Optional Gemini provider smoke | direct `/recommend` local run | Gemini returned `provider=gemini-policy-agent-v1`; verification remains local by default | Done manually |

---

# 6. Additional reliability hardening

| Hardening | Why it matters | Status |
|---|---|---:|
| Config-driven policy router | Router thresholds are not hardcoded in business logic. | Implemented |
| Separate verification policy config | Acceptance tests can prove edge cases without weakening default demo policy. | Implemented |
| Cumulative autonomy budget | Prevents many small invoices from bypassing per-invoice ceiling. | Implemented |
| Payment state guard | Prevents malformed, premature, replayed, or forged payment events from creating payment effects. | Implemented |
| Approval state guard | Prevents stale human actions from mutating workflows that already moved forward. | Implemented |
| Approval optimistic concurrency | Uses Dapr ETags and first-write concurrency so simultaneous human actions converge to one winner. | Implemented |
| Revision-aware approval events | Duplicate and stale approval events cannot reopen or overwrite a newer approval revision. | Implemented |
| Strict public JSON inputs | Rejects unsupported media types, malformed or unknown JSON, multiple values, and bodies above 1 MiB. | Implemented |
| Hardened HTTP servers | Shared read, write, idle, header, and request limits reduce exposure to slow or abusive clients. | Implemented |
| Operations runbook | Makes local demo diagnosable and explainable. | Implemented |

---

# 7. Current limitations

| Area | Limitation | Production direction |
|---|---|---|
| Authentication | Demo role headers only. | JWT/OIDC, RBAC, service-to-service auth. |
| Rate limiting | In-memory gateway limiter. | Distributed limiter, Redis/API gateway, per-tenant quotas. |
| Messaging consistency | No transactional outbox. | DB-backed outbox/inbox, retries, DLQ. |
| Payment | Simulated provider. | Real payment provider integration, reconciliation, provider idempotency keys. |
| Audit storage | Local demo state. | Append-only audit DB/WORM storage. |
| Observability | Logs and audit trail only. | OpenTelemetry traces, metrics, dashboards, alerts. |
| Policy management | Mounted file-based config with per-decision reload in local demo. | Production should add admin UI/config service, policy versioning, approval workflow, and audit for policy changes. |
| AI provider | Local deterministic agent. | Swappable LLM providers, model monitoring, evals, fallback policy. |
| Deployment | Docker Compose. | Kubernetes/Helm, secrets manager, HA, autoscaling. |
| Dapr secrets | Local dev-only secret store. | Production should use Vault, Kubernetes Secrets, or a cloud secret manager. |

---

# 8. Future engineering roadmap

| Stage | Reason |
|---|---|
| Dapr secrets minimal implementation | Done. `payment-service` loads a dev-only provider token through Dapr secrets. |
| Policy no-redeploy config behavior | Done. Mounted policy config is reloaded per decision and verified with `INV-1022`. |
| UI/AuthZ smoke verification | Done. `verify.sh` checks UI availability and protected route authorization. |
| Optional Gemini/provider abstraction | Done. Gemini is available as an optional provider with deterministic local fallback. |
| Kubernetes manifests | Done. Reference manifests are provided under `k8s/`. |
| Final polish and presentation prep | Remaining. Prepare the final explanation, tradeoffs, and demo script. |

---

# 9. Verification commands

Before a demo or release:

````bash
go test ./...
docker compose config --quiet
./scripts/verify.sh
````

Expected ending:

````text
ALL VERIFICATION CHECKS PASSED
````

## Recent test-hardening note

Unit tests were expanded across all Go packages. During this hardening pass, a decision-service edge case was found and fixed: unknown decision routes now fail closed to `HUMAN_REVIEW_REQUIRED` instead of remaining in `PROCESSING`.
