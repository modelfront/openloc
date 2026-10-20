.PHONY: dev dev-down dev-restart dev-logs seed-spellcheck seed-task-matrix seed-task-statuses seed-ai-steps seed-analysis seed-docs docs-screenshots journeys-parallel \
       run watch test test-short test-integration ci-go ci-types check-sqlc ci-web ci-images ci-lint ci-e2e ci-journeys check-go-short check-tsc check-licenses scan-secrets scan-vulns lint fmt vet \
       migrate-up migrate-down migrate-status migrate-create \
       sqlc types frontend build clean check e2e reset reset-storage seed \
       sidecar sidecar-test help

# --- Config (dev instance) ---
# Dev "instance" config. Each checkout runs in a 20-wide port block in the
# 9000s (BASE(N)=9000+(N-1)*20) so it never touches the common dev ports that
# clash with other apps. The instance-1 defaults below let a checkout with no
# instance.conf run as the original. An optional, git-ignored instance.conf
# (`KEY = value` lines for the ports below and COMPOSE_PROJECT_NAME) overrides
# these through the `-include`, so multiple checkouts run side-by-side. `export` pushes them into every recipe shell
# (air, go run, seed, docker compose) so the backend + Compose target this
# instance's infra instead of the code defaults.
FRONTEND_PORT        ?= 9000
BACKEND_PORT         ?= 9001
POSTGRES_PORT        ?= 9002
REDIS_PORT           ?= 9003
# S3-compatible storage (SeaweedFS): API and file browser.
S3_PORT              ?= 9004
S3_UI_PORT           ?= 9005
LIBRETRANSLATE_PORT  ?= 9006
PARSER_PORT          ?= 9007
LANGUAGETOOL_PORT    ?= 9008
COMPOSE_PROJECT_NAME ?= openloc

DATABASE_URL       ?= postgres://openloc:openloc@localhost:$(POSTGRES_PORT)/openloc?sslmode=disable
PORT               ?= $(BACKEND_PORT)
REDIS_ADDR         ?= localhost:$(REDIS_PORT)
S3_ENDPOINT        ?= http://localhost:$(S3_PORT)
PARSER_SIDECAR_URL ?= http://localhost:$(PARSER_PORT)
LIBRETRANSLATE_URL ?= http://localhost:$(LIBRETRANSLATE_PORT)
SPELLCHECK_LT_URL  ?= http://localhost:$(LANGUAGETOOL_PORT)/v2/check
BASE_URL           ?= http://localhost:$(FRONTEND_PORT)
APP_URL            ?= http://localhost:$(FRONTEND_PORT)
API_BASE           ?= http://localhost:$(BACKEND_PORT)
# The seed's platform admin (internal/seedid OperatorEmail): reads reported issues.
PLATFORM_ADMIN_EMAILS ?= ops@operators.example.com

-include instance.conf

BIN          := bin/server
FRONTEND_URL := http://localhost:$(FRONTEND_PORT)

# --- Go-installed dev tools ---
# `go install` puts these in GOPATH/bin, which is NOT on PATH on every dev box
# (it isn't in the default macOS /etc/paths). When it's absent, `goose`/`air`
# fail with "command not found" — even right after `go install` succeeds. Prefer
# PATH, fall back to GOPATH/bin, so both setups work. Call these via the vars.
GO_BIN := $(shell go env GOPATH)/bin
GOOSE  ?= $(shell command -v goose 2>/dev/null || echo $(GO_BIN)/goose)
AIR    ?= $(shell command -v air   2>/dev/null || echo $(GO_BIN)/air)

export INSTANCE COMPOSE_PROJECT_NAME
export FRONTEND_PORT BACKEND_PORT POSTGRES_PORT REDIS_PORT S3_PORT S3_UI_PORT LIBRETRANSLATE_PORT PARSER_PORT LANGUAGETOOL_PORT
export DATABASE_URL PORT REDIS_ADDR S3_ENDPOINT PARSER_SIDECAR_URL LIBRETRANSLATE_URL SPELLCHECK_LT_URL BASE_URL APP_URL API_BASE
export PLATFORM_ADMIN_EMAILS

# Cross-platform browser open
OPEN_CMD := $(shell command -v xdg-open 2>/dev/null || command -v open 2>/dev/null || echo "")

# --- Dev environment ---

dev: ## Start dev dependencies (postgres, redis, seaweedfs, …)
	docker compose up -d
	@echo "Waiting for postgres..."
	@until docker compose exec -T postgres pg_isready -U openloc > /dev/null 2>&1; do sleep 0.5; done
	@echo "Dependencies ready. Run 'make migrate-up' then 'make watch'."

dev-down: ## Stop dev dependencies
	docker compose down

dev-restart: ## Restart dev dependencies
	docker compose restart

dev-logs: ## Tail dev dependency logs
	docker compose logs -f

# --- App ---

run: ## Run the server (no reload)
	go run ./cmd/server

watch: ## Run backend + frontend with live reload
	@if [ ! -x "$(AIR)" ] && ! command -v air > /dev/null 2>&1; then \
		echo "Installing air..."; \
		go install github.com/air-verse/air@latest; \
	fi
	@# Bare `air` here failed with "command not found" right after the install
	@# above, because GOPATH/bin isn't on PATH on this box.
	@[ -x "$(AIR)" ] || { echo "air still not found at $(AIR) — check 'go env GOPATH'"; exit 1; }
	@# npm writes node_modules/.package-lock.json only after a complete install:
	@# missing means never or not finished, older than package-lock.json means a
	@# pull changed the dependencies since.
	@[ web/node_modules/.package-lock.json -nt web/package-lock.json ] || { echo "Installing frontend dependencies..."; cd web && npm ci --no-audit --no-fund; }
	@# Dev sign-in warning. Only `fresh` sets DEV_AUTO_LOGIN, and there
	@# are no Google/Microsoft creds in dev — so under `watch` the login page
	@# renders NO buttons, just "Contact your administrator for access.". That
	@# looks exactly like an auth bug, so say it out loud instead of dead-ending.
	@if [ "$$DEV_AUTO_LOGIN" != "true" ]; then \
		echo ""; \
		echo "\033[33m⚠  DEV_AUTO_LOGIN is not set — dev sign-in is DISABLED.\033[0m"; \
		echo "\033[33m   The login page will show no buttons, only \"Contact your administrator\".\033[0m"; \
		echo "\033[33m   To sign in: use 'make fresh', or 'export DEV_AUTO_LOGIN=true' first.\033[0m"; \
		echo ""; \
	fi
	@# Kill orphan Vite/Air/esbuild processes from previous runs to avoid stale servers
	@$(MAKE) -s kill >/dev/null
	@sleep 0.5
	@trap 'kill 0' EXIT; \
	$(AIR) & \
	cd web && npm run dev & \
	sleep 2 && [ -n "$(OPEN_CMD)" ] && $(OPEN_CMD) $(FRONTEND_URL) & \
	wait

build: ## Build server binary
	go build -o $(BIN) ./cmd/server

clean: ## Remove build artifacts
	rm -f server seed
	rm -rf tmp/

# --- Test & lint ---

# Package parallelism for the integration suites: each package starts its own
# Postgres testcontainer, so size this to DOCKER's capacity (`docker info
# --format '{{.NCPU}}'`), not the host's — Go's GOMAXPROCS default oversubscribes
# the VM and wedges the daemon. scripts/test-all.sh keeps a matching default and
# omits the cap on CI, where the daemon is native.
GO_TEST_P ?= 4

test: ## Run all tests including integration (requires Docker) — quiet; only failures expand
	go test ./... -count=1 -p $(GO_TEST_P)

test-short: ## Run unit tests only (skip integration) — quiet
	go test ./... -short -count=1

test-integration: ## Run only integration tests (requires Docker) — quiet
	go test ./... -count=1 -p $(GO_TEST_P) -run Integration

test-ci: ## Same as test but verbose (every test name + log). For local debugging.
	go test ./... -v -count=1 -p $(GO_TEST_P)

test-ci-short: ## Same as test-short but verbose. Local debugging — see test-ci.
	go test ./... -v -short -count=1

# CI runs these targets. Run them locally to get the same checks.
ci-go: ## CI: build, vet and test the Go code, integration tests included (needs Docker)
	go build ./...
	go vet ./...
	go test ./... -count=1

ci-web: ## CI: install, typecheck and unit-test the frontend (Node 24)
	cd web && npm ci --no-audit --no-fund && npx tsc -b && npm test

# The platforms images.yml publishes, so a merge proves a release can build.
IMAGE_PLATFORMS ?= linux/amd64,linux/arm64

ci-images: ## CI: build both production images for every published platform, without pushing them (buildx; IMAGE_PLATFORMS=linux/amd64 for one)
	docker buildx build --platform $(IMAGE_PLATFORMS) -f dockerfiles/openloc.dockerfile .
	docker buildx build --platform $(IMAGE_PLATFORMS) -f dockerfiles/openloc-docparser.dockerfile .

ci-types: ## CI: the TypeScript types tygo generates match the committed web/src/types
	@tmp=$$(mktemp -d) && cp -R web/src/types "$$tmp/types" && $(TYGO) generate && \
	if diff -ru "$$tmp/types" web/src/types; then rm -rf "$$tmp"; \
	else rm -rf "$$tmp"; echo "web/src/types is stale: run 'make types' and commit the result"; exit 1; fi

check-sqlc: ## The sqlc code in internal/database/sqlcgen matches the queries (needs the sqlc version it was generated with; tested by scripts/check-sqlc.test.sh)
	scripts/check-sqlc.sh

ci-lint: ## CI: frontend eslint with zero problems and the production-only import check (Node 24)
	cd web && npm ci --no-audit --no-fund
	bash scripts/lint-guards.sh

# Quick checks without the database tests. CI runs the full ci-go and ci-web
# instead.
check-go-short: ## Build, vet and run the Go tests without the database ones (-short, no Docker)
	go build ./...
	go vet ./...
	go test ./... -short -count=1

check-tsc: ## Install and type-check the frontend (Node 24)
	cd web && npm ci --no-audit --no-fund && npx tsc -b

check-licenses: ## Build both images and fail when a package they ship has a license outside the allowlist (copyleft other than MPL-2.0, or unknown), unless dependency-license-exceptions.toml accepts it (Docker); its own test runs first
	scripts/check-licenses-test.sh
	scripts/check-licenses.sh

# Scanners to run locally. CI does not run them.
# They run from pinned images, so neither needs a local install. Run them on a
# clean checkout: node_modules folders would be scanned too.
GITLEAKS_IMAGE ?= ghcr.io/gitleaks/gitleaks:v8.30.1@sha256:c00b6bd0aeb3071cbcb79009cb16a60dd9e0a7c60e2be9ab65d25e6bc8abbb7f
OSV_IMAGE      ?= ghcr.io/google/osv-scanner:v2.6.0@sha256:afd838850ac1a0fcc15ff4a041dc9ba11123c3f0d2666217a5f0fcf9222b55fa

scan-secrets: ## Scan the tree for secrets with gitleaks (Docker); a test fixture carries a `gitleaks:allow` comment
	docker run --rm -v "$(CURDIR):/repo:ro" -w /repo $(GITLEAKS_IMAGE) dir --no-banner --redact --exit-code 1 .

scan-vulns: ## Known vulnerabilities in Go, npm and Python dependencies with osv-scanner (Docker); accepted ones are in osv-scanner.toml
	@# No call analysis: it builds the Go call graph, which the image has no Go
	@# toolchain for and which runs out of memory in a small Docker VM.
	docker run --rm -v "$(CURDIR):/src:ro" $(OSV_IMAGE) scan source --no-call-analysis=all --config=/src/osv-scanner.toml -r /src

ci-e2e: ## CI: boot the dev stack and run the Playwright e2e suite (Docker, Go, Node 24); PW_SHARD=1/2 runs one shard
	bash scripts/ci-playwright.sh e2e

ci-journeys: ## CI: boot the dev stack and run the Playwright journey suite (Docker, Go, Node 24); PW_SHARD=1/3 runs one shard
	bash scripts/ci-playwright.sh journeys

lint: ## Run golangci-lint
	golangci-lint run ./...

fmt: ## Format code
	gofmt -w .
	goimports -local openloc -w .

vet: ## Run go vet
	go vet ./...

# --- Database ---

migrate-up: ## Run all pending migrations (-allow-missing: timestamped migrations can merge out of order)
	$(GOOSE) -dir internal/database/migrations postgres "$(DATABASE_URL)" up -allow-missing

migrate-down: ## Roll back last migration
	$(GOOSE) -dir internal/database/migrations postgres "$(DATABASE_URL)" down

migrate-status: ## Show migration status
	$(GOOSE) -dir internal/database/migrations postgres "$(DATABASE_URL)" status

migrate-create: ## Create a new migration (usage: make migrate-create NAME=add_users)
	$(GOOSE) -dir internal/database/migrations create $(NAME) sql

# --- Seed & Reset ---

seed: ## Seed demo data (tenant + admin user)
	go run ./cmd/seed

seed-spellcheck: ## Create the "Spellcheck QA" project for manual spell check testing
	go run ./cmd/seed-spellcheck

seed-task-matrix: ## Create the "Task Matrix Showcase" project: every Tasks/Files state
	go run ./cmd/seed-task-matrix

seed-task-statuses: ## Create the "All task statuses" project: 2 files × 5 languages, one task state per job, editable (run by `make reset`)
	go run ./cmd/seed-task-statuses

seed-ai-steps: ## Create the "AI step states" project: Hybrid → AI workflow, an AI run failed and one running in each step (run by `make reset`)
	go run ./cmd/seed-ai-steps

seed-analysis: ## Create the "Analysis export demo" project: 5 files × 5 languages and one completed analysis with realistic numbers (run by `make reset`)
	go run ./cmd/seed-analysis

# `make reset` refuses to drop a database something is still connected to (a
# dev server, a psql session) and says so; FORCE=1 ends those sessions and drops
# anyway. The server reconnects by itself.
RESET_FORCE_SQL := $(if $(FORCE),WITH (FORCE),)

reset: reset-storage ## Drop + recreate DB, wipe the storage bucket, run migrations, seed. Refuses while a server is connected; FORCE=1 drops anyway
	@echo "==> Dropping and recreating database..."
	@docker compose exec -T postgres psql -U openloc -d postgres -v ON_ERROR_STOP=1 \
	  -c "DROP DATABASE IF EXISTS openloc $(RESET_FORCE_SQL);" -c "CREATE DATABASE openloc OWNER openloc;" \
	  || { echo "✗ reset stopped: something is still connected to the database (a dev server?). Stop it, or run: make reset FORCE=1" >&2; exit 1; }
	@echo "==> Running migrations..."
	@$(MAKE) migrate-up
	@echo "==> Seeding data..."
	@$(MAKE) seed
	@$(MAKE) seed-task-statuses
	@$(MAKE) seed-ai-steps
	@$(MAKE) seed-analysis
	@echo "==> Reset complete ✓"

reset-storage: ## Wipe the dev storage bucket. Pairs with `reset` to keep DB + storage consistent.
	@# Deletes the bucket with everything in it and creates it again empty, so
	@# stored files match the freshly seeded database. Two weed shell calls:
	@# weed shell exits 1 when any command fails, and deleting a bucket that
	@# doesn't exist yet (fresh stack) must not stop the create. The create's
	@# status is the target's, so a down container still fails loudly.
	@echo "==> Wiping storage bucket 'openloc'..."
	@docker compose exec -T seaweedfs sh -c 'printf "s3.bucket.delete -name openloc\n" | weed shell -master=localhost:9333 >/dev/null 2>&1; printf "s3.bucket.create -name openloc\n" | weed shell -master=localhost:9333 >/dev/null 2>&1'
	@echo "==> Storage wiped ✓"

# --- Codegen ---

sqlc: ## Generate sqlc code + regenerate mock querier stubs (pipeline + worker)
	sqlc generate
	cd internal/pipeline && go run gen_mock_querier.go
	cd internal/worker && go run gen_mock_querier.go

# Pinned: a tygo release can change its output, and ci-types compares it with
# the committed files.
TYGO := go run github.com/gzuidhof/tygo@v0.2.21

types: ## Generate TypeScript types from Go structs (tygo)
	$(TYGO) generate
	@echo "✓ Generated web/src/types/generated.ts"
	@# Built-in palettes: Go is the source of truth, and the
	@# frontend needs them as a bundled constant because palettes resolve before
	@# first paint. tygo emits types, not values, so this is a separate step.
	cd internal/appearance && go run gen_ts_palettes.go

# --- Pre-flight check ---

check: ## Run all pre-flight checks (build, test, typecheck, sqlc)
	@echo "==> Go build..."
	@go build ./...
	@echo "==> Go tests (short)..."
	@go test -short -count=1 ./...
	@echo "==> Frontend build (tsc + vite — matches Docker)..."
	@cd web && npm run build
	@echo "==> All checks passed ✓"

# --- E2E tests ---

e2e: ## Run Playwright E2E tests (requires running backend + frontend)
	cd e2e && npx playwright test

e2e-headed: ## Run E2E tests with visible browser
	cd e2e && npx playwright test --headed

e2e-ui: ## Open Playwright UI mode
	cd e2e && npx playwright test --ui

journeys: ## Run journey tests headless (results written to e2e/journey-results.json)
	cd e2e && npx playwright test --config=playwright.journeys.config.ts

journeys-parallel: ## Run journeys as N isolated sharded stacks (default 3, ~2x faster); ARGS=--on-dev puts shard 1 on the dev instance
	bash scripts/journeys-parallel.sh $(SHARDS) $(ARGS)

journeys-headed: ## Run journey tests with visible browser
	cd e2e && npx playwright test --config=playwright.journeys.config.ts --headed

seed-docs: ## Add the realistic dataset the help-page screenshots are taken from (on top of make seed; no-op if present)
	go run ./cmd/seed-docs

docs-screenshots: seed-docs ## Re-shoot the help-page screenshots (fresh DB first: make kill && make reset && make fresh)
	cd e2e && npx playwright test --config=playwright.docs.config.ts

# --- Parser sidecar (Python) ---

sidecar: ## Start parser sidecar in Docker (dev mode, live reload)
	docker compose up -d parser-sidecar

sidecar-test: ## Run parser sidecar Python tests (inside Docker)
	docker compose exec parser-sidecar python -m pytest tests/ -v

# Runs uv from an untagged build of the sidecar dev image, so it never
# replaces the image a running compose stack uses.
sidecar-lock: ## Update the parser sidecar's uv.lock from pyproject.toml (UPGRADE=1 bumps every pin)
	docker run --rm -v "$(CURDIR)/sidecars/parser:/app" -w /app \
		$$(docker build -q -f dockerfiles/openloc-docparser.dev.dockerfile .) \
		uv lock $(if $(UPGRADE),--upgrade)

# --- Frontend ---

frontend: ## Start frontend dev server (no backend)
	@$(MAKE) -s kill >/dev/null
	@sleep 0.5
	cd web && npm run dev

kill: ## Kill THIS checkout's dev processes and free its backend/frontend ports (leaves other checkouts alone)
	@# Other checkouts may run side-by-side on their own port blocks. The
	@# vite/esbuild pkills are anchored to $(CURDIR) so they only match processes
	@# launched from THIS directory — a bare
	@# `pkill -x air` / `pkill -f vite` would also kill the other instance's
	@# servers. air/npm listeners are freed by the port-based kills below.
	@-pkill -9 -f "$(CURDIR).*node_modules/.bin/vite" 2>/dev/null || true
	@-pkill -9 -f "$(CURDIR).*node_modules/@esbuild/.*/bin/esbuild" 2>/dev/null || true
	@# Reap AIR ITSELF, or it notices its child died and respawns the server —
	@# reclaiming the port seconds after this target reports success. That stale
	@# server then answers `fresh`'s health check while your new binary and env
	@# are nowhere, which is a silent wrong-build session.
	@#
	@# air's argv is just the binary path, so it can't be matched on $(CURDIR)
	@# like vite; match on its WORKING DIRECTORY instead, which keeps a
	@# side-by-side instance's air untouched.
	@-for p in $$(pgrep -f "go/bin/air" 2>/dev/null); do 		cwd=$$(lsof -a -p $$p -d cwd -Fn 2>/dev/null | grep '^n' | cut -c2-); 		[ "$$cwd" = "$(CURDIR)" ] && kill -9 $$p 2>/dev/null || true; 	done
	@# The binary air builds. Absolute path, so it's anchored to this checkout.
	@-pkill -9 -f "^$(CURDIR)/tmp/server" 2>/dev/null || true
	@-lsof -ti :$(BACKEND_PORT) 2>/dev/null | xargs kill -9 2>/dev/null || true
	@-lsof -ti :$(FRONTEND_PORT) 2>/dev/null | xargs kill -9 2>/dev/null || true
	@echo "Killed this checkout's dev processes (ports $(BACKEND_PORT)/$(FRONTEND_PORT)) ✓"

fresh: ## Kill everything, start backend + frontend from scratch
	@echo "==> Stopping old processes..."
	@$(MAKE) -s kill >/dev/null
	@sleep 1
	@# Assert the ports are actually FREE before starting. Without this, a
	@# survivor keeps the port, the new server dies with "bind: address already
	@# in use" AFTER printing all its config lines, and the health check below is
	@# satisfied by the old process — so this target reports success while you
	@# are talking to a stale binary with stale env. Once the
	@# ports are known free, anything answering the health check is ours.
	@for p in $(BACKEND_PORT) $(FRONTEND_PORT); do \
		if lsof -ti :$$p >/dev/null 2>&1; then \
			echo ""; \
			echo "\033[31m✗ port $$p is still in use after 'make kill'.\033[0m"; \
			echo "  Starting now would leave you talking to the OLD server while the new"; \
			echo "  one dies on bind — with its config lines already in the log."; \
			echo "  Who has it:  lsof -i :$$p -sTCP:LISTEN"; \
			echo "  Then:        ps -o pid,lstart,command -p <pid>"; \
			exit 1; \
		fi; \
	done
	@echo "==> Starting docker services..."
	@docker compose up -d 2>/dev/null || true
	@echo "==> Starting backend + frontend..."
	@trap 'kill 0' EXIT; \
	DEV_AUTO_LOGIN=true $(AIR) & \
	cd web && npm run dev & \
	sleep 3; \
	echo "==> Waiting for backend..."; \
	waited=0; \
	until curl -sf http://localhost:$(BACKEND_PORT)/api/auth/config > /dev/null 2>&1; do \
		waited=$$((waited + 1)); \
		if [ $$waited -gt 120 ]; then \
			echo ""; \
			echo "\033[31m✗ backend did not come up within 60s.\033[0m"; \
			echo "  Check the air output above for a compile error, or a"; \
			echo "  'bind: address already in use'."; \
			exit 1; \
		fi; \
		sleep 0.5; \
	done; \
	echo "==> Backend ready ✓"; \
	echo "==> Frontend: http://localhost:$(FRONTEND_PORT)"; \
	echo "==> Fresh start complete ✓"; \
	wait

# --- Help ---

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-18s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
