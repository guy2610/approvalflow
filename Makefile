SHELL := /bin/bash

.PHONY: help setup compose-up compose-down compose-logs test verify lint fmt clean

help:
	@echo "ApprovalFlow commands:"
	@echo "  make setup        - prepare local repo files"
	@echo "  make compose-up   - start the full local system"
	@echo "  make compose-down - stop the full local system"
	@echo "  make compose-logs - follow docker compose logs"
	@echo "  make test         - run all tests"
	@echo "  make verify       - run verification scenarios and safety checks"
	@echo "  make lint         - run linters"
	@echo "  make fmt          - format code"
	@echo "  make clean        - remove local generated files"

setup:
	@cp -n .env.example .env || true
	@echo "Local .env ready"

compose-up:
	docker compose up --build

compose-down:
	docker compose down --remove-orphans

compose-logs:
	docker compose logs -f

test:
	@echo "Tests will be wired after service skeletons are added"

verify:
	@echo "Verification harness will be wired in Stage 12"

lint:
	@echo "Linters will be wired after service skeletons are added"

fmt:
	@echo "Formatters will be wired after service skeletons are added"

clean:
	rm -rf tmp
	find . -name ".DS_Store" -delete