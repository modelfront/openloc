#!/usr/bin/env bash
# ============================================================================
# journeys-parallel.sh — run the journey suite as N sharded, ISOLATED stacks,
# instead of one ~6.5-minute serial run.
#
#   scripts/journeys-parallel.sh [N] [--on-dev]     # N shards, default 3
#
# Why isolation and not `workers: N`:
#   Journey files clean up their own data, but several of them (j4, j15, j18,
#   j22, j25, j26 — 71 `apiCount` assertions) snapshot GLOBAL counts and assert
#   they haven't moved. Two files running concurrently against ONE database
#   break that. Giving every shard its own stack + database and keeping
#   `workers: 1` inside each avoids it, which is the shape this script builds.
#
# What each shard gets vs. shares (see scripts/lib/stack.sh):
#   isolated  postgres, redis, seaweedfs, backend, vite   (state — must not mix)
#   shared    libretranslate, languagetool, parser    (stateless request/reply)
#
# Sharing the three heavy sidecars is what keeps this affordable: N shards cost
# N × (postgres + redis + seaweedfs), not N × the whole compose file. They're
# taken from the dev instance described by instance.conf, whose compose
# services must be up (`make dev`); its backend and Vite need not be running.
#
# Every shard gets a fresh stack on its own port block (instance 91+), torn
# down on exit — including on Ctrl-C. The developer's database and ports are
# never touched. `--on-dev` is the exception: shard 1 then runs on the dev
# instance itself (reset + reseeded, backend + Vite must be running), which
# saves one stack at the price of the dev data.
#
# Before the first stack boots, the run waits for one gate slot per stack
# (scripts/lib/stack-slots.sh), or for every slot when it has more shards than
# there are slots.
# ============================================================================
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

SHARDS=3
on_dev=0
for arg in "$@"; do
    case "$arg" in
        --on-dev) on_dev=1 ;;
        -h|--help) awk 'NR==1{next} !/^#/{exit} {sub(/^# ?/,""); print}' "$0"; exit 0 ;;
        ''|*[!0-9]*) echo "error: unknown argument '$arg' (usage: journeys-parallel.sh [N] [--on-dev])" >&2; exit 2 ;;
        *) SHARDS="$arg" ;;
    esac
done
[ "$SHARDS" -ge 1 ] || { echo "error: shard count must be >= 1" >&2; exit 2; }

readonly LOGDIR="e2e/check-results/shards"

# The dev instance supplies everyone's sidecars (and shard 1's stack under
# --on-dev). An optional, git-ignored instance.conf (KEY=value lines) moves it
# off the default instance-1 ports.
set -a
[ -f instance.conf ] && . ./instance.conf
set +a
FRONTEND_PORT="${FRONTEND_PORT:-9000}"
BACKEND_PORT="${BACKEND_PORT:-9001}"

# shellcheck source=scripts/lib/stack.sh
. scripts/lib/stack.sh

mkdir -p "$LOGDIR"

cleanup() {
    local code=$?
    trap - EXIT INT TERM
    stack_down_all
    exit "$code"
}
trap cleanup EXIT INT TERM

stack_require_sidecars || exit 1

echo "▸ Building server + seed binaries once, shared by every shard..."
stack_build_bins || exit 1

# Shard 1 under --on-dev is the dev instance, not a throwaway stack.
stack_slots_take $((SHARDS - on_dev)) || exit 1

declare -a SHARD_URLS=()
first=1
if [ "$on_dev" -eq 1 ]; then
    # --- shard 1: the dev instance, reset like test-all.sh --on-dev does ----
    echo "▸ Shard 1 uses the dev instance on :${FRONTEND_PORT} (reset + reseed)"
    # The spell check specs need a project the demo seed does not create. A
    # reset that fails must stop the run, not leave shard 1 on last run's data.
    { make reset && make seed-spellcheck; } > "$LOGDIR/shard1.reset.log" 2>&1 \
        || { echo "error: resetting the dev instance failed (see $LOGDIR/shard1.reset.log)" >&2; exit 1; }
    # The reset ends the dev backend's DB sessions. Wait until a query runs
    # again: its static routes answer long before that.
    stack_wait_http "http://localhost:${BACKEND_PORT}/health/complete" \
        || { echo "error: the dev backend on :${BACKEND_PORT} did not get a working DB connection back" >&2; exit 1; }
    SHARD_URLS+=("${FRONTEND_PORT} ${BACKEND_PORT}")
    first=2
fi

# Not `seq "$first" "$SHARDS"`: with one shard under --on-dev that is `seq 2 1`,
# which BSD seq (macOS) counts down, booting two stacks nobody asked for.
for ((i = first; i <= SHARDS; i++)); do
    stack_up "$i" "openloc-jshard${i}" "$LOGDIR/shard${i}" \
        || { echo "error: shard $i failed to boot (see $LOGDIR/shard${i}.*)" >&2; exit 1; }
    SHARD_URLS+=("${STACK_FE} ${STACK_BE}")
done

# --- run every shard concurrently -----------------------------------------
echo "▸ Running ${SHARDS} shards of the journey suite..."
declare -a RUN_PIDS=()
for i in $(seq 1 "$SHARDS"); do
    read -r fe be <<< "${SHARD_URLS[$((i - 1))]}"
    (
        cd e2e && PW_SHARD="${i}/${SHARDS}" \
            APP_URL="http://localhost:${fe}" API_BASE="http://localhost:${be}" \
            npx playwright test --config=playwright.journeys.config.ts \
            > "../${LOGDIR}/shard${i}.journeys.log" 2>&1
    ) &
    RUN_PIDS+=($!)
done

fail=0
for i in "${!RUN_PIDS[@]}"; do
    if wait "${RUN_PIDS[$i]}"; then
        printf '  ✓ shard %d\n' "$((i + 1))"
    else
        printf '  ✗ shard %d  (log: %s/shard%d.journeys.log)\n' "$((i + 1))" "$LOGDIR" "$((i + 1))"
        fail=1
    fi
done

if [ "$fail" -eq 0 ]; then
    echo "RESULT: PASS (${SHARDS} shards)"
else
    echo "RESULT: FAIL — see per-shard logs in ${LOGDIR}/"
fi
exit "$fail"
