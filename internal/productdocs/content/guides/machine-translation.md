---
title: "Machine translation"
summary: "Connect MT engines, route each target language to one with an MT router, and pre-translate with TM first and MT second."
order: 70
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - web/src/pages/mt/**
  - web/src/components/mt/**
  - web/src/components/routers/**
  - internal/mtrouter/**
  - web/src/components/ai/AIVendorsSection.tsx
  - web/src/components/ai/AttachKeyDialog.tsx
  - internal/keyattach/**
  - internal/enginehealth/**
  - web/src/components/ai/AIEnginesSection.tsx
  - web/src/components/ai/model-selection.tsx
  - web/src/components/SettingsEditor.tsx
  - web/src/lib/settings-coverage.ts
  - internal/worker/pipeline_outcome.go
  - internal/pipeline/outcome.go
  - web/src/components/project/project-event-render-workflow.tsx
  - web/src/components/workflow/RawMTPane.tsx
  - web/src/components/EditorSidebar.tsx
  - internal/mt/**
  - internal/mtengine/**
  - internal/mtdefaults/**
  - internal/langcode/**
  - internal/ai/handler.go
  - web/src/components/ai/vendor-catalog.ts
  - web/src/components/project/BulkJobActionsBar.tsx
  - web/src/components/JobPipelineActions.tsx
  - web/src/hooks/useJobPipelineActions.ts
  - web/src/components/SegmentSelectionBar.tsx
  - internal/job/handler_pipeline.go
  - internal/job/handler_rerun.go
related:
  - concepts/how-a-translation-flows
  - concepts/quality-estimation
  - guides/translation-memory
  - guides/templates
  - guides/workflows-and-tasks
  - guides/cat-editor
  - guides/ai-engines-and-prompts
screenshots:
  - mt-routers.png
  - editor-sidebar.png
---
Machine translation (MT) fills in a first draft of every segment that the translation memory cannot cover. For the order in which memory and MT run, see [How a translation flows](/docs/concepts/how-a-translation-flows#stage-3-pre-translation).

## Know the two kinds of engine

The **MT** page in the left menu has three tabs: **Routers**, **Engines** and **Prompts**. The **Engines** tab lists everything your organization can translate with.

| Kind | Examples | Where it is set up |
| --- | --- | --- |
| Provider engines | DeepL, Google Translate | **Engines** tab, from a key you add in **AI settings** |
| LLM translation engines | A large language model from OpenAI or Claude, with a prompt | **LLM** on the **Engines** tab |

A large language model (LLM) is an AI model that follows written instructions. As an MT engine it translates with a prompt you choose, which lets you shape tone and terminology.

Engines that *check* a translation are not MT engines, so they are not in either list. A QE+APE engine runs inside a workflow step instead: it approves the machine translations a professional would accept and post-edits the rest. See [Quality estimation](/docs/concepts/quality-estimation) and [Hybrid and AI steps](/docs/guides/workflows-and-tasks#hybrid-and-ai-steps).

Some deployments also supply built-in engines. Your organization gets them when it is created, and they are yours from then on: open one to rename it or switch it off, and delete it if you do not want it. You cannot change which provider it uses, and it needs no key — its tile in **AI settings** reads **No key required**.

## Add a provider key (owner or admin)

1. Open the account menu at the top right and choose **AI settings**.
2. In **Providers**, click the provider tile: **DeepL** or **Google Translate**.
3. Leave **Custom endpoint** empty unless the provider gave you a special address.
4. Under **Keys**, click **Add key**. Enter the **API key** the provider gave you, give the key a **Name**, and save. The key is stored encrypted and checked when you save it. A key that fails the check is not stored: the form shows why under the field, with what to do next. The tile counts the keys the provider holds.

Add one key for each account you hold with the provider. A key translates nothing on its own — an MT engine is what uses it.

When engines of that provider have no key, saving a new key opens a dialog that lists them. Engines the key can run are ticked; an engine it cannot run is greyed out with the reason, for example "This key cannot use gpt-3." Click **Use this key** to give the key to the ticked engines, or **Skip** to leave them as they are.

You can remove a key at any time, and you do not have to change the engines first. The confirmation names the engines that use the key. Each one keeps its name, its options and its place in every router, and is marked **No key** until you choose another key for it. An engine marked that way stops translating, and a job that reaches it [fails](#when-machine-translation-fails) with a message that names the engine and tells you to choose a key for it in **MT** → **Engines** (for a QE+APE engine, **AI** → **Engines**).

## Create a provider engine (owner, admin or project manager)

1. On the **Engines** tab, click **New MT engine**.
2. Give it a name.
3. Pick the **Provider** and the **Key** it uses. The **Key** list offers only the keys you added for that provider, and tells you when there are none.
4. Set the provider's options. DeepL offers **Formality**, **Glossary ID**, **Preserve formatting**, **Split sentences** and **Model**; Google Translate offers **Model**. An option you leave alone keeps the provider's own default.
5. Click **Create**.

Each row shows the engine's name and the key it uses; click the row to change it. One key can back several engines that differ only in their options — one formal and one informal, say — and an MT router then sends each target language to the one it should use. Deleting an engine asks you to confirm; an engine an MT router routes to cannot be deleted until you change those routes.

## Turn an engine off without deleting it

On the **Engines** tab, click the engine and turn **Enabled** off, then **Update**. A switched-off engine stops translating but stays in its routers, so you can switch it back on later; its row is marked **Off** until you do. Any owner, admin or project manager can do this.

## Create an LLM translation engine (owner or admin)

You need an OpenAI or Claude key in **AI settings** first.

1. On the **Engines** tab, click **New LLM translation engine**.
2. Give it a name, or keep the one made from the provider and model.
3. Pick the **Provider** and the **Model**.
4. Pick a **System prompt**. The built-in **Default** prompt works out of the box. Open it to see what each segment carries with it, and to add rules for particular languages.
5. Click **Create**.

The **Prompts** tab holds the MT prompts. Built-in prompts are read-only; you can add your own. An engine's purpose (translation or quality review) is fixed when you create it.

## Send each language to the right engine with an MT router

An MT router maps target languages to engines. A project points at one router, and the router decides which engine translates each of the project's languages.

1. On the **Routers** tab, click **New MT router**.
2. Give it a name.
3. A new router starts with one route: **All languages** to your first engine. That engine is the default and translates every language no other route lists. Change the engine if you want another default, or replace **All languages** with specific languages if you do not want a default; a language with no route is not machine translated.
4. Under **Routes**, click **Add route** for each further engine and pick the target languages it translates. A route is either **All languages** or a list of languages, never both, and every route needs both halves before you can save. When the route's provider engine has reported its languages, a target language it does not translate is refused when you save, and the message names the engine and the language. A router always keeps at least one route.
5. Click **Create**.

![The MT routers list, one row per router, with New MT router above it.](assets/mt-routers.png)

Provider engines and LLM translation engines can be mixed in one router. A job takes the engine of the route for its target language; a route also covers regional variants of its language, so a route for French serves a Canadian French job unless Canadian French has a route of its own. Every other language takes the default.

To change a router, click its row. Projects point at the router itself, so the change reaches every job that is pre-translated from then on. Deleting a router asks you to confirm; a router that a project or template still uses cannot be deleted. Owners, admins and project managers can create, edit and delete routers.

## Choose the router for a project or template

Open the project's **Settings** tab (or a template) and find the **Machine translation** card. In **MT router**, pick the router for the project. An empty field reads **No machine translation**; the **X** in the field clears a router you picked. The choice saves at once; in the **New project** form it is saved with the project. The list also offers **New MT router**, which opens the **MT** page in a new tab.

A warning icon beside a card's title flags a gap in a saved project or template; hover it for the details. Beside **Translation memory** it lists the target languages no memory covers (a memory for `de` also covers `de-de`), and beside **Machine translation** it says **No MT router**. Pre-translation cannot fill a language that has neither.

Once a router is picked, a warning icon beside **MT router** lists the target languages it has no engine for, in the **New project** form too. Those languages are not machine translated, and their files read [**No MT**](/docs/guides/projects-and-files#upload-files) on the **Files** tab until the router covers them and you run **Pre-translate** > **Apply MT** (below); on a [Raw MT](/docs/guides/workflows-and-tasks#pre-translate-only-with-no-linguist) project the job waits on its **Pre-translation** step until then. The job's **History** entry for pre-translation says "MT skipped: the MT router has no engine for this language". With no router at all, nothing is flagged: machine translation is simply not set up.

The checkbox **Fill in machine translation on start or upload**, at the top of the card, decides whether MT runs by itself. In the **New project** form it is locked until you pick an MT router, and locks again if you clear the router; in a saved project it is editable. With [**Raw MT**](/docs/guides/workflows-and-tasks), a mode of the **Workflow** card, it is always on and cannot be cleared (the card reads **Always on with Raw MT.**), and the project cannot start until it has an MT router. While MT on upload is on and a router is chosen, the **Workflow** card heads its steps with a **Pre-translation** row that names the router; click the name to go to this card. Memory always runs first, and MT never overwrites an existing translation or touches a locked segment. To run it later, while the job is on its first workflow step, choose **Pre-translate** > **Apply MT** in the selection bar on the project's [Tasks tab](/docs/guides/projects-and-files#select-tasks-and-act-on-them) or in the CAT editor toolbar. With segments selected in the editor, the **Pre-translate** menu in the editor's selection bar runs **Apply TM** or **Apply MT** on those segments only. Both skip a segment [someone else has open](/docs/guides/cat-editor#when-someone-else-is-editing-a-segment) and say how many. The project's [**History**](/docs/guides/projects-and-files#the-history-tab) names the engine each run used. See [Translation memory](/docs/guides/translation-memory) for the memory side, and [Templates](/docs/guides/templates) to reuse one setup.

## Regional languages and unsupported pairs

Languages carry an optional region, for example "es-MX" for Mexican Spanish. Every provider engine reports the languages it translates, and that list refreshes daily, so a language a provider adds needs nothing set up here. An engine that keeps a variant apart from its base language — British and American English, Brazilian and European Portuguese, Simplified and Traditional Chinese — translates into the variant the project asks for. When an engine has no entry for the regional form, OpenLoc sends the base language instead ("es"), and translation continues. Chinese is matched by script: a Traditional Chinese target (zh-TW, zh-HK) goes to the engine's own Traditional code, and a Simplified one to its Simplified code. A Traditional target is never sent as plain "zh", which engines read as Simplified; if the engine has no Traditional Chinese, that pair fails. If the engine has no entry for the base language either, that pair [fails](#when-machine-translation-fails) and its segments stay untranslated. A router does not check its engines against a project's languages in advance, so you find this out when a job runs, not when you save the router. LLM engines accept any pair and receive the project's language exactly as named, for example "es-AR" or "zh-TW".

## When machine translation fails

Machine translation fails when the engine cannot translate: it is marked **No key** or **Off**, it cannot handle the language pair, or the provider turns the request down — a rejected key or a used-up quota, for example. The CAT editor shows the reason, and the job's task takes no edits until someone clicks **Re-run** or completes the step; see [Recover from a failed run](/docs/guides/workflows-and-tasks#recover-from-a-failed-run). Fix what the message names first — the key in **AI settings**, the engine, or the language's route — or the re-run fails the same way.

## Tags in machine translation

When the engine handles tags itself, a segment's tags travel with the text as the same numbered chips the editor shows, and the engine places them in the translation. It can move a tag pair, split it or repeat it, because the words that carry the formatting often sit elsewhere in the target language. Every LLM translation engine works this way, and among the provider engines DeepL does. An engine without tag support translates the text on its own, and OpenLoc puts each tag back beside the word it stood next to in the source. See [Work with tags](/docs/guides/cat-editor#work-with-tags).

## Use the MT suggestion in the editor

In the CAT editor, the **Resources** sidebar has an **MT suggestion** section. It shows the machine translation the pipeline already produced for the active segment, with the engine's name on the right; a segment that has none is translated on demand. Changing the project's router does not refresh a suggestion that is already stored — run **Apply MT** again for that. Click the suggestion to insert it, even when the segment already holds a memory match. The insert is saved as a **Draft** and **Match** shows **MT**, as after pre-translation; see [Origins and other marks](/docs/reference/segment-statuses#origins-and-other-marks). **No MT available** means the project has no router, the router has no route for the language, or the engine returned an error.

![The editor sidebar: memory matches, the MT suggestion, glossary terms, QA findings and comments](assets/editor-sidebar.png)

MT output is marked **Pretranslated**, so a person checks it before it counts as done — except with **Raw MT**, whose task completes itself once MT has run. Confirmed translations go to memory; raw MT never does. See [The CAT editor](/docs/guides/cat-editor).
