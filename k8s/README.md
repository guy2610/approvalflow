# ApprovalFlow Kubernetes Manifests

These manifests are reference Kubernetes manifests for the ApprovalFlow demo.

They are intended to show how the local Docker Compose architecture maps to Kubernetes:

- one Deployment and Service per application service
- Dapr sidecar annotations per service
- ConfigMap-mounted policy configuration
- Kubernetes Secret placeholder for demo secrets
- Redis and PostgreSQL development dependencies
- gateway exposed through a `LoadBalancer` Service

## Scope

This is a reference deployment, not a production-ready platform.

Production deployment should add:

- real container registry image names and immutable tags
- managed Redis/PostgreSQL or persistent volumes
- production Dapr components for pub/sub, state, and secret store
- TLS ingress
- JWT/OIDC authentication
- network policies
- resource requests and limits
- HPA/autoscaling
- external secret manager integration
- observability stack and alerting

## Prerequisites

Install Dapr in the target cluster before applying these manifests:

````bash
dapr init -k
````

Build and push service images, or load them into a local cluster such as kind/minikube with names matching the manifests:

````text
approvalflow/gateway-service:latest
approvalflow/submission-service:latest
approvalflow/decision-service:latest
approvalflow/approval-service:latest
approvalflow/payment-service:latest
approvalflow/audit-service:latest
approvalflow/agent-service:latest
````

## Apply

````bash
kubectl apply -f k8s/
````

Check pods:

````bash
kubectl get pods -n approvalflow
kubectl get svc -n approvalflow
````

Port-forward the gateway if a LoadBalancer is not available:

````bash
kubectl -n approvalflow port-forward svc/gateway-service 8080:8080
curl -i http://localhost:8080/healthz
````

## Policy config

`decision-service` mounts `approvalflow-policy-config` at:

````text
/app/data/policy-config.json
````

This mirrors the local Docker Compose behavior where policy configuration can be changed without rebuilding the service image.

## Secrets

`secret-example.yaml` contains placeholders only. Do not commit real secrets.

For the local demo, the payment service expects:

````text
payment-provider-token
````

The optional Gemini agent provider expects:

````text
gemini-api-key
````

The default agent provider remains `local`, so Gemini is not required for verification.
