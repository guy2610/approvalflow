# ApprovalFlow

ApprovalFlow is an event-driven invoice and expense approval platform built around AI-assisted policy evaluation, deterministic safety controls, and human-in-the-loop review.

## Browser demo

**Live demo:** https://guy2610.github.io/approvalflow/demo/

A browser-only simulation is included under `docs/`. It demonstrates
automatic approval, human review, request-info, revision-aware resubmission,
payment, analytics, and audit behavior without running backend services.

Run it locally from the repository root:

````bash
python3 -m http.server 4173 --directory docs
````

Then open:

````text
http://localhost:4173/
````


The project explores how an organization can automate routine low-risk approvals without giving an LLM direct authority over financial decisions. AI-generated recommendations are treated as advisory input, while deterministic policy code controls routing, autonomy limits, and workflow side effects.

The complete implementation runs locally through Docker Compose and includes the
Go and Python services, Dapr sidecars, Redis, PostgreSQL, durable workflow state,
idempotency, concurrency protection, and event-driven processing.

The system accepts an invoice or expense submission, returns a tracking id immediately, evaluates the submission against company policy, and then either:

- auto-approves a low-risk item,
- escalates the item to a human approver,
- rejects a clear policy violation,
- or continues an approved item into a simulated payment flow.

The main design rule is:

````text
The agent recommends. The deterministic router decides.
````

The agent is useful for recommendation context, confidence, and cited policy rules, but it is not allowed to make the final approval decision. The final route is enforced by deterministic code in the decision service.

## Goals

The project focuses on the core architecture and safety properties of an AI-assisted approval workflow:

- asynchronous submission with an immediate tracking id
- deterministic policy guardrails around agent recommendations
- human-in-the-loop approval with durable pause and resume
- duplicate submission protection
- idempotent payment processing
- simulated payment failure and compensation
- audit trail lookup by correlation id
- local Docker Compose deployment with Dapr sidecars
- one verification command for the main workflow and safety scenarios

## Tech stack

- Go for the core backend services
- Python FastAPI for the agent service
- Dapr for service invocation, pub/sub, state, and local secrets
- Postgres through Dapr state management
- Redis through Dapr pub/sub
- Docker Compose for local deployment
- Browser workflow console served by the gateway
- Static browser-only workflow simulation under `docs/`
- GitHub Actions for CI, Docker build validation, and static-site checks

## Services

- `gateway-service`: the only external entry point; handles routing, rate limiting, local role checks, and the browser UI
- `submission-service`: accepts submissions, creates tracking ids, stores submission state, detects duplicate submissions, and publishes `submission.received`
- `decision-service`: loads the submission, calls the agent service, applies the deterministic policy router, and publishes the next workflow event
- `agent-service`: returns a structured advisory recommendation with confidence and cited policy rules
- `approval-service`: stores the human approval queue and handles approve, reject, and request-info actions
- `payment-service`: processes `payment.requested` events idempotently and simulates success or failure
- `audit-service`: stores audit events and exposes audit trails by correlation id

## Local run

Prepare the local environment file:

````bash
make setup
````

This creates `.env` from `.env.example` and initializes the ignored local Dapr secret file from `infra/dapr/secrets.example.json`. Existing local files are preserved.

Start the full system:

````bash
make compose-up
````

Then open the local UI:

````text
http://localhost:8080/
````

The local UI can submit invoices, follow workflow status automatically, inspect
human-review items independently, approve, reject, request additional
information, resume a revised submission, inspect friendly response summaries,
view raw API responses, review controller analytics, and fetch audit events by
correlation id.

The Compose stack uses named volumes for PostgreSQL and Redis, with Redis append-only persistence enabled. A normal stop preserves local workflow state:

````bash
docker compose down
````

To remove all local workflow state and start from a clean environment, remove the named volumes explicitly:

````bash
docker compose down -v
````

## Local authorization model

Sensitive local endpoints use a lightweight role header:

````text
X-Demo-Role: approver
X-Demo-Role: auditor
X-Demo-Role: admin
````

Approval endpoints require:

````text
approver or admin
````

Audit endpoints require:

````text
auditor or admin
````

This authorization mechanism is intentionally local-only. It is not a production authentication system.

## Configuration

The deterministic policy router is configured from:

````text
POLICY_CONFIG_PATH=data/policy-config.json
````

The policy config contains the autonomy ceiling, minimum confidence, FX rates, receipt threshold, category-specific limits, review trigger flags, and cumulative daily auto-approval exposure limits.

The gateway rate limit is still configured separately:

````text
RATE_LIMIT_REQUESTS_PER_MINUTE=120
````

The router remains deterministic. The config changes the policy posture, but the agent still cannot approve, reject, pay, or bypass guardrails.

The cumulative exposure guardrail prevents many small invoices from bypassing the per-invoice ceiling. If the daily auto-approved total for the same submitter or vendor would exceed the configured limit, the decision service converts the route from auto-approve to human review with `AUTONOMY-BUDGET`.

## Verification

Run the main verification command:

````bash
make verify
````

or directly:

````bash
./scripts/verify.sh
````

The verification script starts a clean Docker Compose environment and checks the main verification scenarios:

- `INV-1001`: low-risk invoice is auto-approved and paid
- `INV-1002`: second low-risk invoice is auto-approved and paid
- `INV-1003`: risky invoice is escalated, approved by a human actor, and then paid
- `INV-1007`: duplicate invoice returns the original tracking id and does not trigger a second payment
- `INV-1012`: human-approved invoice reaches a simulated payment failure and ends as `PAYMENT_FAILED`
- `INV-1013`: prompt-injection text causes the agent to recommend approval, but the router still forces `HUMAN_REVIEW_REQUIRED`
- `INV-1020` / `INV-1021`: split-invoice style cumulative exposure check; the first low-risk item is paid, while the second is forced to human review with `AUTONOMY-BUDGET`

The verification harness uses `data/policy-config-verify.json` by default. That file intentionally sets a lower cumulative daily exposure limit so the split-invoice guardrail can be demonstrated quickly. The normal runtime policy remains in `data/policy-config.json`.

For local no-rebuild policy changes, `decision-service` reloads the policy config for every decision. In Docker Compose, `./data` is mounted into the service container as read-only, so editing the policy JSON on the host affects the next decision without rebuilding the image. `scripts/verify.sh` proves this with `INV-1022` by lowering the runtime autonomy ceiling and verifying that the next otherwise-low-risk invoice is routed to human review.

The local runtime also uses Dapr secrets. `payment-service` loads a dev-only `payment-provider-token` from the `localsecrets` Dapr secret store at startup. The token value is never printed; logs only confirm that the secret was loaded.

The agent supports two recommendation providers. By default it uses the deterministic `local` policy-retrieval provider so CI and verification do not require external network access. To run with a real LLM provider, set `AGENT_PROVIDER=gemini` and provide `GEMINI_API_KEY`; the service uses Gemini and falls back to the local provider if the model is unavailable, returns invalid JSON, or is not configured.

The gateway enforces local role-based authorization for protected routes. `/approvals` requires `X-Demo-Role: approver` or `admin`, and `/audit/{correlation_id}` requires `X-Demo-Role: auditor` or `admin`. The verification harness includes smoke tests for the UI route and these protected routes.

The script also checks audit events for the main flows.

A successful run ends with:

````text
ALL VERIFICATION CHECKS PASSED
````

## CI workflows

The repository includes three GitHub Actions workflows:

````text
.github/workflows/ci.yml
.github/workflows/docker-build.yml
.github/workflows/pages.yml
````

The CI workflow runs Go tests, verification-script syntax validation, OpenAPI linting, and Docker Compose configuration validation.

The Docker build workflow checks that all Docker Compose images can be built.

The static-site workflow validates the JavaScript and required files under `docs/`. The browser simulation is validated in CI and can be published through GitHub Pages.

## Safety and idempotency model

ApprovalFlow uses state-backed idempotency keys:

````text
duplicate:{hash}
approval:{tracking_id}
payment:{tracking_id}
````

These keys are used to prevent:

- starting a second workflow for the same invoice
- repeating a finalized human approval action
- creating a second payment result for a retried payment event

The payment service also validates the current submission state before processing a payment request. A `payment.requested` event is not enough by itself. The submission must already be in a payment-eligible state such as `AUTO_APPROVED_PENDING_PAYMENT`, `APPROVED_BY_HUMAN`, or `PAYMENT_PENDING`.

The approval service also validates workflow state before accepting a human action. `approve`, `reject`, and `request-info` are allowed only while the submission is still in `HUMAN_REVIEW_REQUIRED`. This prevents stale or repeated human actions from changing a workflow that has already moved to another state.

Gateway throttling is configured with:

````text
RATE_LIMIT_REQUESTS_PER_MINUTE=120
````

More details are documented in:

````text
docs/adr/0005-idempotency-and-throttling.md
````

## Documentation

- [Architecture](./ARCHITECTURE.md)
- [Autonomy Design](./docs/AUTONOMY-DESIGN.md)
- [Autonomy Safety Report](./reports/autonomy-safety-report.md)
- [Evaluation Report](./reports/evaluation-report.md)
- [Observability Notes](./docs/operations/observability.md)
- [Operations Runbook](./docs/operations/runbook.md)
- [Project Capabilities](./docs/PROJECT-CAPABILITIES.md)
- [Post-Release Engineering Roadmap](./docs/POST-RELEASE-ROADMAP.md)
- [Architecture Decision Records](./docs/adr)

### API documentation

The public gateway API is documented using OpenAPI 3.1:

- Specification: [`docs/openapi.yaml`](docs/openapi.yaml)
- Local validation: `make openapi-lint`
- CI validation: `.github/workflows/ci.yml`

The specification covers asynchronous submissions, status polling,
request-info resumption, human approval actions, audit trails, controller
analytics, and durable terminal notifications.

Protected local routes use the `X-Demo-Role` header:

| Capability | Allowed roles |
|---|---|
| Approval queue and actions | `approver`, `admin` |
| Audit trail | `auditor`, `admin` |
| Controller analytics | `controller`, `admin` |
| Workflow notifications | `submitter`, `controller`, `admin` |

The role header is intentionally a local authorization mechanism. A production
deployment should replace it with JWT/OIDC authentication and server-enforced
RBAC claims.

Validate the specification without installing a local package:

````bash
make openapi-lint
````

The YAML can also be opened in any OpenAPI-compatible editor or documentation
viewer. The runtime API remains available through the gateway at
`http://localhost:8080`.

## Current status

Implemented:

- asynchronous invoice submission through the gateway
- Dapr service invocation, pub/sub, state, and local secrets
- duplicate detection based on vendor, invoice number, and total
- deterministic policy routing with agent recommendation context
- configurable per-invoice and cumulative autonomy guardrails
- human approval queue with approve, reject, and request-info actions
- durable additional-information resume and policy re-evaluation
- idempotent simulated payment processing
- forced payment failure path with compensation to `PAYMENT_FAILED`
- durable terminal notifications with acknowledgement state
- audit trail by `correlation_id`
- controller analytics read model and browser dashboard
- local role separation for approvals, audit, analytics, and notifications
- gateway rate limiting
- optimistic concurrency for conflicting human approval actions
- revision-aware rejection of duplicate and stale approval events
- strict public JSON input validation and bounded request bodies
- shared HTTP server timeouts and header limits
- local policy retrieval with cited rules
- optional Gemini recommendation provider with safe local fallback
- OpenAPI 3.1 documentation with CI validation
- end-to-end verification through `./scripts/verify.sh`
- GitHub Actions quality and Docker image build workflows
- Kubernetes reference manifests with Dapr annotations

## Current limitations

The current implementation is designed to run locally and is not presented as a production deployment.

The static experience under `docs/` is a browser-only simulation. It
does not run the Go services, Python agent, Dapr, Redis, PostgreSQL, payment
processing, authorization checks, or durable event handling. Those capabilities
belong to the Docker Compose implementation in this repository.

The static site under `docs/` is designed to be publishable through GitHub Pages independently of the local backend.

Known limitations:

- The local deterministic agent is the default; Gemini is optional and is not a production-managed model integration.
- Payment execution is simulated.
- `X-Demo-Role` headers provide local authorization boundaries but are not production authentication.
- There is no live continuous-deployment target or production high-availability deployment.
- There is no real payment provider, budget reservation service, or accounting integration.
- Audit, analytics, and notification read models are suitable for the current local architecture, not transactional production stores.
- Audit-event persistence and derived read-model updates are not performed in one atomic transaction.
- Dapr state, pub/sub, and secrets are configured for the local Docker Compose environment.

Planned post-release work includes a transactional outbox and durable inbox, dead-letter and replay tooling, distributed rate limiting, OpenTelemetry-based observability, registry-backed external deployment, and an MCP integration. These are documented in the [Post-Release Engineering Roadmap](./docs/POST-RELEASE-ROADMAP.md) and are not part of the current `v1.0` implementation.

## Kubernetes reference manifests

Reference Kubernetes manifests are provided under `k8s/` to demonstrate how the local Compose architecture maps to Kubernetes. They map the Docker Compose architecture to Kubernetes Deployments and Services, include Dapr sidecar annotations, mount policy configuration through a ConfigMap, and provide a Secret placeholder for payment and optional Gemini credentials. These manifests are intended as a reference deployment, not a production-ready cluster configuration.

## Testing strategy

The project uses layered verification:

- Go unit tests cover domain constants, platform helpers, Dapr client behavior, health responses, structured logging, policy routing, workflow transitions, gateway authorization, submission validation, request-info updates, duplicate fingerprinting, decision status mapping, payment state guards, approval helpers, audit analytics, and notification derivation.
- `scripts/verify.sh` runs the full Docker Compose + Dapr acceptance flow, including auto-approval, durable request-info resume, human approval, duplicate/idempotency behavior, payment failure compensation, cumulative autonomy limits, runtime policy reload, terminal notifications and acknowledgement, controller analytics, UI smoke, and local authorization checks.
- `make openapi-lint` validates the OpenAPI 3.1 specification locally and in CI.
- `go test -race ./...` checks the test-exercised Go code paths for data races.
- Targeted runtime tests cover concurrent approval actions, stale revision-event replay, state recovery, and HTTP `400` / `413` / `415` behavior.
- [`reports/evaluation-report.md`](reports/evaluation-report.md) consolidates the automated and manual evaluation evidence.

During final hardening, unit tests caught and fixed a fail-closed edge case in decision status mapping: unknown decision routes now map to `HUMAN_REVIEW_REQUIRED` instead of remaining in `PROCESSING`.
