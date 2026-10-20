---
title: "Welcome to OpenLoc"
summary: "What the TMS is, who it is for, and how a translation moves through it."
order: 10
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - web/src/App.tsx
  - web/src/components/layout/NavRail.tsx
  - README.md
related:
  - getting-started/first-project
  - getting-started/interface-tour
  - concepts/how-a-translation-flows
  - concepts/quality-estimation
  - reference/roles-and-permissions
  - reference/glossary
screenshots:
  - projects-list.png
---
OpenLoc is a translation management system (TMS): the place where a team keeps its translation projects, files, translation memories and term lists, and where translators do their work.

## What is in it

- Projects and files: upload documents, pick target languages, set deadlines and assign work.
- A translation memory: every confirmed translation is saved and reused next time.
- Terminology: approved terms are checked against the translation, so wording stays consistent.
- Workflows and tasks: project managers see who is working on what, and when it is due.
- A CAT editor and QA checks for the people doing the translating.
- Analytics and dashboards across all projects.

## Work with AI

Machine translation, quality checks and post-editing run on the AI providers you connect; language models follow your own prompts. Translation memory matches, glossary terms and surrounding context can go along with each segment. A built-in assistant answers questions, creates projects and builds dashboards, and the same tools are open to Claude and other AI tools over MCP. You choose which AI providers see your content: see [What leaves your organization](/docs/guides/integrations#what-leaves-your-organization).

## Who uses it

Everyone in an organization has one of four roles:

| Role | Typical person | What they do |
|---|---|---|
| Owner | The person who set up the organization | Everything an Admin can do. The organization's top-level account. |
| Admin | Localization lead, IT | Manage members, MT routers and engines, QA profiles and organization settings; delete things |
| Project manager | Localization PM, content owner | Create projects, upload files, assign work, review analyses, download results |
| Linguist | Translator, reviewer, vendor | Open assigned tasks in the editor, translate, confirm, complete steps |

A linguist's left menu holds only Tasks. Managers and admins see the full left menu. Docs and API are in the account menu, for every role. See [Roles and permissions](/docs/reference/roles-and-permissions) for the exact list.

## The main objects

- **Organization** — your company or team. Everything below belongs to one organization.
- **Project** — one piece of work: a source language, one or more target languages, and settings that decide how files are translated.
- **File** — a document you upload to a project (a Word file, an XLIFF, a JSON file, and so on).
- **Job** — one file in one target language. A file uploaded to a project with three target languages creates three jobs.
- **Segment** — one unit of text inside a file, usually a sentence or a paragraph. Translation happens segment by segment.
- **Translation memory (TM)** — a database of past source and target sentence pairs. The system reuses them when the same or similar text appears again.
- **Term base** — a list of approved terms and their translations. The editor shows them and QA checks them.
- **Template** — saved project settings you can reuse when creating new projects.
- **Workflow** — the ordered steps a job goes through, for example Translation then Review. Steps and workflows live in a shared library on the Workflows page; a project or template picks a workflow and gets its own copy of the steps. Each step is Human, Hybrid or AI: a Human step is worked by a linguist, a Hybrid step runs AI quality estimation and post-editing first and then a linguist reviews it, and an AI step runs and completes on its own. In a project, each step also has an optional assignee and an optional deadline.
- **Task** — one workflow step on one job, as seen by the person assigned to it.

## The left menu

![The Projects page with the left menu](assets/projects-list.png)

Managers work from **Projects**; linguists work from **Tasks**. The other entries hold the resources a project draws on: memories, term bases, customers, templates, workflows, MT routers and engines, AI engines, integrations, QA profiles and dashboards. The [Interface tour](/docs/getting-started/interface-tour) describes each one.

Your personal **Settings**, **Organization settings** (owners and admins; project managers reach only its Vendors tab), **AI settings**, **Docs** and **API** live in the account menu — your name at the top right — together with **Sign out**. **Docs** opens these documentation pages; everyone sees the same pages, and Ctrl+K (Cmd+K on Mac) searches them. **API** opens the documentation for connecting other systems to OpenLoc.

New here? Follow [Your first project](/docs/getting-started/first-project) — it takes about ten minutes and walks a file from upload to download.
