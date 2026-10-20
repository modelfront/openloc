---
title: "AI engines and prompts"
summary: "Set up the AI engines that check and post-edit translations, the routers that pick an engine for each target language, and the prompts that tell the model what to send back."
order: 75
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - web/src/pages/ai/**
  - web/src/components/ai/AIRoutersSection.tsx
  - web/src/components/routers/RoutersSection.tsx
  - web/src/components/workflow/WorkflowStepChips.tsx
  - web/src/components/ai/AIEnginesSection.tsx
  - web/src/components/ai/AIPromptsSection.tsx
  - web/src/components/ai/PromptForm.tsx
  - web/src/components/ai/PromptFormats.tsx
  - web/src/components/ai/mode-cards.tsx
  - web/src/components/ai/segment-context.tsx
  - web/src/components/ai/LanguageAdditionsField.tsx
  - web/src/components/ai/PromptSends.tsx
  - web/src/components/ai/output-mode.ts
  - web/src/components/ai/model-selection.tsx
  - web/src/components/ai/model-preset-ui.tsx
  - internal/aiengine/**
  - internal/aiprompt/**
  - internal/airouter/**
  - internal/segmentprompt/**
  - internal/ai/**
  - internal/keyattach/**
  - web/src/components/ai/vendor-catalog.ts
related:
  - concepts/quality-estimation
  - guides/workflows-and-tasks
  - guides/machine-translation
  - reference/settings
  - reference/roles-and-permissions
screenshots:
  - ai-routers.png
  - ai-engines.png
  - system-prompts.png
---
The **AI** page in the left menu has three tabs. **Engines** holds the AI engines that check and fix translations, **Prompts** the instructions they follow, and **Routers** the AI routers that send each job to an engine by its target language. A workflow step uses a router, and the engine it picks does its work on its own, with no one assigned. For what the checking itself means, read [Quality estimation](/docs/concepts/quality-estimation).

Owners and admins can create, edit and delete everything on this page. Project managers can read it. Linguists do not have the page.

Engines that *translate* are not here. An LLM translation engine and its prompt live on the **MT** page, next to the other things that translate — see [Machine translation](/docs/guides/machine-translation).

## Know the three parts

| Part | What it is |
| --- | --- |
| AI router | The thing you attach to a workflow step. It maps target languages to engines, with an optional default engine for every other language. |
| AI engine | The thing that does the checking. It names a provider key and, for a language model, a model and the prompts to use. |
| System prompt | The standing instructions for the model, the choice of what it sends back, and what each segment carries with it. |

One system prompt can be used by any number of AI engines, so the wording stays the same everywhere.

## Create an AI engine

You need a key for the provider in **AI settings** first. Every provider needs its own.

1. On the **Engines** tab, click **New AI engine**.
2. Give it a name.
3. Pick the **Provider** and the **Model**.
4. Pick the **System prompt** — any prompt on the **Prompts** tab that reviews translations. See [Write your own system prompt](#write-your-own-system-prompt).
5. Click **Create**.

![The AI engines list with one review engine per provider, each row showing its provider, its model where it has one, and what it returns.](assets/ai-engines.png)

Each row carries the provider or model it runs on and what it returns, so two similar names stay apart.

To change an engine later, click its card. Deleting one asks you to confirm; remove it from every AI router first, or the delete is refused.

When its key is removed, an engine keeps its settings and routes, is marked **No key**, and stops working: a step that reaches it fails with a message that names the engine and tells you to choose a key for it in **AI** → **Engines**. Open the engine and pick a key under **Provider**. A new key for that provider also offers itself to the engines without one — see [Add a provider key](/docs/guides/machine-translation#add-a-provider-key-owner-or-admin).

## Choose what the model sends back

Every system prompt has an **output** — the kind of answer it asks for. The **Prompts** tab shows it as a small chip, and so does the **System prompt** picker on an engine, both in its list and next to the chosen prompt.

| Chip | What the model does |
| --- | --- |
| QE | Judges each translation and approves or rejects it with a score. |
| QE+APE | Judges each translation and post-edits it when it needs fixing. |
| APE | Post-edits every translation, correcting defects in place. It never approves anything, so a person reviews every segment it touches. |
| MT | Translates the source segment from scratch. |

The output decides what the engine can be used for, so you never pick that separately: a prompt that only translates makes a translation engine, and anything else makes a quality-review engine. This is fixed once the engine is created.

## Write your own system prompt

The **Prompts** tab lists your own prompts. Some deployments also supply built-in ones; those are read-only — open one and use **Duplicate** to get a copy you can edit. A prompt opens on its own page, so you can link to it.

1. Click **New prompt**, or **Duplicate** on a prompt that is close to what you want.
2. Click one of the **What this prompt does** cards: **Quality estimation**, **Quality estimation and automatic post-editing**, or **Automatic post-editing**. The type becomes a fixed label — the chip shown in the list — and the remaining fields appear with matching starter instructions. A duplicate starts with the original type and instructions.
3. Keep the name the type gives it, or type your own.
4. Write the **Base prompt** in plain words. Collapse it when you want to focus on the other sections. Use the small **Insert** buttons to drop `<source_lang>` and `<target_lang>` into the text: each is replaced with the job's language when the prompt runs, as a name and a code — an English to Austrian German job sends `English (en)` and `Austrian German (de-AT)`. Hover over a token, or focus it with the keyboard, for help.
5. Click **Create**. The type is fixed after creation and appears as a label when you reopen the prompt.

![The Prompts tab, each row with its output chip; built-in prompts offer only Duplicate](assets/system-prompts.png)

A built-in prompt offers **Duplicate**. Delete your own prompts from the list. Rename a saved prompt with the pencil beside its name in the breadcrumb; its type appears below the breadcrumb. **Update** becomes available after you change the prompt contents. Expand **Output format**, which is read-only and follows the selected type, to see the instructions and, below them, the JSON shape a reply must satisfy. There is no matching section for what goes *in*: a segment reaches the model as JSON with named keys, so **Context** already shows it exactly as it is sent. There is nothing to choose there: every provider that supports a schema is held to the same one, and it follows from the type rather than from the model. An engine on an older model that takes no schema is asked for the shape in words instead, and its own **Prompt preview** shows that. The shape of the answer is not yours to write: the field names and their types are added when the prompt is sent, and enforced as a schema on models that support one. What each field *means* is in your own text, under the instructions — reword it to steer what the model puts in a field. Delete it and the model still gets the field names, so replies keep their shape; only the guidance is gone.

## Choose what each segment carries

Expand **Context** on the prompt page to choose what travels with every sentence. **Source text** is always checked and locked. For prompts that judge or post-edit translations, **Target text** is also checked and locked.

The JSON example below the checkboxes updates to show the selected context with placeholder values. Two things are yours to choose:

| Option | What it sends |
| --- | --- |
| Translation memory match | The closest memory entry: source, target and match score. The score is the one in the Match column, after penalties, so the model can tell a weak or less trusted match from a strong one. |
| Glossary terms | The approved and forbidden terms found in the segment's source text. |

Leave one unticked and it costs nothing — the lookup is skipped entirely. A field with nothing to fill in is left out, so a segment with no memory match does not send the model an empty one.

On a segment with inline tags, the source and the translation carry those tags as the numbered chips the editor shows (see [Work with tags](/docs/guides/cat-editor#work-with-tags)); other segments stay plain text. The prompt needs nothing extra for this.

## Add rules for particular languages

**Additional prompts** on the prompt page holds extra wording that applies only to some jobs. Every rule is listed there, one under the other. Click **Add prompt**, pick the target languages it applies to, and write the text. The rule is appended to the instructions only when a job goes into one of those languages. A language covers its regional variants, so German covers Austrian German. **Add prompt** stays closed until the open rule has a language and some text.

## Change the advanced settings of an engine

Open an engine and click **Advanced**:

- **On bad output** — what happens when the model answers with something unusable: **Reject the segment**, **Add output as a comment**, or **Ignore (leave unchanged)**. New engines start on **Ignore**.

**Prompt preview** shows the full prompt as the model receives it, so you can read it before saving. Where the model offers its own settings, a settings toggle appears next to it and marks how many you have changed.

## Send each language to the right engine with an AI router

1. On the **Routers** tab, click **New AI router**.
2. Give it a name.
3. A new router starts with one route: **All languages** to your first engine. That engine is the default and reviews every language no other route lists. Change the engine if you want another default, or replace **All languages** with specific languages if you do not want a default; without one, the other languages are not checked.
4. Under **Routes**, click **Add route** for each further engine and pick the target languages it reviews. A route is either **All languages** or a list of languages, never both, and every route needs both halves before you can save. A router always keeps at least one route.
5. Click **Create**.

![The AI routers list, one row per router, with New AI router above it.](assets/ai-routers.png)

Only review engines are offered — any engine that is not built for machine translation. For a language-model engine, what it does to a segment follows from its prompt's mode, above: an APE engine post-edits and leaves every segment for a person. A job takes the engine of the rule for its target language; a rule also covers regional variants of its language, so a rule for French serves a Canadian French job unless Canadian French has a rule of its own. Every other language takes the default. With no rule and no default, the job skips the check and its segments are left as they are for the person on the step.

To change a router, click its row. A step points at the router itself, so the change reaches every job that starts the step from then on, including in projects that already exist. Deleting a router asks you to confirm; remove it from every workflow step first, or the delete is refused.

## Put the router to work

An AI router does nothing until a workflow step uses it. Open **Workflows**, edit a step, set its type to **Hybrid** or **AI**, and pick the router under **AI router**. See [Workflows and tasks](/docs/guides/workflows-and-tasks#define-steps-in-the-step-library) for the step itself.

Wherever a workflow lists its steps, a step's router name is a link: it opens the **Routers** tab, scrolls to that router's card and highlights it for a moment.
