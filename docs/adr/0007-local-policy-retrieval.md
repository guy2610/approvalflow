# ADR 0007: Local policy retrieval for agent recommendations

## Status

Accepted

## Context

Agent recommendations should be grounded in explicit policy text rather than relying only on model knowledge.

ApprovalFlow already has a deterministic policy router that is the final authority. The agent is advisory only and cannot approve, reject, pay, or bypass policy controls.

A full vector database and embedding pipeline would add substantial complexity without clear value at the current policy size and retrieval scope.

## Decision

The agent service implements local deterministic policy retrieval from:

````text
data/policy.md
````

At startup, the agent loads the policy file and extracts rule ids and rule text from the markdown tables.

For each recommendation request, the agent selects relevant policy rules based on submission fields such as:

- category
- amount
- currency
- vendorKnown
- receiptPresent
- notes

The selected rules are returned as `cited_rules`.

This is local policy retrieval, not production semantic RAG.

## Consequences

The agent uses policy-grounded context instead of relying only on hardcoded explanations.

The deterministic router remains the source of truth.

A production implementation could replace this with embeddings, a vector database, policy versioning, and retrieval evaluation.
