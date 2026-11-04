---
title: "The AI assistant"
summary: "Ask the built-in assistant about your projects, get charts, and let it act for you in write mode."
order: 120
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-11-03
sources:
  - internal/mcp/register_issues.go
  - web/src/api/chat.ts
  - web/src/components/chat/**
  - web/src/components/charts/**
  - web/src/pages/AISettingsPage.tsx
  - web/src/components/ai/AIVendorsSection.tsx
  - web/src/components/ai/vendor-catalog.ts
  - web/src/lib/screenshot.ts
  - web/src/lib/watch-recording.ts
  - internal/chat/**
  - internal/mcp/**
  - internal/taskedit/**
  - internal/ai/catalog.go
related:
  - guides/analytics
  - guides/mcp
  - getting-started/demo-and-tours
  - reference/roles-and-permissions
  - reference/settings
---
The AI assistant is a chat panel that is available on every page. You ask questions in plain language, and it answers by looking up your real data: projects, jobs, deadlines, segments, translation memories, terminology, QA reports and analytics. It only sees what your role may see. By default it can only read. When you allow it, it can also make changes for you: translate and confirm segments, set up and run projects, fill memories and term bases, and edit settings. A delete, a confirm or an edit to shared settings waits for your click, and segments it confirms stay out of the translation memory until a person confirms them.

## Open the assistant

1. Click the round button at the bottom right of any page. It shows the logo of the AI provider that answers you.
2. Type your question in the box at the bottom and press Enter. Shift+Enter adds a new line. On a phone or tablet, Enter adds a new line and the **Send** button sends. Web addresses in your message become clickable links.
3. While the assistant works, a **Working…** line shows what it is doing. Click the square **Stop** button to cancel an answer.

The header has four buttons:

- **Minimize** hides the panel but keeps the conversation.
- **Earlier conversations** lists your past conversations, newest first. Pick one to reopen it.
- **New conversation** starts an empty thread.
- **Close** ends the conversation.

Drag the top-left corner to resize the panel. The assistant is available to every role, including linguists. In the Playground the panel shows an example conversation instead of answering.

An empty conversation offers one example question; click it to send it. A linguist sees **What are my open tasks?**. Owners, admins and project managers see **Help me create my first project** while the organization has no projects, and **What needs my attention today?** after that. When the assistant guides you, it can point at a part of the screen with a short card. Some answers end with quick replies: buttons with a likely next answer, on the card or above the message box. Click one to send it, or type your own.

## Attach files

1. Click the paperclip next to the message box, **Attach a file (TMX, terms, a document to translate)**, and pick one or more files, or drop them on the assistant panel. Each file uploads at once, up to 50 MB. A message takes up to 10 files.
2. Each file shows as a chip above the message box. Click the **✕** next to one to remove it.
3. Say what to do with them, for example "Import this into the Marketing memory" or "Add these documents to the Helix project". You can also send the files without typing anything.

The assistant adds several documents to a project in one step and tells you which ones it could not add, and why.

Owners, admins and project managers can import a TMX file into a translation memory, import a CSV or TBX file into a term base, or add a document to a project, just like an upload on the project page. An attached file is kept for 24 hours.

Every role can also ask the assistant to read an attached file, for example "Summarize this style guide". An owner or admin can say "Write an AI prompt from this style guide". The assistant reads every format a project accepts, up to 1 MB of text per file.

## Choose a model

The model picker sits at the bottom of the message box. It lists the Claude and OpenAI models your organization's keys can reach, newest first, up to eight per provider, so what you see follows the keys rather than a fixed list. The list updates as soon as a key is added, changed or removed. Your choice is remembered. The default model is the newest Sonnet your organization's Claude key lists, or Sonnet 5 when the list has none.

A provider your organization has no key for adds nothing to the list. When the list is empty it says which case you are in: **Loading models…** while it is still fetching, **Couldn't load models** if the fetch failed, or **No AI provider connected**. An owner or admin connects a key under **AI settings** → **Providers**, which you find in your avatar menu; with no key at all, the panel itself says so and links there.

## Ask about your work

Some questions that work well:

- "Which jobs are overdue?"
- "When is the review step due on this project?"
- "How much of job 42 comes from the translation memory?"
- "Show the QA issues for this job."
- "How was 'release notes' translated before in this project?"
- "Which vendors translate English into Spanish, and who is busy right now?"
- "Which MT engines do my projects use?"

When it reads a QA report, it lists findings someone marked as ignored as ignored, and counts them apart from the errors, warnings and information.

Small chips above the answer show which lookups the assistant made, so you can see where a number comes from. If a lookup returns nothing, it says so rather than guessing.

What you can ask depends on your role:

| Role | The assistant can look up |
|---|---|
| Owner, admin, project manager | All projects, jobs, files, members, customers, TM matches, terms, QA reports, organization-wide analytics, a project's MT router with the engine each target language goes to, and a project's workflow steps with their default assignee and due dates |
| Linguist | Your own tasks, a job's details, segments, TM leverage and QA report, TM and term lookups, and the segmentation profiles with a preview of how they split a text. Not the project, customer or member lists. |

See [Roles and permissions](/docs/reference/roles-and-permissions) for the full model.

## Ask how OpenLoc works

The assistant can read these docs. Three tools are available to every role: **List docs pages**, **Search docs** and **Get docs page**. So "How do I import a TMX file?" or "What does a 101% match mean?" gets an answer taken from the manual, in the words the app uses.

## Use it on the current page

The assistant knows which page you are on. On a project page, "this project" means that project; in the CAT editor, "this job" means the open job. It can also open pages for you: ask "Open the tasks page" or "Take me to the dashboard", and it navigates there and says what it opened. It only opens pages inside OpenLoc, never outside links.

## Report a problem to the support team

Tell the assistant something in OpenLoc is broken, or ask for a change, and it writes a short report for the support team. Every role can do this, in read-only mode too, where your OpenLoc takes reports. The assistant checks the problem by looking: it reads the data behind the page you are on. It may ask you for a screenshot or to repeat the steps while your browser records them, and you review and send the report yourself; see [Report an issue](/docs/guides/report-an-issue#report-it-with-the-assistant). It also lists and reads the reports you can see on the **Reported issues** page, in either mode.

## Get a chart

Managers can ask for organization-wide numbers and see them as a chart in the chat.

1. Ask, for example: "Show me the post-editing volume per month for the last six months, by target language."
2. The assistant queries the analytics and draws a line, bar, area or pie chart inside its answer.
3. To keep the chart, ask it to save the chart to a dashboard. This is a change, so switch to **Allow write** first (see below).

Charts drawn in the chat are not stored; when you start a new conversation, they are gone. Saved charts live on the **Dashboards** page and stay live. See [Analytics and dashboards](/docs/guides/analytics).

## Let it make changes: write mode

Next to the model picker is the mode picker. It has two options:

- **Read-only** — the default. Answers questions; never changes data.
- **Allow write** — the assistant may make changes in this conversation. Deletes ask you first.

When write mode is on, the header shows an amber **write** label so you always know. The setting is remembered until you change it back. In read-only mode, if you ask for a change, the assistant tells you to switch the mode and ask again. The one exception is reporting an issue to the support team: the assistant does it in either mode, and asks you to confirm the report first. See [Report an issue](/docs/guides/report-an-issue#report-it-with-the-assistant).

In write mode the assistant can:

| Who | Changes the assistant can make |
|---|---|
| Every role | **Segments:** save drafts, one or many at once; confirm; clear; unlock; find and replace across a job; fill untranslated segments with **Apply TM** or **Apply MT**; clear a job's translations; import an edited XLIFF. **Work:** add and delete comments; run QA on a job or one segment; ignore or un-ignore a QA finding; complete the current workflow step, with an optional comment of up to 4,000 characters for whoever works the next step ([Complete a step](/docs/guides/workflows-and-tasks#complete-a-step)), or send the job back. **Reported issues:** reply to the issues you can see, which reaches the support team and reopens a closed one; confirm a fix; change an issue's kind. **You:** mark notifications as read, change your notification settings, your color theme and your palettes. A linguist does this only on jobs assigned to them. |
| Owner, admin, project manager | Also: **Segments:** lock one, which approves it; the assistant's approval waits for a person before it reaches the memory. **Projects:** create one (it needs a workflow, either picked or brought by a template; it asks which when unsure), publish a draft (which also needs a workflow), edit it, add an attached document, pre-translate it, clear its translations, run its AI step again, assign people to steps, set due dates, and choose its memories, term bases, MT router, labels and settings. **Resources:** create customers, labels, memories, term bases and TM profiles; edit TM profiles; rename memories and term bases; import a TMX or term file; edit a memory entry; add, edit and delete terms and concepts. **Setup:** create, edit and delete MT engines, MT routers, steps, workflows, QA profiles and segmentation profiles; create, copy and edit templates. **Team:** invite people and withdraw an invite; a project manager does both only for linguists. **Dashboards:** save a chart, edit or delete charts, set filters, save analyses. |
| Owner, admin | Also: create and edit AI prompts, and create, edit and delete AI engines and AI routers; delete a memory, TM profile, term base, label or label value; change a member's role or language pairs, and remove a member. |

### Changes that wait for your click

A delete, a clear, a confirm, a find and replace, an XLIFF re-import, a chart edit, a change to a project's workflow, and an edit to settings other things use (an engine, router, workflow, step, template, QA profile, TM profile, segmentation profile or a member's role) never run from your message alone. The assistant shows a card, **Needs your confirmation**, with one line on what will happen. Click **Review** to read the full change, including knock-on effects such as how many projects use the thing, then click **Confirm**, or **Delete** for a delete or a clear. **Cancel** drops it. The card then reads **Done** or **Failed**.

A find and replace leaves locked segments out unless you ask it to include them. When you do, an owner or admin has the text replaced in locked segments too, which unlocks them; for anyone else they are left as they are. The card says which of the two will happen. See [Find and replace](/docs/guides/cat-editor#find-and-replace).

### Segments the assistant confirms

A segment the assistant confirms is **Approved**: it counts as done for export and for completing a step. The editor marks it **Confirmed by assistant**, and it is not written to the translation memory, because no person decided that text. When a person confirms it with Ctrl+Enter, the mark goes away and the translation is saved to the memory like any other.

Ask it to run QA and it runs every enabled check on every segment of the job and reports the new counts. Findings someone ignored stay ignored as long as the same problem is found again. If the project has no QA profile, it says so and asks you to assign one in the project settings first.

Ask it to ignore a finding and it marks that one finding on that one segment as ignored, under your name. The finding stays visible as **Ignored** in the editor and in the report, stops counting toward the totals, and no longer blocks confirming the segment. Ask it to un-ignore and the finding counts again. Keep this for false positives — a real error belongs in the text, not in an ignore.

Ask it to move a due date and it sets the step's date for every target language, or a date for one language only if you name one. Steps you do not mention keep theirs, and jobs already on a step are re-dated unless that step is finished. See [Workflows and tasks](/docs/guides/workflows-and-tasks#read-deadlines).

The assistant cannot change a segment a colleague has [open in the editor](/docs/guides/cat-editor#when-someone-else-is-editing-a-segment); a change across many segments skips those and says how many, and the confirmation card for a clear or a re-import says they are kept.

Before it creates something, the assistant asks for the details it is missing and restates what it is about to do. Before it saves a draft over existing text, it re-reads the segment, so it does not overwrite a colleague's edit by accident. It changes a job's text only while the job's current step is in progress and no AI step is running. On a completed step, a step whose automation failed, or a job whose steps have not started, it refuses and tells you why. Clearing translations works up to and including the workflow's first human or hybrid step, and **Apply TM** and **Apply MT** only on its first step; see [Select several segments and act on them](/docs/guides/cat-editor#select-several-segments-and-act-on-them). Every change is recorded in the audit log with your name.

## What the assistant cannot do

- **Do more than your role allows.** Write mode does not change your permissions.
- **Skip your confirmation.** A change behind a **Needs your confirmation** card runs only when you click.
- **Reopen a completed step**, whatever your role. A manager reopens one by [editing a segment in the editor](/docs/guides/workflows-and-tasks#reopen-a-completed-task).
- **Delete a project or a file.**
- **Handle keys.** It cannot add or change an AI provider key, an access key or any other secret. An owner or admin connects providers under **AI settings** → **Providers**.
- **Invite a vendor organization.** It can invite a person as a member and list pending invites, each marked as a member invite or as a vendor organization's setup invite with that organization's name. A vendor organization is set up only with **Invite vendor** on the **Members** tab of the **Organization** page.
- **Reach another organization.** It only works inside the organization you are signed in to. A vendor organization has no assistant.

## If something goes wrong

| Message | What to do |
|---|---|
| **No AI provider is connected for your organization.** | An owner or admin adds a key under **AI settings** → **Providers**. |
| Your API key was rejected | Reconnect the key on the same page. |
| The provider is rate-limiting requests, or is out of credit | Wait a moment, or check the account with the provider. |
| Stopped: too many tool calls, the answer got too long, or it took too long | Press **Continue**. The assistant goes on from where it stopped. |
| The connection went quiet and was closed, or the connection to the assistant was interrupted | Press **Try again**. The assistant answers the same question again. In **Allow write** mode the button does not show once the assistant has started an action: check what changed before you ask again. |

To use OpenLoc from Claude Desktop, Claude Code or ChatGPT instead, see [Connect external AI tools](/docs/guides/mcp).
