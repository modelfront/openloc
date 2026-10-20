# Developing OpenLoc

How to run OpenLoc from source on your own machine. For running it in production, see [deploy/README.md](../deploy/README.md); for the project's rules and how to open a pull request, see [CONTRIBUTING.md](../CONTRIBUTING.md).

## Prerequisites

- Go 1.26+
- Node.js 24+
- Docker & Docker Compose

## Development setup

```bash
# Start dependencies (Postgres, Redis, SeaweedFS, LibreTranslate, LanguageTool, parser sidecar)
make dev

# Run database migrations
make migrate-up

# Seed demo data
make seed

# Start backend + frontend with live reload
make watch
```

The frontend runs on `http://localhost:9000` and the API on `http://localhost:9001`.

## Login

`make fresh` starts the stack with `DEV_AUTO_LOGIN=true`: click "Dev Login" on the login page to sign in as the seed owner, `owner@seed.example.com` (owner of "Example Org"). `make seed` also creates a linguist, `vendor@seed.example.com`. Otherwise sign in with a password or Google/Microsoft sign-in.

## Parser sidecar (DOCX/PDF)

The parser sidecar handles DOCX and PDF file formats via a Python FastAPI service. It starts automatically with `make dev` (Docker Compose), with `sidecars/parser` mounted so code changes apply without a rebuild.

```bash
make sidecar        # Start only the sidecar container
make sidecar-test   # Run its Python tests inside the container
```

## Useful commands

```bash
make test          # Run all Go tests
scripts/test-all.sh --skip-e2e   # Go + TypeScript typecheck + lint guards + frontend unit tests + script tests
scripts/test-all.sh              # Everything, including E2E and journey tests
make build         # Build server binary
make check         # Pre-flight: build + test + typecheck
make sqlc          # Regenerate sqlc code after SQL changes
make types         # Regenerate TypeScript types from Go structs
make e2e           # Run Playwright E2E tests
make seed          # Seed demo data
make reset         # Nuclear: drop DB + migrate + seed
make help          # Show all available targets
```

## Environment variables

The defaults are the ports of the development stack (`make dev`); `make` sets
the same values, and the self-hosting stack sets its own.

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | `postgres://openloc:openloc@localhost:9002/openloc?sslmode=disable` | PostgreSQL connection. Connect directly, not through a pooler in transaction mode: each server process keeps one `LISTEN` connection so editors see segment holds change at once and the notification bell updates live (behind such a pooler the bell refreshes only on its 60-second poll) |
| `REDIS_ADDR` | `localhost:9003` | Redis connection |
| `S3_ENDPOINT` | `http://localhost:9004` | S3-compatible storage |
| `S3_ACCESS_KEY` | `changeme-s3-access-key` | S3 access key |
| `S3_SECRET_KEY` | `changeme-s3-secret-key` | S3 secret key |
| `S3_BUCKET` | `openloc` | S3 bucket name |
| `JWT_SECRET` | `dev-secret-change-me` (development only) | JWT signing secret. Outside `ENV=development` the server refuses to start with the default, a `CHANGE_ME` value, or one shorter than 32 characters (`openssl rand -hex 32`) |
| `AI_SECRETS_KEY` | `dev-ai-secrets-key-change-me` (development only) | Encrypts stored AI/MT provider keys. Outside `ENV=development` the server refuses to start without it. Replace before storing a real key — a value that does not match what the keys were sealed with leaves every provider engine on its built-in language list |
| `PARSER_SIDECAR_URL` | `http://localhost:9007` | Python parser sidecar URL (DOCX/PDF) |
| `GOOGLE_CLIENT_ID` | — | Google OAuth client ID |
| `GOOGLE_CLIENT_SECRET` | — | Google OAuth client secret |
| `BASE_URL` | `http://localhost:9000` | Base URL for OAuth callbacks |
| `DEV_AUTO_LOGIN` | `false` | Enable one-click dev login (dev only) |
| `DEMO_ORG` | `true` unless `ENV=production` | Keep the shared, read-only demo organization: built or refreshed in the background at server start, refreshed daily (independent of `RUN_SCHEDULER`), and built by `make seed` |
