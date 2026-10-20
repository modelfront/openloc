---
title: "Roles and permissions"
summary: "The four roles — owner, admin, project manager, linguist — and what each one can see and do."
order: 10
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - internal/auth/roles.go
  - internal/auth/ownership.go
  - internal/issues/service.go
  - internal/translation/find_replace.go
  - internal/invite/service.go
  - internal/taskedit/decide.go
  - internal/job/handler_rerun.go
  - internal/job/handler_skip_ai_step.go
  - web/src/lib/roles.ts
  - web/src/App.tsx
  - web/src/components/project/files/ProjectFilesTab.tsx
  - web/src/components/project/files/file-columns.tsx
  - web/src/components/layout/NavRail.tsx
  - web/src/pages/org/**
  - web/src/pages/AISettingsPage.tsx
  - internal/api/api.go
  - internal/publicapi/register.go
related:
  - guides/customers-and-vendors
  - guides/workflows-and-tasks
  - guides/projects-and-files
  - reference/settings
  - getting-started/interface-tour
---

Every member of an organization has one of four roles: **Owner**, **Admin**, **Project manager** or **Linguist**. The role decides which pages appear in the left menu and which actions the server accepts. Owners and admins change roles on the **Members** tab of the **Organization** page (open the account menu at the top right, then **Organization settings**).

## The four roles

| Role | In short |
|---|---|
| **Owner** | Everything. The only role that can delete a customer, and the only role that can remove another owner. |
| **Admin** | Full day-to-day control: projects, resources, members, organization settings, AI providers. Cannot do the owner-only deletes. |
| **Project manager** | Runs projects: creates them, uploads files, assigns people, manages translation memories, terminology, templates, workflows, engines and QA profiles. Cannot delete memories or term bases, cannot delete a project or its files once the project is no longer a **Draft**, and cannot manage members or organization settings. |
| **Linguist** | Works only on the jobs assigned to them. Sees the **Tasks** page, the CAT editor and the Docs pages. |

Owner, admin and project manager are called **managers** in this documentation. Everything a manager can do, a linguist cannot, unless the table below says otherwise.

The API follows the same table: a request made with your access key may do only what your role may do on screen, and a **Read only** key can only read.

## What each role can do

✓ = allowed, — = not allowed.

### Navigation and viewing

| Capability | Owner | Admin | Project manager | Linguist |
|---|---|---|---|---|
| See **Projects**, **Translation memories**, **Terminology**, **Customers**, **Templates**, **Workflows**, **MT**, **AI**, **Integrations**, **QA profiles**, **Dashboards** | ✓ | ✓ | ✓ | — |
| Open the **Organization** page (account menu → **Organization settings**) | ✓ all tabs | ✓ all tabs | ✓ **Vendors** tab only | — |
| See **Tasks** in the left menu | ✓ (all tasks in the organization) | ✓ (all tasks in the organization) | ✓ (own tasks) | ✓ (own tasks) |
| Open a job in the CAT editor | ✓ any job | ✓ any job | ✓ any job | ✓ only jobs assigned to them |
| See **Learn** (guided tours, in the account menu, when the Playground is on) | ✓ | ✓ | ✓ | ✓ |
| See **Docs** (in-app documentation) | ✓ | ✓ | ✓ | ✓ |
| See **API** (API guides and endpoint list, in the account menu) | ✓ | ✓ | ✓ | ✓ |
| Personal **Settings** and **AI settings** (connecting your own AI tools, and your own access keys) | ✓ | ✓ | ✓ | ✓ |

### Projects and files

| Capability | Owner | Admin | Project manager | Linguist |
|---|---|---|---|---|
| Create, rename, edit a project; change status, customer, deadline, workflow, settings | ✓ | ✓ | ✓ | — |
| Create project labels, add values to them (**Manage labels**), and choose a project's labels | ✓ | ✓ | ✓ | — |
| Delete a label's value, or a whole label | ✓ | ✓ | — | — |
| Upload files, run pre-translation, run an analysis | ✓ | ✓ | ✓ | — |
| **Stop** a pre-translation or AI step that is running on a job | ✓ | ✓ | ✓ | ✓ for own jobs |
| **Re-run** a task that shows an error | ✓ | ✓ | ✓ | ✓ for own jobs, when pre-translation failed |
| **Skip AI step** on a Hybrid step whose AI run failed | ✓ | ✓ | ✓ | — |
| Assign jobs, set step deadlines, clear translations of a job, delete jobs | ✓ | ✓ | ✓ | — |
| Complete or reject a workflow step | ✓ when assigned* | ✓ when assigned* | ✓ when assigned* | ✓ when assigned* |
| Edit a segment of a completed task, which reopens the task | ✓ | ✓ | ✓ | — |
| Run QA on a segment, and **Ignore** or **Un-ignore** a QA finding | ✓ | ✓ | ✓ | ✓ for own jobs |
| Confirm a segment (which copies it to identical segments), and use find and replace across a job | ✓ | ✓ | ✓ | ✓ for own jobs |
| Add a word to the project's spell-check dictionary (**Ignore in this project**) | ✓ | ✓ | ✓ | — |
| Remove a word from the project's spell-check dictionary | ✓ | ✓ | — | — |
| Download translated files, bilingual XLIFF and source files | ✓ | ✓ | ✓ | ✓ for own jobs |
| Import an edited XLIFF into a task | ✓ | ✓ | ✓ | ✓ for own jobs |
| Delete a file | ✓ | ✓ | ✓ only while the project is a **Draft** | — |
| Delete a project | ✓ | ✓ | ✓ only while it is a **Draft** | — |
| Lock a segment, which approves it | ✓ | ✓ | ✓ | — |
| Unlock a segment | ✓ | ✓ | ✓ | ✓ for own jobs |
| Lock a segment while someone else is editing it | ✓ | ✓ | — | — |
| Replace text in locked segments with find and replace (**Include locked**), which unlocks them | ✓ | ✓ | — | — |
| Change a locked segment with spell check (**All segments (includes locked)**, **In all segments**), which unlocks them | ✓ | ✓ | — | — |

\* A step is completed or rejected by the person assigned to it. Owners, admins and project managers can also complete or send back any step, assigned or not. A linguist who is not the assignee cannot.

A linguist's access is per target language. When a file has jobs in several languages, a linguist reaches only the languages whose job is assigned to them: translations, comments, QA, history, propagation and the translated-file and bilingual XLIFF downloads of another language's job are refused. The file's source text and the source download stay open to any linguist who has a job in that file.

A task that shows an error takes no edits from any role until it is re-run, its AI step is skipped, or it is completed. See [Recover from a failed run](/docs/guides/workflows-and-tasks#recover-from-a-failed-run).

### Resources

| Capability | Owner | Admin | Project manager | Linguist |
|---|---|---|---|---|
| Create and edit translation memories, TM profiles and term bases; import and export | ✓ | ✓ | ✓ | — |
| Delete a translation memory, TM profile or term base | ✓ | ✓ | — | — |
| Create, edit and delete templates, MT routers, MT engines and QA profiles | ✓ | ✓ | ✓ | — |
| Create, edit and delete workflows and steps in the workflow library (**Workflows** page) | ✓ | ✓ | ✓ | — |
| View AI routers, AI engines and prompts (**AI** page) | ✓ | ✓ | ✓ | — |
| Create, edit and delete AI routers, AI engines and prompts | ✓ | ✓ | — | — |
| Manage the organization's AI provider keys (**Providers** on the AI settings page) | ✓ | ✓ | — | — |
| See the providers and key names (**Providers** on the AI settings page) | ✓ | ✓ | ✓ | — |
| View dashboards and analytics | ✓ | ✓ | ✓ | — |
| Create, edit and delete your own dashboards and their charts (**My dashboards**) | ✓ | ✓ | ✓ | — |
| Edit or delete an organization dashboard or its charts; edit analytics weights | ✓ | ✓ | — | — |
| Copy an organization dashboard or chart into **My dashboards** | ✓ | ✓ | ✓ | — |
| Pin a dashboard to your own **Pinned** tab, and rename or reorder what you pinned (pinning an organization dashboard needs no edit rights, and pins are visible only to you) | ✓ | ✓ | ✓ | — |
| Create or stop a share link for a chart (anyone who can change the chart) | ✓ | ✓ | ✓ | — |
| Open a chart from a share link, and download it | ✓ | ✓ | ✓ | ✓ |

### Customers, vendors and members

| Capability | Owner | Admin | Project manager | Linguist |
|---|---|---|---|---|
| Create and edit customers | ✓ | ✓ | ✓ | — |
| Delete a customer | ✓ | — | — | — |
| Invite a vendor (creates a linguist account) | ✓ | ✓ | ✓ | — |
| Invite an admin or project manager | ✓ | ✓ | — | — |
| See, **Resend** or **Revoke** a pending invitation | ✓ | ✓ except an invitation to join as owner | ✓ only vendor invitations | — |
| Change a member's role or remove a member | ✓ | ✓ | — | — |
| Remove another owner | ✓ | — | — | — |
| Edit language pairs of a member | ✓ | ✓ | — | — |
| **Settings** tab of the **Organization** page: open registration, notification rules, analysis weights | ✓ | ✓ | — | — |

### Reported issues

| Capability | Owner | Admin | Project manager | Linguist |
|---|---|---|---|---|
| Report an issue, and see and delete your own | ✓ | ✓ | ✓ | ✓ |
| See every report of the organization | ✓ | ✓ | ✓ | — |
| Change the kind of a report you can see (**Kind**) | ✓ | ✓ | ✓ | ✓ own reports |
| Delete another member's report | ✓ any | ✓ a project manager's or linguist's | ✓ a linguist's | — |

## What a linguist sees

A linguist's left menu has one entry: **Tasks**. Every management page redirects to it. The account menu at the top right offers **Settings** (profile, notifications, appearance), **AI settings** (connecting their own AI tools, and their own access keys), **Learn**, **Docs**, **API** and **Sign out**. **Docs** is not restricted: it shows every documentation page, and search covers all of them, so a linguist can read how the parts they do not administer actually work.

From **Tasks** a linguist opens a job in the CAT editor. There they can translate, confirm segments, apply translation memory or machine translation, stop a run that is still going with **Stop**, re-run a pre-translation that failed with **Re-run**, run QA and spell check, ignore a QA finding that does not apply, search the concordance, download their own files and complete or reject their step. They cannot edit a completed task, open a project page, browse a translation memory or see other people's jobs.

## Vendors are linguists

A **vendor** is not a fifth role. It is an external translator you invite from the **Vendors** tab of the **Organization** page with **Send invitation**. The account that is created has the **Linguist** role. The **Vendors** tab lists pending invitations and the linguists who have joined. See [Labels and vendors](/docs/guides/customers-and-vendors).

## Inviting and changing roles

- Owners and admins invite from the **Members** tab of the **Organization** page → **Invite member** and choose **Admin** or **Project manager**. Nobody can invite an owner; ownership is given by changing an existing member's role.
- Project managers can invite only linguists, from the **Vendors** tab.
- Roles are changed with the dropdown next to each member on the **Members** tab. Changing someone to **Linguist** asks for confirmation, because they lose access to every management page.
- The last active owner or admin cannot be removed.
