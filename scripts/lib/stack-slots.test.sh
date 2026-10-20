#!/usr/bin/env bash
# Tests for the gate-slot queue (scripts/lib/stack-slots.sh). Each "run" is a
# background shell that sources scripts/lib/stack.sh, takes its slots and
# holds them until the test lets it go. The lock directory is a temporary one,
# so the user's real gate slots are never touched. No Docker needed.
#
#   scripts/lib/stack-slots.test.sh
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
T="$(mktemp -d "${TMPDIR:-/tmp}/stack-slots-test.XXXXXX")"
T="$(cd "$T" && pwd -P)"
# Only the test's own shell cleans up: a child forked for `sleep … &` and
# killed before it becomes sleep still holds this trap.
cleanup() {
  # BASHPID is bash 4+; the sh's parent is the shell running this.
  [ "${BASHPID:-$(exec sh -c 'echo $PPID')}" = "$$" ] || return 0
  local f
  for f in "$T"/*.pid; do
    [ -e "$f" ] && kill -9 "$(cat "$f")" 2>/dev/null
  done
  rm -rf "$T"
}
trap cleanup EXIT

# CI turns the queue off; only the test that is about CI sets it.
unset CI
# One locale and time zone for every run; the test that checks they do not
# matter gives one run others.
export TZ=UTC0 LC_ALL=C
# STACK_SLOT_WAIT bounds a run that a broken queue would leave waiting.
export STACK_MAX_CONCURRENT=2 STACK_SLOT_WAIT=30 STACK_SLOT_POLL_SEC=0.05
export TEST_PID=$$
# stack_reclaim_dead runs `docker compose … down`: record it instead, and run
# $DOCKER_HOOK with the same arguments when a test has written one.
export DOCKER_CALLS="$T/docker-calls" DOCKER_HOOK="$T/docker-hook"
mkdir "$T/bin"
cat > "$T/bin/docker" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$DOCKER_CALLS"
[ ! -x "$DOCKER_HOOK" ] || "$DOCKER_HOOK" "$@"
SH
chmod +x "$T/bin/docker"
# ps that cannot read start times when PS_NO_LSTART is set, as on a system
# whose ps lacks lstart, and that answers PS_FAKE_START for pid PS_FAKE_PID,
# which a real ps refuses.
real_ps=$(command -v ps)
cat > "$T/bin/ps" <<SH
#!/usr/bin/env bash
if [ -n "\${PS_NO_LSTART:-}" ]; then
  case " \$* " in *" lstart= "*) exit 1 ;; esac
fi
if [ -n "\${PS_FAKE_PID:-}" ]; then
  case " \$* " in *" -p \$PS_FAKE_PID "*) echo "\$PS_FAKE_START"; exit 0 ;; esac
fi
exec "$real_ps" "\$@"
SH
chmod +x "$T/bin/ps"
export PATH="$T/bin:$PATH"

# A run takes <n> slots, or calls stack_up ("up"), or both, the way
# journeys-parallel.sh does ("<n>+up"). "project" takes a slot and records a
# Compose project, as a booted stack does. "again" takes a slot, gives it
# back with stack_down_all on <name>.release, and queues again on
# <name>.again, the way test-all.sh goes from e2e to journeys. A run says
# what it got, holds it until <name>.release appears, then tears down. The
# traps are the ones scripts/test-all.sh sets. stack_up's Docker memory check
# records that it ran and stops the boot.
cat > "$T/run.sh" <<'SH'
name=$1 want=$2
. "$STACK_SH"
trap 'stack_down_all; exit 130' INT TERM
stack_require_memory() { echo checked > "$RUNS/$name.memory"; return 1; }
hold() { while [ ! -e "$RUNS/$name.$1" ] && kill -0 "$TEST_PID" 2>/dev/null; do sleep 0.05; done; }
case "$want" in
  up) stack_up 0 openloc-test "$RUNS/$name" ;;
  *+up)
    stack_slots_take "${want%+up}" || exit 3
    stack_up 0 openloc-test "$RUNS/$name" ;;
  project)
    stack_slots_take 1 || exit 3
    STACK_PROJECTS+=("openloc-test-$name") ;;
  again)
    stack_slots_take 1 || exit 3
    echo got > "$RUNS/$name.state"
    hold release
    rm -f "$RUNS/$name.release"
    stack_down_all
    echo down > "$RUNS/$name.state"
    hold again
    stack_slots_take 1 || exit 3 ;;
  *) stack_slots_take "$want" || exit 3 ;;
esac
echo got > "$RUNS/$name.state"
hold release
stack_down_all
SH
export STACK_SH="$here/stack.sh"

# Gives sourced helpers (stack_proc_start) to the test itself.
# shellcheck source=scripts/lib/stack.sh
. "$STACK_SH"

failures=0
check() {
  local name="$1"; shift
  if "$@"; then echo "  ok    $name"; else echo "  FAIL  $name"; failures=$((failures + 1)); fi
}
# Polls for up to 5 s.
eventually() {
  local i
  for ((i = 0; i < 100; i++)); do "$@" && return 0; sleep 0.05; done
  return 1
}
# Holds for 0.5 s: the opposite of eventually, for "still waiting".
throughout() {
  local i
  for ((i = 0; i < 10; i++)); do "$@" || return 1; sleep 0.05; done
}

# fresh <test> — a new lock directory and run directory for one test.
fresh() {
  export STACK_LOCK_DIR="$T/$1/locks" RUNS="$T/$1"
  mkdir -p "$STACK_LOCK_DIR"
}
# start <name> [slots] [VAR=value…] — starts a run in the background. Job
# control (set -m) gives it its own process group, so SIGINT reaches its trap
# the way Ctrl-C would; without it bash starts background jobs with SIGINT
# ignored.
start() {
  local name=$1 want=${2:-1}
  shift $(($# < 2 ? $# : 2))
  set -m
  if [ $# -gt 0 ]; then
    env "$@" "$BASH" "$T/run.sh" "$name" "$want" > "$RUNS/$name.log" 2>&1 &
  else
    "$BASH" "$T/run.sh" "$name" "$want" > "$RUNS/$name.log" 2>&1 &
  fi
  echo $! > "$T/$name.pid"
  set +m
}
# start_unread <name> <slots> — like start, but the run's output goes to a
# reader that is already gone, as after Ctrl-C ends the tee test-all.sh pipes
# a suite through.
start_unread() {
  set -m
  "$BASH" "$T/run.sh" "$1" "$2" > >(exit 0) 2>&1 &
  echo $! > "$T/$1.pid"
  set +m
}
pid_of() { cat "$T/$1.pid"; }
state_is() { [ "$(cat "$RUNS/$1.state" 2>/dev/null)" = "$2" ]; }
got() { state_is "$1" got; }
not_got() { ! got "$1"; }
waiting() { grep -q "waiting for a gate slot" "$RUNS/$1.log"; }
never_waited() { ! waiting "$1"; }
says() { grep -qF -- "$2" "$RUNS/$1.log"; }
release() { touch "$RUNS/$1.release"; }
pid_gone() { ! kill -0 "$1" 2>/dev/null; }
exited() { pid_gone "$(pid_of "$1")"; }
# ended_with <name> <code> — waits up to 5 s for the run to exit and checks
# its exit code. A run still going is killed and fails the check.
ended_with() {
  local pid rc
  pid=$(pid_of "$1")
  rm -f "$T/$1.pid"
  if ! eventually pid_gone "$pid"; then
    kill -9 "$pid" 2>/dev/null
    wait "$pid" 2>/dev/null
    return 1
  fi
  wait "$pid"; rc=$?
  [ "$rc" -eq "$2" ]
}
# finish <name>… — lets runs go; each must exit cleanly.
finish() {
  local name
  for name in "$@"; do release "$name"; done
  for name in "$@"; do check "  $name finishes" ended_with "$name" 0; done
}
# fill_slots <a> <b> — starts two runs and waits until both hold a slot.
fill_slots() {
  start "$1"; start "$2"
  check "two runs hold the slots" eventually got "$1"
  check "  (both of them)" eventually got "$2"
}
never_torn_down() { ! grep -qF -- "compose -p $1 down" "$DOCKER_CALLS"; }
# names_holder <run> <holder> — the run's waiting line lists the holder's pid.
names_holder() { grep -qE "in use \(pids( [0-9]+)* $(pid_of "$2")[ )]" "$RUNS/$1.log"; }
slots_in_use() { find "$STACK_LOCK_DIR" -maxdepth 1 -name 'slot-*' -type l | wc -l | tr -d ' '; }
slots_are() { [ "$(slots_in_use)" -eq "$1" ]; }
tickets_are() { [ "$(find "$STACK_LOCK_DIR/queue" -type l 2>/dev/null | wc -l | tr -d ' ')" -eq "$1" ]; }
owns_a_slot() {
  local s
  for s in "$STACK_LOCK_DIR"/slot-*; do
    [ "$(readlink "$s" 2>/dev/null)" = "$(pid_of "$1")@$(stack_proc_start "$(pid_of "$1")")" ] && return 0
  done
  return 1
}

echo "▸ a third run waits for one of two slots"
fresh limit
fill_slots a b
# Another time zone or locale must not make the holders look gone.
start c 1 TZ=Asia/Tokyo LC_ALL=fr_FR.UTF-8
check "the third run says it is waiting" eventually waiting c
check "the message counts the slots in use" says c "2 of 2 in use (pids "
check "  and names both holders" names_holder c a
check "  (by pid)" names_holder c b
check "the third run holds no slot while it waits" throughout not_got c
finish a
check "the third run gets the slot the first gave back" eventually got c
check "it is the third run's slot now" owns_a_slot c
check "two slots in use, never three" slots_are 2
finish b c
check "every slot is free once the runs finish" slots_are 0
check "no ticket is left in the queue" tickets_are 0

echo "▸ stack_up checks Docker memory only once it holds a slot"
fresh order
fill_slots a b
start m up
check "stack_up waits for a slot" eventually waiting m
check "  before it checks Docker memory" throughout test ! -e "$RUNS/m.memory"
finish a
check "it checks memory once it has the slot" eventually test -e "$RUNS/m.memory"
finish b m
check "every slot is free" slots_are 0

echo "▸ the slot of a run that is gone is freed"
fresh dead
sleep 30 & gone=$!
gone_id="${gone}@$(stack_proc_start "$gone")"
kill "$gone"; wait "$gone" 2>/dev/null
ln -s "$gone_id" "$STACK_LOCK_DIR/slot-1"
# The gone run left a stack on instance 95.
ln -s "$gone" "$STACK_LOCK_DIR/95"
echo "openloc-gate-e2e-95" > "$STACK_LOCK_DIR/95.project"
# A live pid with another start time: the pid was handed to a new process.
ln -s "$$@Thu_Jan_1_00:00:00_1970" "$STACK_LOCK_DIR/slot-2"
# This live process's instance lock is not the gone run's to tear down.
ln -s "$$" "$STACK_LOCK_DIR/96"
# The gone run's instance 97, which another run reclaims and takes while
# this one is still tearing it down.
ln -s "$gone" "$STACK_LOCK_DIR/97"
echo "openloc-gate-e2e-97" > "$STACK_LOCK_DIR/97.project"
cat > "$DOCKER_HOOK" <<'SH'
#!/usr/bin/env bash
case "$*" in *"-p openloc-gate-e2e-97 down"*)
  rm -f "$STACK_LOCK_DIR/97"
  ln -s "$TEST_PID" "$STACK_LOCK_DIR/97"
  echo live > "$STACK_LOCK_DIR/97.project" ;;
esac
SH
chmod +x "$DOCKER_HOOK"
start d 2
check "a run takes both slots without waiting" eventually got d
check "  and never said it was waiting" never_waited d
check "the dead run's stack is taken down" grep -qxF "compose -p openloc-gate-e2e-95 down -v --remove-orphans" "$DOCKER_CALLS"
check "the dead run's instance lock is gone" test ! -e "$STACK_LOCK_DIR/95" -a ! -L "$STACK_LOCK_DIR/95"
check "a live pid's instance lock is left alone" test -L "$STACK_LOCK_DIR/96"
check "a block taken during the teardown keeps its new lock" test "$(readlink "$STACK_LOCK_DIR/97")" = "$$"
check "  and its new files" grep -qx live "$STACK_LOCK_DIR/97.project"
# A run judged instance 98's owner dead, but another run took the block
# before the reclaim started.
ln -s "$$" "$STACK_LOCK_DIR/98"
echo "openloc-gate-e2e-98" > "$STACK_LOCK_DIR/98.project"
stack_reclaim_dead 98 "$gone"
check "a reclaim leaves a block taken before it started alone" test "$(readlink "$STACK_LOCK_DIR/98")" = "$$"
check "  and its stack" never_torn_down openloc-gate-e2e-98
finish d
check "both slots are free after it finishes" slots_are 0
rm -f "$DOCKER_HOOK"
# A run killed while it waited left its ticket at the head of the queue.
mkdir -p "$STACK_LOCK_DIR/queue"
ln -s "$gone_id" "$STACK_LOCK_DIR/queue/0000000001-$(printf '%07d' "$gone")"
start e
check "a run behind a gone run's ticket takes a free slot" eventually got e
check "  without waiting" never_waited e
check "  and the gone run's ticket is removed" tickets_are 0
finish e

echo "▸ an interrupted run frees what it holds"
fresh interrupt
start a
check "a run takes a slot" eventually got a
kill -INT "$(pid_of a)"
check "Ctrl-C ends it with 130" ended_with a 130
check "its slot is free" slots_are 0
fill_slots b c
start w
check "a third run waits" eventually waiting w
check "  with a ticket in the queue" tickets_are 1
kill -INT "$(pid_of w)"
check "Ctrl-C while waiting ends it with 130" ended_with w 130
check "its ticket is gone" tickets_are 0
finish b c

echo "▸ a run gives its slot back only once its stacks are down"
fresh downfirst
cat > "$DOCKER_HOOK" <<'SH'
#!/usr/bin/env bash
case "$*" in *"-p openloc-test-"*" down"*)
  if [ -L "$STACK_LOCK_DIR/slot-1" ]; then echo held; else echo free; fi > "$RUNS/at-down" ;;
esac
SH
chmod +x "$DOCKER_HOOK"
start a project
check "a run with a stack holds a slot" eventually got a
finish a
check "its stack is taken down" grep -qxF "compose -p openloc-test-a down -v --remove-orphans" "$DOCKER_CALLS"
check "  while it still holds the slot" grep -qx held "$RUNS/at-down"
check "the slot is free after" slots_are 0
rm -f "$DOCKER_HOOK"

echo "▸ Ctrl-C tears down although the output's reader is gone"
fresh unread
start_unread u project
check "a run with a stack holds a slot" eventually got u
kill -INT "$(pid_of u)"
check "Ctrl-C ends it with 130" ended_with u 130
check "its stack is taken down" grep -qxF "compose -p openloc-test-u down -v --remove-orphans" "$DOCKER_CALLS"
check "its slot is free" slots_are 0

echo "▸ a run that gave its slot back queues again"
fresh again
start r again
check "a run takes a slot" eventually got r
start a
check "a second run takes the other" eventually got a
release r
check "the first gives its slot back" eventually state_is r down
start b
check "a third run takes the freed slot" eventually got b
touch "$RUNS/r.again"
check "the first, queueing again, waits" eventually waiting r
check "  and holds nothing while it waits" throughout state_is r down
check "  with both slots still the others'" slots_are 2
finish a
check "it gets the slot the second gave back" eventually got r
check "  as its own" owns_a_slot r
finish b r
check "every slot is free" slots_are 0

echo "▸ the lock directory must be this user's alone"
fresh private
chmod 0770 "$STACK_LOCK_DIR"
start a
check "a lock directory the group may write to is refused" ended_with a 3
check "  and the run says why" says a "is not a private directory of this user"
chmod 0707 "$STACK_LOCK_DIR"
start a2
check "one anybody may write to is refused" ended_with a2 3
chmod 0755 "$STACK_LOCK_DIR"
if [ "$(id -u)" -ne 0 ]; then
  start f 1 STACK_LOCK_DIR=/
  check "another user's directory is refused" ended_with f 3
  check "  as not this user's" says f "is not a private directory of this user"
else
  echo "  skip  another user's directory: the test runs as root, who owns /"
fi
mkdir "$T/private/real"
rm -rf "$STACK_LOCK_DIR"
ln -s "$T/private/real" "$STACK_LOCK_DIR"
start b
check "a symlinked lock directory is refused" ended_with b 3
rm -f "$STACK_LOCK_DIR"
export STACK_LOCK_DIR="$T/private/new"
start c
check "a missing one is created" eventually got c
check "  private to this user" test -z "$(find "$STACK_LOCK_DIR" -maxdepth 0 -perm -0004)"
finish c

echo "▸ a reclaim kills only the processes the dead run recorded"
fresh pids
sleep 30 & recorded=$!
sleep 30 & reused=$!
sleep 30 & gone=$!
gone_id="${gone}@$(stack_proc_start "$gone")"
kill "$gone"; wait "$gone" 2>/dev/null
ln -s "$gone_id" "$STACK_LOCK_DIR/95"
# The second pid stands for one the system has handed to a new process.
stack_proc_ids "$recorded" > "$STACK_LOCK_DIR/95.pids"
echo "${reused}@Thu_Jan_1_00:00:00_1970" >> "$STACK_LOCK_DIR/95.pids"
stack_reclaim_dead 95 "$gone_id"
check "the recorded process is killed" eventually pid_gone "$recorded"
check "the dead run's lock is gone" test ! -L "$STACK_LOCK_DIR/95"
kill -TERM "$reused" 2>/dev/null
wait "$reused"; reused_rc=$?
# 143 is this TERM; 137 would be a kill -9 from the reclaim.
check "a pid now another process's is left alone" test "$reused_rc" -eq 143
wait "$recorded" 2>/dev/null

echo "▸ an owner id names a process only with a pid and its start time"
not_alive() { ! stack_owner_alive "$1"; }
check "this test's own id names it" stack_owner_alive "$$@$(stack_proc_start $$)"
sleep 30 & startless=$!
kill "$startless"; wait "$startless" 2>/dev/null
# ps prints nothing for a gone pid, which an empty start time would match.
check "a pid without a start time names nothing" not_alive "${startless}@"
# Even when ps answers for -1: kill would read it as every process.
export PS_FAKE_PID=-1 PS_FAKE_START="Thu Jan  1 00:00:00 1970"
check "-1 names nothing, whatever ps says" not_alive "-1@Thu_Jan_1_00:00:00_1970"
unset PS_FAKE_PID PS_FAKE_START
check "a bare pid names nothing" not_alive "$$"
# ps reads "0<pid>" as <pid>; kill would not be so sure.
check "a pid written with a leading zero names nothing" not_alive "0$$@$(stack_proc_start $$)"

echo "▸ a run whose start time ps cannot read does not queue"
fresh nostart
start z 1 PS_NO_LSTART=1
check "it ends with a failure" ended_with z 3
check "  and says why" says z "cannot read this run's start time"
check "  holding no slot" slots_are 0

echo "▸ a live lock left by an older checkout keeps its block"
fresh legacy
export STACK_LEGACY_LOCK_DIR="$T/legacy/old-locks"
mkdir -p "$STACK_LEGACY_LOCK_DIR"
ln -s "$$" "$STACK_LEGACY_LOCK_DIR/90"
sleep 30 & oldgone=$!
kill "$oldgone"; wait "$oldgone" 2>/dev/null
ln -s "$oldgone" "$STACK_LEGACY_LOCK_DIR/91"
echo "openloc-gate-e2e-91" > "$STACK_LEGACY_LOCK_DIR/91.project"
: > "$DOCKER_CALLS"
claimed=$(
  stack_port_answers() { return 1; }
  STACK_LOCKS=()
  stack_claim_instance 90 > "$RUNS/claim.log"
  echo "$STACK_INSTANCE"
  rm -f "${STACK_LOCKS[@]}"
)
check "a block an older checkout's live gate holds is skipped" test "$claimed" = 91
check "  and the claim says why" grep -q "older checkout holds it" "$RUNS/claim.log"
check "an older checkout's dead lock is left where it is" test -L "$STACK_LEGACY_LOCK_DIR/91"
check "  and its stack is not touched" never_torn_down openloc-gate-e2e-91
unset STACK_LEGACY_LOCK_DIR

echo "▸ STACK_SLOT_WAIT gives up"
fresh giveup
fill_slots a b
start c 1 STACK_SLOT_WAIT=1
check "the third run gives up after a second" eventually exited c
check "  with a failure" ended_with c 3
check "  and says why" says c "no gate slot after 1s (STACK_SLOT_WAIT): 2 of 2 in use"
check "  and leaves no ticket" tickets_are 0
finish a b

echo "▸ a parallel run takes its slots at once and never waits on itself"
fresh parallel
start p 3+up
check "three shards with two slots: it takes both and goes" eventually got p
check "  never waiting on its own slots when stack_up boots a shard" never_waited p
check "  stack_up gets as far as the memory check" test -e "$RUNS/p.memory"
check "  both slots are its own" slots_are 2
finish p
start a
check "one run holds a slot" eventually got a
start q 2
check "a run that needs two waits" eventually waiting q
check "  and holds nothing while it waits" throughout slots_are 1
start s
check "a later single run queues behind it" eventually waiting s
check "  although one slot is free" throughout not_got s
check "  and the queue says why" says s "1 run(s) ahead"
finish a
check "the first in line gets both slots" eventually got q
check "  the later run still waits" throughout not_got s
finish q
check "then the later run gets its turn" eventually got s
finish s
check "every slot is free" slots_are 0

echo "▸ no queue under CI"
fresh ci
fill_slots a b
start c 1 CI=true
check "a CI run does not wait" eventually got c
check "  and takes no slot" slots_are 2
finish a b c

echo
if [ "$failures" -eq 0 ]; then
  echo "PASS"
else
  echo "FAIL ($failures)"
  exit 1
fi
