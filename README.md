# OpenLoc

> [!WARNING]
> **Public beta.** OpenLoc works and is used by early adopters, but it is pre-1.0. Expect bugs, missing features and breaking changes between releases. Back up your data before upgrading. Self-hosting is supported but comes with no warranty (see [License](#license)). If you would rather have us run it, join the [Early Access Program](https://openloc.com) for hosted OpenLoc.
>
> What we work on now and next is in [ROADMAP.md](ROADMAP.md). We build first what our Early Access Program participants need most. If a feature or fix matters to you, open an issue or [get in touch](https://openloc.com/contact/) — we would like to hear from you.
>
> **Who can use it for free.** Companies translating their own content can self-host OpenLoc at no cost. Translation agencies, consultancies and anyone running OpenLoc for others need a commercial license, or can use our hosted service (pricing to be published). This is a summary; the [LICENSE](LICENSE) file is the only binding text.

The open translation management system (TMS) built for AI, by [ModelFront](https://modelfront.com). Source-available: free to self-host and modify for your own translation work — see [License](#license).

## What is this?

OpenLoc is a TMS for localization teams that work with AI. It holds your projects, files, translation memories, terminology and workflows, and runs them with the AI models you choose: machine translation, quality checks and post-editing through your own prompts, with translation memory matches, glossary terms and surrounding context sent along. A built-in AI assistant and an MCP server let AI tools read and manage the same data. Around that is a complete TMS: a CAT editor, QA checks, roles and SSO, analytics and dashboards, and a customer API. See the user guide at [docs.openloc.com](https://docs.openloc.com) for what each feature does, and [docs/architecture.md](docs/architecture.md) for how it fits together.

## Architecture

- **Backend:** Go (Chi router) + PostgreSQL + Redis
- **Frontend:** React + Vite + TypeScript + Tailwind CSS
- **File storage:** S3-compatible (SeaweedFS for dev)
- **Background jobs:** asynq (Redis-based)
- **MT engines:** Pluggable — DeepL, Google Translate, LibreTranslate
- **Quality:** AI quality checks and post-editing through your own prompts
- **Document parsing:** Python sidecar (FastAPI) for DOCX/PDF — all other formats parsed natively in Go

## Quick start with Docker

Everything runs in containers on one machine: the app, Postgres, Redis, file
storage (SeaweedFS) and the DOCX/PDF parser. In short:

```bash
cd deploy
cp env.example .env              # fill in every CHANGE_ME
docker compose up -d --build
read -rs OWNER_PASSWORD && export OWNER_PASSWORD
docker compose run --rm -e OWNER_PASSWORD app \
  ./server create-org --name "Acme" --owner-email you@example.com
```

Replace `JWT_SECRET` before the first start (`openssl rand -hex 32`): with the
`CHANGE_ME` default the app refuses to start and keeps restarting.

Then sign in at http://localhost:8080. The full guide, with published images,
optional services, upgrades, backups and troubleshooting, is
[deploy/README.md](deploy/README.md).

## Development

To run OpenLoc from source, see [docs/development.md](docs/development.md): prerequisites, setup, the parser sidecar, useful commands and environment variables.

## Organizations

One instance can hold several organizations, each isolated by a `tenant_id` column on root tables. Each organization has its own projects, translation memories, MT engine configs, templates and terminology. Child records (files, segments, translations) inherit the isolation through foreign key chains.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development workflow, the project's rules
(tenant isolation, tests, migrations, frontend components) and how to open a pull request.

## Docs

- [docs.openloc.com](https://docs.openloc.com) — the user guide: every feature, step by step
- [developer.openloc.com](https://developer.openloc.com) — the API reference and guides for the customer API (`/api/v1`)
- [`docs/development.md`](docs/development.md) — running OpenLoc from source: setup, commands, environment variables
- [`docs/architecture.md`](docs/architecture.md) — Mermaid diagrams: system overview, pipeline, ER diagram, pages
- [`docs/testing.md`](docs/testing.md) — how the test suites are organized and how to run them
- [`deploy/README.md`](deploy/README.md) — self-hosting with Docker

## License

OpenLoc is **source-available**, licensed under the [Business Source License 1.1](LICENSE). In plain words:

- **Free** to download, read, modify, self-host and run in production to translate content for your own business — including content you publish for others, such as listings on a marketplace or a store. Your contractors and freelance translators may work inside your instance.
- **Needs a commercial license** if you translate for other companies as a service (an LSP or agency), host or run OpenLoc for someone else — paid or not — build a product on top of it, sell translation memories or datasets made with it, or use it to train AI models you offer to others.
- Testing and evaluation are always free.
- Each version becomes open source under the Apache License 2.0 four years after its release.

The license text is what counts; this summary is not legal advice. For a commercial license, write to licensing@modelfront.com.
