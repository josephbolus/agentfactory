SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c

ROOT := $(CURDIR)
DATA_HOME ?= $(or $(FACTORY_DATA_HOME),$(FACTORY_V2_DATA_HOME),$(HOME)/.factory)
BUILD_DIR ?= $(or $(FACTORY_BUILD_DIR),$(FACTORY_V2_BUILD_DIR),$(DATA_HOME)/bin)

DEMO_OWNER := josephbolus
DEMO_REPO := $(DEMO_OWNER)/factory-demo
PROJECT_NUMBER := 5
PROJECT_ID := PVT_kwHOAB2MnM4Bitvg
STATUS_FIELD_ID := PVTSSF_lAHOAB2MnM4BitvgzhhktV0
READY_OPTION_ID := 61e4505c
STATE := $(ROOT)/.demo/factory
RUN := $(STATE)/run
SERVER_PID := $(RUN)/factory-server.pid
WORKER_PID := $(RUN)/factory-worker.pid
DEMO_PORT := 7339
DEMO_ADDRESS := 127.0.0.1:$(DEMO_PORT)
DEMO_URL := http://$(DEMO_ADDRESS)

.PHONY: all default build run ui-install ui-build ui-check test-browser format-check vet vuln staticcheck boundary test test-go test-worker-race test-tooling test-launcher release test-release check help demo-setup demo-start demo-stop demo-status demo-autopoll demo-issue-search demo-issue-total demo-issue-dba demo-issue test-local-runtimes

default: check

# Build operator binaries from committed embedded UI assets. Node is not used.
build:
	@mkdir -p "$(BUILD_DIR)"
	go build -o "$(BUILD_DIR)/factory" ./cmd/factory
	go build -o "$(BUILD_DIR)/factory-server" ./cmd/factory-server
	go build -o "$(BUILD_DIR)/factory-worker" ./cmd/factory-worker
	@printf 'Agent Factory binaries built in %s\n' "$(BUILD_DIR)"

# Start one control plane and worker. Pass CONFIG=... when needed.
run:
	@if [ -n "$(CONFIG)" ]; then ./scripts/run-local.sh "$(CONFIG)"; else ./scripts/run-local.sh; fi

# Install pinned UI dependencies.
ui-install:
	cd web && npm ci

# Rebuild committed embedded UI assets. Pass INSTALL=0 to reuse installed dependencies.
INSTALL ?= 1
ui-build:
	@if [ "$(INSTALL)" = "1" ] && [ -z "$$FACTORY_V2_SKIP_INSTALL" ]; then cd web && npm ci; fi
	cd web && npm run build

# Run UI lint, type checks, and component tests.
ui-check:
	cd web && npm run lint
	cd web && npm run typecheck
	cd web && npm test

# Run browser tests against the real Go server.
test-browser:
	cd web && npm run test:browser

# Report Go files that need formatting.
format-check:
	@test -z "$$(find cmd internal migrations web -path web/node_modules -prune -o -name '*.go' -exec gofmt -l {} +)"

# Run Go static analysis.
vet:
	go vet ./...

# Fail on reachable Go vulnerabilities using the supported patched toolchain.
vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Run correctness and dead-code checks without style-only churn.
staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@latest -checks 'SA*,U1000' ./...

# Prove workers do not import control-plane implementation code.
boundary:
	@! go list -deps ./internal/worker | grep -qx 'github.com/josephbolus/agentfactory/internal/controlplane'

# Run all Go tests.
test: test-go

test-go:
	go test -timeout 5m ./...

# Race-check the worker package, including coordination and process cancellation.
test-worker-race:
	@set -euo pipefail; \
	log="$$(mktemp)"; \
	trap 'rm -f "$$log"' EXIT; \
	go test -timeout 5m -race -count=1 -v ./internal/worker 2>&1 | tee "$$log"; \
	count="$$(grep -c '^=== RUN   Test' "$$log" || true)"; \
	if [ "$$count" -eq 0 ]; then \
		printf 'test-worker-race selected zero tests in ./internal/worker\n' >&2; \
		exit 1; \
	fi; \
	printf 'test-worker-race ran %s tests with the race detector\n' "$$count"

# Test the Node-free build and command surface.
test-tooling:
	./scripts/test-build.sh
	./scripts/test-update-go-minimum.sh

# Test local startup, readiness, and signal handling.
test-launcher:
	./scripts/test-run-local.sh

test-local-runtimes:
	@FACTORY_TEST_LOCAL_RUNTIMES=1 go test ./internal/worker -run '^TestLocalRuntimeProfiles$$' -count=1

# Build a tagged release set from the current checkout.
VERSION ?=
COMMIT ?=
OUTPUT ?= dist
release:
	./scripts/release.sh "$(VERSION)" "$(COMMIT)" "$(OUTPUT)"

# Rebuild twice and verify every release target and native version output.
test-release:
	./scripts/test-release.sh

# Run the normal local and CI checks, excluding the slower browser suite.
check: format-check vet vuln staticcheck boundary test-go ui-check test-tooling test-launcher

# Include the browser, race, and reproducible-release checks.
all: check test-worker-race test-browser test-release

help:
	@echo 'Agent Factory make targets:'
	@echo '  make build              Build factory, factory-server, and factory-worker'
	@echo '  make run [CONFIG=...]   Start local control plane and worker'
	@echo '  make check              Run format, vet, vuln, staticcheck, boundary, test-go, ui-check, tooling'
	@echo '  make format-check       Check Go formatting'
	@echo '  make vet                Run go vet'
	@echo '  make boundary           Verify worker does not import controlplane'
	@echo '  make staticcheck        Run staticcheck'
	@echo '  make test-go            Run Go unit tests'
	@echo '  make ui-check           Run UI lint, typecheck, and component tests'
	@echo '  make demo-setup         Build Agent Factory and configure a clean demo Worker'
	@echo '  make demo-start         Start Agent Factory server and worker'
	@echo '  make demo-stop          Stop demo server and worker'
	@echo '  make demo-status        Show demo status'
	@echo '  make demo-autopoll      Start GitHub intake demo'
	@echo '  make test-local-runtimes Check configured Pi and Claude models'

demo-setup:
	@test -f "$(ROOT)/internal/controlplane/github_intake.go" || { echo 'This demo requires GitHub issue intake support.' >&2; exit 1; }
	@mkdir -p "$(STATE)/bin" "$(STATE)/server" "$(STATE)/worker" "$(RUN)"
	@go build -o "$(STATE)/bin/factory" ./cmd/factory
	@go build -o "$(STATE)/bin/factory-server" ./cmd/factory-server
	@go build -o "$(STATE)/bin/factory-worker" ./cmd/factory-worker
	@cp "$(ROOT)/examples/demo-server.toml" "$(STATE)/config.toml"
	@cp "$(ROOT)/examples/demo-worker.toml" "$(STATE)/worker.toml"
	@echo 'Demo configured.'

demo-start: demo-setup
	@if curl --fail --silent "$(DEMO_URL)/healthz" >/dev/null; then \
		if test -f "$(SERVER_PID)" && kill -0 "$$(cat "$(SERVER_PID)")" 2>/dev/null; then \
			echo 'Demo is already running: $(DEMO_URL)/'; \
			exit 0; \
		fi; \
		echo 'Port $(DEMO_ADDRESS) is in use by another server. Run make demo-stop only if it is this demo.' >&2; \
		exit 1; \
	fi; \
	FACTORY_DATA_HOME="$(STATE)" "$(STATE)/bin/factory-server" & \
	server_pid=$$!; \
	echo "$$server_pid" > "$(SERVER_PID)"; \
	until curl --fail --silent "$(DEMO_URL)/healthz" >/dev/null; do sleep 1; done; \
	repository_id=$$(curl --fail --silent --show-error -X POST "$(DEMO_URL)/api/v1/repositories" \
		-H 'Content-Type: application/json' -d '{"remote_identity":"github.com/$(DEMO_REPO)"}' | sed -nE 's/.*"id":"([^"]+)".*/\1/p'); \
	test -n "$$repository_id"; \
	curl --fail --silent --show-error -X POST "$(DEMO_URL)/api/v1/repositories/$$repository_id/workflows/refresh" \
		-H 'Content-Type: application/json' -d '{}' >/dev/null; \
	curl --fail --silent --show-error -X PUT "$(DEMO_URL)/api/v1/repositories/$$repository_id/issue-intake" \
		-H 'Content-Type: application/json' -d '{"enabled":true}' >/dev/null; \
	PATH="$(STATE)/bin:$$PATH" "$(STATE)/bin/factory-worker" --config "$(STATE)/worker.toml" & \
	worker_pid=$$!; \
	echo "$$worker_pid" > "$(WORKER_PID)"; \
	trap 'kill "$$server_pid" "$$worker_pid" 2>/dev/null || true; rm -f "$(SERVER_PID)" "$(WORKER_PID)"' EXIT INT TERM; \
	echo 'Agent Factory UI: $(DEMO_URL)/'; \
	wait

demo-autopoll: demo-start

demo-stop:
	@for pid_file in "$(SERVER_PID)" "$(WORKER_PID)"; do \
		if test -f "$$pid_file" && kill -0 "$$(cat "$$pid_file")" 2>/dev/null; then kill "$$(cat "$$pid_file")"; fi; \
		rm -f "$$pid_file"; \
	done; \
	for pid in $$(lsof -tiTCP:$(DEMO_PORT) -sTCP:LISTEN 2>/dev/null); do \
		case "$$(ps -p "$$pid" -o command= 2>/dev/null)" in *factory-server*) kill "$$pid" ;; esac; \
	done; \
	for pid in $$(lsof -t "$(STATE)/worker/worker.lock" 2>/dev/null); do kill "$$pid"; done; \
	echo 'Demo stopped.'

demo-status:
	@if curl --fail --silent "$(DEMO_URL)/healthz" >/dev/null; then \
		echo 'Demo server: $(DEMO_URL)/'; \
	else \
		echo 'Demo server: stopped'; \
	fi; \
	if lsof -t "$(STATE)/worker/worker.lock" >/dev/null 2>&1; then \
		echo 'Demo worker: running'; \
	else \
		echo 'Demo worker: stopped'; \
	fi

demo-issue-search:
	@TITLE='Fix case-insensitive product search' BODY=$$'Typing `factory` does not find `Factory T-Shirt`; search should be case-insensitive.\n\nAcceptance criteria:\n- A lowercase query finds matching products regardless of product-name casing.\n- Add a regression test.\n- `npm test` passes.' $(MAKE) --no-print-directory demo-issue

demo-issue-total:
	@TITLE='Fix cart total with multiple quantities' BODY=$$'Cart total ignores an item quantity greater than one.\n\nAcceptance criteria:\n- The total is price multiplied by quantity for every item.\n- Add a regression test.\n- `npm test` passes.' $(MAKE) --no-print-directory demo-issue

demo-issue-dba:
	@TITLE='Review missing database index' LABEL='team:dba' BODY=$$'The orders query regressed after the latest data growth. Review index coverage and representative query plans.\n\nAcceptance criteria:\n- Reproduce the slow query with a focused fixture.\n- Propose the smallest safe index change.\n- Add or update a regression check.\n- `npm test` passes.' $(MAKE) --no-print-directory demo-issue

demo-issue:
	@test -f "$(SERVER_PID)" && test -f "$(WORKER_PID)" || { echo 'This checkout demo is not running. Run make demo-start first.' >&2; exit 1; }; \
	kill -0 "$$(cat "$(SERVER_PID)")" 2>/dev/null || { echo 'This checkout demo server is not running.' >&2; exit 1; }; \
	lsof -t "$(STATE)/worker/worker.lock" >/dev/null 2>&1 || { echo 'This checkout demo worker is not running.' >&2; exit 1; }; \
	curl --fail --silent "$(DEMO_URL)/healthz" >/dev/null || { echo 'This checkout demo server is unhealthy.' >&2; exit 1; }; \
	label=$${LABEL:-enhancement}; \
	gh label create "$$label" --repo "$(DEMO_REPO)" --color 1d76db --description 'Factory workflow routing label' --force >/dev/null 2>&1 || true; \
	issue_url=$$(gh issue create --repo "$(DEMO_REPO)" --title "$$TITLE" --body "$$BODY" --label "$$label"); \
	item_id=$$(gh project item-add "$(PROJECT_NUMBER)" --owner "$(DEMO_OWNER)" --url "$$issue_url" --format json --jq .id); \
	gh project item-edit --id "$$item_id" --project-id "$(PROJECT_ID)" --field-id "$(STATUS_FIELD_ID)" --single-select-option-id "$(READY_OPTION_ID)"; \
	gh issue edit "$$issue_url" --repo "$(DEMO_REPO)" --add-label needs-agent; \
	echo "Created $$issue_url (Project: Ready; label: needs-agent)"
