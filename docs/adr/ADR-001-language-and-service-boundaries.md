# ADR-001: Language and Service Boundaries

## Context

ApprovalFlow combines asynchronous workflow processing, Dapr-based infrastructure integration, deterministic decision logic, and an agent-assisted evaluation boundary across several services.

I wanted the core services to be simple to build, test, containerize, and reason about.

## Decision

The core services are implemented in Go.

The agent service is implemented in Python.

Core Go services:

- gateway-service
- submission-service
- decision-service
- approval-service
- payment-service
- audit-service

Python service:

- agent-service

The minimal UI is served by the gateway and kept intentionally simple.

## Consequences

Go is a good fit for the core services because it gives static binaries, strong typing, simple HTTP servers, and straightforward Docker builds.

Python is a better fit for the agent boundary because it is easier to evolve later if a real model provider or retrieval library is added.

The split adds one cross-language boundary, but this boundary is intentional. The agent is treated as an advisory component behind a stable HTTP API, while the deterministic decision logic stays in Go.
