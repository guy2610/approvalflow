SHELL := /bin/bash

.PHONY: help setup compose-up compose-up-detached compose-down compose-logs test verify openapi-lint lint fmt clean

help:
	@echo "ApprovalFlow commands:"
	@echo "  make setup              - prepare local repo files"
	@echo "  make compose-up         - start the full local system in the foreground"
	@echo "  make compose-up-detached - start the full local system in the background"
	@echo "  make compose-down       - stop the full local system"
	@echo "  make compose-logs       - follow docker compose logs"
	@echo "  make test               - run Go tests"
	@echo "  make verify             - run verification scenarios and safety checks"
	@echo "  make openapi-lint       - validate docs/openapi.yaml with Redocly"
	@echo "  make fmt                - format Go code"
	@echo "  make clean              - remove local generated files"

setup:
	@cp -n .env.example .env || true
	@mkdir -p infra/dapr/secrets
	@cp -n infra/dapr/secrets.example.json infra/dapr/secrets/secrets.json || true
	@echo "Local .env and Dapr secrets ready"

compose-up:
	docker compose up --build

compose-up-detached:
	docker compose up --build -d

compose-down:
	docker compose down --remove-orphans

compose-logs:
	docker compose logs -f

test:
	go test ./...

verify:
	./scripts/verify.sh

openapi-lint:
	docker run --rm -v "$(CURDIR):/spec" redocly/cli:latest lint /spec/docs/openapi.yaml

lint:
	@echo "No linter configured yet. Run 'make test' for current checks."

fmt:
	gofmt -w internal services

clean:
	rm -rf tmp
	find . -name ".DS_Store" -delete
