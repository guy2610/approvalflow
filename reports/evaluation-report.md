# ApprovalFlow Evaluation Report

## 1. Purpose

This report summarizes the functional, safety, reliability, and input-hardening evaluation performed for ApprovalFlow.

ApprovalFlow is an asynchronous invoice and expense approval workflow implemented as a local-first event-driven microservice system. The system combines deterministic policy enforcement, agent-generated advisory context, durable human review, simulated payment processing, audit records, notifications, and controller analytics.

The evaluation focuses on whether the system:

- completes the main workflow scenarios correctly,
- fails closed when policy or workflow state is unclear,
- prevents duplicate or conflicting side effects,
- preserves human-review state across revisions,
- rejects stale events and conflicting concurrent actions,
- rejects malformed or abusive HTTP inputs,
- remains reproducible through automated commands.

## 2. Evaluation environment

The evaluated local environment uses:

- Go 1.22 services,
- Python FastAPI agent service,
- Docker Compose,
- Dapr sidecars,
- Redis for local pub/sub and state integration,
- PostgreSQL for the local stack,
- deterministic local policy retrieval by default.

The main acceptance harness starts the stack with:

```text
POLICY_CONFIG_PATH=data/.policy-config-verify-runtime.json
```

The runtime file is copied from:

```text
data/policy-config-verify.json
```

This verification-specific policy keeps cumulative-autonomy scenarios short and deterministic without weakening the default runtime policy in:

```text
data/policy-config.json
```

## 3. Evaluation methods

The project uses four complementary evaluation layers.

### 3.1 Unit tests

Go unit tests cover deterministic and isolated behavior, including:

- policy routing,
- cumulative autonomy budget logic,
- workflow transitions,
- Dapr state and invocation clients,
- optimistic-concurrency conflict handling,
- gateway authorization,
- rate limiting,
- strict JSON decoding,
- HTTP server limits,
- submission validation and duplicate fingerprints,
- request-info updates,
- decision status mapping,
- payment state guards,
- approval action behavior,
- stale approval-event classification,
- audit analytics,
- terminal notification derivation.

### 3.2 Race-enabled tests

The Go suite is also executed with the race detector:

```bash
go test -race ./...
```

This checks the test-exercised code paths for data races.

### 3.3 Automated acceptance harness

The deterministic end-to-end harness is:

```bash
./scripts/verify.sh
```

It builds and starts the local Docker Compose and Dapr environment, submits fixtures through the gateway, polls asynchronous state, performs human actions, inspects audit evidence, and checks the expected terminal outcomes.

A successful run ends with:

```text
ALL VERIFICATION CHECKS PASSED
```

### 3.4 Targeted runtime fault and adversarial tests

Additional runtime tests were performed manually for cases that are awkward to express in the main deterministic fixture harness:

- simultaneous conflicting approval actions,
- simultaneous identical approval actions,
- replay of an old approval event after a newer revision,
- Docker Compose service recreation with persisted state,
- malformed HTTP bodies and unsupported media types,
- oversized HTTP request bodies.

## 4. Core business scenario matrix

| Area | Scenario | Expected result | Verification | Result |
|---|---|---|---|---:|
| Submission | Asynchronous submission | Gateway returns `202 Accepted` with a tracking id before final workflow completion | `scripts/verify.sh` | Pass |
| Auto approval | Low-risk `INV-1001` | Submission reaches `PAID` | `scripts/verify.sh` | Pass |
| Auto approval | Second low-risk `INV-1002` | Submission reaches `PAID` | `scripts/verify.sh` | Pass |
| Human review | `INV-1003` enters review | Submission and approval item reach human-review state | `scripts/verify.sh` | Pass |
| Request info | Human requests more information | Submission reaches `INFO_REQUESTED`; approval reaches `REQUEST_INFO` | `scripts/verify.sh` | Pass |
| Revision resume | Additional information is submitted | Revision advances to `2` and policy evaluation runs again | `scripts/verify.sh` | Pass |
| Approval reopen | Revision 2 still requires review | Existing approval item reopens as `PENDING` | `scripts/verify.sh` and targeted runtime test | Pass |
| Human approval | Human approves `INV-1003` | Workflow resumes and reaches `PAID` | `scripts/verify.sh` | Pass |
| Human rejection | Pending approval is rejected | Submission reaches `REJECTED_BY_HUMAN` | Unit and targeted runtime verification | Pass |
| Duplicate submission | `INV-1007` duplicates `INV-1001` | Existing tracking id is returned and no second payment occurs | `scripts/verify.sh` | Pass |
| Payment failure | Human-approved `INV-1012` | Submission reaches `PAYMENT_FAILED` with compensation audit evidence | `scripts/verify.sh` | Pass |
| Notification | Successful payment | Durable `PAID` notification exists and acknowledgement persists | `scripts/verify.sh` | Pass |
| Notification | Failed payment | Durable `PAYMENT_FAILED` notification exists | `scripts/verify.sh` | Pass |
| Analytics | Controller summary | Throughput, route, monetary, and failure metrics are returned | `scripts/verify.sh` | Pass |

## 5. Policy and autonomy safety evaluation

| Scenario | Expected safety behavior | Verification | Result |
|---|---|---|---:|
| Per-invoice autonomy ceiling | Amounts above the configured ceiling cannot be auto-approved | Policy tests and acceptance scenarios | Pass |
| Cumulative split-invoice guardrail | Multiple individually small invoices cannot bypass the daily submitter/vendor exposure limit | `INV-1020` / `INV-1021` in `scripts/verify.sh` | Pass |
| Prompt-injection text | Agent recommendation cannot override deterministic policy | `INV-1013` in `scripts/verify.sh` | Pass |
| Runtime policy reload | Updated mounted policy affects the next decision without rebuilding services | `INV-1022` in `scripts/verify.sh` | Pass |
| Unknown decision route | System fails closed to `HUMAN_REVIEW_REQUIRED` | Decision-service unit test | Pass |
| Agent provider failure | Local deterministic evaluation remains available as fallback | Provider implementation and documented manual Gemini smoke | Pass |

The deterministic router remains the final authority. Agent output is advisory and cannot directly approve, reject, pay, or bypass policy rules.

## 6. Idempotency and retry evaluation

| Boundary | Scenario | Expected result | Verification | Result |
|---|---|---|---|---:|
| Submission | Same invoice submitted again | Existing workflow is returned; no second workflow starts | `scripts/verify.sh` | Pass |
| Approval | Identical `request-info` retry | Returns success without creating a conflicting state transition | `scripts/verify.sh` | Pass |
| Approval | Identical `approve` retry after payment | Returns the existing successful result; submission remains `PAID` | `scripts/verify.sh` | Pass |
| Approval | Conflicting `reject` after approval | Rejected with `409 Conflict`; workflow remains paid | `scripts/verify.sh` | Pass |
| Payment | Duplicate payment event | Existing payment result is reused and no duplicate effect is created | Unit and acceptance evidence | Pass |
| Notification | Repeated acknowledgement | Acknowledgement remains idempotent | `scripts/verify.sh` | Pass |
| Audit | Retried decision events | Revision-aware audit ids do not collapse distinct revisions | Decision-service tests and acceptance audit assertions | Pass |

## 7. Approval concurrency evaluation

Approval mutations use Dapr state ETags with first-write optimistic concurrency.

### 7.1 Conflicting simultaneous actions

A targeted runtime test sent `approve` and `reject` concurrently for the same pending approval.

Observed behavior:

- one action succeeded,
- the competing action received `409 Conflict`,
- the approval and submission converged to the winner,
- the service logged an optimistic-concurrency conflict and reconciled the stored winner.

Result: **Pass**

### 7.2 Identical simultaneous actions

A targeted runtime test sent two identical approval actions concurrently.

Observed behavior:

- the final approval state remained consistent,
- the retry reconciled to the already-stored action,
- no contradictory workflow transition was produced.

Result: **Pass**

## 8. Revision and stale-event evaluation

Approval items store:

- `source_event_id`,
- `revision_number`.

The approval-required event handler classifies incoming events as:

- duplicate,
- stale,
- newer and eligible for reopen,
- newer but incompatible with the current approval state.

A targeted runtime sequence was executed:

1. revision 1 entered human review,
2. the approver requested additional information,
3. additional information advanced the submission to revision 2,
4. revision 2 reopened the approval item as `PENDING`,
5. the revision 1 approval event was replayed manually.

Observed final state:

```text
revision_number = 2
status = PENDING
```

The stored revision 2 event id remained unchanged, and the service logged:

```text
stale approval required event ignored
```

Result: **Pass**

## 9. HTTP input-hardening evaluation

Public JSON endpoints use:

- content-type validation,
- a 1 MiB request-body limit,
- strict schema decoding with unknown-field rejection,
- malformed-JSON rejection,
- empty-body rejection,
- rejection of multiple JSON values.

| Input case | Expected response | Verification | Result |
|---|---|---|---:|
| `Content-Type: text/plain` | `415 Unsupported Media Type` | Targeted runtime test | Pass |
| Missing `Content-Type` | `415 Unsupported Media Type` | Targeted runtime test | Pass |
| Malformed JSON | `400 Bad Request` | Unit and targeted runtime test | Pass |
| Unknown JSON field | `400 Bad Request` | Unit and targeted runtime test | Pass |
| Two JSON values in one body | `400 Bad Request` | Unit and targeted runtime test | Pass |
| Body larger than 1 MiB | `413 Request Entity Too Large` | Unit and targeted runtime test | Pass |
| Valid JSON with UTF-8 charset | Normal endpoint result | Unit and targeted runtime regression | Pass |

Error responses preserve the request correlation id.

## 10. HTTP server-hardening evaluation

All Go HTTP services use a shared hardened server configuration:

| Setting | Value |
|---|---:|
| Read header timeout | 5 seconds |
| Read timeout | 15 seconds |
| Write timeout | 30 seconds |
| Idle timeout | 60 seconds |
| Maximum header bytes | 1 MiB |

A full Compose rebuild and service recreation completed successfully after applying the server limits. Gateway health returned `200`, and no panic, fatal startup error, or service failure was observed.

Result: **Pass**

## 11. Persistence and recovery evaluation

The Docker Compose stack uses persistent local storage for Redis and PostgreSQL.

A targeted recovery test recreated the local services and verified that previously stored workflow state remained available after service restart.

The recovery behavior is appropriate for the current local runtime. It is not a substitute for production backup, replication, disaster recovery, or state-store high availability.

Result: **Pass**

## 12. Authorization evaluation

Protected gateway routes use the local `X-Demo-Role` header.

The acceptance harness verifies:

| Capability | Missing role | Allowed role |
|---|---:|---:|
| Approval queue | `403` | `200` for `approver` |
| Audit trail | `403` | `200` for `auditor` |
| Analytics | `403` | `200` for `controller` |
| Notifications | `403` | Route available for `submitter` |

This verifies route separation for the local authorization model. It is not production authentication.

Result: **Pass**

## 13. Reproduction commands

Run deterministic unit and static checks:

```bash
gofmt -w internal services
go test ./...
go test -race ./...
go vet ./...
git diff --check
```

Validate infrastructure and API documentation:

```bash
docker compose config --quiet
make openapi-lint
```

Run the full local acceptance evaluation:

```bash
./scripts/verify.sh
```

or:

```bash
make verify
```

## 14. Result summary

| Evaluation layer | Result |
|---|---:|
| Go unit tests | Pass |
| Go race-enabled tests | Pass |
| Go vet | Pass |
| Docker Compose build and startup | Pass |
| Automated end-to-end acceptance harness | Pass |
| Policy and autonomy safety scenarios | Pass |
| Human-in-the-loop pause/resume | Pass |
| Duplicate and idempotent retry behavior | Pass |
| Approval optimistic concurrency | Pass |
| Stale revision-event rejection | Pass |
| HTTP input hardening | Pass |
| HTTP server limits | Pass |
| Local state recovery | Pass |
| Local route authorization | Pass |

## 15. Known evaluation limitations

The evaluation is intentionally scoped to the current local implementation.

Not included:

- a large statistical or model-quality dataset,
- production LLM accuracy, bias, latency, and cost evaluation,
- multi-node distributed concurrency testing,
- long-duration soak tests,
- production-scale load and capacity tests,
- fault injection against Redis, PostgreSQL, or Dapr,
- real payment-provider reconciliation,
- production JWT/OIDC authentication,
- penetration testing,
- Kubernetes cluster deployment verification,
- production observability and alert validation,
- transactional outbox and general-purpose durable inbox validation,
- dead-letter and controlled replay validation,
- distributed rate-limiter validation,
- MCP tool and authorization validation.

These areas were not tested because they are outside the scope of the current local implementation. The reported results apply only to the implementation and scenarios described in this report.

Planned engineering work for these areas is documented in `docs/POST-RELEASE-ROADMAP.md` and is not part of the evaluated `v1.0` functionality.
