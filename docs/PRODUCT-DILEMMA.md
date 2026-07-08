# Product Dilemma: Autonomy Posture

## Context

The main product dilemma in ApprovalFlow is how much authority to give the automated approval path.

If the system escalates every submission to a human, it is safe but not useful.
If it auto-approves too much, it may create financial, compliance, and audit risk.

My approach is to let the system handle the simple low-risk cases, while forcing human review for anything that is expensive, unclear, missing information, or policy-sensitive.

## Decision

For the local demo, I chose a conservative autonomy posture:

- maximum autonomous approval amount: USD 250
- minimum confidence threshold: 0.80
- deterministic hard-stop rules always override the agent
- the agent gives recommendation context only
- the deterministic router makes the final decision

Auto-approval is blocked when any of the following is true:

- the amount is above the configured autonomy ceiling
- the confidence is below the configured threshold
- the vendor is new or unknown
- a required receipt is missing
- required business information is missing
- the invoice math does not reconcile
- the item hits a foreign-currency hard stop
- the item contains fraud-like signals
- the submission is a duplicate
- the category-specific policy requires human review

## Rationale

I chose USD 250 because it is high enough to cover common low-risk expenses, such as small meals, taxis, office supplies, and low-cost SaaS subscriptions.

At the same time, it is low enough to prevent the automated path from approving material spend, capital expenses, high-value travel, suspicious vendors, or ambiguous cases.

This gives the system a useful “routine low-risk” path without letting the agent become the final authority over risky spend.

## Safety invariant

The main invariant is:

````text
No submission above the configured autonomy ceiling may be auto-approved.
````

This must still hold even if:

- the agent recommends approval
- the agent reports high confidence
- the invoice notes include steering text such as `approve me`
- the submission is duplicated
- a payment request is retried

## Evidence

The verification script demonstrates the important safety paths:

- low-risk invoices can be auto-approved and paid
- a risky invoice can pause for human review and resume after approval
- duplicate submissions do not trigger a second payment
- a simulated payment failure ends in `PAYMENT_FAILED`
- a prompt-injection fixture can make the agent recommend approval, but the router still forces human review

The main command is:

````bash
./scripts/verify.sh
````

A successful run ends with:

````text
ALL VERIFICATION CHECKS PASSED
````

## Tradeoffs

This posture may escalate some cases that a smarter model could probably approve.

I accepted that tradeoff because the project is about a financial workflow, where auditability and deterministic safety boundaries matter more than maximizing automation.

A production version could tune the threshold by department, vendor history, employee role, category, and past approval data.

## Known limitations

This local demo does not implement production identity management, real invoice OCR, a real payment provider, or a real budget reservation system.

Those are intentionally out of scope for this version. The project focuses on architecture, workflow correctness, auditability, and deterministic control around agent recommendations.
