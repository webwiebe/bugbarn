SHELL := /bin/sh
.DEFAULT_GOAL := help

SPEC_DIR := specs/001-personal-error-tracker
LOCAL_DIRS := .cache var
FIND_PRUNE := \( -path './.git' -o -path './.cache' -o -path './.claude' -o -path '*/node_modules' -o -path '*/dist' -o -path '*/build' -o -path '*/.venv' \) -prune -o
export XDG_CACHE_HOME := $(CURDIR)/.cache
export GOCACHE := $(CURDIR)/.cache/go-build
export GOMODCACHE := $(CURDIR)/.cache/go-mod

.PHONY: help setup test lint build dev docker-build spec-check \
	lint-go check-file-length coverage dup quality \
	woodpecker-secrets-edit woodpecker-secrets-sync

help:
	@printf '%s\n' \
		'BugBarn targets:' \
		'  setup        bootstrap local tooling when manifests exist' \
		'  test         run spec checks plus available language tests' \
		'  lint         run available linters and static checks' \
		'  lint-go      run golangci-lint (complexity/dupl/size/correctness)' \
		'  check-file-length  fail on any source file over 500 lines' \
		'  coverage     measure Go coverage and ratchet against baseline' \
		'  dup          scan the repo for cross-file duplication (jscpd)' \
		'  quality      run all code-quality gates (file-length, lint, coverage, dup)' \
		'  build        run available build checks' \
		'  dev          start the local compose stack if available' \
		'  docker-build build placeholder images when Dockerfiles exist' \
		'  spec-check   verify the current spec-first scaffolding' \
		'' \
		'Woodpecker CI:' \
		'  woodpecker-secrets-edit  edit the SOPS-encrypted Woodpecker secrets' \
		'  woodpecker-secrets-sync  push secrets into the Woodpecker server'

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
	@sh scripts/check-file-length.sh

# golangci-lint for every Go module (root + SDK). Full run; CI narrows this to
# --new-from-rev so only PR-introduced issues block the build.
lint-go:
	@set -eu; \
	for dir in $(GOCACHE) $(GOMODCACHE); do mkdir -p "$$dir"; done; \
	for mod in $$(find . $(FIND_PRUNE) -name go.mod -print 2>/dev/null); do \
		dir=$$(dirname "$$mod"); \
		echo "[lint-go] $$dir"; \
		(cd "$$dir" && golangci-lint run ./...); \
	done

check-file-length:
	@sh scripts/check-file-length.sh

coverage:
	@set -eu; \
	for dir in $(GOCACHE) $(GOMODCACHE); do mkdir -p "$$dir"; done; \
	sh scripts/check-coverage.sh

dup:
	@npx --yes jscpd@latest .

# Umbrella gate used by CI: every code-quality check in one target.
quality: check-file-length lint-go coverage dup

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

# ── Woodpecker CI secrets ─────────────────────────────────────────────────────
# Source of truth for the Woodpecker secrets, encrypted at rest with SOPS/age.
# `sync` pushes them into the Woodpecker server. The three sops_age_key_* secrets
# are injected from the local age key file (not stored in the repo).
WOODPECKER_REPO          ?= webwiebe/bugbarn
WOODPECKER_SECRETS_FILE  := deploy/woodpecker-secrets.yaml
WOODPECKER_SECRET_EVENTS := --event push --event tag --event manual
SOPS_AGE_KEY_FILE        ?= $(HOME)/Library/Application Support/sops/age/keys.txt
export SOPS_AGE_KEY_FILE

woodpecker-secrets-edit:
	sops "$(WOODPECKER_SECRETS_FILE)"

woodpecker-secrets-sync:
	@command -v woodpecker-cli >/dev/null 2>&1 || { echo "woodpecker-cli not installed (brew install woodpecker-cli)"; exit 1; }
	@command -v sops >/dev/null 2>&1 || { echo "sops not installed"; exit 1; }
	@test -n "$$WOODPECKER_SERVER" || { echo "set WOODPECKER_SERVER (e.g. https://woodpecker.nijmegen.wiebe.xyz)"; exit 1; }
	@test -n "$$WOODPECKER_TOKEN"  || { echo "set WOODPECKER_TOKEN (run: woodpecker-cli setup)"; exit 1; }
	@set -eu; \
	get() { sops -d --extract "[\"$$1\"]" "$(WOODPECKER_SECRETS_FILE)"; }; \
	put() { \
		case "$$2" in REPLACE_ME|"") echo "ERROR: secret '$$1' is unset — run 'make woodpecker-secrets-edit'"; exit 1;; esac; \
		woodpecker-cli repo secret add    --repository "$(WOODPECKER_REPO)" --name "$$1" --value "$$2" $(WOODPECKER_SECRET_EVENTS) >/dev/null 2>&1 \
		|| woodpecker-cli repo secret update --repository "$(WOODPECKER_REPO)" --name "$$1" --value "$$2" $(WOODPECKER_SECRET_EVENTS) >/dev/null; \
		echo "  synced $$1"; \
	}; \
	AGE_KEY=$$(grep -E 'AGE-SECRET-KEY' "$$SOPS_AGE_KEY_FILE" | head -1); \
	test -n "$$AGE_KEY" || { echo "no AGE-SECRET-KEY in $$SOPS_AGE_KEY_FILE"; exit 1; }; \
	echo "Syncing Woodpecker secrets to $(WOODPECKER_REPO)…"; \
	put ghcr_token            "$$(get ghcr_token)"; \
	put ghcr_pull_pat         "$$(get ghcr_pull_pat)"; \
	put bugbarn_api_key       "$$(get bugbarn_api_key)"; \
	put sops_age_key_testing    "$$AGE_KEY"; \
	put sops_age_key_staging    "$$AGE_KEY"; \
	put sops_age_key_production  "$$AGE_KEY"; \
	echo "Done."
