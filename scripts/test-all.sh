#!/usr/bin/env bash
#
# scripts/test-all.sh — pre-merge verification gate.
#
# Single command that answers "is the app green?" with structured output that
# tools can parse without guessing.
#
# ── Suites run, in order ───────────────────────────────────────────────────
#   1. go-build      — `go build ./...`                  (typecheck + link)
#   2. codegen       — `make ci-types check-sqlc`        (the committed tygo
#                                                         and sqlc output match
#                                                         the Go structs and
#                                                         the queries)
#   3. go-test       — `go test -count=1 ./...`          (unit + integration)
#   4. tsc           — `cd web && npx tsc -b`            (TS typecheck — vitest
#                                                         uses esbuild and DOES
#                                                         NOT typecheck, so
#                                                         without this the
#                                                         FE type-error class
#                                                         slips through)
#   5. guards        — `scripts/lint-guards.sh`          (full eslint, zero
#                                                         problems; web/src imports
#                                                         production deps only)
#   6. vitest        — `cd web && npm test`              (FE unit + component)
#   7. scripts       — every `scripts/*.test.sh` and     (the queue the
#                      `scripts/lib/*.test.sh`            throwaway stacks
#                                                         wait in, and the
#                                                         other script tests;
#                                                         no Docker; also under
#                                                         /bin/bash when it is
#                                                         another bash, as
#                                                         macOS's 3.2)
#   8. e2e           — `make e2e`                        (Playwright page-level)
#   9. journeys      — `make journeys`                   (Playwright multi-step)
#
# Every suite runs to completion regardless of prior failures so a single
# early break doesn't hide downstream regressions. Exit code is 0 ONLY when
# every suite passed.
#
# ── Output discipline (for unambiguous parsing) ────────────────────────────
# Three independent signals, any of which is sufficient:
#
#   1. Exit code: 0 = all pass; non-zero = at least one suite failed.
#   2. Last line of stdout, exactly one of:
#        RESULT: PASS
#        RESULT: FAIL (<n>): <suite1>, <suite2>, ...
#      Parse with `tail -1` — guaranteed to be the final line, no trailing
#      blank, no decorations. Nothing after this line.
#   3. JSON summary at e2e/check-results/summary.json:
#        { "result": "PASS" | "FAIL", "failed_suites": [...],
#          "suites": [{"name", "passed", "exit_code", "duration_sec",
#                      "log_path"}], "node_version", "go_version",
#          "tree", "started_at", "finished_at" }
#      `tree` is the git tree the run tested (empty when the working tree
#      differs from the index).
#      Per-suite full stdout+stderr captured to
#      e2e/check-results/<suite>.log for post-mortem inspection.
#
# The `guards` suite runs eslint over web/ and fails on any error or warning,
# listing first the `no-restricted-syntax` rules, which encode architectural
# invariants (draft flush, post-save sync, Chip vs hand-rolled pill). It also
# fails when web/src imports a package that is not a production dependency,
# because only those reach the image's third-party notices. See
# scripts/lint-guards.sh.
#
# ── Where the Playwright suites run ────────────────────────────────────────
# Locally, e2e and journeys each get a throwaway stack (scripts/lib/stack.sh):
# postgres + redis + seaweedfs on their own port block (instance 90+), plus a
# backend and a Vite built from the current source, seeded fresh and torn down
# after the suite. The developer's database and ports are never touched, so
# the gate can run while you keep working, and it tests the code on disk
# rather than whatever server happens to be running. The stateless sidecars
# (libretranslate, languagetool, parser) are shared from the dev instance.
#
# Under CI ($CI set) — and with --on-dev — the suites run on the dev instance
# instead: its DB is dropped + recreated + seeded before EACH of e2e and
# journeys, and its backend + Vite must already be running. CI boots exactly
# one fresh stack per node, so a second one would be waste.
#
# Throwaway stacks wait their turn: at most STACK_MAX_CONCURRENT (default 2)
# run at once for the user, across every checkout and worktree, and a gate
# that finds none free waits for one, saying so now and then. STACK_SLOT_WAIT
# (seconds) makes it give up instead. $CI runs never wait; --on-dev waits
# only for the throwaway shards of --parallel-journeys.
#
# Go uses ephemeral testcontainers; FE uses jsdom; neither touches any DB.
#
# ── Toolchain requirements ─────────────────────────────────────────────────
#   - Node 24  (older Node hits a require(ESM) bug with jsdom 29)
#   - Go 1.26 or newer
#   - Docker compose stack up for THIS dev instance (`make dev`): the sidecars
#     are shared with the throwaway stacks. About 1 GB of Docker memory free,
#     or the stack refuses to boot (STACK_MIN_FREE_MB in scripts/lib/stack.sh).
#   - --on-dev only: backend + Vite for THIS dev instance running (ports from
#     instance.conf; instance-1 :9001 / :9000 by default), i.e. `make fresh`.
#
# ── Flags ──────────────────────────────────────────────────────────────────
#   --skip-e2e          skip e2e + journeys (Go + codegen + tsc + guards +
#                       vitest + scripts only)
#   --skip-journeys     skip journeys only
#   --only=<suite>      run a single suite. Valid: go-build, codegen,
#                       go-test, tsc, guards, vitest, scripts, e2e, journeys
#   --on-dev            run e2e + journeys on the dev instance (reset + reseed
#                       its DB) instead of throwaway stacks. The default when
#                       $CI is set.
#   --isolated          the opposite: throwaway stacks even under CI.
#   --parallel-journeys[=N]
#                       run journeys as N isolated sharded stacks (default 2)
#                       instead of one serial pass — ~6:30 → ~3:30 locally.
#                       Each shard boots its own postgres, redis, seaweedfs,
#                       backend and vite, so N=3 costs three stateful stacks
#                       (two with --on-dev, where shard 1 is the dev instance);
#                       on a small Docker VM that can cost more than it saves.
#   --fail-fast         abort after the first failing suite. Default is to
#                       run every suite to completion so you see the whole
#                       state of the codebase; use --fail-fast when iterating
#                       on a known break and you don't want to wait for the
#                       remaining 5+ min of downstream suites you'd re-run
#                       after the fix anyway. Summary JSON + final RESULT
#                       line are still written before exiting.
#   --help              print this header
#
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# Dev-instance config (ports + compose project) so this gate targets THIS
# checkout's stack. Falls back to base (instance-1) ports if instance.conf is
# absent. `make e2e`/`make journeys` re-export these to Playwright too.
set -a
[ -f instance.conf ] && . ./instance.conf
set +a

# Throwaway stacks for the Playwright suites (stack_up / stack_down_all).
# shellcheck source=scripts/lib/stack.sh
. scripts/lib/stack.sh

# ── Toolchain PATH ─────────────────────────────────────────────────────────
#
# Node comes from whatever is on PATH; require_node() below checks the version
# actually resolved, so an old Node fails legibly instead of as a wall of
# ERR_REQUIRE_ESM errors (jsdom's dependency tree is ESM-only and needs
# `require(esm)`). Never hardcode an install path.
# $NODE_BIN (the resolved Node, e.g. the nvm-selected one) must come FIRST so a
# stale Homebrew node in /opt/homebrew/bin can't shadow it.
NODE_BIN="$(dirname "$(command -v node 2>/dev/null || true)")"
export PATH="$NODE_BIN:/opt/homebrew/bin:$HOME/go/bin:$PATH"

# Minimum Node for the frontend suites. Keep in sync with web/package.json
# `engines` (the declaration npm reads) and the `node-version` the CI workflows
# install. jsdom 29 needs ^20.19 || ^22.12 || >=24; we require 24+
# to match CI rather than allowing a Node that only CI-differs.
NODE_MIN_MAJOR=24
NODE_MIN_MINOR=0

# require_node aborts with an actionable message when the resolved Node is too
# old for the suites about to run. Called only when a Node suite is selected, so
# `--only=go-test` still works on a machine with an old Node.
require_node() {
  local raw major minor
  raw="$(node --version 2>/dev/null || true)"
  if [ -z "$raw" ]; then
    echo "✗ node not found on PATH — the tsc/vitest/e2e/journeys suites need Node ${NODE_MIN_MAJOR}.${NODE_MIN_MINOR}+" >&2
    echo "  Install it:  nvm install 24 && nvm use 24" >&2
    exit 1
  fi
  major="${raw#v}"; major="${major%%.*}"
  minor="${raw#v*.}"; minor="${minor%%.*}"
  if [ "$major" -lt "$NODE_MIN_MAJOR" ] ||
     { [ "$major" -eq "$NODE_MIN_MAJOR" ] && [ "$minor" -lt "$NODE_MIN_MINOR" ]; }; then
    echo "✗ Node $raw is too old for the frontend suites (need >= ${NODE_MIN_MAJOR}.${NODE_MIN_MINOR})." >&2
    echo "  jsdom 29's dependencies are ESM-only; on older Node every vitest file" >&2
    echo "  fails with ERR_REQUIRE_ESM rather than a real assertion." >&2
    echo "  Fix:  nvm use 24        (see web/package.json \"engines\")" >&2
    exit 1
  fi
}

# ── CLI flags ──────────────────────────────────────────────────────────────
run_go_build=1 run_codegen=1 run_go_test=1 run_tsc=1 run_guards=1 run_vitest=1 run_scripts=1 run_e2e=1 run_journeys=1
fail_fast=0
# Where e2e + journeys run: 1 = throwaway stacks (local default), 0 = the dev
# instance (CI default; its one booted stack is the whole point of the job).
# --on-dev / --isolated override either way.
isolated=1
[ -n "${CI:-}" ] && isolated=0
# 0 = one serial pass (the default). N>=1 = hand the suite to
# scripts/journeys-parallel.sh as N sharded stacks. See --parallel-journeys.
journeys_shards=0
# --only flags are ADDITIVE: `--only=e2e --only=journeys` runs both suites.
# When any --only is present it wins over the defaults (and
# over --skip-* flags).
only_selected=0
sel_go_build=0 sel_codegen=0 sel_go_test=0 sel_tsc=0 sel_guards=0 sel_vitest=0 sel_scripts=0 sel_e2e=0 sel_journeys=0
for arg in "$@"; do
  case "$arg" in
    --skip-e2e)        run_e2e=0; run_journeys=0 ;;
    --skip-journeys)   run_journeys=0 ;;
    --only=go-build)   only_selected=1; sel_go_build=1 ;;
    --only=codegen)    only_selected=1; sel_codegen=1 ;;
    --only=go-test)    only_selected=1; sel_go_test=1 ;;
    --only=tsc)        only_selected=1; sel_tsc=1 ;;
    --only=guards)     only_selected=1; sel_guards=1 ;;
    --only=vitest)     only_selected=1; sel_vitest=1 ;;
    --only=scripts)    only_selected=1; sel_scripts=1 ;;
    --only=e2e)        only_selected=1; sel_e2e=1 ;;
    --only=journeys)   only_selected=1; sel_journeys=1 ;;
    --fail-fast)       fail_fast=1 ;;
    --on-dev)          isolated=0 ;;
    --isolated)        isolated=1 ;;
    --parallel-journeys)   journeys_shards=3 ;;
    --parallel-journeys=*) journeys_shards="${arg#*=}"
                       case "$journeys_shards" in
                         ''|*[!0-9]*) echo "--parallel-journeys=N needs a positive integer (got '$journeys_shards')" >&2; exit 2 ;;
                       esac
                       [ "$journeys_shards" -ge 1 ] || { echo "--parallel-journeys=N needs N >= 1" >&2; exit 2; } ;;
    # Header block only — stop at the first non-comment line. `grep '^#'` over
    # the whole file drags every internal implementation note into --help.
    -h|--help)         awk 'NR==1{next} !/^#/{exit} {sub(/^# ?/,""); print}' "$0"; exit 0 ;;
    *)                 echo "unknown flag: $arg (try --help)" >&2; exit 2 ;;
  esac
done
if [ "$only_selected" -eq 1 ]; then
  run_go_build=$sel_go_build; run_codegen=$sel_codegen; run_go_test=$sel_go_test; run_tsc=$sel_tsc
  run_guards=$sel_guards; run_vitest=$sel_vitest; run_scripts=$sel_scripts; run_e2e=$sel_e2e
  run_journeys=$sel_journeys
fi

# ── Output paths ───────────────────────────────────────────────────────────
# NOT under e2e/test-results: that's Playwright's outputDir, which Playwright
# DELETES at the start of every run. Keep this dir outside any tool's
# clean-on-start path.
OUT_DIR="$ROOT/e2e/check-results"
mkdir -p "$OUT_DIR"
# Remove any prior summary so a half-finished run can't be mistaken for the
# current one.
SUMMARY="$OUT_DIR/summary.json"
rm -f "$SUMMARY"
: > "$SUMMARY.tmp"

# ── Suite registry ─────────────────────────────────────────────────────────
declare -a SUITE_RESULTS=()  # one JSON object per suite (joined at end)
declare -a FAILED=()

# Defined before run_suite, which calls it on --fail-fast.
write_summary_and_exit() {
  local aborted="${1:-0}"
  # A throwaway stack still up (--fail-fast exits from inside a suite) comes
  # down first: its teardown lines must not land after the RESULT line, which
  # is the last line by contract. The EXIT trap then finds nothing left.
  stack_down_all
  # Belt-and-braces: if some tool removed OUT_DIR mid-run, recreate it — a missing summary.json turns a clean
  # failure signal into a confusing bash error at the very last step.
  mkdir -p "$OUT_DIR"
  local finished_at
  finished_at="$(date -u +%FT%TZ)"
  local node_ver go_ver
  node_ver="$(node --version 2>/dev/null || echo unknown)"
  go_ver="$(go version 2>/dev/null | awk '{print $3}' || echo unknown)"

  # Guard the empty case: on macOS bash 3.2, `${arr[*]}` on an EMPTY array
  # under `set -u` errors "unbound variable" (fixed upstream in bash 4.4).
  local suites_array="[]"
  if [ "${#SUITE_RESULTS[@]}" -gt 0 ]; then
    suites_array="[$(IFS=,; echo "${SUITE_RESULTS[*]}")]"
  fi
  # Built with an explicit loop, not `IFS='","'`: bash IFS is a SET OF SINGLE
  # CHARACTERS, so that joins on `"` alone and emits ["e2e"journeys"] — invalid
  # JSON, and only ever with 2+ failures, i.e. exactly when the machine-readable
  # signal matters most.
  local failed_array="[]"
  if [ "${#FAILED[@]}" -gt 0 ]; then
    local joined=""
    for s in "${FAILED[@]}"; do
      [ -n "$joined" ] && joined="$joined,"
      joined="$joined\"$s\""
    done
    failed_array="[$joined]"
  fi
  local result
  if [ "${#FAILED[@]}" -eq 0 ]; then result="PASS"; else result="FAIL"; fi

  cat > "$SUMMARY" <<EOF
{
  "result": "$result",
  "aborted_early": $([ "$aborted" -eq 1 ] && echo true || echo false),
  "failed_suites": $failed_array,
  "suites": $suites_array,
  "node_version": "$node_ver",
  "go_version": "$go_ver",
  "tree": "$GATE_TREE",
  "started_at": "$STARTED_AT",
  "finished_at": "$finished_at"
}
EOF
  rm -f "$SUMMARY.tmp"

  printf "\n\033[1;36m═══ Summary ═══\033[0m  (JSON: %s)\n" "${SUMMARY#$ROOT/}"
  if [ "${#FAILED[@]}" -eq 0 ]; then
    printf "\033[1;32mAll %d suites passed.\033[0m\n" "${#SUITE_RESULTS[@]}"
    echo "RESULT: PASS"
    exit 0
  fi
  printf "\033[1;31m%d/%d suite(s) failed%s:\033[0m\n" \
    "${#FAILED[@]}" "${#SUITE_RESULTS[@]}" \
    "$([ "$aborted" -eq 1 ] && echo " (aborted early via --fail-fast)" || echo "")"
  for f in "${FAILED[@]}"; do
    printf "  - %s  (log: %s)\n" "$f" "e2e/check-results/$f.log"
  done
  echo "RESULT: FAIL (${#FAILED[@]}): $(IFS=,; echo "${FAILED[*]}")"
  exit 1
}

# Run a suite. Captures stdout+stderr to a log file. Records pass/fail.
# Args: name, command-string-eval'd
run_suite() {
  local name="$1" cmd="$2"
  local log="$OUT_DIR/$name.log"
  local start end rc dur
  printf "\n\033[1;36m═══ %s ═══\033[0m  (log: %s)\n" "$name" "${log#$ROOT/}"
  start="$(date +%s)"
  # `tee` so the user sees the output live AND we capture it for the JSON.
  # `set -o pipefail` is on, so the pipeline's exit reflects the command's.
  set +u  # bash $? inside eval can trip -u
  eval "$cmd" 2>&1 | tee "$log"
  rc="${PIPESTATUS[0]}"
  set -u
  end="$(date +%s)"
  dur=$((end - start))

  if [ "$rc" -eq 0 ]; then
    printf "\033[1;32m✓ %s (%ds)\033[0m\n" "$name" "$dur"
  else
    printf "\033[1;31m✗ %s (exit %d, %ds)\033[0m\n" "$name" "$rc" "$dur"
    FAILED+=("$name")
  fi

  # Build the per-suite JSON object. log_path is repo-relative so it's portable.
  local passed_json
  if [ "$rc" -eq 0 ]; then passed_json="true"; else passed_json="false"; fi
  SUITE_RESULTS+=("{\"name\":\"$name\",\"passed\":$passed_json,\"exit_code\":$rc,\"duration_sec\":$dur,\"log_path\":\"${log#$ROOT/}\"}")

  # --fail-fast: bail before the remaining suites. Summary JSON + final
  # RESULT line are still written so the harness sees a consistent shape;
  # `aborted_early: true` marks the JSON as partial.
  if [ "$rc" -ne 0 ] && [ "$fail_fast" -eq 1 ]; then
    printf "\n\033[1;33m⏹  --fail-fast: aborting before remaining suites\033[0m\n"
    write_summary_and_exit 1
  fi
}

# Reset the dev DB to a known clean baseline. Used before each of e2e and
# journeys. Waits for the backend to come back online before returning.
reset_db() {
  printf "  ↺ reset dev DB + reseed...\n"
  docker compose exec -T postgres psql -U openloc -d postgres -c \
    "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='openloc' AND pid != pg_backend_pid();" \
    > /dev/null 2>&1
  docker compose exec -T postgres psql -U openloc -d postgres -c "DROP DATABASE openloc;" > /dev/null 2>&1
  docker compose exec -T postgres psql -U openloc -d postgres -c "CREATE DATABASE openloc OWNER openloc;" > /dev/null 2>&1
  make migrate-up > /dev/null 2>&1
  make seed > /dev/null 2>&1
  # Spell check specs need deterministic typo'd content with tags, locked and
  # approved rows. It lives in its own project rather than demo.txt, which is
  # shared by every other spec.
  make seed-spellcheck > /dev/null 2>&1
  # Probe /health/complete, not a DB-free endpoint: dropping the database
  # leaves the running server's pgx pool holding handles to a database that no
  # longer exists, and it answers static routes happily while every query on a
  # stale connection fails. The first wave of specs then hits an app that looks
  # up but reads an empty world — jobs lists with no rows, API GETs that hang.
  # /health/complete checks connection + schema +
  # migration version through the same pool the handlers use, so a 200 here
  # means a query can actually run.
  local i=0
  until curl -sf "http://localhost:${BACKEND_PORT:-9001}/health/complete" > /dev/null 2>&1; do
    i=$((i+1))
    if [ "$i" -gt 60 ]; then
      echo "  ✗ backend did not recover a working DB connection within 30s" >&2
      return 1
    fi
    sleep 0.5
  done
  printf "  ✓ DB ready\n"
}

# Warm the Vite dev server before handing over to Playwright.
#
# The per-test timeout is 10s by design (fail-fast). A cold Vite takes longer
# than that to transform the app's module graph, so the first spec would fail
# in its `beforeEach` on `page.goto('/')` for reasons unrelated to the code.
# Requesting the entry module makes Vite do that work here, on the script's
# clock. (`server.warmup` in vite.config.ts covers server start; this also
# fails loudly if the frontend isn't up.)
warm_frontend() {
  local url="http://localhost:${FRONTEND_PORT:-9000}"
  local i=0
  until curl -sf "$url/" > /dev/null 2>&1; do
    i=$((i+1))
    if [ "$i" -gt 60 ]; then
      echo "  ✗ frontend not serving on $url within 30s — is the dev server running?" >&2
      return 1
    fi
    sleep 0.5
  done
  # index.html is served without touching the module graph; the entry module is
  # what forces the transform.
  curl -sf --max-time 120 "$url/src/main.tsx" > /dev/null 2>&1 || true
  printf "  ✓ frontend warm\n"
}

# Package parallelism for `go test`, capped locally only. Each package starts its
# own Postgres testcontainer; Go's GOMAXPROCS default sizes `-p` from the HOST's
# cores, oversubscribing Docker's smaller VM until the daemon wedges. On CI,
# Docker runs natively on the same cores, so Go's default is kept there. The
# local default matches the Makefile's.
if [ -n "${CI:-}" ]; then
  GO_TEST_P="${GO_TEST_P:-}"
else
  GO_TEST_P="${GO_TEST_P:-4}"
fi
go_test_p_flag=""
[ -n "$GO_TEST_P" ] && go_test_p_flag="-p $GO_TEST_P"

STARTED_AT="$(date -u +%FT%TZ)"
# The git tree this run tests: the index's tree when the working tree matches
# it (nothing unstaged, no untracked file), else empty. Lets a tool tell which
# content a PASS covers. The hash comes from a scratch copy of the index, so
# the real index is never written and another git command holding index.lock
# cannot blank it.
GATE_TREE=""
if git -C "$ROOT" diff --quiet 2>/dev/null \
    && [ -z "$(git -C "$ROOT" ls-files --others --exclude-standard 2>/dev/null)" ]; then
  gate_scratch="$(mktemp -d)"
  if (cd "$ROOT" && cp "$(git rev-parse --git-path index)" "$gate_scratch/index") 2>/dev/null; then
    GATE_TREE="$(GIT_INDEX_FILE="$gate_scratch/index" git -C "$ROOT" write-tree 2>/dev/null || true)"
  fi
  rm -rf "$gate_scratch"
  [ -n "$GATE_TREE" ] || echo "test-all.sh: warning: the index could not be hashed, so summary.json names no tree" >&2
fi

# ── Run suites ─────────────────────────────────────────────────────────────
[ "$run_go_build"  -eq 1 ] && run_suite "go-build" "go build ./..."
[ "$run_codegen"   -eq 1 ] && run_suite "codegen"  "make ci-types check-sqlc"
# If scripts/pre-go-test.sh exists and is executable, it runs first to build
# extra test inputs, and OPENLOC_PRIVATE_GATE=1 makes the tests that need those
# inputs fail instead of skip.
go_test_cmd="go test -count=1 $go_test_p_flag ./..."
if [ -x scripts/pre-go-test.sh ]; then
  go_test_cmd="scripts/pre-go-test.sh && OPENLOC_PRIVATE_GATE=1 $go_test_cmd"
fi
[ "$run_go_test"   -eq 1 ] && run_suite "go-test"  "$go_test_cmd"
# Fail fast on an unusable Node rather than reporting a wall of ERR_REQUIRE_ESM
# as if the app were broken.
if [ "$run_tsc" -eq 1 ] || [ "$run_vitest" -eq 1 ] || [ "$run_e2e" -eq 1 ] || [ "$run_journeys" -eq 1 ]; then
  require_node
fi

[ "$run_tsc"       -eq 1 ] && run_suite "tsc"      "cd web && npx tsc -b"
[ "$run_guards"    -eq 1 ] && run_suite "guards"   "scripts/lint-guards.sh"
[ "$run_vitest"    -eq 1 ] && run_suite "vitest"   "cd web && npm test"
# The script tests are the scripts/*.test.sh and scripts/lib/*.test.sh files,
# run with bash whatever their mode. The gate's scripts run under whichever
# bash a machine has: on macOS without a newer one that is /bin/bash 3.2,
# which behaves differently, so the tests run under it too.
script_tests=""
for t in scripts/*.test.sh scripts/lib/*.test.sh; do
  [ -f "$t" ] && script_tests="$script_tests $t"
done
scripts_cmd=""
for t in $script_tests; do scripts_cmd="${scripts_cmd:+$scripts_cmd && }\"\$BASH\" $t"; done
if [ -x /bin/bash ] && [ "$(/bin/bash -c 'echo "$BASH_VERSION"')" != "$BASH_VERSION" ]; then
  for t in $script_tests; do scripts_cmd="$scripts_cmd && /bin/bash $t"; done
fi
[ "$run_scripts"   -eq 1 ] && run_suite "scripts"  "$scripts_cmd"

# Throwaway stacks are torn down right after their suite; the traps cover
# Ctrl-C and --fail-fast, which exits from inside run_suite.
trap 'stack_down_all' EXIT
trap 'stack_down_all; exit 130' INT TERM

# Boot a throwaway stack for one Playwright suite and run the suite against
# it. Torn down before the next suite, so two stacks never coexist. A stack
# that fails to boot is recorded as a failed suite rather than a bash error,
# so the summary JSON and the RESULT line keep their shape.
run_isolated() {
  local name="$1" idx="$2" cmd="$3"
  local log="$OUT_DIR/stacks/$name"
  printf "\n  ⇢ %s runs on a throwaway stack (dev DB + ports untouched)\n" "$name"
  if stack_require_sidecars && stack_build_bins && stack_up "$idx" "openloc-gate-$name" "$log"; then
    run_suite "$name" "$cmd APP_URL=http://localhost:$STACK_FE API_BASE=http://localhost:$STACK_BE"
  else
    run_suite "$name" "echo '✗ throwaway stack for $name failed to boot (see ${log#$ROOT/}.*.log)'; false"
  fi
  stack_down_all
  # The gate goes on, with its output still read: the usual SIGPIPE again
  # (stack_down_all ignores it).
  trap - PIPE
}

if [ "$run_e2e" -eq 1 ]; then
  if [ "$isolated" -eq 1 ]; then
    run_isolated "e2e" 0 "make e2e"
  else
    reset_db || true
    warm_frontend || true
    run_suite "e2e" "make e2e"
  fi
fi
if [ "$run_journeys" -eq 1 ]; then
  if [ "$journeys_shards" -gt 0 ]; then
    # journeys-parallel.sh boots its own stacks — and with --on-dev resets +
    # reseeds shard 1 on the dev instance itself — so no reset_db here.
    if [ "$isolated" -eq 1 ]; then
      run_suite "journeys" "scripts/journeys-parallel.sh $journeys_shards"
    else
      warm_frontend || true
      run_suite "journeys" "scripts/journeys-parallel.sh $journeys_shards --on-dev"
    fi
  elif [ "$isolated" -eq 1 ]; then
    run_isolated "journeys" 1 "make journeys"
  else
    reset_db || true
    warm_frontend || true
    run_suite "journeys" "make journeys"
  fi
fi

write_summary_and_exit 0  # full run completed; emit summary + exit
