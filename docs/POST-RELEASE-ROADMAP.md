# ApprovalFlow Post-Release Engineering Roadmap

## Purpose

ApprovalFlow `v1.0` focuses on a reliable and reproducible local implementation of an asynchronous, policy-controlled approval workflow.

The current implementation already includes:

- asynchronous event-driven processing,
- deterministic policy enforcement,
- human-in-the-loop pause and resume,
- boundary-specific idempotency,
- optimistic concurrency for approval actions,
- revision-aware stale-event protection,
- simulated payment compensation,
- audit records, notifications, analytics, and local observability,
- Docker Compose, CI validation, and Kubernetes reference manifests.

This roadmap documents engineering improvements intentionally deferred beyond the initial local release.

The items below are planned production-evolution directions. They are not claims about functionality already implemented in `v1.0`.

## Prioritization principles

Post-release work should preserve the following principles:

1. deterministic policy remains authoritative,
2. new infrastructure must not bypass the gateway or authorization boundary,
3. reliability mechanisms should be testable through failure injection,
4. state transitions must remain idempotent and revision-aware,
5. production complexity should be added incrementally,
6. each milestone should include documentation, tests, and operational guidance.

## Track A — Messaging reliability and distributed safety

### Current state

ApprovalFlow currently provides:

- duplicate-submission protection,
- payment idempotency,
- audit duplicate suppression,
- idempotent notification acknowledgement,
- approval retry reconciliation,
- optimistic concurrency for simultaneous approval actions,
- stale and duplicate approval-event protection,
- local in-memory gateway rate limiting,
- Dapr-backed durable state for workflow records.

These controls are sufficient for the current local scope.

### A1. Transactional outbox

#### Problem

A service may successfully persist workflow state but fail before publishing the corresponding event.

The reverse ordering can also cause an event to be published before the related state update is durable.

#### Proposed direction

- store workflow changes and outbox records in one transaction,
- publish pending outbox records from a dedicated worker,
- use stable event ids,
- retry failed publishing with backoff,
- record attempt count and last error,
- mark successfully published records,
- retain or archive published records according to policy.

A complete implementation may require moving event-producing state transitions to PostgreSQL or another store with suitable transactional guarantees.

#### Acceptance criteria

- state and outbox records are committed atomically,
- temporary pub/sub failure does not lose an event,
- retry does not create duplicate business effects,
- worker restart resumes unpublished records,
- failure-injection tests demonstrate recovery.

### A2. Durable inbox

#### Problem

Individual consumers currently apply domain-specific idempotency checks, but there is no shared durable inbox abstraction for all event handlers.

#### Proposed direction

- persist consumed event ids,
- reject already-completed deliveries,
- distinguish processing, completed, and failed states,
- use a lease or concurrency token for simultaneous delivery,
- retain metadata for troubleshooting and replay,
- define cleanup and retention rules.

#### Acceptance criteria

- duplicate delivery is ignored consistently across consumers,
- simultaneous delivery cannot execute the same handler twice,
- failed processing can be retried,
- processed-event state survives service restart.

### A3. Dead-letter and replay tooling

#### Proposed direction

- configure retry policies,
- route exhausted events to dead-letter topics,
- retain original payload and error metadata,
- expose an inspection command or administrative endpoint,
- support controlled replay,
- audit replay actions.

#### Acceptance criteria

- poison events do not block healthy traffic,
- operators can inspect the failure reason,
- replay is explicit, authorized, and auditable,
- replay preserves the original event identity or records a clear lineage.

### A4. Distributed rate limiting

#### Current limitation

The current gateway limiter is in-memory and applies only within one gateway process.

#### Proposed direction

- store counters in Redis or an API-gateway rate-limit service,
- use atomic increment and expiry,
- support limits by tenant, actor, route, and source,
- produce consistent rate-limit headers across replicas,
- add multi-instance concurrency tests.

#### Acceptance criteria

- multiple gateway replicas enforce one shared quota,
- counters expire correctly,
- concurrent traffic does not exceed the configured tolerance,
- failure behavior is explicitly fail-open or fail-closed.

## Track B — Production observability

### Current state

ApprovalFlow currently includes:

- structured JSON logs,
- correlation-id propagation,
- health endpoints,
- durable audit records,
- controller analytics,
- operational documentation,
- an end-to-end verification harness.

This provides useful local observability but is not a complete production telemetry platform.

### B1. OpenTelemetry tracing

#### Proposed direction

- add OpenTelemetry SDK configuration to Go and Python services,
- create server spans for inbound requests,
- trace Dapr service invocation,
- propagate trace context through events,
- add workflow spans around decision, approval, payment, and audit operations,
- attach service name, version, environment, and correlation id attributes.

#### Acceptance criteria

- one submission can be followed across all participating services,
- trace context survives asynchronous event boundaries,
- errors are attached to the relevant span,
- tracing can be disabled without changing business behavior.

### B2. Collector and trace backend

#### Proposed direction

- add an OpenTelemetry Collector,
- configure batching and resource processing,
- export to Jaeger, Tempo, or another trace backend,
- provide local Compose configuration,
- document production exporter alternatives.

### B3. Metrics

Planned counters include:

- submissions accepted,
- duplicate submissions,
- decision routes,
- approvals completed,
- request-info actions,
- payments requested,
- payments succeeded,
- payments failed,
- concurrency conflicts,
- stale events ignored,
- HTTP validation rejections,
- rate-limit rejections.

Planned histograms include:

- decision latency,
- approval handling latency,
- payment latency,
- end-to-end workflow duration,
- external-provider latency.

### B4. Dashboards and alerts

#### Proposed direction

- Prometheus-compatible metrics collection,
- Grafana dashboards,
- service health and workflow panels,
- failure-rate alerts,
- latency SLOs,
- backlog and dead-letter alerts.

#### Acceptance criteria

- operators can identify a failing service,
- workflow failures and latency regressions are visible,
- alerts link to a relevant runbook,
- dashboards avoid exposing sensitive submission data.

## Track C — Container delivery and external deployment

### Current state

ApprovalFlow currently includes:

- Docker images for all services,
- Docker Compose orchestration,
- CI validation,
- Docker image build validation,
- persistent local volumes,
- Kubernetes reference manifests,
- configuration and secret placeholders.

The current repository does not deploy automatically to an external environment.

### C1. Registry publishing

#### Proposed direction

- publish immutable images to GitHub Container Registry or another registry,
- tag images by commit SHA and release version,
- avoid relying on mutable `latest` tags for releases,
- generate an SBOM,
- attach provenance or signatures where practical.

#### Acceptance criteria

- each release maps to immutable image digests,
- images can be reproduced from a repository commit,
- registry credentials are stored only in protected CI secrets.

### C2. Deployment environments

Potential targets include:

- a managed Kubernetes cluster,
- a development namespace,
- a staging environment,
- a small VM-based Compose deployment.

Environment-specific configuration should be separated from repository defaults.

### C3. Deployment workflow

#### Proposed direction

- deploy from protected branches or signed tags,
- validate manifests before deployment,
- run migrations before application rollout where required,
- wait for health and readiness checks,
- run a post-deployment smoke test,
- support rollback to the prior image digest.

#### Acceptance criteria

- failed health checks prevent successful promotion,
- rollback is documented and tested,
- production secrets are never stored in repository files,
- deployment activity is auditable.

### C4. Production infrastructure

Future deployment work should also address:

- managed PostgreSQL,
- managed Redis,
- backup and restore,
- external secret management,
- ingress and TLS,
- network policies,
- resource requests and limits,
- horizontal scaling,
- Dapr production configuration,
- disaster-recovery objectives.

## Track D — MCP integration

### Goal

Provide a controlled Model Context Protocol interface for agents or developer tools without bypassing the existing ApprovalFlow gateway and policy boundaries.

### D1. Initial read-only MCP server

The first version should expose read-only tools such as:

- `get_submission_status`,
- `get_audit_trail`,
- `get_notification`,
- `get_analytics_summary`,
- `list_pending_approvals`.

### Architecture principles

- the MCP server calls the gateway,
- it does not read directly from Redis or PostgreSQL,
- it does not bypass gateway authorization,
- tool responses use structured schemas,
- timeouts and upstream errors are mapped consistently,
- correlation ids are propagated,
- sensitive fields are minimized.

### D2. Write tools

Possible later tools include:

- `submit_invoice`,
- `approve_submission`,
- `reject_submission`,
- `request_additional_info`.

Write tools should require stronger safeguards:

- explicit user confirmation,
- role and identity validation,
- clear display of the target submission,
- idempotency keys,
- audit events,
- restricted tool exposure.

### Testing direction

- unit tests with a mocked gateway,
- schema validation,
- authorization failure tests,
- timeout and malformed-response tests,
- read-only safety tests,
- confirmation tests before adding write operations.

### Acceptance criteria for the first MCP release

- read-only tools return the same state exposed by the gateway,
- the MCP service cannot bypass authorization,
- gateway failures return structured tool errors,
- tests cover all exposed tools,
- architecture and security limitations are documented.

## Suggested post-release sequence

| Milestone | Scope |
|---|---|
| `v1.1` | Read-only MCP service and tests |
| `v1.2` | OpenTelemetry tracing, collector, and basic metrics |
| `v1.3` | Transactional outbox, durable inbox, dead-letter and replay tooling |
| `v1.4` | Registry publishing and external deployment pipeline |

The version numbers are planning markers and not release commitments.

## Work intentionally excluded from the initial release

The following items are intentionally deferred from the current implementation:

- transactional outbox,
- general-purpose durable inbox,
- dead-letter and replay administration,
- distributed rate limiting,
- full OpenTelemetry stack,
- Prometheus and Grafana deployment,
- cloud or external CD,
- live production Kubernetes deployment,
- MCP server.

These items are intentionally left for later versions and are not part of the current release.

## Definition of done for roadmap items

Each roadmap item should include implementation tests, failure-path coverage,
documentation, and an update to the relevant architecture or operations notes.
