---
title: "Glossary of terms"
summary: "Short definitions of the words the TMS uses: segment, job, leverage, band, context match and more."
order: 60
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - docs/architecture.md
  - internal/mtrouter/**
  - internal/mtengine/**
  - internal/airouter/**
  - web/src/lib/segment-status.ts
  - web/src/components/SegmentRow.shared.tsx
  - web/src/components/ai/APITokensSection.tsx
  - web/src/components/ai/vendor-catalog.ts
related:
  - getting-started/welcome
  - reference/segment-statuses
  - concepts/tm-matching
  - concepts/analysis-bands
---
The words you meet in the product, in alphabetical order.

| Term | Meaning |
|---|---|
| **Access key** | A secret key you create under **AI settings → Access keys** so a tool that cannot sign in can act as you. See [MCP: connect AI tools](/docs/guides/mcp). |
| **AI router** | What a Hybrid or AI workflow step points at. It maps target languages to the AI engines that check them, with an optional default engine for the rest. See [AI engines and prompts](/docs/guides/ai-engines-and-prompts). |
| **Analysis** | A word count sorted into bands by how much the translation memory helps. It predicts effort. See [Analysis bands](/docs/concepts/analysis-bands). |
| **Assignee** | The person responsible for a workflow step on a job. |
| **Automatic post-editing (APE)** | A check that goes one step past judging a translation and proposes a corrected version. Any AI engine whose output is APE or QE+APE can do it. Fixed segments show the **APE** chip. |
| **Band** | A group in the analysis: Repetitions, 101%, 100%, 85–99%, 65–84% or New. It says how close the best memory match is. |
| **Bilingual file** | A file with both source and target text, such as an XLIFF or a two-column Excel sheet. Its target text is imported with the **File** origin. |
| **Concept** | One entry in a term base: one idea, with a term for it in each language. See [Terminology](/docs/guides/terminology). |
| **Concordance** | A search of the translation memory for a word or phrase from inside the editor. See [the CAT editor](/docs/guides/cat-editor#search-the-tm-with-concordance). |
| **Customer** | The client you translate for. A built-in label, so a project carries it as one of its labels. See [Labels and vendors](/docs/guides/customers-and-vendors). |
| **Label** | A named set of choices a project is classified by — **Customer** and **Domain** come built in, and an organization adds its own. A project takes at most one value per label. See [Labels and vendors](/docs/guides/customers-and-vendors). |
| **Dashboard** | A page of saved charts you build with **New chart** or by asking the AI assistant. See [Analytics](/docs/guides/analytics). |
| **Deadline** | The due date of a workflow step on a job. New files inherit the step's default. |
| **File** | A document you upload to a project. It becomes one job per target language. See [Projects and files](/docs/guides/projects-and-files). |
| **Forbidden term** | A translation marked **Forbidden** in a term base. The editor warns when it appears and QA reports it. |
| **Job** | One file in one target language, with a workflow, assignees, a deadline and a progress bar. |
| **Leverage** | How much of a job the translation memory already covers. High leverage means little new work. |
| **Locale (language variant)** | A language plus a region, such as `pt-BR`. A match from another variant is filled in but not confirmed. |
| **Machine translation (MT)** | Translation produced by software. Filled segments show the **MT** chip. See [Machine translation](/docs/guides/machine-translation). |
| **Match** | A memory entry similar to the segment. **Exact** (100%): identical source text. **Context** (101%): the neighbours match too. **Fuzzy** (below 100%): similar; the number is the similarity after penalties. See [TM matching](/docs/concepts/tm-matching). |
| **MCP** | A standard that lets AI assistants such as Claude use your TMS as a tool. See [MCP: connect AI tools](/docs/guides/mcp). |
| **MT engine** | A machine translation setup you build on the **Engines** tab of the **MT** page: a name, a provider, one key from **AI settings** and that provider's options. The same key can back several engines that translate differently. An MT router picks the engine for each target language. |
| **MT router** | What a project points at for machine translation. It maps target languages to MT engines, with an optional default engine for the rest. See [Machine translation](/docs/guides/machine-translation). |
| **Organization** | Your company or team. Projects, memories, terms, members and settings belong to one organization. |
| **Origin** | Where a segment's text came from when no person typed it: TM, MT, APE or File. Shown as a chip in the **Match** column. |
| **Pre-translation** | The automatic fill of a job: memory first, then machine translation. Runs on upload or with **Apply TM** and **Apply MT**. |
| **Project** | One piece of work: a source language, target languages, files and the settings that decide how they are translated. |
| **QA check** | One automatic test on a segment, for example **Empty target** or **Terminology**. See [Quality assurance](/docs/guides/quality-assurance). |
| **QA profile** | A named set of QA checks with a severity for each. A project or template uses one profile. |
| **Quality estimation (QE)** | A check that decides whether a translation is good enough to approve without a person reading it. Any AI engine whose output is QE or QE+APE can do it, from any connected provider that reviews translations. Good segments become **Auto-approved**. See [Quality estimation](/docs/concepts/quality-estimation). |
| **Repetition** | A source text that appears more than once in a file. Confirming the first copies the translation to the others. |
| **Roles** | **Owner**, **Admin**, **Project manager** and **Linguist**. Owners and admins run the organization, project managers run projects, linguists work on assigned tasks. See [Roles and permissions](/docs/reference/roles-and-permissions). |
| **Segment** | One unit of text in a file, usually a sentence or a paragraph. Translation happens segment by segment. |
| **Source and target language** | The source language is the language of the uploaded file. Target languages are the ones you translate into. |
| **Status** | How far a segment is: **Untranslated**, **Pretranslated**, **Draft**, **Auto-approved** or **Approved**. A person approves with Ctrl+Enter (Cmd+Enter on Mac); an exact memory match is approved for you. See [Segment statuses](/docs/reference/segment-statuses). |
| **Tag** | A chip in a segment that stands for something that is not text: a link, a line break, bold. See [Work with tags](/docs/guides/cat-editor#work-with-tags). |
| **Task** | One workflow step on one job, as seen by the person assigned to it. The **Tasks** page lists yours. |
| **TBX** | The exchange format for terminology. Term bases import from and export to it. See [Terminology](/docs/guides/terminology#import-and-export). |
| **Template** | Saved project settings you reuse for new projects. See [Templates](/docs/guides/templates). |
| **Term** | One word or phrase in one language inside a concept. It is **Approved**, **Forbidden** or **Pending**. |
| **Term base** | A list of concepts and their terms. The editor shows matching terms while you type and QA checks them. |
| **TM profile** | A saved set of memory assignments that several projects follow. See [Translation memory](/docs/guides/translation-memory#share-a-setup-with-tm-profiles). |
| **TMX** | The exchange format for translation memories. See [Import and export TMX](/docs/guides/translation-memory#import-and-export-tmx). |
| **Translation memory (TM)** | A store of past source and target pairs, reused when the same or similar text appears again. See [Translation memory](/docs/guides/translation-memory). |
| **Vendor** | An external linguist or agency invited to work on your jobs. Vendors have the linguist role. |
| **Weighted words** | A word count where each band counts at a different rate, so a 100% match counts less than a new word. Used to measure effort. |
| **Workflow and workflow step** | The ordered steps a job goes through, for example Translation then Review. **Complete step** moves the job on. See [Workflows and tasks](/docs/guides/workflows-and-tasks). |
| **XLIFF** | The exchange format for bilingual files. You can upload it, download any job as one, and re-import an edited copy. See [Supported file formats](/docs/reference/file-formats). |
