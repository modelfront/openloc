#!/usr/bin/env bash
# ============================================================================
# scripts/lib/stack-slots.sh — the queue for throwaway stacks, shared by
# every checkout and worktree of one user.
#
# Sourced by scripts/lib/stack.sh, which supplies STACK_LOCK_DIR,
# stack_lock_dir_ready and stack_reclaim_dead.
#
# stack_up's memory check runs once, at boot: gates that start together all
# pass it and then fight over Docker memory and host CPU. So at most
# STACK_MAX_CONCURRENT stacks run at once for the user, across every checkout
# and worktree, and a run that finds no free slot waits its turn.
#
# A slot is <lock dir>/slot-<k>, a symlink (`ln -s` is atomic) whose target is
# the owner's id: pid@start time, so a pid the system has handed to another
# process does not keep a dead run's slot. A waiting run holds a ticket,
# queue/<epoch>-<pid>. Only the oldest live ticket may take slots, and it
# takes all it needs at once or none, so no run holds a slot while it waits
# for another and nobody is overtaken. A slot or ticket whose owner is gone is
# freed by the next run that looks; for a slot, so are the stacks that owner
# left (stack_reclaim_dead).
#
# A run takes its slots once, before its first stack, and gives them back in
# stack_down_all. Under $CI there is no queue: a CI node runs one stack.
# ============================================================================

# Two: a stack needs little Docker memory (~250 MB) but runs a backend, a
# Vite and Playwright workers on the host; three at once, beside a dev
# instance, overload the host CPU and E2E times out.
STACK_MAX_CONCURRENT="${STACK_MAX_CONCURRENT:-2}"
# Give up after this many seconds of waiting; 0 waits as long as it takes.
STACK_SLOT_WAIT="${STACK_SLOT_WAIT:-0}"
# How often a waiting run looks at the queue (tests look faster).
STACK_SLOT_POLL_SEC="${STACK_SLOT_POLL_SEC:-2}"
declare -a STACK_SLOTS=()
STACK_TICKET=""
STACK_OWNER=""

# stack_proc_start <pid> — the process's start time as one word; empty when
# there is no such process. Fixed locale and time zone: every run on the
# machine must print the same text for the same process.
stack_proc_start() {
  local t
  t=$(TZ=UTC0 LC_ALL=C ps -o lstart= -p "$1" 2>/dev/null) || return 0
  # Unquoted on purpose: collapses ps's padding into single separators.
  # shellcheck disable=SC2086
  set -- $t
  local IFS=_
  echo "$*"
}

# stack_owner_alive <pid@start> — whether that process still runs. An id
# without a positive pid or a start time names none.
stack_owner_alive() {
  local pid=${1%%@*} start=${1#*@}
  case "$pid" in ''|0*|*[!0-9]*) return 1 ;; esac
  [ -n "$start" ] && [ "$start" = "$(stack_proc_start "$pid")" ]
}

# "<in use> of <max> in use (pids …)" plus how many runs wait ahead, if any.
stack_slot_status() {
  local ahead=${1:-0} s owner n=0 pids=""
  for s in "$STACK_LOCK_DIR"/slot-*; do
    [ -L "$s" ] || continue
    owner=$(readlink "$s")
    stack_owner_alive "$owner" || continue
    n=$((n + 1))
    pids="$pids ${owner%%@*}"
  done
  printf '%d of %d in use' "$n" "$STACK_MAX_CONCURRENT"
  [ -z "$pids" ] || printf ' (pids%s)' "$pids"
  [ "$ahead" -eq 0 ] || printf ', %d run(s) ahead' "$ahead"
}

# Live tickets older than ours. Tickets whose owner is gone are removed.
stack_slot_ahead() {
  local t n=0
  for t in "$STACK_LOCK_DIR"/queue/*; do
    [ "$t" = "$STACK_TICKET" ] && break
    [ -L "$t" ] || continue
    if stack_owner_alive "$(readlink "$t")"; then n=$((n + 1)); else rm -f "$t"; fi
  done
  echo "$n"
}

# stack_slot_reap <slot> <owner> — frees a slot whose owner is gone and
# tears down the stacks that owner left.
stack_slot_reap() {
  local slot=$1 pid=${2%%@*} lock
  rm -f "$slot"
  printf '    %s freed: its run (pid %s) is gone\n' "${slot##*/}" "$pid"
  # A running pid now belongs to another process; its instance locks are not
  # this dead run's to judge.
  ps -p "$pid" > /dev/null 2>&1 && return 0
  for lock in "$STACK_LOCK_DIR"/[0-9]*; do
    [ -L "$lock" ] && [ "$(readlink "$lock")" = "$pid" ] && stack_reclaim_dead "${lock##*/}" "$pid"
  done
  return 0
}

# stack_slot_try <slot> — takes the slot if it is free or its owner is gone.
stack_slot_try() {
  local slot=$1 owner
  ln -s "$STACK_OWNER" "$slot" 2>/dev/null && return 0
  owner=$(readlink "$slot" 2>/dev/null)
  stack_owner_alive "$owner" && return 1
  [ -z "$owner" ] || stack_slot_reap "$slot" "$owner"
  ln -s "$STACK_OWNER" "$slot" 2>/dev/null
}

# stack_slot_grab <n> — takes n slots at once, or none. It touches no slot
# unless enough look free; one taken by a run in between makes it give back
# what it took.
stack_slot_grab() {
  local want=$1 k free=0
  local -a got=()
  for ((k = 1; k <= STACK_MAX_CONCURRENT; k++)); do
    stack_owner_alive "$(readlink "${STACK_LOCK_DIR}/slot-${k}" 2>/dev/null)" || free=$((free + 1))
  done
  [ "$free" -ge "$want" ] || return 1
  for ((k = 1; k <= STACK_MAX_CONCURRENT && ${#got[@]} < want; k++)); do
    stack_slot_try "${STACK_LOCK_DIR}/slot-${k}" && got+=("${STACK_LOCK_DIR}/slot-${k}")
  done
  if [ "${#got[@]}" -lt "$want" ]; then
    [ "${#got[@]}" -eq 0 ] || rm -f "${got[@]}"
    return 1
  fi
  STACK_SLOTS+=("${got[@]}")
}

# stack_slots_take [n] — blocks until this run holds n slots (default 1). A
# run that needs more stacks than STACK_MAX_CONCURRENT takes every slot and
# runs alone, so it can never wait on itself. No-op when the run already
# holds slots, and under $CI.
stack_slots_take() {
  local want=${1:-1} ahead start=$SECONDS waited last=-1
  [ -z "${CI:-}" ] && [ "$want" -gt 0 ] && [ "${#STACK_SLOTS[@]}" -eq 0 ] || return 0
  case "$STACK_MAX_CONCURRENT" in ''|*[!0-9]*|0)
    echo "✗ STACK_MAX_CONCURRENT must be a positive whole number (got '$STACK_MAX_CONCURRENT')" >&2; return 1 ;;
  esac
  case "$STACK_SLOT_WAIT" in ''|*[!0-9]*)
    echo "✗ STACK_SLOT_WAIT must be a whole number of seconds (got '$STACK_SLOT_WAIT')" >&2; return 1 ;;
  esac
  [ "$want" -le "$STACK_MAX_CONCURRENT" ] || want=$STACK_MAX_CONCURRENT
  stack_lock_dir_ready && mkdir -p "$STACK_LOCK_DIR/queue" || return 1
  STACK_OWNER="$$@$(stack_proc_start $$)"
  if [ "$STACK_OWNER" = "$$@" ]; then
    echo "✗ cannot read this run's start time with ps, so it cannot queue for a gate slot" >&2
    return 1
  fi
  STACK_TICKET="$STACK_LOCK_DIR/queue/$(printf '%010d-%07d' "$(date +%s)" "$$")"
  ln -sfn "$STACK_OWNER" "$STACK_TICKET" || return 1
  while :; do
    ahead=$(stack_slot_ahead)
    if [ "$ahead" -eq 0 ] && stack_slot_grab "$want"; then
      rm -f "$STACK_TICKET"
      STACK_TICKET=""
      [ "$last" -lt 0 ] || printf '  ✓ got a gate slot after %ds\n' "$((SECONDS - start))"
      return 0
    fi
    waited=$((SECONDS - start))
    if [ "$STACK_SLOT_WAIT" -gt 0 ] && [ "$waited" -ge "$STACK_SLOT_WAIT" ]; then
      rm -f "$STACK_TICKET"
      STACK_TICKET=""
      echo "✗ no gate slot after ${STACK_SLOT_WAIT}s (STACK_SLOT_WAIT): $(stack_slot_status "$ahead")." >&2
      echo "  Wait for the other gates to finish, raise STACK_MAX_CONCURRENT, or run with --on-dev." >&2
      return 1
    fi
    if [ "$last" -lt 0 ] || [ $((waited - last)) -ge 60 ]; then
      printf '  ⏸ waiting for a gate slot: %s\n' "$(stack_slot_status "$ahead")"
      last=$waited
    fi
    sleep "$STACK_SLOT_POLL_SEC"
  done
}

# Gives back this run's slots, and its ticket when it is still waiting. Only
# links that still name this run are removed.
stack_slots_release() {
  local slot
  for slot in "${STACK_SLOTS[@]:-}"; do
    [ -n "$slot" ] && [ "$(readlink "$slot" 2>/dev/null)" = "$STACK_OWNER" ] && rm -f "$slot"
  done
  [ -z "$STACK_TICKET" ] || rm -f "$STACK_TICKET"
  STACK_SLOTS=()
  STACK_TICKET=""
}
