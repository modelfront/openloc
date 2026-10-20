#!/usr/bin/env bash
# ============================================================================
# scripts/lib/stack.sh — throwaway app stacks for the Playwright suites.
#
# Sourced (not run) by scripts/test-all.sh and scripts/journeys-parallel.sh
# from the repo root, after instance.conf. A stack is
# everything that holds state: postgres, redis, seaweedfs (one Docker Compose
# project on its own port block), plus a backend and a Vite dev server started
# from the current source. The stateless sidecars — libretranslate,
# languagetool, the parser — are shared from the dev instance, so a stack
# costs three small containers and two processes, not the whole compose file.
#
# Stacks live on dev-instance numbers 90+ (port block 9000 + (N-1)*20, i.e.
# 10780 and up), far above any dev-ready checkout. stack_up takes the first
# FREE instance at or above 90+idx: one another stack_up holds the lock of, or
# with anything answering on one of its six ports, is skipped, never
# reclaimed. Two sessions (or two worktrees) running stacks at once therefore
# get separate blocks and never touch each other's processes or data. The
# Compose project name carries the instance number for the same reason.
#
#   stack_require_sidecars        fail legibly when the dev sidecars are down
#   stack_build_bins [cmd...]     go build server + seeders (+ cmd...) into bin/
#   stack_slots_take [n]          wait for n gate slots (stack_up takes one
#                                 itself when this shell holds none)
#   stack_up <idx> <proj> <log> [seeder...]
#                                 boot one stack; sets STACK_FE / STACK_BE /
#                                 STACK_INSTANCE / STACK_DBURL
#   stack_down_all                tear down every stack this shell booted and
#                                 give back its gate slots
#
# stack_up is deliberately NOT called inside `$( … )`: the backend and Vite it
# leaves running inherit the substitution's stdout pipe, and the caller then
# waits forever for it to close.
# ============================================================================

readonly STACK_INSTANCE_BASE=90

# Docker's VM has a fixed memory budget (Docker Desktop: a few GB). A sidecar
# the kernel kills for memory mid-run fails a suite in confusing ways, so a
# stack boots only while at least this much of the budget is free.
STACK_MIN_FREE_MB="${STACK_MIN_FREE_MB:-1024}"
# How far past 90+idx stack_up looks for a free block before giving up.
readonly STACK_INSTANCE_SPAN=20
# Shared by every checkout and worktree of one user, and private to them:
# what is in it decides which processes a gate kills and which stacks it tears
# down. Only tests point it somewhere else.
STACK_LOCK_DIR="${STACK_LOCK_DIR:-/tmp/openloc-stack-locks-$(id -u)}"
# Where checkouts on an older stack.sh keep their instance locks. A live one
# there marks the block busy too; nothing there is reclaimed or killed. Only
# tests point it somewhere else.
STACK_LEGACY_LOCK_DIR="${STACK_LEGACY_LOCK_DIR:-/tmp/openloc-stack-locks}"
STACK_DEFAULT_SEEDERS=(seed seed-spellcheck)

# Sidecars shared from the dev instance. instance.conf (sourced by the caller)
# moves them off the instance-1 defaults.
STACK_LIBRETRANSLATE_URL="${LIBRETRANSLATE_URL:-http://localhost:9006}"
STACK_PARSER_URL="${PARSER_SIDECAR_URL:-http://localhost:9007}"
STACK_SPELLCHECK_URL="${SPELLCHECK_LT_URL:-http://localhost:9008/v2/check}"

STACK_GOOSE="$(command -v goose 2>/dev/null || echo "$(go env GOPATH)/bin/goose")"

# Everything a stack starts, so stack_down_all can take it all down — on
# success, on failure and on Ctrl-C alike.
declare -a STACK_BG_PIDS=()
declare -a STACK_PROJECTS=()
declare -a STACK_HELD_PORTS=()
declare -a STACK_LOCKS=()

# The queue stack_up waits in for a gate slot, shared by the user's checkouts.
# shellcheck source=scripts/lib/stack-slots.sh
. "$(dirname "${BASH_SOURCE[0]}")/stack-slots.sh"

stack_port_base() { echo $((9000 + ($1 - 1) * 20)); }

# Free memory in Docker's VM: its total minus what every running container
# uses now, in MiB. Empty when Docker does not report it.
stack_docker_free_mib() {
  local total used
  total=$(docker info --format '{{.MemTotal}}' 2>/dev/null)
  [ -n "$total" ] && [ "$total" -gt 0 ] 2>/dev/null || return 0
  used=$(docker stats --no-stream --format '{{.MemUsage}}' 2>/dev/null | awk '
    { v = $1; u = v; sub(/^[0-9.]+/, "", u); sub(/[A-Za-z]+$/, "", v)
      if (u == "GiB") v *= 1024; else if (u == "KiB") v /= 1024; else if (u == "B") v /= 1048576
      s += v }
    END { printf "%d", s }')
  echo $(( total / 1048576 - ${used:-0} ))
}

stack_require_memory() {
  local free
  free=$(stack_docker_free_mib)
  [ -n "$free" ] || return 0
  if [ "$free" -lt "$STACK_MIN_FREE_MB" ]; then
    echo "✗ Docker has ${free} MB free; a throwaway stack needs ${STACK_MIN_FREE_MB} MB headroom (STACK_MIN_FREE_MB)." >&2
    echo "  Stop an idle dev instance (docker compose -p <project> stop), give Docker more memory, or run the gate with --on-dev." >&2
    return 1
  fi
}

# Probe the three shared sidecars over HTTP. They come from `make dev` (the
# dev instance's compose project) and are the only thing a gate run needs from
# the developer's environment.
stack_require_sidecars() {
  local lt_languages="${STACK_SPELLCHECK_URL%/check}/languages"
  local probe name url
  for probe in \
      "libretranslate|${STACK_LIBRETRANSLATE_URL}/languages" \
      "parser sidecar|${STACK_PARSER_URL}/health" \
      "languagetool|${lt_languages}"; do
    name="${probe%%|*}"; url="${probe#*|}"
    stack_wait_http "$url" 20 || {
      echo "✗ $name is not answering at $url — the dev sidecars must be up: make dev" >&2
      return 1
    }
  done
}

# stack_wait_http <url> [tries] — polls every 0.5 s until <url> answers 2xx;
# fails after <tries> attempts (default 60). Each request is capped at 2 s, so
# a server that accepts the connection and never answers cannot hang the run.
stack_wait_http() {
  local url=$1 tries=${2:-60}
  for _ in $(seq 1 "$tries"); do
    curl -sf --max-time 2 "$url" > /dev/null 2>&1 && return 0
    sleep 0.5
  done
  return 1
}

# Builds the server and the seeders every stack runs, plus any extra commands
# named (cmd/<name> → bin/<name>) for a caller's own seeders.
stack_build_bins() {
  local cmd
  go build -o bin/server ./cmd/server || return 1
  for cmd in "${STACK_DEFAULT_SEEDERS[@]}" "$@"; do
    go build -o "bin/${cmd}" "./cmd/${cmd}" || return 1
  done
}

# True when something accepts a connection on the port. Probes BOTH families:
# the Go backend binds 0.0.0.0 (v4 reachable), but Vite binds "localhost",
# which on macOS resolves to ::1 first — a v4-only probe misses it.
stack_port_answers() {
  local port=$1
  if (exec 3<>"/dev/tcp/127.0.0.1/${port}") 2>/dev/null; then return 0; fi
  if (exec 3<>"/dev/tcp/::1/${port}") 2>/dev/null; then return 0; fi
  return 1
}

# Only for the app processes started here. Docker services use compose's own
# healthchecks (`up --wait`): a forwarded port accepts a TCP connection the
# moment the container starts, long before Postgres will answer a query.
stack_wait_for_port() {
  local port=$1 label=$2 tries=${3:-120}
  for _ in $(seq 1 "$tries"); do
    stack_port_answers "$port" && return 0
    sleep 0.5
  done
  echo "error: $label never came up on :$port" >&2
  return 1
}

# stack_reclaim_dead <instance> <dead pid> — tears down what a killed run
# left on its block: the Compose project and the processes it recorded next
# to its lock (<lock>.project, <lock>.pids, one pid@start time per line). A
# recorded process is killed only while it is still that process (same pid
# and start time), never whatever holds the port or has the pid since.
# Nothing happens unless the lock still names <dead pid>: another run may
# have reclaimed and taken the block since the caller looked.
stack_reclaim_dead() {
  local lock="${STACK_LOCK_DIR}/$1" dead=$2 id proj
  [ -n "$dead" ] && [ "$(readlink "$lock" 2>/dev/null)" = "$dead" ] || return 0
  if [ -f "${lock}.pids" ]; then
    while read -r id; do
      [ -n "$id" ] && stack_owner_alive "$id" && kill -9 "${id%%@*}" 2>/dev/null
    done < "${lock}.pids"
  fi
  if [ -f "${lock}.project" ]; then
    proj=$(cat "${lock}.project")
    printf '    instance %s: tearing down %s, left by a killed run\n' "$1" "$proj"
    docker compose -p "$proj" down -v --remove-orphans > /dev/null 2>&1
  fi
  # The same during the teardown: the lock and its files are that run's now.
  [ "$(readlink "$lock" 2>/dev/null)" = "$dead" ] || return 0
  rm -f "$lock" "${lock}.project" "${lock}.pids"
}

# stack_lock_dir_ready — creates the lock directory private to this user, and
# refuses one that is a symlink, another user's, or writable by others.
#
# One find decides, on one lstat that never follows a link: separate tests
# would each look up the path again, and a symlink swapped in between could
# pass them all. Once it passes, sticky /tmp keeps anyone else from replacing
# it.
stack_lock_dir_ready() {
  local d=$STACK_LOCK_DIR
  [ -e "$d" ] || [ -L "$d" ] || mkdir -m 0700 "$d" 2>/dev/null
  if [ -z "$(find "$d" -maxdepth 0 -type d -uid "$(id -u)" ! -perm -0020 ! -perm -0002 2>/dev/null)" ]; then
    echo "✗ $d is not a private directory of this user: remove it, or set STACK_LOCK_DIR" >&2
    return 1
  fi
}

# stack_claim_instance <first> — sets STACK_INSTANCE to the first free
# instance at or above <first> and holds its lock until stack_down_all.
#
# The lock is a symlink whose target is the owner's pid: `ln -s` is atomic, so
# two callers can never both create it. A lock whose owner is gone is
# reclaimed (stack_reclaim_dead) and the block tried again; the port probe
# then covers whatever the dead owner left running.
stack_claim_instance() {
  local first=$1 inst lock owner base port busy legacy
  stack_lock_dir_ready || return 1
  for ((inst = first; inst < first + STACK_INSTANCE_SPAN; inst++)); do
    lock="${STACK_LOCK_DIR}/${inst}"
    legacy=$(readlink "${STACK_LEGACY_LOCK_DIR}/${inst}" 2>/dev/null)
    if [ -n "$legacy" ] && ps -p "$legacy" > /dev/null 2>&1; then
      printf '    instance %d skipped: a gate on an older checkout holds it\n' "$inst"
      continue
    fi
    if ! ln -s "$$" "$lock" 2>/dev/null; then
      owner=$(readlink "$lock" 2>/dev/null)
      if [ -n "$owner" ] && ! ps -p "$owner" > /dev/null 2>&1; then
        # Its run was killed before teardown: take down what it left, then
        # try this block again.
        stack_reclaim_dead "$inst" "$owner"
        inst=$((inst - 1))
      fi
      continue
    fi
    base=$(stack_port_base "$inst")
    busy=""
    for ((port = base; port <= base + 5; port++)); do
      if stack_port_answers "$port"; then busy=$port; break; fi
    done
    if [ -n "$busy" ]; then
      printf '    instance %d skipped: :%d is in use\n' "$inst" "$busy"
      rm -f "$lock"
      continue
    fi
    STACK_LOCKS+=("$lock")
    STACK_INSTANCE=$inst
    return 0
  done
  echo "error: no free stack port block on instances ${first}-$((first + STACK_INSTANCE_SPAN - 1))" >&2
  return 1
}

# stack_up <idx> <project> <logprefix> [seeder...]
#   idx        0.. — the search for a free instance starts at
#              STACK_INSTANCE_BASE + idx
#   project    Compose project name prefix, e.g. openloc-gate-e2e; the
#              instance number is appended
#   logprefix  path prefix for <prefix>.docker.log / .backend.log / .vite.log
#   seeder     bin/<seeder> to run, in order, before the app starts. Default:
#              seed seed-spellcheck. Each gets the stack's DATABASE_URL and
#              the shared sidecar URLs.
# Sets STACK_FE, STACK_BE, STACK_INSTANCE and STACK_DBURL on success.
# Requires stack_build_bins (with every non-default seeder named) first.
stack_up() {
  local idx=$1 proj=$2 log=$3
  shift 3
  local -a seeders=("$@")
  [ "${#seeders[@]}" -gt 0 ] || seeders=("${STACK_DEFAULT_SEEDERS[@]}")
  mkdir -p "$(dirname "$log")"

  # The slot first: after a wait, only the memory left now counts.
  stack_slots_take 1 || return 1
  stack_require_memory || return 1
  stack_claim_instance $((STACK_INSTANCE_BASE + idx)) || return 1
  local inst=$STACK_INSTANCE
  local base; base=$(stack_port_base "$inst")
  local fe=$((base + 0)) be=$((base + 1)) pg=$((base + 2)) rd=$((base + 3)) s3=$((base + 4)) s3c=$((base + 5))
  proj="${proj}-${inst}"
  # What a later run needs to clean up if this one is killed before teardown.
  local meta="${STACK_LOCK_DIR}/${inst}"
  printf '%s\n' "$proj" > "${meta}.project"

  printf '  ↑ stack %s (ports %d-%d)\n' "$proj" "$base" "$((base + 5))"
  STACK_HELD_PORTS+=("$fe" "$be")
  # Record the project before `up`: a half-started stack still needs tearing
  # down, and `up` leaves containers behind when --wait times out.
  STACK_PROJECTS+=("$proj")
  # A crashed run with this name can leave stopped containers and volumes;
  # `up` would restart them with their old data. The lock makes the name ours.
  docker compose -p "$proj" down -v --remove-orphans > "${log}.docker.log" 2>&1
  COMPOSE_PROJECT_NAME="$proj" POSTGRES_PORT="$pg" REDIS_PORT="$rd" \
    S3_PORT="$s3" S3_UI_PORT="$s3c" \
    docker compose -p "$proj" up -d --wait postgres redis seaweedfs >> "${log}.docker.log" 2>&1 || return 1

  local dburl="postgres://openloc:openloc@localhost:${pg}/openloc?sslmode=disable"
  "$STACK_GOOSE" -dir internal/database/migrations postgres "$dburl" up -allow-missing >> "${log}.docker.log" 2>&1 || return 1

  # Seed + serve with the stack's own state, but the shared sidecars.
  #
  # LIBRETRANSLATE_URL matters at SEED time, not just at run time: the seeder
  # writes it into the mt_engines row, and an unset value leaves the row's url
  # empty so the pipeline falls back to localhost:5100 and every MT step 500s
  # with "connection refused". seed-spellcheck writes deterministic typo'd
  # content for the spell check specs from SPELLCHECK_LT_URL.
  local seeder
  for seeder in "${seeders[@]}"; do
    DATABASE_URL="$dburl" REDIS_ADDR="localhost:${rd}" S3_ENDPOINT="http://localhost:${s3}" \
      LIBRETRANSLATE_URL="$STACK_LIBRETRANSLATE_URL" PARSER_SIDECAR_URL="$STACK_PARSER_URL" \
      SPELLCHECK_LT_URL="$STACK_SPELLCHECK_URL" \
      "./bin/${seeder}" >> "${log}.docker.log" 2>&1 || { echo "error: ${seeder} failed (see ${log}.docker.log)" >&2; return 1; }
  done

  DATABASE_URL="$dburl" REDIS_ADDR="localhost:${rd}" S3_ENDPOINT="http://localhost:${s3}" \
    PORT="$be" BASE_URL="http://localhost:${fe}" DEV_AUTO_LOGIN=true \
    PLATFORM_ADMIN_EMAILS="${PLATFORM_ADMIN_EMAILS:-ops@operators.example.com}" \
    PARSER_SIDECAR_URL="$STACK_PARSER_URL" LIBRETRANSLATE_URL="$STACK_LIBRETRANSLATE_URL" \
    SPELLCHECK_LT_URL="$STACK_SPELLCHECK_URL" \
    ./bin/server < /dev/null > "${log}.backend.log" 2>&1 &
  STACK_BG_PIDS+=($!)
  disown $! 2>/dev/null

  local server_pid=$!
  (cd web && FRONTEND_PORT="$fe" BACKEND_PORT="$be" exec npx vite) < /dev/null > "${log}.vite.log" 2>&1 &
  STACK_BG_PIDS+=($!)
  disown $! 2>/dev/null
  stack_proc_ids "$server_pid" "$!" > "${meta}.pids"

  stack_wait_for_port "$be" "$proj backend" || return 1
  stack_wait_for_port "$fe" "$proj frontend" || return 1
  # `npx vite` serves from a child node process: record the listeners too.
  # shellcheck disable=SC2046 # one pid per word
  stack_proc_ids $(lsof -t -iTCP:"$fe" -iTCP:"$be" -sTCP:LISTEN 2>/dev/null) >> "${meta}.pids"

  # Warm Vite: the per-test timeout is 10s by design, and a cold Vite takes
  # longer than that to transform the app's module graph, so the first spec
  # would fail in `page.goto('/')` for reasons unrelated to the code.
  # index.html is served without touching the module graph; the entry module
  # is what forces the transform.
  curl -sf --max-time 120 "http://localhost:${fe}/src/main.tsx" > /dev/null 2>&1 || true

  STACK_FE="$fe"
  STACK_BE="$be"
  STACK_DBURL="$dburl"
}

# stack_proc_ids <pid>… — one pid@start time per line, what <lock>.pids holds.
stack_proc_ids() {
  local pid
  for pid in "$@"; do printf '%s@%s\n' "$pid" "$(stack_proc_start "$pid")"; done
}

# Tear down everything stack_up started. Idempotent: safe from an EXIT trap
# after an explicit call.
stack_down_all() {
  local pid port holder proj lock
  # Ctrl-C may also have ended whatever reads this output: a write to it must
  # not end the teardown before the stacks are down and the slots given back.
  # Not restored here: after Ctrl-C the output may still be dead, and the
  # shell's next write to it would end the run. A caller that goes on, with
  # its output still read, restores it (test-all.sh).
  trap '' PIPE
  for pid in "${STACK_BG_PIDS[@]:-}"; do
    [ -n "$pid" ] && kill "$pid" 2>/dev/null
  done
  # `npx vite` spawns node as a CHILD, so killing the npx pid can orphan the
  # server still holding the port — and the next run then dies with "Port N
  # is already in use". Sweep the ports we claimed; the instance lock means
  # nothing else was started on them.
  for port in "${STACK_HELD_PORTS[@]:-}"; do
    [ -n "$port" ] || continue
    holder=$(lsof -t -iTCP:"${port}" -sTCP:LISTEN 2>/dev/null)
    [ -n "$holder" ] && kill -9 $holder 2>/dev/null
  done
  for proj in "${STACK_PROJECTS[@]:-}"; do
    [ -n "$proj" ] || continue
    # External printf: when the write fails (the reader is gone), bash 3.2's
    # builtin keeps the text and leaks it into the next $(…) — the slot
    # check's readlink among them.
    env printf '  ↓ tearing down %s\n' "$proj" 2>/dev/null
    # -v: the whole point of a throwaway stack is that its data does not
    # outlive the run.
    docker compose -p "$proj" down -v --remove-orphans > /dev/null 2>&1
  done
  # Released last, so no other stack_up claims the block while it still holds
  # containers.
  for lock in "${STACK_LOCKS[@]:-}"; do
    [ -n "$lock" ] && rm -f "$lock" "${lock}.project" "${lock}.pids"
  done
  # The gate slots after that: the next run in the queue boots only once this
  # run's stacks are gone.
  stack_slots_release
  STACK_BG_PIDS=()
  STACK_HELD_PORTS=()
  STACK_PROJECTS=()
  STACK_LOCKS=()
}
