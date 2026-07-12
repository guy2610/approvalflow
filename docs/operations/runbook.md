# ApprovalFlow Operations Runbook

## Purpose

This runbook explains how to inspect and operate the local ApprovalFlow demo when a submission is stuck, duplicated, escalated, rejected, or payment-failed.

ApprovalFlow is a local demo, not a production deployment. The goal of this runbook is to make the workflow inspectable through the gateway API, correlation ids, audit events, and service logs.

## Core operational model

The main debugging key is:

````text
correlation_id
````

A correlation id connects the original request, service logs, workflow events, decision trail, approval action, payment outcome, and audit records.

The most useful operational endpoint is:

````http
GET /audit/{correlation_id}
````

Use it together with:

````http
GET /submissions/{tracking_id}
GET /approvals
````

## Start, stop, and reset

Start the complete local environment:

```bash
docker compose up --build -d
```

Because each application container shares its network namespace with a Dapr sidecar, avoid restarting only the application containers with `docker compose restart <service>`. A partial restart can leave the associated sidecar attached to the old network namespace. Use a full `docker compose down` followed by `docker compose up -d` for a clean local restart.

A normal shutdown preserves PostgreSQL and Redis state in named Docker volumes:

```bash
docker compose down
```

Start the services again to continue using the stored local workflow state:

```bash
docker compose up --build -d
```

To perform a destructive local reset, including submission, approval, payment, audit, notification, and analytics state:

```bash
docker compose down -v
```

The `-v` command removes the named volumes. Use it only when a fully clean local environment is intended.

Redis append-only persistence is enabled for the local demo. This improves restart recovery but does not replace production backup, replication, or disaster-recovery controls.

## Health checks

Check minimal UI:

````bash
curl -i http://localhost:8080/
````

Protected demo routes require role headers:

````bash
curl -i http://localhost:8080/approvals \
  -H "X-Demo-Role: approver"

curl -i http://localhost:8080/audit/<correlation_id> \
  -H "X-Demo-Role: auditor"
````

Check gateway health:

````bash
curl -i http://localhost:8080/healthz
````

Check all containers:

````bash
docker compose ps
````

Check service logs:

````bash
docker compose logs gateway-service
docker compose logs submission-service
docker compose logs decision-service
docker compose logs approval-service
docker compose logs payment-service
docker compose logs audit-service
docker compose logs agent-service
````

For a specific correlation id:

````bash
docker compose logs | grep '<correlation-id>'
````

## Inspect a submission

Check current submission status:

````bash
curl -s http://localhost:8080/submissions/<tracking_id> | jq
````

Important statuses:

| Status | Meaning |
|---|---|
| `ACCEPTED` | Submission was accepted but async processing has not finished yet. |
| `AUTO_APPROVED_PENDING_PAYMENT` | Router approved automatically and payment should follow. |
| `HUMAN_REVIEW_REQUIRED` | Workflow is durably paused for human approval. |
| `APPROVED_BY_HUMAN` | Human approved and payment should follow. |
| `INFO_REQUESTED` | Human requested more information. |
| `REJECTED` | Router rejected the submission. |
| `REJECTED_BY_HUMAN` | Human rejected the submission. |
| `PAID` | Payment flow succeeded. |
| `PAYMENT_FAILED` | Payment failed and compensation state was recorded. |
| `DUPLICATE` | Duplicate submission was short-circuited. |

## Inspect audit trail

Fetch audit events by correlation id:

````bash
curl -s http://localhost:8080/audit/<correlation_id> \
  -H "X-Demo-Role: auditor" | jq
````

Important audit actions:

| Action | Meaning |
|---|---|
| `submission_accepted` | Submission was accepted and persisted. |
| `duplicate_submission_detected` | Duplicate was detected and no second workflow was started. |
| `decision_produced` | Decision service produced the deterministic route. |
| `approval_required_published` | Human review was requested. |
| `approval_item_queued` | Approval service stored a durable approval item. |
| `human_approved` | Human approved the item. |
| `human_rejected` | Human rejected the item. |
| `info_requested` | Human requested more information. |
| `payment_requested_published` | Payment request event was published. |
| `payment_succeeded` | Simulated payment succeeded. |
| `payment_failed_compensated` | Simulated payment failed and terminal compensation state was recorded. |
| `duplicate_payment_ignored` | Duplicate payment event was ignored by idempotency. |
| `payment_request_rejected_invalid_state` | Payment event was rejected because submission state was not payment-eligible. |
| `approval_action_rejected_invalid_state` | Human action was rejected because submission was no longer in human review. |

## Problem: submission is stuck in HUMAN_REVIEW_REQUIRED

Meaning:

The deterministic router escalated the item to human review.

Check approval queue:

````bash
curl -s http://localhost:8080/approvals \
  -H "X-Demo-Role: approver" | jq
````

Approve:

````bash
curl -s -X POST http://localhost:8080/approvals/<tracking_id>/approve \
  -H "Content-Type: application/json" \
  -H "X-Demo-Role: approver" \
  -d '{"actor":"demo.approver@northwind.example","reason":"Approved after review."}' | jq
````

Reject:

````bash
curl -s -X POST http://localhost:8080/approvals/<tracking_id>/reject \
  -H "Content-Type: application/json" \
  -H "X-Demo-Role: approver" \
  -d '{"actor":"demo.approver@northwind.example","reason":"Rejected after review."}' | jq
````

Request more information:

````bash
curl -s -X POST http://localhost:8080/approvals/<tracking_id>/request-info \
  -H "Content-Type: application/json" \
  -H "X-Demo-Role: approver" \
  -d '{"actor":"demo.approver@northwind.example","reason":"Missing business context."}' | jq
````

## Problem: submission is stuck in AUTO_APPROVED_PENDING_PAYMENT

Expected behavior:

Payment service should consume `payment.requested` and move the submission to `PAID` or `PAYMENT_FAILED`.

Check payment logs:

````bash
docker compose logs payment-service | grep '<tracking_id>'
````

Check audit:

````bash
curl -s http://localhost:8080/audit/<correlation_id> \
  -H "X-Demo-Role: auditor" | jq
````

Possible causes:

| Cause | What to check |
|---|---|
| Payment service did not receive event | Check Dapr sidecar and Redis logs. |
| Payment request was rejected | Look for `payment_request_rejected_invalid_state`. |
| Payment service failed loading submission | Check payment-service logs. |
| Event ordering bug | Submission status should be updated before publishing payment event. |

## Problem: PAYMENT_FAILED

Meaning:

The simulated payment flow failed and the workflow reached a terminal failure state.

Check status:

````bash
curl -s http://localhost:8080/submissions/<tracking_id> | jq
````

Check audit:

````bash
curl -s http://localhost:8080/audit/<correlation_id> \
  -H "X-Demo-Role: auditor" | jq
````

Expected audit action:

````text
payment_failed_compensated
````

Local demo limitation:

Compensation means the workflow reaches `PAYMENT_FAILED` and audit evidence is written. There is no real payment provider or external reservation to release.

## Problem: duplicate submission

A duplicate submission should return the original tracking id and should not start a second workflow.

Check the response:

````json
{
  "duplicate": true,
  "duplicate_of": "sub_..."
}
````

Check audit:

````text
duplicate_submission_detected
````

Duplicate detection is based on normalized:

````text
vendor + invoiceNumber + total
````

Local demo limitation:

A production system would use stronger duplicate detection, normalization, vendor identity, invoice date, currency, and possibly fuzzy matching.

## Problem: approval action returns 409 Conflict

Meaning:

The approval item is no longer pending, or the submission is no longer in `HUMAN_REVIEW_REQUIRED`.

Check status:

````bash
curl -s http://localhost:8080/submissions/<tracking_id> | jq
````

Check approval queue:

````bash
curl -s http://localhost:8080/approvals \
  -H "X-Demo-Role: approver" | jq
````

Check audit for:

````text
approval_action_rejected_invalid_state
````

This is expected defense-in-depth. A stale human action should not mutate a workflow that already moved forward.

## Problem: payment event was rejected

Check audit for:

````text
payment_request_rejected_invalid_state
````

Meaning:

The payment service received a payment request, but the current submission status was not payment-eligible.

Payment-eligible states:

````text
AUTO_APPROVED_PENDING_PAYMENT
APPROVED_BY_HUMAN
PAYMENT_PENDING
````

This prevents a premature, malformed, replayed, or forged payment event from creating a payment effect.

## Problem: policy config fails startup

The decision service loads:

````text
POLICY_CONFIG_PATH=data/policy-config.json
````

Check logs:

````bash
docker compose logs decision-service
````

Common config issues:

| Issue | Expected behavior |
|---|---|
| Missing config file | Decision service fails cleanly on startup. |
| Invalid JSON | Decision service fails cleanly on startup. |
| Invalid confidence value | Config validation fails. |
| Invalid FX rate | Config validation fails. |

This is intentional. A financial workflow should fail closed when policy config is invalid.

For local runtime policy changes, edit the mounted policy config file under `data/`. `decision-service` reloads the policy config for each new decision, so the next submitted invoice uses the updated thresholds without rebuilding the image. The verification harness demonstrates this behavior with `INV-1022`.

## Problem: many small invoices are routed to human review

Check decision reason and violations.

Expected violation:

````text
AUTONOMY-BUDGET
````

Meaning:

The invoice may be individually below the autonomy ceiling, but approving it would exceed the daily cumulative auto-approval exposure limit for the submitter or vendor.

This prevents split-invoice bypass.

## Verification command

Run the full local verification harness:

The verification harness starts the stack with `POLICY_CONFIG_PATH=data/policy-config-verify.json` by default. This verify-specific config keeps the cumulative budget scenario short and deterministic while leaving the default demo policy in `data/policy-config.json`.

````bash
./scripts/verify.sh
````

Expected final output:

````text
ALL VERIFICATION CHECKS PASSED
````

The verification harness checks the main verification scenarios:

| Fixture | Scenario |
|---|---|
| `INV-1001` | Auto-approve and pay. |
| `INV-1002` | Second auto-approved item. |
| `INV-1003` | Human review, approval, resume, payment. |
| `INV-1007` | Duplicate submission, no second payment. |
| `INV-1012` | Human approval followed by simulated payment failure. |
| `INV-1013` | Prompt-injection text cannot bypass router. |
| `INV-1020` / `INV-1021` | Cumulative exposure guardrail blocks split-invoice bypass. |

## Production gaps

This local demo does not include:

- real authentication,
- real payment provider,
- real budget reservation,
- distributed rate limiting,
- transactional outbox,
- production audit database,
- OpenTelemetry tracing,
- dashboards and alerting,
- high-availability deployment.

A production version should add those before real enterprise use.
