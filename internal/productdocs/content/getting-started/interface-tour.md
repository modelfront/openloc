---
title: "A tour of the interface"
summary: "The left menu, the top bar, notifications, themes and the account menu."
order: 30
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - web/src/App.tsx
  - web/src/components/layout/NavRail.tsx
  - web/src/components/NotificationBell.tsx
  - web/src/components/OrgSwitcher.tsx
  - web/src/components/layout/ThemeToggle.tsx
  - web/src/components/demo/DemoBanner.tsx
  - web/src/components/guide/**
  - web/src/pages/SettingsPage.tsx
  - web/src/pages/AISettingsPage.tsx
  - web/src/components/settings/AppearanceSection.tsx
  - web/src/pages/docs/DocsPage.tsx
  - web/src/pages/docs/DocsSidebar.tsx
  - web/src/pages/docs/DocsSearch.tsx
  - web/src/pages/docs/DocsArticle.tsx
  - web/src/pages/api-docs/ApiDocsPage.tsx
  - web/src/components/chat/ChatPanel.tsx
  - web/src/components/ui/input.tsx
  - web/src/components/ui/textarea.tsx
related:
  - getting-started/welcome
  - getting-started/first-project
  - getting-started/demo-and-tours
  - guides/notifications
  - guides/ai-assistant
  - reference/settings
  - reference/roles-and-permissions
screenshots:
  - projects-list.png
---
Every page in OpenLoc shares the same frame: a menu down the left side, a thin bar across the top of the page content, and the content in the middle. Sign in and follow along on your own screen.

![The Projects page, with the left menu and the top bar](assets/projects-list.png)

## The left menu

At the top of the left menu is the name **OpenLoc**; click it to return to your home page. Below it, the menu stays in place while you move between pages. The current page has a grey background. As a manager (owner, admin or project manager) you see these entries, in this order:

| Entry | What it opens |
|---|---|
| **Projects** | All projects of the organization. This is your home page. |
| **Tasks** | Workflow steps assigned to people. Owners and admins see every task in the organization; project managers and linguists see their own. |
| **Translation memories** | Your translation memories and TM profiles. |
| **Terminology** | Term bases and their terms. |
| **Labels** | How projects are classified: Customer, Domain, and any label your organization adds. |
| **Templates** | Saved project settings for reuse. |
| **Workflows** | Reusable workflows and the step library they are built from. The page has two tabs, **Workflows** and **Steps**. |
| **MT** | Machine translation: routers, engines and prompts. |
| **AI** | Quality estimation and post-editing setup, in three tabs: **Routers** (which AI engine reviews each target language), **Engines** and **Prompts**. |
| **Integrations** | Connections to content systems. |
| **QA profiles** | Rule sets for automatic quality checks. |
| **Dashboards** | Live charts you build with **New chart** or the AI assistant. Three tabs: **Organization dashboards**, **My dashboards** and **Pinned**. |

If you are a linguist, your menu holds one entry: **Tasks**. Every other address sends you back to it. The [Roles and permissions](/docs/reference/roles-and-permissions) page lists exactly what each role can do.

To give the page more width, click the panel button at the right end of the menu's top row, next to the name. The whole menu shrinks to a strip of icons: the name disappears, and every entry keeps only its icon — hover one to read its name. The top of the strip then holds only the same button; click it to bring the full menu back. This works on every page, and your choice is remembered in this browser. On a phone the menu always starts as the strip of icons. Opened, it lies over the page and closes again when you pick a page or tap beside it.

## The top bar

The bar shows your organization at its left end and three controls at its right end.

### The organization switcher

The left end of the bar shows the name of the organization you are working in. Hover over it and a small menu opens with the organizations you belong to. The current one has a check mark. Click another one to switch — the app reloads and shows that organization's projects and settings. Below a line, the menu also offers **Playground**: a shared organization with sample data, where you can look around freely. A banner on every page there offers the way back to your organization. See [The Playground and guided tours](/docs/getting-started/demo-and-tours).

### The notification bell

The bell shows a pale red badge with the number of unread notifications. Click it to open a small panel named **Notifications**; press Esc or click anywhere outside it to close the panel. Unread items have a dot on the right and a light background. **Mark all read** clears the badge. The badge updates live, so you do not need to reload the page when someone assigns you a step. [Notifications](/docs/guides/notifications) explains which events create a notification and how to mute emails.

### The theme toggle

Next to the bell is a small sun, moon or monitor icon. Click it to cycle through the three themes: **Light**, then **Dark**, then **System**. System follows the setting of your computer. Hover over the icon to see the current theme, for example "Theme: Dark". You can also pick a theme, and choose colour palettes, in your personal Settings under **Appearance**.

### The account menu

The last control shows your name. Click it to open the account menu. The top of the menu shows your name, your email address and the organization you are working in right now. Below that:

- **Settings** — your profile, email notifications, appearance and connected apps.
- **Organization settings** — the **Organization** page, in three tabs: **Members** (members and invites, the tab it opens on), **Settings** (registration, notification rules and analysis weights) and **Vendors** (external linguists you invite). Owners and admins see all three. Project managers see this entry too, and it opens **Vendors** only.
- **AI settings** — under **Providers**, the organization's AI provider credentials (owners and admins manage them, project managers see them read-only); under **Connect an AI tool** and **Access keys**, connecting your own AI tools (everyone).
- **Learn** — the guided tours your role can take. It shows only when the Playground is on. See [The Playground and guided tours](/docs/getting-started/demo-and-tours).
- **Docs** — these documentation pages. Every role sees the same pages.
- **API** — documentation for connecting your own systems to OpenLoc with an access key. One sidebar holds the guides and every endpoint, and one search covers both. Every role sees it.
- **Sign out**.

The [Settings](/docs/reference/settings) reference describes every option on the three settings pages.

## Text fields

When a single-line field holds text, a small **✕** appears at its right-hand end. Click it to empty that field. It clears only the field it sits in — on a search box the results return to the full list, while any filters you set stay on. Password boxes, number boxes and boxes for longer text, such as a comment or a prompt, have no **✕**.

## The Docs section

Open the account menu at the top right and click **Docs**. The docs open in a new tab, without the app's left menu; the button at the top right, **Back to app** or **Open app**, returns to the app. You land on an overview of all sections. The docs pages have their own smaller sidebar on the left with one group per section: Getting started, Guides, Reference and Concepts. Every signed-in user sees the same pages, whatever their role — a linguist can read how projects are set up, and a manager can read the editor guide. To start a guided tour, see [The Playground and guided tours](/docs/getting-started/demo-and-tours).

1. Click **Search docs** at the top of the docs sidebar, or press Ctrl+K (Cmd+K on Mac) — the same keys close the search box again. The shortcut works only while you are in Docs. Type at least two characters. Each result shows the page title, its section and a short snippet. Click a result or press Enter to open it.
2. Open any page. Under the title you see a one-line summary and **Updated** with the date of the last change. On the right, **On this page** lists the headings, so you can jump to one part of a long page.
3. Scroll to the bottom of a page. **Related** lists pages that go well with the one you are reading.

Docs works the same on every device. On a narrow screen the docs sidebar is hidden, and with it the **Search docs** button — press Ctrl+K (Cmd+K) to search, or widen the window.

## The AI assistant

The round button at the bottom right of any app page opens the built-in AI assistant. It can answer questions about your projects, jobs and deadlines, and it can search these docs for you. Click the button to open the chat; click **Minimize** to keep the conversation for later, or **Close** to end it. The [AI assistant](/docs/guides/ai-assistant) guide explains what it can and cannot do.

Next, follow [Your first project](/docs/getting-started/first-project) to upload a file and get a translation back.
