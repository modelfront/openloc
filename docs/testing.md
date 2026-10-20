# Testing Guide

All test types used in OpenLoc, what they cover, and how to run them.

---

## Test Types

| Type | Location | What it tests | Needs DB? | Needs browser? | How to run |
|------|----------|--------------|-----------|---------------|------------|
| **Go unit tests** | `internal/*/..._test.go` | Pure functions in isolation — no DB, no HTTP | No | No | `go test ./internal/qa/` |
| **Go integration tests** | `internal/**/integration_test.go` | Full HTTP handler round-trips — creates a test DB, spins up the real router, sends real HTTP requests | Yes (auto-created) | No | `go test ./internal/workflow/` |
| **Go flow tests** | `internal/flowtest/flow_test.go` | Complete user journeys across multiple API endpoints — register → create → use → verify. Same API paths a frontend would call, but no browser. | Yes (auto-created) | No | `go test ./internal/flowtest/` |
| **Frontend unit tests** | `web/src/lib/*.test.ts` | Pure TypeScript/React logic — utility functions, component rendering in jsdom (no real browser) | No | No | `cd web && npm test` |
| **E2E tests (Playwright)** | `e2e/tests/*.spec.ts` | Opens a real Chromium browser, navigates pages, clicks buttons, fills forms, verifies what's visible. **Full-stack**: real backend, real DB, real frontend. This is what we call "E2E". | Yes (dev DB) | Yes (Chromium) | `make e2e` |

### Naming convention
- **"E2E tests"** always means **Playwright** — real browser, real everything. Run with `make e2e`.
- **"Go flow tests"** are API-level journeys in `internal/flowtest/` — no browser, but test full API paths.

### Running journeys faster

`make journeys` runs all 32 spec files serially (~6.5 min) — one database, one
backend, `workers: 1`.

`make journeys-parallel` (optionally `SHARDS=N`, default 3) runs them as N
isolated stacks in parallel (~3.5 min). Each shard gets its own postgres/redis/seaweedfs/backend/vite;
libretranslate, languagetool and the parser sidecar are stateless and shared
from the dev instance, so the extra cost is modest. Per-shard logs land in
`e2e/check-results/shards/`. `ARGS=--on-dev` runs shard 1 on the dev instance
itself (reset + reseeded) to save one stack.

**Do not "speed this up" by raising `workers` in `playwright.journeys.config.ts`.**
Six journey files (j4, j15, j18, j22, j25, j26 — 71 `apiCount` assertions)
snapshot GLOBAL counts and assert they haven't moved, so two files sharing one
database fail immediately (`j15: expected 5 projects, received 6`). The
file-level independence the config documents is about cleanup, not isolation.

- **"Go integration tests"** are per-handler HTTP round-trips.
- New backend features come with Go flow tests and integration tests. UI features also get E2E (Playwright) tests.

---

## Running Tests

### Quick check (during development)
```bash
make check          # go build + go test -short + frontend build (tsc + vite)
cd web && npm test  # frontend unit tests
```

### Full test suite
```bash
# Backend: all Go tests (unit + integration + E2E flows)
go test -count=1 ./...

# Frontend: all Vitest tests
cd web && npm test

# Browser E2E: requires servers running
make watch    # terminal 1: starts Go server + Vite dev
make e2e      # terminal 2: runs Playwright against localhost
```

### Run specific test packages
```bash
# Only QA checker unit tests
go test -count=1 ./internal/qa/

# Only workflow integration tests
go test -count=1 ./internal/workflow/

# Only E2E flow tests
go test -count=1 ./internal/flowtest/

# Only frontend term-insert tests
cd web && npx vitest run src/lib/term-insert.test.ts

# Only Playwright glossary tests
cd e2e && npx playwright test tests/glossary.spec.ts
```

### Headed Playwright (see the browser)
```bash
make e2e-headed     # opens visible Chromium window
```

---

## Test Architecture

### Go integration tests
Each test function calls `testutil.SetupTestDB(t)` which:
1. Starts a PostgreSQL container with testcontainers the first time a test in the package needs it (Docker must be running), and runs all migrations on it
2. Truncates every table, so each test starts from an empty database
3. Returns a connection pool + queries + cleanup function
4. The cleanup function truncates again when the test finishes

Each test starts from empty tables, so no test sees another test's rows. The container is shared by the tests of one package, so those tests must not call `t.Parallel()`; packages run in parallel, each with its own container.

The test sends real HTTP requests through the real Chi router using `testutil.DoRequest()`, which injects auth headers (JWT with user/tenant IDs).

### Go flow tests (`internal/flowtest/flow_test.go`)
Same infrastructure as integration tests, but each test function walks a complete user story:
- `TestIntegrationFullTranslationFlow` — register → TM → template → project → upload → translate → approve → complete → verify TM
- `TestIntegrationGlossaryE2EFlow` — register → glossary → terms → assign → lookup → edit → live update → forbidden → multi-match
- `TestIntegrationQACheckFlow` — register → QA profile → rules → template → project → segments → run QA → verify report → fix → re-run
- `TestIntegrationWorkflowFlow` — register → template → workflow steps → job → advance → 403 hard lock → reject → reopen → complete

### Playwright browser tests
`make e2e` and `make journeys` connect to the **already running** dev servers (backend :9001, frontend :9000, or the ports in `instance.conf`) and use the dev database, so run `make fresh` (or `make watch`) and `make seed` first.

`scripts/test-all.sh` is different: locally it boots a throwaway stack per suite (postgres, redis, seaweedfs, backend and Vite built from the source on disk, on ports 10780 and up), seeds it, runs the suite there and tears it down. Only the dev compose services need to be up (`make dev`) — the sidecars are shared. `--on-dev` makes the gate use the dev instance instead, resetting and reseeding its database; that is the default in CI.

Throwaway stacks wait their turn. At most `STACK_MAX_CONCURRENT` (default 2) run at once for each user on the machine, counting every checkout and worktree. A gate that finds no free slot prints `waiting for a gate slot: 2 of 2 in use (pids …)` and starts when a slot frees, first come, first served. `make journeys-parallel` and `--parallel-journeys` take one slot per stack, or every slot when there are more shards than slots. A slot left by a killed run is freed by the next gate. Set `STACK_SLOT_WAIT=<seconds>` to give up instead of waiting. CI never waits; `--on-dev` waits only for the throwaway shards of a parallel journeys run. The queue has its own test, `scripts/lib/stack-slots.test.sh`, which the gate runs in its `scripts` suite.

**Existing Playwright test files:**
- `auth.spec.ts` — login flow, dev auto-login
- `cat-editor.spec.ts` — segment editing, save, keyboard shortcuts
- `engines.spec.ts` — MT engine list/create
- `glossary.spec.ts` — glossary CRUD, term editing, term panel
- `navigation.spec.ts` — page navigation, sidebar links
- `org-settings.spec.ts` — organization settings, members
- `project-analysis.spec.ts` — analysis bands, word counts
- `project-crud.spec.ts` — project create/delete
- `templates.spec.ts` — template CRUD, assignments
- `tm.spec.ts` — TM browser, import/export

---

## When to Write Which Test

| Scenario | Test type |
|----------|-----------|
| New pure function (checker, parser, utility) | Go unit test or frontend unit test |
| New API endpoint | Go integration test (handler round-trip) |
| New feature with multiple endpoints working together | Go E2E flow test |
| New UI page or interaction | Playwright browser test |
| New frontend logic (state, computed values) | Frontend unit test (Vitest) |
| Bug fix | Regression test at the level where the bug lived |

### The rule
Every feature needs a test that walks the full user journey. Write it together with the feature, not after a bug shows up.

For every new feature, write at minimum a Go E2E flow test (full API journey) and ideally a Playwright test (full browser journey).
