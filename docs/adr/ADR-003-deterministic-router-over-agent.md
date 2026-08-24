# ADR-003: Deterministic Router over Agent Recommendation

## Context

ApprovalFlow uses an agent to help evaluate submissions against policy.

An agent recommendation can be wrong, overconfident, unavailable, or influenced by user-provided invoice text.

For a financial workflow, the system must be able to enforce and demonstrate a hard autonomy ceiling independently of model output.

## Decision

The agent never makes the final approval decision.

The agent returns structured context:

- recommended route
- confidence
- cited rules
- missing information
- policy-related reasoning

The `decision-service` applies a deterministic router that enforces:

- autonomy ceiling
- confidence threshold
- category rules
- hard-stop rules
- duplicate checks
- math reconciliation
- vendor checks
- receipt requirements
- foreign-currency rules
- fraud-like signals

## Consequences

This keeps the safety boundary testable and auditable.

It prevents prompt-injection text or forced agent approval from bypassing policy.

The tradeoff is that some edge cases may be escalated even when the agent appears confident. This is intentional: financial safety and auditability take priority over maximizing automation.
