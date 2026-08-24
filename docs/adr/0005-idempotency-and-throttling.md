# ADR 0005: Idempotency and throttling model

## Status

Accepted

## Context

ApprovalFlow is an asynchronous, event-driven approval system. Events can be retried or redelivered, users can resubmit the same invoice, and a human approver can accidentally repeat an action.

Without idempotency controls, these cases could create duplicate workflow execution, duplicate payments, or inconsistent approval state.

Because the gateway accepts external submission traffic, the design also needs basic abuse protection and clear operational guardrails.

## Decision

ApprovalFlow uses explicit idempotency keys and durable state records at the workflow boundaries.

### Submission idempotency

`submission-service` computes a duplicate fingerprint from normalized vendor, invoice number, and total.

The fingerprint is stored as:

````text
duplicate:{hash}
````

If the same invoice is submitted again, the service returns the original tracking id and does not publish a second `submission.received` event.

### Approval idempotency

`approval-service` stores human-review items by tracking id:

````text
approval:{tracking_id}
````

An approval item can only be acted on while it is pending. Once it is approved, rejected, or moved to info-requested, repeated actions are rejected and cannot publish duplicate payment requests.

Duplicate `approval.required` events are ignored when the approval item already exists.

### Payment idempotency

`payment-service` stores payment records by tracking id:

````text
payment:{tracking_id}
````

If a duplicate `payment.requested` event is delivered, the existing payment result is reused and no second payment outcome is created.

The duplicate delivery is recorded as an audit event with action:

````text
duplicate_payment_ignored
````

### Gateway throttling

`gateway-service` applies a local in-memory rate limiter. The default limit is configured by:

````text
RATE_LIMIT_REQUESTS_PER_MINUTE=120
````

This protects the local runtime from accidental request floods and establishes an explicit abuse-protection boundary.

## Consequences

This model provides several important safety properties for the event-driven financial workflow:

- Duplicate invoice submissions do not start duplicate workflows.
- Replayed payment events do not create duplicate payments.
- Repeated human approval actions cannot resume the workflow more than once.
- Gateway traffic is rate-limited before reaching internal services.

The local implementation is intentionally simple. In production, these patterns would usually be backed by database transactions, unique constraints, optimistic concurrency, a transactional outbox, and distributed rate limiting.
