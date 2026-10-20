---
title: "Integrations"
summary: "Content system connectors, and where to check that an AI provider is connected."
order: 140
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - web/src/pages/IntegrationsPage.tsx
  - web/src/lib/cms-connectors.ts
  - web/src/components/ai/AIVendorsSection.tsx
  - web/src/components/ai/vendor-catalog.ts
limitations:
  - cms-integrations
related:
  - guides/mcp
  - guides/machine-translation
  - concepts/quality-estimation
---
The **Integrations** page lists the content systems (CMS) OpenLoc is built to connect to, so content can be pulled in for translation and pushed back when it is done. None is connected yet.

## What the page shows

Open **Integrations** in the left menu; owners, admins and project managers see it. Each row shows a system, its category and whether it is connected.

| Connector | Category |
|---|---|
| Adobe Experience Manager | Enterprise DXP |
| Adobe Commerce | Commerce |
| Adobe Marketo Engage | Marketing |
| WordPress | Traditional CMS |
| Contentful | Headless CMS |
| Contentstack | Headless CMS |
| Sitecore | Enterprise DXP |
| Drupal | Traditional CMS |
| GitHub | Code & docs |

The status badge reads **Connected** for systems that are linked to your organization and **Disconnected** for the rest. The **Connect** button sends us a request for that system. Tell us which one you need — every request helps us decide what to build next.

## Check that an AI provider is connected

A provider that reviews translations can run [quality estimation and post-editing](/docs/concepts/quality-estimation), and each one needs its own API key for your organization — nothing runs before somebody adds one.

Open the menu under your name and choose **AI settings**. The **Providers** section holds one tile per provider; machine translation providers (DeepL, Google Translate) only translate. Owners and admins manage this section; project managers see it read-only.

- A lit tile of a provider that reviews translations means it is connected and can be picked for a QE+APE step.
- A dimmed tile means no key is stored. A step pointing at that provider approves nothing, and its machine translations go straight to human review.

Click a tile to open its **Keys** list and add or replace the API key.

### What leaves your organization

While a check runs, the segments being judged go to the provider you picked for that step. No provider receives anything once the check is done.

Read more in [Quality estimation](/docs/concepts/quality-estimation) and [Machine translation](/docs/guides/machine-translation).

## Connect AI tools instead

Connectors on this page are for content systems. To let Claude or another AI assistant work with your projects, memories and terms, use a personal access key or a connected app. See [MCP: connect AI tools](/docs/guides/mcp).
