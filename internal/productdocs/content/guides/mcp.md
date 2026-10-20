---
title: "Connect external AI tools"
summary: "Connect Claude, ChatGPT, Claude Code, Codex or another AI tool that supports MCP: paste one address and approve. Access keys are the fallback."
order: 130
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - internal/mcp/register_issues.go
  - web/src/pages/AISettingsPage.tsx
  - web/src/pages/SettingsPage.tsx
  - web/src/components/ai/APITokensSection.tsx
  - web/src/components/ai/ConnectAIToolsSection.tsx
  - web/src/components/settings/ConnectedAppsSection.tsx
  - internal/mcp/**
  - internal/oauthserver/**
  - internal/auth/ratelimit.go
  - internal/taskedit/decide.go
limitations:
  - oauth-app-link-redirects
related:
  - guides/ai-assistant
  - reference/roles-and-permissions
  - reference/settings
---
You can use OpenLoc from an AI tool that supports MCP, such as Claude, Claude Desktop, ChatGPT, Claude Code or Codex. The tool connects through MCP (Model Context Protocol), signs in as you, and gets the same actions as the built-in assistant, with your role, in one organization.

## Connect Claude or ChatGPT

1. Open your avatar menu at the top right and click **AI settings**.
2. Expand **Connect an AI tool** and copy the **MCP server address**. It is your OpenLoc address followed by `/mcp`: `{app_url}/mcp`.
3. Click **Open Claude connectors** or **Open ChatGPT plugins**, and paste the address there:
   - **Claude** (on the web or in Claude Desktop): paste it as the server URL, click **Add**, then **Connect**. Leave everything else empty.
   - **ChatGPT**: click **+**, then **Add custom MCP server**. Paste it as the **Server URL**, set authentication to **OAuth**, select **I understand and want to continue**, then **Create as a plugin**, and install the plugin.
4. The tool opens OpenLoc. [Approve access](#approve-access).

Then ask something simple, such as "List my tasks in OpenLoc".

On a Claude Team or Enterprise plan, an Owner of your Claude organization adds the connector once, under **Organization settings > Connectors**. Each member then clicks **Connect** under **Customize > Connectors** and signs in as themselves. If ChatGPT does not let you add a custom MCP server, ask your ChatGPT workspace admin.

## Connect Claude Code or Codex

Run the commands for your tool in a terminal. Your browser then opens OpenLoc to [approve access](#approve-access).

```bash
claude mcp add --transport http --scope user openloc {app_url}/mcp
claude mcp login openloc
```

```bash
codex mcp add openloc --url {app_url}/mcp
```

Claude Code: `--scope user` makes OpenLoc available in every folder; start a new session to use it, and run `claude mcp list` to check. Codex: if the browser does not open, run `codex mcp login openloc`. The Codex CLI, the Codex IDE extension and the ChatGPT desktop app share this setup.

## Connect another AI tool

Most tools that support MCP with sign-in (OAuth) connect the same way. Look in its settings for a custom MCP server, connector or integration.

1. Add a new one and paste the **MCP server address** from **AI settings → Connect an AI tool**: `{app_url}/mcp`. The address field may be called server URL, endpoint or remote URL.
2. If the tool asks how to authenticate, choose OAuth or sign-in. Leave client ID and secret empty: the tool registers itself.
3. [Approve access](#approve-access) in OpenLoc.

If sign-in does not work, or the tool can only send a fixed token, [use an access key instead](#use-an-access-key-instead).

## Approve access

The tool opens an OpenLoc page that reads **… wants access to OpenLoc**. Sign in first if OpenLoc asks.

1. Read the box: **Read-only access** or **Read and write access**. The tool decides which one it asks for.
2. Check where OpenLoc sends the approval: `claude.ai` for Claude, `chatgpt.com` for ChatGPT, and `localhost` or `127.0.0.1` with a port for Claude Code and Codex. Only continue if you recognize it.
3. Under **Organization**, pick the organization the tool works in.
4. Click **Approve**.

The tool appears under **Connected apps** in your **Settings**. To use another organization, connect again and pick it.

## Use an access key instead

Use an access key only for a tool that cannot sign in, or for your own scripts and agents. A key is a secret: anyone who has it acts as you. Do not add one to a Claude connector: on a Team or Enterprise plan, everyone who uses that connector would act as you.

1. Open **AI settings** and expand **Access keys**.
2. Enter a **Name**, such as "My script", choose when it **Expires** (**30 days** to **1 year**), and set **Access** to **Read only** or **Read and write**.
3. Click **Create access key**, copy the key from the yellow box, then click **Done**. It is shown only once; if you lose it, revoke it and create a new one.

Give the key to the tool as a bearer token:

```bash
claude mcp add --transport http --scope user openloc {app_url}/mcp --header "Authorization: Bearer <your access key>"
```

```bash
export OPENLOC_KEY="<your access key>"
codex mcp add openloc --url {app_url}/mcp --bearer-token-env-var OPENLOC_KEY
```

A key belongs to you and to the organization you created it in. The same key works with the [OpenLoc API](/api-docs).

## Fix connection problems

| What you see | What to do |
|---|---|
| **You are in the Playground. Leave it to connect an app.** | Leave the Playground, then connect again from the tool. |
| An OpenLoc page that reads **Can't authorize this request** | Close the window and connect again from the tool. |
| **This action needs write access. Reconnect and approve write access.** | The tool signed in with read-only access. Disconnect it under **Connected apps**, connect again from the tool, and approve **Read and write access**. |
| **writes require user confirmation or a token with write access** | The access key is **Read only**. Create a **Read and write** key and give it to the tool. |
| The tool cannot connect with an access key, or Claude Code shows **Failed to connect** | The key is wrong, expired or revoked. Check its status under **AI settings** → **Access keys**, and create a new key if needed. |
| The tool shows another organization's work | Disconnect it and connect again, picking the right organization. An access key works only in the organization where you created it. |
| **too many requests for this key — slow down and retry** | The tool sent too many requests at once. Wait a few seconds and try again. |

## Review and revoke access

**Access keys**

1. Open **AI settings** → **Access keys**. With no keys yet, the list reads **No MCP/API access keys created yet.**
2. Each key shows its status (**Active**, **Expired** or **Revoked**), its access level, the first characters of the key, when it was last used, and when it expires.
3. Click the trash icon to revoke a key. Revoking is immediate and cannot be undone.

**Connected apps**

1. Open your avatar menu and click **Settings**. Scroll to **Connected apps**.
2. Each app shows its name, its access level, when it connected, and when it was last used.
3. Click **Disconnect** and confirm. The app's access to this organization stops at once. You can connect it again from the app any time.

## What a connected tool can do

The tools follow your role.

| Role | Read |
|---|---|
| Owner, admin, project manager | **Projects:** projects (with their MT router and the engine each target language goes to), files, jobs and their steps, segments and their history, comments, analyses, progress and project history. **Resources:** memories and their entries and imports, TM search and matches, term bases, terms and concepts, TM profiles. **Setup:** MT engines, MT routers, AI prompts, engines and routers, provider connections, steps, workflows, templates, QA profiles, labels and customers. **Team and analytics:** members, invites (project managers see only linguist invites), productivity, what needs attention, analytics metrics, dashboards and saved analyses. **Files:** download links for source files, translated files, TMX and term exports. **You:** your profile, notifications and theme. **Docs pages.** |
| Linguist | Your tasks; a job's details, steps, analysis and TM leverage; segments and their history; comments; TM search and matches; term lookup; QA report; a machine translation preview; download links for the source and translated file; your profile, notifications and theme; docs pages. |

With write access (an access key with **Read and write**, or a connected app approved with write access), a connected tool makes the same changes as the built-in assistant in write mode, with the same split by role — see [the list](/docs/guides/ai-assistant#let-it-make-changes-write-mode).

Long lists come in pages. **List project files** (`openloc_list_files`, newest first) and **List members** (`openloc_list_members`, by name) return up to 200 entries (50 by default) and, when there are more, `has_more` set to true and a `next_cursor`. The tool passes it back as `cursor` to get the next page.

A tool cannot change a segment someone else has [open in the editor](/docs/guides/cat-editor#when-someone-else-is-editing-a-segment). Tools that change many segments skip it and report it as `held`; an XLIFF re-import counts it as `skipped_held` and lists the keys in `skipped_held_keys`. The previews of a clear and of a re-import say these segments are kept.

When a tool completes a step, it can pass a comment for the next assignee, like **Comment for next assignee** in the editor — see [Complete a step](/docs/guides/workflows-and-tasks#complete-a-step).

A connected tool cannot use files attached in OpenLoc's own assistant. To add a document to a project, an owner, admin or project manager's tool sends the file's text instead: plain text, HTML, Markdown, JSON, XLIFF, PO/POT, Apple strings or XML, up to 1 MB. It becomes a project file like an upload on the project page. Upload Word, PDF and Excel files, and anything larger, on the project page. The same way back: a tool can read an exported file's text, for these formats or a bilingual XLIFF, up to 1 MB, in the target languages the user may export.

Every role can also report an issue to the support team, even with read access only. Like the changes below, it takes two calls: the first shows you the report, the second sends it. See [Report an issue](/docs/guides/report-an-issue).

### Changes that need a second call

A delete, a clear, a confirm, a find and replace, an XLIFF re-import, a chart edit, a change to a project's workflow, an invite to the organization, and an edit to settings other things use (an engine, router, workflow, step, template, QA profile, TM profile or a member's role) take two calls:

1. The first call changes nothing. It returns a preview of what would happen, with knock-on effects such as how many projects use the thing, and a `confirm_token`.
2. The tool shows you the preview. Only after you approve does it call again with the `confirm_token` and the same arguments. That call makes the change.

A `confirm_token` works only for that tool, those arguments, you and this organization, and it expires after 10 minutes. After that, or if any argument changes, the tool must ask for a fresh preview. Deletes are marked as such, so an AI tool that asks you before risky actions asks here too.

Find and replace leaves locked segments out by default. With `include_locked` set, an owner or admin also replaces in them, which unlocks them; for anyone else they are left as they are. The preview says which applies. Locking a segment through a tool needs a project manager, admin or owner and approves it; anyone who may edit a segment may unlock it.

### Set up AI prompts, engines and workflows

A connected tool can build the AI configuration a project runs on, in the order the app uses: a system prompt, an AI engine that binds it to a provider connection, a router that decides which engine each target language reaches, and then either a workflow step (for a review engine) or a project (for a translation engine). An engine is never used directly — a router always sits in between:

- **To review or post-edit:** the engine goes into an **AI router**, the router goes on a Hybrid or AI workflow step, and the step goes into a workflow.
- **To machine-translate:** the engine goes into an **MT router**, and the project is pointed at that router. That last step is what makes the engine translate; the project follows exactly one router.

Ask for what you want and the tool lists what already exists before creating anything — the built-in prompt templates and the standard **Translation** and **Review** steps are usually enough to build on.

Creating and editing prompts, and creating, editing and deleting AI engines and AI routers, is owner or admin only, matching the [AI engines and prompts](/docs/guides/ai-engines-and-prompts) page. A project manager can build, edit and delete MT engines, MT routers, steps and workflows, but not prompts, AI engines or AI routers, and gets a message saying so.

A tool can edit a prompt you already have: its instructions, what each segment carries with it, and the rules for particular languages. It cannot change the prompt's type — engines are built on it — and it cannot touch the built-in templates, which stay read-only. The context and the language rules are replaced rather than added to, so a tool that changes one of them sends the whole set back.

A connected tool never adds a provider connection, because that means handling an API key — add it once under AI settings, and the tool picks it from the connections that already exist. Pointing a project at an MT router replaces whatever router the project followed before.

Running QA from a tool works exactly like the button in the app, ignored findings included — see [The AI assistant](/docs/guides/ai-assistant#let-it-make-changes-write-mode).

The three docs tools — **List docs pages** (`openloc_list_docs`), **Search docs** (`openloc_search_docs`) and **Get docs page** (`openloc_get_doc_page`) — are available to every role, so the AI tool can explain OpenLoc to you from the manual.

A connected tool cannot reopen a completed task, delete a project or a file, handle keys, reach another organization, or do more than your role allows — the same limits as the built-in assistant, listed under [What the assistant cannot do](/docs/guides/ai-assistant#what-the-assistant-cannot-do). A segment it confirms stays out of the translation memory until a person confirms it — see [why](/docs/guides/ai-assistant#segments-the-assistant-confirms).

Every tool call is written to an audit trail, including refused ones. The arguments are not stored, so source text is not kept in that log.
