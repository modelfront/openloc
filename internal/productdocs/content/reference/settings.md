---
title: "Settings"
summary: "Personal, organization and AI settings, and where each option lives."
order: 50
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - web/src/pages/SettingsPage.tsx
  - web/src/pages/org/**
  - web/src/pages/AISettingsPage.tsx
  - web/src/components/SettingsEditor.tsx
  - web/src/components/settings/TMProfilePicker.tsx
  - web/src/components/settings/AppearanceSection.tsx
  - web/src/components/settings/ConnectedAppsSection.tsx
  - web/src/components/org/RegistrationSettings.tsx
  - web/src/components/org/NotificationRules.tsx
  - web/src/components/org/AnalysisWeights.tsx
  - web/src/components/ai/AIVendorsSection.tsx
  - web/src/components/ai/ConnectAIToolsSection.tsx
  - web/src/components/ai/vendor-catalog.ts
  - web/src/components/ai/APITokensSection.tsx
  - internal/ai/handler.go
  - internal/auth/handler.go
  - internal/invite/service.go
  - web/src/components/ProjectSettingsTab.tsx
  - web/src/pages/ProjectCreate.tsx
  - web/src/components/project/ProjectCreateWorkflowCard.tsx
  - web/src/components/workflow/WorkflowPicker.tsx
  - web/src/components/workflow/WorkflowModeToggle.tsx
  - web/src/components/workflow/RawMTPane.tsx
  - web/src/components/workflow/WorkflowTab.tsx
  - internal/settings/**
  - internal/appearance/**
limitations:
  - access-level-labels
related:
  - reference/roles-and-permissions
  - guides/notifications
  - guides/templates
  - guides/translation-memory
  - guides/machine-translation
  - guides/mcp
  - concepts/analysis-bands
screenshots:
  - project-settings.png
---
Settings live in three places, all reached from the account menu (click your name in the top right): **Settings** for you, **Organization settings** for the whole organization, and **AI settings** for AI providers and access keys. Options that decide how a project is translated live on the project itself and on templates; reusable workflows and steps live on the **Workflows** page.

## Personal Settings

Account menu → **Settings**. Every role has this page. The options apply to you only.

| Section | Option | Notes |
|---|---|---|
| **Profile** | **Name**, **Email** | **Email** cannot be changed. Notifications are emailed to this address. **Save changes** saves your name. |
| **Email notifications** | **Task assignment**, **Step rejection**, **Workflow completion**, **Deadline approaching** | Untick to stop the email for that event. In-app notifications still arrive. See [Notifications](/docs/guides/notifications). |
| **Appearance** | **Light**, **Dark**, **System** | The theme. System follows your computer. |
| | **Dark palette**, **Light palette** | The colour set for each theme. Click one to select it. **New palette** creates your own; each palette has a button to clone it, and the ones you made also have buttons to edit or delete them. Palettes are saved to your account and follow you across organizations. |
| **Connected apps** | AI assistants you authorized to act as you | Shows the app name, **Read-only** or **Read and write**, and last use. **Disconnect** stops its access at once. |

There is no password field here. To set a new password, use **Forgot password?** on the sign-in page.

## Organization settings

Account menu → **Organization settings** opens the **Organization** page. Owners and admins see three tabs: **Members**, **Settings** and **Vendors**. Project managers see the entry too, and it opens **Vendors** only, with no tab row. Linguists do not see it. Changes apply to every member.

| Tab | Section | Option | Notes |
|---|---|---|---|
| **Members** | **Members** | Role dropdown per member | **Owner**, **Admin**, **Project manager** or **Linguist**. Changing someone to Linguist asks for confirmation; they then appear on the **Vendors** tab, which lists every member with the Linguist role. |
| | | **Language pairs** | The pairs a member works in, shown as chips next to the name. |
| | **Invite member** | **Email**, **Role** (**Admin** or **Project manager**), **Send invite** | Linguists are invited from the **Vendors** tab. |
| | **Pending invites** | **Resend invite**, revoke | Each invite shows its expiry date. See [Resend an invitation](/docs/guides/customers-and-vendors#resend-an-invitation). |
| | **Accepted invites** | History | Who accepted, and when. |
| **Settings** | **Registration** | **Invite only** / **Anyone with organization email can join** | When open, people who sign in with your email domain join as project managers. People already on the **Vendors** tab join as linguists. |
| | **Notification rules** | **Task assignment**, **Step rejection**, **Workflow completion**, **Deadline approaching** | Master switches. Off means no notification of that kind for anyone, in the app or by email. |
| | **Analysis weights** | One percentage per match band, from **100% (exact)** to **New (no match)**, plus **Repetitions** and **QE approved** | How much effort each band counts for in weighted words. 0% is free, 100% is full effort. See [Analysis bands](/docs/concepts/analysis-bands). |
| **Vendors** | Vendor table | Invite, resend, revoke | External linguists and their invitations. See [Invite a vendor](/docs/guides/customers-and-vendors#invite-a-vendor). |

## AI settings

Account menu → **AI settings**. Every role can open the page, but each section has its own owners.

| Section | Who | What it holds |
|---|---|---|
| **Providers** | Owners and admins; project managers see it read-only | The organization's connections to AI providers and the keys that authenticate there. Keys are stored encrypted and checked when saved. A key marked **Use for chat** powers the built-in assistant. Engines on the **AI** page and on the **MT** page each name a key from here. Removing a key is allowed even while engines use it — they are marked **No key** until you point them at another one (see [Machine translation](/docs/guides/machine-translation#add-a-provider-key-owner-or-admin)). Providers this deployment supplies itself show **No key required**. |
| **Connect an AI tool** | Everyone | The **MCP server address** with a copy button, for AI tools that support MCP. **Open Claude connectors** and **Open ChatGPT plugins** open the page in those two tools where you paste it. The tool then signs in as you. See [Connect external AI tools](/docs/guides/mcp). |
| **Access keys** | Everyone, for their own keys | Access keys for tools that cannot sign in and for your own scripts, over MCP or the API. Each has a **Name**, an **Expires** value (**30 days** to **1 year**) and an **Access** level (**Read only** or **Read and write**). A **Read only** key can look but not change anything, over MCP or the API. The key acts as you, with your permissions, in this organization only. It is shown once, right after **Create access key**, and only a signed-in person can create one — a key cannot create another key. See [Connect external AI tools](/docs/guides/mcp). |

## Where each project option lives

A project carries its own settings on its **Settings** tab and its workflow steps on its **Workflow** tab. A template has the same two tabs and is a saved copy that new projects can start from. Some things belong to the organization and are only *chosen* on a project. "Managers" means owner, admin and project manager; see [Roles and permissions](/docs/reference/roles-and-permissions) for what project managers cannot delete.

![The Settings tab of a project](assets/project-settings.png)

| Option | Organization | Template | Project | Who can change |
|---|---|---|---|---|
| Source and target languages | — | — | Set at creation | Managers |
| **Customer**, **Deadline**, project status | — | — | Project header | Managers |
| **Complete** the project when every task is done | — | ✓ | ✓ | Managers |
| Apply **translation memory** on start or upload | — | ✓ | ✓ | Managers |
| Fill in **machine translation** on start or upload | — | ✓ | ✓ | Managers |
| **QA profile** (in the **Quality assurance** block) | Defined on the **QA profiles** page | Chosen | Chosen | Managers |
| **Strings contain HTML** (under auto-complete). On by default: tags in JSON, PO, TXT and Excel strings become protected tags, and a translation may hold only the source's tags. Off: strings are plain text, with no tags, no tag check, and export exactly as typed. Each file keeps the mode it was uploaded with, so a change applies to new uploads only | — | ✓ | ✓ | Managers |
| **Translation memory** rows and match scoring (**Fuzzy match threshold**, **Language-variant mismatch penalty**, **Near-exact penalty**, **TM lookup order**; see [Several memories](/docs/concepts/tm-matching#several-memories)) | TMs live on the **Translation memories** page | ✓ | ✓ | Managers |
| **TM profile**: a shared TM setup to follow, or **From scratch** for a list this project keeps alone | Profiles are created on the **Translation memories** page | Followed | Followed | Managers |
| **Machine translation**: the **MT router** that picks the engine per target language, or empty for no machine translation | Routers and engines live on the **MT** page | ✓ | ✓ | Managers |
| **Terminology** rows | Term bases live on the **Terminology** page | ✓ | ✓ | Managers |
| **Workflow**: the **Workflow** / **Raw MT** toggle on the **Workflow** tab. **Workflow** takes the steps of a library workflow; **Raw MT** is one automated **Pre-translation** step that fills in machine translation and completes itself (a project chooses it in the **New project** form and keeps it) | Workflows and steps are built on the **Workflows** page; **Raw MT** is built in | Copied | Copied | Managers |
| **Analysis weights**, **Notification rules** | ✓ | — | — | Owners and admins |
| Email notification mute, theme, palettes | — | — | — | Each member, for themselves |

On the **New project** form the **Translation memory** and **Machine translation** blocks start unanswered: the **TM profile** dropdown and the **MT router** are empty, and each *on start or upload* switch is ticked but locked. Choosing a TM profile or **From scratch** unlocks the translation memory switch and shows the memory list and the match scoring fields; choosing an **MT router** unlocks the machine translation switch. On the form, the project's **Settings** tab and the template editor, a warning icon beside **MT router** lists the target languages the chosen router has no engine for; see [Machine translation](/docs/guides/machine-translation#choose-the-router-for-a-project-or-template). A saved project or template always shows what it has, with both switches editable. Under **Raw MT**, the machine translation switch stays on and locked (“Always on with Raw MT.”), and the project cannot be created until it has an **MT router**. See [Create a project](/docs/guides/projects-and-files#create-a-project).

### How settings move between template and project

Templates and library workflows are **copied** into a project once, so editing them later changes nothing that already copied them. A **TM profile** is the exception: it stays linked, and every follower picks up a change at once. See [Understand what "applied" means](/docs/guides/templates#understand-what-applied-means).

Linguists never open project or template settings; they only see the result in the editor.
