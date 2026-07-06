SHELL := /bin/bash

.PHONY: help setup compose-up compose-up-detached compose-down compose-logs test verify lint fmt clean

help:
	@echo "ApprovalFlow commands:"
	@echo "  make setup              - prepare local repo files"
	@echo "  make compose-up         - start the full local system in the foreground"
	@echo "  make compose-up-detached - start the full local system in the background"
	@echo "  make compose-down       - stop the full local system"
	@echo "  make compose-logs       - follow docker compose logs"
	@echo "  make test               - run Go tests"
	@echo "  make verify             - run verification scenarios and safety checks"
	@echo "  make fmt                - format Go code"
	@echo "  make clean              - remove local generated files"

setup:
	@cp -n .env.example .env || true
	@echo "Local .env ready"

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

lint:
	@echo "No linter configured yet. Run 'make test' for current checks."

fmt:
	gofmt -w internal services

clean:
	rm -rf tmp
	find . -name ".DS_Store" -delete
