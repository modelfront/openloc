# Roadmap

Updated: 2026-11-03

This page shows what we work on now and what comes next. It is not a commitment: there are no dates, and the order can change. We build first what our Early Access Program participants need most.

What connects today: AI assistants over MCP (Claude, ChatGPT, Claude Code, Codex), the customer API, ModelFront, OpenAI and Claude for AI steps, DeepL and Google Translate for machine translation, and Google and Microsoft sign-in. What does not connect yet: content systems. The CMS integrations shown in the app and on openloc.com are placeholders. That work starts after launch, and a connector is listed as available here only after it has run in production with a team that uses that system.

## Features

Each line carries the area it belongs to.

- `editor` Progress per job and workflow step: approved, edited and remaining words, with a CSV download
- `editor` More segment filters and sort orders
- `editor` Bring back an old version of a segment from History, and undo a Replace All
- `editor` Document preview: see the active segment in the file's real layout
- `editor` Comment threads: replies, resolve, @mentions and comments on part of a segment
- `editor` Comments from the source file shown next to the segment
- `editor` File context next to the segment: string key, developer notes, maximum length
- `editor` Join segments across paragraphs, for line breaks left by OCR
- `files` Protect text that follows a pattern, such as `{name}` or `[1]`, as a tag
- `files` Read segment status and match % from XLIFF and SDLXLIFF files
- `files` Re-importing a bilingual file updates its matches and segment status
- `files` Choose the segmentation profile per file
- `files` PowerPoint: chart text and slide masters
- `files` Markdown: formatting and links protected as tags
- `memory` Filters on the translation memory and term base lists
- `memory` Use a memory or term base in the reverse language direction
- `qa` Report double spaces in the target
- `qa` Numbers written as words and as digits count as the same number
- `qa` Optional domain matching for terms, in QA and in the editor
- `qa` Export QA results as one HTML report
- `workflow` Vendor management: onboarding, vendor and LSP profiles, smart assignment
- `workflow` A different workflow per target language in one project
- `workflow` Default assignee per workflow step and target language
- `analytics` Full analysis breakdown by segments, words and characters, with totals
- `analytics` Projects delivered on time versus overdue
- `analytics` Metrics for every label, not only the built-in ones
- `analytics` Linguist quality from reviewer edits
- `analytics` Dashboards in Power BI, Tableau, Looker and Snowflake
- `ai` Copilot as an AI provider
- `ai` OpenLoc apps in the Claude, ChatGPT and Copilot app directories
- `ai` AI steps see more of the document: nearby segments, file and project details, more memory matches
- `platform` A customer portal where customers pre-translate with MT themselves
- `platform` An access role for people in your organization outside the localization team
- `platform` Choose the date format, not only mm/dd/yyyy
- `platform` The OpenLoc interface in more languages
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

- `files` Bilingual XLIFF export writes auto-approved segments as final, and statuses from files made in other tools are not kept on import.
- `memory` Japanese and Chinese terms are found only when they are separated by spaces.
- `qa` The punctuation QA check reports a missing full stop on correct Chinese, Japanese and Korean translations.
- `workflow` The Read box on a term base assignment has no effect.
- `workflow` Project history and deadline reminder emails show deadlines in UTC or server time, not in your time zone.

This is not the full list. Found another one? Report it from inside the app, or open an issue here.

Something missing? Open an issue or get in touch: https://openloc.com/contact/
