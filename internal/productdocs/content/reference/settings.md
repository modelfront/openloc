---
title: "Settings"
summary: "Personal, organization and AI settings, and where each option lives."
order: 50
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-11-03
sources:
  - web/src/pages/SettingsPage.tsx
  - web/src/pages/org/**
  - web/src/pages/AISettingsPage.tsx
  - web/src/components/SettingsEditor.tsx
  - web/src/components/settings/TMProfilePicker.tsx
  - web/src/components/settings/FileConfigurationsSection.tsx
  - web/src/lib/asset-links.ts
  - web/src/components/settings/AppearanceSection.tsx
  - web/src/components/ai/ConnectedAppsList.tsx
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
  - internal/filterprofile/**
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

Each section on **Settings** and on the **Organization** page sums up its state next to its title, for example **Registration · Open to @example.com**.

## Personal Settings

Account menu → **Settings**. Every role has this page. The options apply to you only.

| Section | Option | Notes |
|---|---|---|
| **Profile** | **Name**, **Email** | **Email** cannot be changed. Notifications are emailed to this address. **Save changes** saves your name. |
| **Email notifications** | **Task assignment**, **Step rejection**, **Workflow completion**, **Deadline approaching** | Untick to stop the email for that event. In-app notifications still arrive. See [Notifications](/docs/guides/notifications). |
| **Appearance** | **Light**, **Dark**, **System** | The theme. System follows your computer. |
| | **Dark palette**, **Light palette** | The colour set for each theme. Click one to select it. **New palette** creates your own; each palette has a button to clone it, and the ones you made also have buttons to edit or delete them. Palettes are saved to your account and follow you across organizations. |

There is no password field here. To set a new password, use **Forgot password?** on the sign-in page.

## Organization settings

Account menu → **Organization settings** opens the **Organization** page. It has two tabs: **Members** and **Settings**. Only owners and admins see it. Changes apply to every member.

| Tab | Section | Option | Notes |
|---|---|---|---|
| **Members** | **Members** | Search by name or email; **Role**, **Source** and **Target** filters; 10 members per page, page numbers under the list. Click a member's row: **Role** and **Language pairs**, **Save** | **Owner**, **Admin**, **Project manager** or **Linguist**. Changing someone to Linguist asks for confirmation, because they lose access to every management page. |
| | **Vendors** | Search by vendor name or admin email; **Source** and **Target** filters; 10 per page | The vendor organizations you invited, with their admins, the languages their people work in and the date each joined. |
| | | **Language pairs** | The pairs a member works in, shown as chips next to the name. |
| | **Invite member** (button at the top of the members list) | A window: **Email**, **Role** (**Admin**, **Project manager** or **Linguist**), **Send invite** | |
| | **Invite vendor** (button at the top of the vendors list) | A window: **Vendor name**, **Admin email**, **Send invite** | Creates a separate vendor organization linked to yours; the person you invite becomes its admin. See [Invite a vendor](/docs/guides/customers-and-vendors#invite-a-vendor-owner-or-admin). |
| | **Pending invites** | **Resend invite**, revoke | Each invite shows its expiry date. A vendor invite shows the vendor name and a **Vendor org admin** label instead of a role. See [Resend an invitation](/docs/guides/customers-and-vendors#resend-an-invitation). |
| | **Accepted invites** | Search by email or vendor name; 10 per page | Who accepted, and when, newest first. |
| **Settings** | **Registration** | **Invite only** / **Anyone with organization email can join** | When open, people who sign in with your email domain join as project managers. |
| | **Notification rules** | **Task assignment**, **Step rejection**, **Workflow completion**, **Deadline approaching** | Master switches. Off means no notification of that kind for anyone, in the app or by email. |
| | **Analysis weights** | One percentage per match band, from **100% (exact)** to **New (no match)**, plus **Repetitions** and **QE approved** | How much effort each band counts for in weighted words. 0% is free, 100% is full effort. See [Analysis bands](/docs/concepts/analysis-bands). |

### In a vendor organization

A vendor organization's admin sees a smaller Organization page, with the **Members** tab only; see [Run a vendor organization](/docs/guides/customers-and-vendors#run-a-vendor-organization).

## AI settings

Account menu → **AI settings**. Every role can open the page, but each section has its own owners.

| Section | Who | What it holds |
|---|---|---|
| **Providers** | Owners and admins; project managers see it read-only | The organization's connections to AI providers and the keys that authenticate there. Keys are stored encrypted and checked when saved. A key marked **Use for chat** powers the built-in assistant. Engines on the **AI** page and on the **MT** page each name a key from here. Removing a key is allowed even while engines use it — they are marked **No key** until you point them at another one (see [Machine translation](/docs/guides/machine-translation#add-a-provider-key-owner-or-admin)). Providers this deployment supplies itself show **No key required**. |
| **Connect an AI tool** | Everyone | The **MCP server address** with a copy button, for AI tools that support MCP. **Open Claude connectors** and **Open ChatGPT plugins** open the page in those two tools where you paste it. The tool then signs in as you. Under it, **Connected apps** lists the tools you connected, with **Read-only** or **Read and write** and last use; **Disconnect** stops one at once. See [Connect external AI tools](/docs/guides/mcp). |
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
| **Segmentation profile** (in the **File configurations** block): how files uploaded afterwards are split into segments; required, and only profiles that suit the source language are offered. See [Segmentation profiles](/docs/guides/segmentation) | Defined under **File configurations** → **Segmentation profiles** | Chosen | Chosen | Managers |
| **Filter profiles** (in the **File configurations** block, under **Segmentation profile**): one picker per format (**HTML**, **Excel**, **JSON**, **PO**, **Plain text**, **XML**) that decides how files of that format are read. The list opens from its heading, which names the formats that have a pick. A new project or template without a template starts each format at your organization's default, else the built-in **Default**; picking **Default** clears the pick. A change applies to files uploaded afterwards. See [Filter profiles](/docs/guides/filter-profiles) | Defined under **File configurations** | Chosen | Chosen | Managers |
| **Linguists can add terms** (in the **Terminology** block, above the term-base rows): **No**, **Yes, saved as pending** (the default) or **Yes, saved as approved**. Decides whether linguists may add terms from the editor, and the status their terms get. Managers always can, and pick the status themselves. The picker is locked until a term-base row has **Write** ticked. See [Add a term while translating](/docs/guides/terminology#add-a-term-while-translating) | — | ✓ | ✓ | Managers |
| **Translation memory** rows and match scoring (**Fuzzy match threshold**, **Language-variant mismatch penalty**, **Near-exact penalty**, **TM lookup order**; see [Several memories](/docs/concepts/tm-matching#several-memories)) | TMs live on the **Translation memories** page | ✓ | ✓ | Managers |
| **TM profile**: a shared TM setup to follow, or **From scratch** for a list this project keeps alone | Profiles are created on the **Translation memories** page | Followed | Followed | Managers |
| **Machine translation**: the **MT router** that picks the engine per target language, or empty for no machine translation | Routers and engines live on the **MT** page | ✓ | ✓ | Managers |
| **Terminology** rows. Terms added from the editor go only to a term base with **Write** ticked | Term bases live on the **Terminology** page | ✓ | ✓ | Managers |
| **Workflow**: the **Workflow** / **Raw MT** toggle on the **Workflow** tab. **Workflow** takes the steps of a library workflow; **Raw MT** is one automated **Pre-translation** step that fills in machine translation and completes itself (a project chooses it in the **New project** form and keeps it). A project needs one of the two; a template may have neither | Workflows and steps are built on the **Workflows** page; **Raw MT** is built in | Copied, optional | Copied, required | Managers |
| **Analysis weights**, **Notification rules** | ✓ | — | — | Owners and admins |
| Email notification mute, theme, palettes | — | — | — | Each member, for themselves |

On a project or template, every asset it uses links to where it lives, in a new tab. A translation memory or term base name in its row is a link. The **TM profile**, **MT router**, **QA profile**, **Segmentation profile**, **Workflow** and per-format **Filter profiles** dropdowns each end with an open icon (hover for "Open QA profile", "Open HTML filter profile" and so on). The MT router link opens the **MT** page with that router's card highlighted. The QA profile link opens the **QA profiles** page with that profile highlighted and its rules expanded. The workflow link opens the **Workflows** page with that workflow's **Edit workflow** dialog open; a built-in workflow has no icon.

On the **New project** form the **Workflow** dropdown starts empty, and **Create project** points at it, framed red, until you pick a workflow or **Raw MT**. **Save as template** works with or without one. The **Translation memory** and **Machine translation** blocks start unanswered: the **TM profile** dropdown and the **MT router** are empty, and each *on start or upload* switch is ticked but locked. Choosing a TM profile or **From scratch** unlocks the translation memory switch and shows the memory list and the match scoring fields; choosing an **MT router** unlocks the machine translation switch. On the form, the project's **Settings** tab and the template editor, a warning icon beside **MT router** lists the target languages the chosen router has no engine for; see [Machine translation](/docs/guides/machine-translation#choose-the-router-for-a-project-or-template). A saved project or template always shows what it has, with both switches editable. Under **Raw MT**, the machine translation switch stays on and locked (“Always on with Raw MT.”), and the project cannot be created until it has an **MT router**. See [Create a project](/docs/guides/projects-and-files#create-a-project).

### How settings move between template and project

Templates and library workflows are **copied** into a project once, so editing them later changes nothing that already copied them. A **TM profile** is the exception: it stays linked, and every follower picks up a change at once. See [Understand what "applied" means](/docs/guides/templates#understand-what-applied-means).

Linguists never open project or template settings; they only see the result in the editor.
