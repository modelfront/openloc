#!/usr/bin/env bash
# Boots the development stack from scratch and runs one Playwright suite: the
# CI's e2e and journeys jobs (`make ci-e2e`, `make ci-journeys`, in
# e2e.yml, run by hand). Needs Docker, Go and Node 24.
#
#   scripts/ci-playwright.sh e2e                 # the whole e2e suite
#   PW_SHARD=1/3 scripts/ci-playwright.sh journeys  # one shard of the journeys
#
# The run itself is `scripts/test-all.sh --only=<suite> --on-dev`: the stack
# booted here IS the fresh one, so the gate runs on it (database reset, reseed
# and frontend warm-up as in every other --on-dev run) instead of booting a
# second throwaway stack. Logs land in e2e/check-results/.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

suite="${1:-}"
case "$suite" in
  e2e|journeys) ;;
  *) echo "usage: scripts/ci-playwright.sh e2e|journeys" >&2; exit 2 ;;
esac

# The instance-1 ports that the Makefile, Docker Compose and Vite default to.
# The server binary runs outside make, so it gets them here.
export DATABASE_URL="postgres://openloc:openloc@localhost:9002/openloc?sslmode=disable"
export REDIS_ADDR=localhost:9003
export S3_ENDPOINT=http://localhost:9004
export LIBRETRANSLATE_URL=http://localhost:9006
export PARSER_SIDECAR_URL=http://localhost:9007
export PORT=9001 BACKEND_PORT=9001 FRONTEND_PORT=9000
# The seed's platform admin (internal/seedid OperatorEmail), as in the Makefile.
export PLATFORM_ADMIN_EMAILS=ops@operators.example.com

logs=e2e/check-results
mkdir -p "$logs"
pids=()
stop() { for p in ${pids[@]+"${pids[@]}"}; do kill "$p" 2>/dev/null || true; done; }
trap stop EXIT

# wait_for <url> <name> [seconds, default 120]
wait_for() {
  local url="$1" name="$2" secs="${3:-120}" i
  for i in $(seq 1 "$secs"); do
    curl -sf "$url" > /dev/null 2>&1 && return 0
    sleep 1
  done
  echo "ci-playwright: $name did not answer at $url within ${secs}s (see $logs/)" >&2
  return 1
}

# Something already answering on these ports would be tested instead of the
# server built below, and the run resets its database.
for port in "$FRONTEND_PORT" "$BACKEND_PORT"; do
  if curl -s -o /dev/null --max-time 2 "http://localhost:$port/"; then
    echo "ci-playwright: port $port is already in use; stop the dev server first (make kill)" >&2
    exit 1
  fi
done

# The machine size decides the Playwright worker count (playwright.config.ts),
# and is the first thing to check when tests time out.
echo "==> Machine: $(nproc 2>/dev/null || sysctl -n hw.ncpu) CPUs, $(free -g 2>/dev/null | awk '/^Mem:/ {print $2 " GB memory"}')"
echo "==> Starting Postgres, Redis, SeaweedFS, LibreTranslate and the parser sidecar"
# LibreTranslate downloads its language models on a fresh runner, which can
# outlast its healthcheck, so it is waited for over HTTP after the builds
# below. The others are ready when their healthchecks pass.
docker compose up -d libretranslate
if ! docker compose up -d --wait postgres redis seaweedfs parser-sidecar; then
  # "is unhealthy" alone says nothing; the container's own output does.
  echo "ci-playwright: a service did not become healthy" >&2
  docker compose ps -a >&2
  for svc in postgres redis seaweedfs parser-sidecar; do
    echo "--- docker compose logs $svc (last 100 lines)" >&2
    docker compose logs --no-color --tail=100 "$svc" >&2 || true
  done
  exit 1
fi

echo "==> Migrating"
export PATH="$(go env GOPATH)/bin:$PATH"
if ! command -v goose > /dev/null; then
  go install "github.com/pressly/goose/v3/cmd/goose@$(go list -m -f '{{.Version}}' github.com/pressly/goose/v3)"
fi
make migrate-up

echo "==> Building the server and installing the frontend and Playwright"
go build -o bin/server ./cmd/server
(cd web && npm ci --no-audit --no-fund)
(cd e2e && npm ci --no-audit --no-fund && npx playwright install --with-deps chromium)

wait_for "$LIBRETRANSLATE_URL/languages" "LibreTranslate" 900

echo "==> Starting the server and Vite"
DEV_AUTO_LOGIN=true ./bin/server > "$logs/server.log" 2>&1 &
pids+=("$!")
(cd web && exec npm run dev > "../$logs/vite.log" 2>&1) &
pids+=("$!")
wait_for "http://localhost:$BACKEND_PORT/api/auth/config" "the server"
wait_for "http://localhost:$FRONTEND_PORT/" "Vite"

scripts/test-all.sh --only="$suite" --on-dev
