# Contributing to OpenLoc

Thanks for helping. This guide covers how to run the project, the rules the code
follows, and what a good pull request looks like.

## Getting started

You need Go 1.26+, Node.js 24+, Python 3 (the lint gate runs a Python script),
Docker with Docker Compose, and [sqlc](https://sqlc.dev) at the version named at
the top of `internal/database/sqlcgen/db.go` (the gate checks the generated code).

```bash
make dev          # Postgres, Redis, SeaweedFS, LibreTranslate, LanguageTool, parser sidecar
make migrate-up   # create the schema
make seed         # demo data
make fresh        # backend + frontend with live reload and the "Dev Login" button
```

Open http://localhost:9000 and click **Dev Login** to sign in as the seed owner,
`owner@seed.example.com`. The API runs on http://localhost:9001. `make help`
lists every target.

To run two checkouts side by side, put an `instance.conf` (git-ignored,
`KEY=value` lines) in the second one with its own ports, for example
`FRONTEND_PORT=9020`, `BACKEND_PORT=9021`, `POSTGRES_PORT=9022`, and so on, and
`COMPOSE_PROJECT_NAME=openloc-2`. The Makefile, Docker Compose, Vite and the
test scripts all read it.

## Tests

```bash
make test                         # Go tests
scripts/test-all.sh --skip-e2e    # Go + generated code + typecheck + eslint + frontend unit tests + script tests (~1 min)
scripts/test-all.sh               # everything, including Playwright E2E and journey tests
```

`scripts/test-all.sh` prints `RESULT: PASS` or `RESULT: FAIL (<n>): <suites>` on its
last line and writes per-suite logs to `e2e/check-results/`. E2E and journey
tests need the dev compose services running (`make dev`); the script boots a
throwaway stack per Playwright suite on its own ports — your dev database and
servers are never touched. At most two such stacks run at once for each user
(`STACK_MAX_CONCURRENT`; a parallel journeys run with more shards runs
alone), so a gate started while others run waits for a free slot and says
so. `--on-dev` runs them on the dev instance instead
(reset + reseed). See [docs/testing.md](docs/testing.md) for how the suites
are organized.

What we expect from tests:

- **A bug fix starts with a failing test.** Write the test, see it fail, fix the
  code, see it pass. If the test still passes with the fix reverted, it isn't
  testing the bug.
- **Test behaviour, not implementation.** Assert on API responses and database
  state, not on how many times a function was called.
- **No conditional logic in tests.** `if (visible) click()` hides failures;
  assert and fail fast instead.
- **E2E tests clean up** the projects, files and other data they create.
- **Wait for the API, not the clock.** Use `waitForResponse` for mutations,
  never fixed timeouts.
- Security boundaries, cross-tenant checks and state changes always get a test.

## Rules the code follows

### Backend

- **Tenant isolation.** Every query that touches user data is scoped by
  `tenant_id`, directly or through a tenant-scoped table. HTTP handlers use the
  tenant-scoped queries (`GetProject(id, tenantID)`, `GetFileTenant`,
  `GetJobTenant`); never trust an id from the client without checking that it
  belongs to the caller's organization. Background job payloads carry and check
  the tenant id too.
- **Validate on the server.** Every constraint the UI enforces is enforced by
  the backend as well.
- **Multi-step writes run in a transaction**, and errors are returned, not
  swallowed: no `_ =` on a database or network call.
- **Handlers** parse ids with `httputil.ParseID`, map "not found" with
  `httputil.DBError`, and respond with the `httputil` helpers
  (`Data`, `Created`, `NoContent`, `BadRequest`, `ServerError`). Internal error
  text is logged, never sent to the client.
- **Access control.** Each route declares who may use it: destructive and
  configuration routes are limited to owners and admins, with a few documented
  exceptions for project managers. Look at how similar routes are gated and
  match them, and say in the PR which roles may use a new route.
- **Soft delete.** Deleting something a user created sets `deleted_at` (or a
  terminal status) and reads filter `WHERE deleted_at IS NULL`. Link and
  assignment rows, and token or invite revocation, may be hard-deleted.
- **Logging** uses `logging.Info/Warn/Error(ctx, …, action, attrs…)` with a
  stable `action` slug and the `logging.*` attribute helpers for ids. Never put
  customer text or credentials in an error-tracking notice. An error that wraps
  a vendor response body implements `logging.LogSafe`, so logs and notices
  carry its `LogMessage`, not the body.
- **Language codes** go through `langcode.Normalize()`, never `strings.ToLower()`.

### Database

- Create migrations with `make migrate-create NAME=<slug>` (timestamped file
  names) and include a working `-- +goose Down` section. Run `make migrate-up`.
- After changing a `.sql` query file, run `make sqlc`. After changing Go types
  that the frontend uses, run `make types`.
- New features come with seed data, so they can be tried right after `make seed`.

### Frontend

- Use the shared components before writing a new one. The component gallery at
  `/debug` (`web/src/pages/DebugPage.tsx`) shows what exists; extend a component
  with a new variant rather than adding a near-duplicate.
- Every colored label is a `Chip` (`@/components/ui/chip`).
- Pages wrap their content in `PageContainer`; sibling pages use `SubNav` tabs.
- Mutations invalidate every query they can make stale.
- Wrap `localStorage` access in `try/catch`. Never render raw HTML without
  DOMPurify. Pages show an error state, never a blank screen.
- The CAT editor's behaviour is described in the help pages
  ([guides/cat-editor.md](internal/productdocs/content/guides/cat-editor.md),
  [reference/segment-statuses.md](internal/productdocs/content/reference/segment-statuses.md)).
  Read them before changing segment statuses or save behaviour.

### Code style

- **Don't repeat yourself.** On the third copy of the same code, extract it.
- **Keep files under about 500 lines**; split them before they grow past that.
- **Comments describe what the code does now**, in present tense. The history
  of a change belongs in the commit message and the pull request, not in the
  code.
- Prefer the architecturally correct solution over the quickest one, and a
  well-maintained library over a hand-rolled version of something complex.

## Pull requests

Every pull request runs the same checks as `make ci-go ci-types ci-web ci-lint
ci-images`: Go build and tests, the generated TypeScript types (run `make types`
after changing a Go struct they mirror), frontend typecheck and tests, eslint
(no errors or warnings) and the production images. The browser suites, `make ci-e2e` (e2e) and
`make ci-journeys` (journey tests), take most of an hour; a maintainer runs them
when a change needs them, and you can run them locally. `make scan-secrets` (gitleaks) and
`make scan-vulns` (known vulnerabilities in dependencies; accepted ones are
listed in `osv-scanner.toml`) are there to run locally.

- Keep a pull request to one change, and explain what it does and why.
- Include tests for what you changed, and run `scripts/test-all.sh --skip-e2e`
  (or the full script for UI and pipeline changes) before opening it.
- Run `make sqlc` / `make types` if you changed queries or shared types, and
  commit the generated files.
- Run `make check-licenses` if you added or updated a dependency; see
  [Dependency licenses](#dependency-licenses).
- Update the docs in `docs/` when you change the behaviour they describe.

An accepted pull request is not merged on GitHub: a maintainer applies it in
the project's main development repository, it reaches `main` with the next
release, with you credited as co-author, and the pull request is closed with a
link to that release. Closed this way means shipped, not rejected.

## Dependency licenses

OpenLoc ships under the Business Source License 1.1, so every package it
bundles has to come under a permissive license: MIT (also MIT-0 and MIT-CMU),
Apache-2.0, BSD-2-Clause, BSD-3-Clause, ISC, 0BSD, CC0-1.0, OFL-1.1, PSF-2.0,
Python-2.0, Unlicense, Zlib or BlueOak-1.0.0, or under MPL-2.0, whose copyleft
stops at the files it covers. Licenses that reach the whole program (GPL,
LGPL, AGPL and the like) are out, and so is a package whose license cannot be
told from what it ships.

`make check-licenses` (Docker) builds both production images, reads the
`/app/THIRD_PARTY_NOTICES.md` each one writes during its build (the license
texts of every Go module, production npm package and Python package it ships),
and fails naming the packages whose license is not on that list. The
maintainers' CI runs it on every change. There is nothing to regenerate or
commit: the images write the notices from what they install.

When it fails on a package you added:

1. Prefer another package with a permissive license.
2. If there is none and a license review clears the package, add it to
   `dependency-license-exceptions.toml`: the package as `<ecosystem>:<name>` (`go`, `npm`
   or `py`, the name as the check prints it), the license label the check
   printed, the reason, and an expiry date; from that day the check fails again.
   Say so in the pull request.
3. If the license is permissive but the check cannot tell (no license file, an
   unusual wording), add the same kind of exception and name the upstream fix
   (a license file, an SPDX `license` field) in its reason.

A package `web/src` imports belongs in `dependencies`, not `devDependencies`:
only production packages reach the notices, and `make ci-lint` checks it.

## Licensing

OpenLoc is licensed under the [Business Source License 1.1](LICENSE), and your
contribution is published under the same license. Before a maintainer applies
your first pull request, you sign the [Contributor License Agreement](CLA.md)
once: the CLA bot asks on the pull request, and you reply with the sentence it
quotes. You keep your copyright; the agreement lets ModelFront use, change and
relicense your contribution, which is what allows one codebase to carry both
the public and the commercial license.

## Reporting security issues

Please don't open a public issue for a security problem. Follow
[SECURITY.md](SECURITY.md).

## Code of conduct

Everyone taking part in the project follows the
[Code of Conduct](CODE_OF_CONDUCT.md).
