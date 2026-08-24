# ApprovalFlow Release Readiness Report

## Purpose

This report records automated, exploratory, documentation, security, and recovery checks performed before the current ApprovalFlow release.

The audit focuses on the current local implementation. Production capabilities that are intentionally deferred are documented separately in `docs/POST-RELEASE-ROADMAP.md`.

## Audited revision

The release-readiness review was completed on `main` after the UI, documentation, and
static-site changes were merged.

The audited release includes:

- optimistic concurrency for human approval actions,
- duplicate and stale approval-event rejection,
- strict JSON decoding and request-body limits,
- shared HTTP server limits and timeouts,
- synchronized API, operational, architecture, and evaluation documentation,
- independent approval-item selection,
- complete request-info and revision-aware submitter flow in the local UI,
- automatic asynchronous status and analytics refresh,
- friendly workflow summaries with collapsible raw API responses,
- a clearly labelled browser-only simulation under `docs/`.

## 1. Automated quality gates

| Check | Result |
|---|---:|
| `go test -count=1 ./...` | Pass |
| `go test -race -count=1 ./...` | Pass |
| `go vet ./...` | Pass |
| Python syntax compilation | Pass |
| Shell syntax validation for `scripts/verify.sh` | Pass |
| Docker Compose configuration validation | Pass |
| OpenAPI 3.1 lint | Pass |
| Go formatting check | Pass |
| Static-site JavaScript syntax | Pass |
| Static-site required-file validation | Pass |
| Local static-site landing and interactive workflow smoke test | Pass |
| `git diff --check` | Pass |

## 2. Repository and secret audit

| Area | Result | Notes |
|---|---:|---|
| Real environment file tracked | Pass | `.env` is ignored and not tracked. |
| Secret-like tracked files | Pass | Tracked values are local-development placeholders only. |
| Private keys or credentials | Pass | No private key or real API credential was found. |
| Generated Python artifacts | Pass | No tracked cache artifacts were found. |
| Large tracked files | Pass | No tracked file exceeded 1 MiB. |
| Unexpected binaries | Pass | Only `scripts/verify.sh` is intentionally executable. |
| TODO/FIXME markers | Pass | No unresolved implementation markers were found. |
| External ports | Pass | Only the gateway is published on host port 8080. |

The locally stored DOCX file is ignored and is not part of the repository.

## 3. Documentation and API consistency

The runtime gateway routes were compared with `docs/openapi.yaml`, README, architecture documentation, the operations runbook, and the project capabilities document.

Corrections made during the audit:

- documented HTTP `413 Request Entity Too Large`,
- documented HTTP `415 Unsupported Media Type`,
- documented approval `revision_number` and `source_event_id`,
- corrected audit-role documentation to `auditor` or `admin`,
- documented persistent Compose state and destructive reset behavior,
- clarified that the local agent is the default and Gemini is optional,
- documented post-release production engineering work separately from `v1.0`.

## 4. Exploratory API testing

| Scenario | Expected | Observed | Result |
|---|---:|---:|---:|
| Malformed submission JSON | `400` | `400` | Pass |
| Unsupported submission Content-Type | `415` | `415` | Pass |
| Unknown JSON field | `400` | `400` | Pass |
| Multiple JSON values in one body | `400` | `400` | Pass |
| Request body above 1 MiB | `413` | `413` | Pass |
| Approval queue without role | `403` | `403` | Pass |
| Audit endpoint with wrong role | `403` | `403` | Pass |
| Approval action for missing item | `404` | `404` | Pass |
| Unsupported HTTP method | Rejected | Rejected | Pass |
| Panic or fatal log during tests | None | None | Pass |

## 5. Restart and persistence testing

A full Compose stop and start was performed without removing named volumes.

Observed behavior:

- gateway health recovered,
- approval queue state remained available,
- analytics state remained available,
- revision-aware approval data remained durable,
- no state loss was observed.

The supported local restart procedure is:

```bash
docker compose down
docker compose up -d
```

A destructive reset is performed only with:

```bash
docker compose down -v
```

## 6. UI and static-site validation

The gateway-served local UI was validated against the real Docker Compose
runtime.

Validated scenarios included:

- low-risk automatic approval through payment,
- independent selection of multiple human-review items,
- human approval and rejection,
- request-info transition to `INFO_REQUESTED`,
- submitter additional-information submission,
- revision increment and return to policy evaluation,
- revision-aware reopening of human review,
- final approval and payment,
- completed-approval history,
- automatic workflow and analytics refresh,
- audit lookup using the workflow correlation id,
- friendly summaries with access to the full raw JSON response.

The static site under `docs/` was also validated locally.

The site is explicitly labelled as a browser-only simulation. It uses
browser-local state and does not represent the Go services, Python agent, Dapr,
Redis, PostgreSQL, durable workflow storage, authorization enforcement, or
payment idempotency as running in the browser.

The repository includes a GitHub Pages deployment workflow for the static site under `docs/`. The browser-only simulation can be deployed independently of the local backend and does not imply that the Go services, Python agent, Dapr, Redis, or PostgreSQL are running in GitHub Pages.

## 7. Findings

### F-01 — Partial application-only restart can break Dapr connectivity

| Attribute | Value |
|---|---|
| Severity | Low / operational |
| Status | Documented |
| Trigger | Restarting only application containers while their Dapr sidecars remain running |
| Impact | Temporary service unavailability through `127.0.0.1:3500` |
| State loss | None observed |
| Recovery | Full `docker compose down` followed by `docker compose up -d`, without `-v` |
| Action taken | Added supported restart guidance to the operations runbook |

This behavior results from the local Compose sidecars sharing application network namespaces. It does not affect the documented full-stack restart procedure.

### F-02 — Expected downstream client errors are logged at error level

| Attribute | Value |
|---|---|
| Severity | Low / observability |
| Status | Accepted for initial release |
| Example | Submission rejection for an unknown JSON field |
| Impact | Log noise and possible false-positive alerting |
| Runtime behavior | Correct HTTP response is still returned to the client |
| Recommended follow-up | Distinguish expected downstream `4xx` responses from infrastructure failures in gateway logging |

Neither finding blocks the demonstrated workflow or current release.

## 8. Production boundaries

The following are not represented as implemented `v1.0` features:

- transactional outbox,
- shared durable inbox,
- dead-letter administration and replay tooling,
- distributed rate limiting,
- production OpenTelemetry infrastructure,
- external continuous deployment,
- production authentication and authorization,
- live production Kubernetes deployment,
- MCP service.

These directions are documented in `docs/POST-RELEASE-ROADMAP.md`.

## 9. Final assessment

The audited local implementation is consistent with its documented scope.

Automated tests, race detection, static analysis, OpenAPI validation, negative
API testing, authorization checks, persistence checks, local UI scenario
validation, static-site validation, and repository inspection passed.

The release-readiness checks did not find any issue that should block the current release. No real
credentials were committed, the tested workflows completed without state loss
or runtime crashes, and the static browser simulation is clearly separated from the real
Docker Compose system.
