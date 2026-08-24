# ADR-002: Dapr for Service Communication

## Context

ApprovalFlow needs consistent abstractions for service invocation, asynchronous messaging, state access, and local secret management.

I also wanted the services to stay loosely coupled. The gateway should not need to know where every internal service runs, and workflow services should be able to publish events without calling each other directly.

## Decision

ApprovalFlow uses Dapr for:

- service invocation between the gateway and internal services
- service invocation from the decision service to the submission and agent services
- pub/sub for workflow events
- state management over the local Postgres-backed Dapr state store
- local development secrets

Redis is used as the local pub/sub component.

Postgres is used behind the Dapr state component for durable local workflow state.

## Consequences

The local runtime also includes a Dapr secret store component named `localsecrets`. `payment-service` uses Dapr's secrets API to load a dev-only `payment-provider-token` at startup. This exercises the secrets integration without storing any real credential in the repository.

Dapr gives the project one consistent communication model for both synchronous and asynchronous flows.

It also keeps the Go services and the Python agent behind the same service boundary style.

The tradeoff is additional local runtime complexity because every application service also has a Dapr sidecar. This is accepted in exchange for a consistent runtime abstraction across services and reduced coupling between business logic and infrastructure-specific clients.
