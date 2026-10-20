# Roadmap

Updated: 2026-10-19

This page shows what we work on now and what comes next. It is not a commitment: there are no dates, and the order can change. We build first what our Early Access Program participants need most.

What connects today: AI assistants over MCP (Claude, ChatGPT, Claude Code, Codex), the customer API, ModelFront, OpenAI and Claude for AI steps, DeepL and Google Translate for machine translation, and Google and Microsoft sign-in. What does not connect yet: content systems. The CMS integrations shown in the app and on openloc.com are placeholders. That work starts after launch, and a connector is listed as available here only after it has run in production with a team that uses that system.

## Features

Each line carries the area it belongs to.

- `editor` Progress per job and workflow step: approved, edited and remaining words, with a CSV download (in progress)
- `editor` Select many segments and approve them at once
- `editor` Combine several filters at once: status, origin, QA result
- `editor` Mark a QA finding as a false positive so it stops showing
- `editor` Date, time and author on translation memory matches in the Resources tab
- `editor` A fuzzy matches view that shows what differs
- `editor` Split and join segments
- `editor` Track changes: see what changed since the previous workflow step
- `editor` Comments from the source file shown next to the segment
- `files` Protect text that follows a pattern, such as `{name}` or `[1]`, as a tag
- `files` Excel: choose which columns hold the source and the target text
- `files` Segmentation rules you can adjust, with shorter segments by default
- `files` PowerPoint: chart text and slide masters
- `files` Markdown: formatting and links protected as tags
- `memory` Search and filters on the translation memory and term base lists
- `memory` Newly confirmed segments show up in the memory faster
- `memory` Use a memory or term base in the reverse language direction
- `qa` Custom checks with regular expressions in QA profiles
- `qa` Stricter number checks: decimals, thousands separators, units
- `workflow` Workflow templates
- `workflow` Default assignee per workflow step and target language
- `workflow` Lock segments across a project after it is created
- `workflow` Reset segment approval when a job moves to the next step
- `analytics` Projects delivered on time versus overdue
- `analytics` Metrics for every label, not only the built-in ones
- `analytics` Linguist quality from reviewer edits
- `analytics` Dashboards in Power BI, Tableau, Looker and Snowflake
- `connector` Adobe Experience Manager, Sitecore, Contentful and SharePoint
- `connector` Salesforce Knowledge
- `connector` GitHub
- `connector` Contentstack, Drupal, WordPress and Confluence
- `connector` ServiceNow and Zendesk
- `connector` Paligo and MadCap Flare
- `connector` GitLab and Figma
- `connector` Adobe Commerce and Adobe Marketo Engage
- `connector` Jira and Slack
- `connector` More: Sanity, OpenText, Document360, Intercom, Freshdesk, HubSpot, Tridion, IXIA CCMS, Bitbucket, Adobe InDesign, Salesforce Marketing Cloud, Akeneo, Plytix, n8n, Adobe Workfront, monday.com, MultiTerm, Quickterm, TermWeb, Trados Studio
- …

The list grows as we go. Items are added as teams ask for them and move up as we start on them.

## Bugs

The ones worth knowing about before you plan around them. Each is real, reproducible and on our list.

- `editor` Applying a fuzzy match from the Resources tab drops its match label.
- `editor` Segmentation often leaves several sentences in one segment, and the rules cannot be changed yet.
- `files` Bilingual XLIFF export writes auto-approved segments as final, and statuses from files made in other tools are not kept on import.
- `memory` Japanese and Chinese terms are found only when they are separated by spaces.
- `qa` The QA number check does not report a dropped decimal point (25.16 becomes 2516).
- `qa` The punctuation QA check reports a missing full stop on correct Chinese, Japanese and Korean translations.
- `workflow` The Read and Write boxes on a term base assignment have no effect.
- `workflow` Project history and deadline reminder emails show deadlines in UTC or server time, not in your time zone.

This is not the full list. Found another one? Report it from inside the app, or open an issue here.

Something missing? Open an issue or get in touch: https://openloc.com/contact/
