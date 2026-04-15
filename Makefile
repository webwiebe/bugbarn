SHELL := /bin/sh
.DEFAULT_GOAL := help

SPEC_DIR := specs/001-personal-error-tracker
LOCAL_DIRS := .cache var
FIND_PRUNE := \( -path './.git' -o -path './.cache' -o -path '*/node_modules' -o -path '*/dist' -o -path '*/build' -o -path '*/.venv' \) -prune -o
export XDG_CACHE_HOME := $(CURDIR)/.cache
export GOCACHE := $(CURDIR)/.cache/go-build
export GOMODCACHE := $(CURDIR)/.cache/go-mod

.PHONY: help setup test lint build dev docker-build spec-check

help:
	@printf '%s\n' \
		'BugBarn targets:' \
		'  setup        bootstrap local tooling when manifests exist' \
		'  test         run spec checks plus available language tests' \
		'  lint         run available linters and static checks' \
		'  build        run available build checks' \
		'  dev          start the local compose stack if available' \
		'  docker-build build placeholder images when Dockerfiles exist' \
		'  spec-check   verify the current spec-first scaffolding'

setup:
	@set -eu; \
	for dir in $(LOCAL_DIRS) $(GOCACHE) $(GOMODCACHE); do mkdir -p "$$dir"; done; \
	found=0; \
	for mod in $$(find . $(FIND_PRUNE) -name go.mod -print 2>/dev/null); do \
		found=1; \
		dir=$$(dirname "$$mod"); \
		echo "[setup] go $$dir"; \
		(cd "$$dir" && go mod download); \
	done; \
	for pkg in $$(find . $(FIND_PRUNE) -name package.json -print 2>/dev/null); do \
		found=1; \
		dir=$$(dirname "$$pkg"); \
		echo "[setup] node $$dir"; \
		if [ -f "$$dir/package-lock.json" ]; then \
			(cd "$$dir" && npm ci); \
		else \
			(cd "$$dir" && npm install); \
		fi; \
	done; \
	for py in $$(find . $(FIND_PRUNE) \( -name pyproject.toml -o -name requirements.txt \) -print 2>/dev/null); do \
		found=1; \
		dir=$$(dirname "$$py"); \
		echo "[setup] python $$dir"; \
		if [ -f "$$dir/pyproject.toml" ]; then \
			(cd "$$dir" && python3 -m pip install -e .); \
		else \
			(cd "$$dir" && python3 -m pip install -r requirements.txt); \
		fi; \
	done; \
	if [ "$$found" -eq 0 ]; then echo "[setup] no manifests found yet"; fi

spec-check:
	@set -eu; \
	for file in \
		.specify/memory/constitution.md \
		$(SPEC_DIR)/spec.md \
		$(SPEC_DIR)/plan.md \
		$(SPEC_DIR)/tasks.md \
		$(SPEC_DIR)/research.md \
		$(SPEC_DIR)/data-model.md \
		$(SPEC_DIR)/quickstart.md \
		$(SPEC_DIR)/contracts/ingest-api.yaml \
		$(SPEC_DIR)/fixtures/example-event.json \
		deploy/k8s/testing/namespace.yaml \
		deploy/k8s/staging/namespace.yaml \
		deploy/k8s/testing/kustomization.yaml \
		deploy/k8s/staging/kustomization.yaml; do \
		test -f "$$file"; \
	done
	@python3 scripts/validate_openapi.py

test: spec-check
	@set -eu; \
	for dir in $(GOCACHE) $(GOMODCACHE); do mkdir -p "$$dir"; done; \
	found=0; \
	for mod in $$(find . $(FIND_PRUNE) -name go.mod -print 2>/dev/null); do \
		found=1; \
		dir=$$(dirname "$$mod"); \
		if find "$$dir" -name '*.go' -print -quit | grep -q .; then \
			echo "[test] go $$dir"; \
			(cd "$$dir" && go test ./...); \
		fi; \
	done; \
	for pkg in $$(find . $(FIND_PRUNE) -name package.json -print 2>/dev/null); do \
		found=1; \
		dir=$$(dirname "$$pkg"); \
		echo "[test] node $$dir"; \
		(cd "$$dir" && npm run test --if-present); \
	done; \
	for py in $$(find . $(FIND_PRUNE) \( -name pyproject.toml -o -name requirements.txt \) -print 2>/dev/null); do \
		found=1; \
		dir=$$(dirname "$$py"); \
		if find "$$dir" \( -name 'test_*.py' -o -name '*_test.py' -o -path '*/tests/*.py' \) -print -quit | grep -q .; then \
			echo "[test] python $$dir"; \
		fi; \
		if find "$$dir" \( -name 'test_*.py' -o -name '*_test.py' -o -path '*/tests/*.py' \) -print -quit | grep -q . && command -v pytest >/dev/null 2>&1; then \
			(cd "$$dir" && PYTHONPATH=src pytest); \
		elif find "$$dir" -name '*.py' -print -quit | grep -q .; then \
			(cd "$$dir" && PYTHONPATH=src python3 -m unittest discover); \
		fi; \
	done; \
	if [ "$$found" -eq 0 ]; then echo "[test] no code manifests found yet"; fi

lint:
	@set -eu; \
	for dir in $(GOCACHE) $(GOMODCACHE); do mkdir -p "$$dir"; done; \
	found=0; \
	for mod in $$(find . $(FIND_PRUNE) -name go.mod -print 2>/dev/null); do \
		found=1; \
		dir=$$(dirname "$$mod"); \
		if find "$$dir" -name '*.go' -print -quit | grep -q .; then \
			echo "[lint] go $$dir"; \
			(cd "$$dir" && go vet ./...); \
			formatted=$$(cd "$$dir" && find . $(FIND_PRUNE) -name '*.go' -print0 | xargs -0 gofmt -l); \
			if [ -n "$$formatted" ]; then \
				printf '%s\n' "$$formatted"; \
				exit 1; \
			fi; \
		fi; \
	done; \
	for pkg in $$(find . $(FIND_PRUNE) -name package.json -print 2>/dev/null); do \
		found=1; \
		dir=$$(dirname "$$pkg"); \
		echo "[lint] node $$dir"; \
		(cd "$$dir" && npm run lint --if-present); \
	done; \
	for py in $$(find . $(FIND_PRUNE) \( -name pyproject.toml -o -name requirements.txt \) -print 2>/dev/null); do \
		found=1; \
		dir=$$(dirname "$$py"); \
		echo "[lint] python $$dir"; \
		if command -v ruff >/dev/null 2>&1; then \
			(cd "$$dir" && ruff check .); \
		else \
			(cd "$$dir" && python3 -m compileall .); \
		fi; \
	done; \
	if [ "$$found" -eq 0 ]; then echo "[lint] no code manifests found yet"; fi

build:
	@set -eu; \
	for dir in $(GOCACHE) $(GOMODCACHE); do mkdir -p "$$dir"; done; \
	found=0; \
	for mod in $$(find . $(FIND_PRUNE) -name go.mod -print 2>/dev/null); do \
		found=1; \
		dir=$$(dirname "$$mod"); \
		if find "$$dir" -name '*.go' -print -quit | grep -q .; then \
			echo "[build] go $$dir"; \
			(cd "$$dir" && go build ./...); \
		fi; \
	done; \
	for pkg in $$(find . $(FIND_PRUNE) -name package.json -print 2>/dev/null); do \
		found=1; \
		dir=$$(dirname "$$pkg"); \
		echo "[build] node $$dir"; \
		(cd "$$dir" && npm run build --if-present); \
	done; \
	for py in $$(find . $(FIND_PRUNE) \( -name pyproject.toml -o -name requirements.txt \) -print 2>/dev/null); do \
		found=1; \
		dir=$$(dirname "$$py"); \
		echo "[build] python $$dir"; \
		if [ -f "$$dir/pyproject.toml" ]; then \
			if python3 -c 'import build' >/dev/null 2>&1; then \
				(cd "$$dir" && python3 -m build); \
			else \
				echo "[build] python build tool missing in $$dir"; \
			fi; \
		else \
			(cd "$$dir" && python3 -m compileall .); \
		fi; \
	done; \
	if [ "$$found" -eq 0 ]; then echo "[build] no code manifests found yet"; fi

dev:
	@set -eu; \
	if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 && docker info >/dev/null 2>&1; then \
		docker compose up --build; \
	else \
		echo "docker compose is not available yet"; \
	fi

docker-build:
	@set -eu; \
	if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then echo "[docker-build] docker is not available"; exit 0; fi; \
	found=0; \
	if [ -f deploy/docker/service.Dockerfile ]; then \
		found=1; \
		docker build -f deploy/docker/service.Dockerfile -t bugbarn/service:local .; \
	fi; \
	if [ -f deploy/docker/web.Dockerfile ]; then \
		found=1; \
		docker build -f deploy/docker/web.Dockerfile -t bugbarn/web:local .; \
	fi; \
	if [ "$$found" -eq 0 ]; then echo "[docker-build] no Dockerfiles found yet"; fi
