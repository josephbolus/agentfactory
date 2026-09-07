SHELL := /bin/bash

ROOT := $(CURDIR)
DEMO_OWNER := jbolus-owens
DEMO_REPO := $(DEMO_OWNER)/factory-demo
PROJECT_NUMBER := 1
PROJECT_ID := PVT_kwHOEA3W384BhRJz
STATUS_FIELD_ID := PVTSSF_lAHOEA3W384BhRJzzhgNgMI
READY_OPTION_ID := 4c7740f5
STATE := $(ROOT)/.demo/factory
RUN := $(STATE)/run
SERVER_PID := $(RUN)/factory-server.pid
WORKER_PID := $(RUN)/factory-worker.pid
DEMO_PORT := 7339
DEMO_ADDRESS := 127.0.0.1:$(DEMO_PORT)
DEMO_URL := http://$(DEMO_ADDRESS)

.PHONY: all check format-check vet vuln staticcheck boundary test-go test-worker-race test-tooling test-launcher test-local-runtimes ui-check test-browser test-release test help demo-setup demo-start demo-stop demo-status demo-autopoll demo-issue-search demo-issue-total demo-issue-dba

# Run the documented local check suite through the canonical Just recipes.
all: test

check:
	@just check

format-check:
	@just format-check

vet:
	@just vet

vuln:
	@just vuln

staticcheck:
	@just staticcheck

boundary:
	@just boundary

test-go:
	@just test

test-worker-race:
	@just test-worker-race

test-tooling:
	@just test-tooling

test-launcher:
	@just test-launcher

test-local-runtimes:
	@FACTORY_TEST_LOCAL_RUNTIMES=1 go test ./internal/worker -run '^TestLocalRuntimeProfiles$$' -count=1

ui-check:
	@just ui-check

test-browser:
	@just test-browser

test-release:
	@just test-release

# Include the browser, race, and reproducible-release checks.
test: check test-worker-race test-browser test-release

help:
	@echo 'Agent Factory demo commands:'
	@echo '  make demo-setup         Build Agent Factory and configure a clean demo Worker'
	@echo '  make demo-start         Start Agent Factory server and worker (or show its URL)'
	@echo '  make demo-stop          Stop this demo server and worker'
	@echo '  make demo-status        Show whether this demo is running'
	@echo '  make demo-autopoll      Start the 30-second GitHub intake demo'
	@echo '  make demo-issue-search  Create Ready + needs-agent search bug issue'
	@echo '  make demo-issue-total   Create Ready + needs-agent cart-total bug issue'
	@echo '  make demo-issue-dba     Create a database index review issue'
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

.PHONY: demo-issue
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
