# ADR-004: Payment Flow and Compensation

## Context

Approved submissions need to continue into a payment flow.

The payment workflow needs a consistent terminal outcome even when downstream processing fails. The current implementation uses a simulated provider boundary rather than a real payment or accounting system.

A key invariant is that a payment request must not leave the workflow in an inconsistent state, and repeated payment requests must not create duplicate effects.

## Decision

ApprovalFlow implements a simulated payment saga inside `payment-service`.

The payment service consumes `payment.requested` events and stores a payment record using a stable idempotency key:

````text
payment:{tracking_id}
````

If the payment request is new, the service simulates either:

- successful payment, updating the submission to `PAID`
- forced payment failure for a verification fixture, updating the submission to `PAYMENT_FAILED`

If the same payment request is delivered again, the existing payment record is reused and no second payment result is created.

Before processing a new payment request, the payment service also loads the submission record and verifies that the workflow is in a payment-eligible state. This prevents a malformed, premature, or forged payment event from creating a payment effect without a prior approval decision.

For the failure path, the service publishes an audit event named:

````text
payment_failed_compensated
````

In the current implementation, compensation means that the workflow is moved into a clear failed terminal state and audit evidence is written. It does not release a real external reservation because no real payment provider or budget reservation system is connected.

## Consequences

The design captures the core saga properties used here: payment is asynchronous, failure is explicit, and duplicate payment events are handled idempotently.

The implementation is intentionally small and local. A production version would add real budget reservation, payment provider integration, transactional outbox, retry policies, and stronger compensation logic.
