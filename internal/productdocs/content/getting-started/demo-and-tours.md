---
title: "The Playground and guided tours"
summary: "Try every feature on sample data, and take a tour that shows the app one step at a time."
order: 40
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-14
sources:
  - web/src/components/guide/**
  - web/src/components/demo/**
  - web/src/components/OrgSwitcher.tsx
  - web/src/components/chat/ChatDemoPanel.tsx
  - web/src/pages/LearnPage.tsx
  - web/src/pages/ProjectList.tsx
  - web/src/lib/demo.ts
  - web/src/lib/guide*.ts
  - web/src/lib/playbooks.ts
  - internal/chat/playbooks/**
  - internal/chat/speech.go
related:
  - getting-started/interface-tour
  - getting-started/first-project
  - guides/ai-assistant
---
## The Playground

In the Playground you try every feature on sample data: projects in every state, a job after AI review, translation memories, a glossary, MT and AI setup, workflows, templates and dashboards. Every signed-in user can open it. There you act as an owner, so every page is open to you.

The Playground and the guided tours are switched on for the whole server. When they are off, the app doesn't offer them.

Look around freely. Nothing you do there is saved: when you try to change something, a message says **This is the Playground. Changes won’t be saved.**

To open it, do one of these:

- Hover your organization's name at the left end of the top bar and click **Playground**.
- Start one of the guided tours below. They always run in the Playground.

A banner at the top of every page there says you are in the Playground. Click **Back to** followed by your organization's name to return; the same item is in the organization menu at the left end of the top bar, and a window offers it when a tour ends. When the button reads **Leave the Playground**, it takes you to your organization, or asks you to pick one.

In the Playground the assistant does not answer questions. Its button, **See what the assistant does**, opens an example conversation instead. In your own organization the assistant answers from your data; see [The AI assistant](/docs/guides/ai-assistant).

## Take a guided tour

A guided tour moves through the app and puts a short card next to one part of the screen at a time. It needs no AI key.

You can start one from:

- **Learn**, in the account menu at the right end of the top bar. The page lists every tour your role can take; click **Start tour** on one.
- **New here? Take the tour**, under an empty project list. It starts the product overview.

Outside the Playground, starting a tour first opens the Playground, then the tour begins there. The tours you see depend on your role:

| Tour | Roles |
|---|---|
| **Product overview demo** | Owner, admin, project manager |
| **AI prompts tour** | Owner, admin, project manager |
| **Dashboards tour** | Owner, admin, project manager |
| **Assistant and MCP tour** | Every role |

On each card, **Next** and **Back** move between steps, the counter shows where you are (for example **3 of 8**), and the **✕** ends the tour.

## Listen to a tour

When your own organization has an OpenAI key connected, each tour card has a **Voice** button. Click it to hear the step read aloud. With the voice on, **Auto-play** also appears: turn it on and the tour moves to the next step when the reading ends. Both are off at first, and this browser remembers your choice. If the browser blocks the sound, the card says **Click Voice to play.**
