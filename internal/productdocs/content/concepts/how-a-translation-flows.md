---
title: "How a translation flows"
summary: "From upload to delivery: segments, jobs, pre-translation, editing, review and export."
order: 10
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - internal/pipeline/**
  - internal/job/**
  - internal/taskedit/**
  - internal/parsers/**
  - internal/translation/**
  - internal/analysis/**
  - internal/inlinetag/**
  - internal/hold/**
  - internal/stepdef/stepdef.go
  - internal/systemworkflow/systemworkflow.go
  - web/src/components/workflow/WorkflowModeToggle.tsx
  - docs/architecture.md
  - web/src/components/project/BulkJobActionsBar.tsx
  - web/src/components/JobPipelineActions.tsx
  - web/src/components/TaskControls.tsx
related:
  - getting-started/first-project
  - concepts/tm-matching
  - concepts/analysis-bands
  - concepts/quality-estimation
  - guides/ai-engines-and-prompts
  - guides/workflows-and-tasks
  - guides/translation-memory
  - reference/segment-statuses
screenshots:
  - project-files.png
  - project-analysis.png
---
A file that enters OpenLoc goes through fixed stages: it is cut into segments, split into one job per target language, filled from translation memory and machine translation, checked by an AI step when the workflow has one, edited and confirmed by people, and rebuilt in its original format.

## Stage 1: The file becomes segments

On upload, OpenLoc extracts only the translatable text: a Word document's paragraphs, an XLIFF file's translation units, a JSON file's string values, an Excel sheet's text cells. Layout and code stay behind and are put back at the end.

Each piece of text is a **segment**, usually one sentence or one short paragraph. Segments are the unit of everything that follows: matching, checking, editing and counting.

Why segments? A whole document is almost never repeated, but a sentence often is. Segments make reuse possible, let several people work on one file, and let the system approve one sentence while leaving the next one to a human. Inline formatting (bold, a link, a placeholder) appears in the editor as a tag chip that can be moved but not broken.

## Stage 2: One job per target language

A project has one source language and one or more target languages. Each uploaded file creates one **job** per target language. A file in a project with German, French and Spanish gives three jobs.

![The Files tab with one row per file](assets/project-files.png)

Jobs are independent. Each has its own progress, status, workflow steps, assignees and download, so each language can go to a different person and finish at its own pace.

## Stage 3: Pre-translation

Pre-translation fills segments before a human sees them. It runs in a fixed order:

1. **Translation memory, exact matches.** A true 100% or 101% match is confirmed automatically, because a human already approved that exact sentence. A match from another regional variant of the language is filled in but not confirmed.
2. **Translation memory, fuzzy matches.** A similar enough entry is filled in and marked **Pretranslated** so a person checks it. The project's fuzzy threshold (75% by default) decides what counts as similar enough.
3. **Machine translation.** Segments still empty go to the engine the project's MT router picks for the target language and come back as **Pretranslated**. Inline tags travel with the text, so the engine places each tag where the target sentence needs it instead of copying the source order.

![Pre-translation runs in three passes: exact memory matches are confirmed for you, fuzzy matches and machine translation are left at Pretranslated for a person to check, and no pass overwrites what an earlier one filled.](assets/pretranslation-order.svg)

With several memories, the **TM lookup order** setting decides whether every memory is searched for exact matches before any fuzzy search, or each memory gets both searches before the next one; see [Several memories](/docs/concepts/tm-matching#several-memories).

Why this order? Memory holds human work from your own organization: more trustworthy than a machine, in your terminology, and free. Machine translation only fills what memory cannot, and never overwrites an existing translation. No pass writes to a locked segment.

Two checkboxes, **Apply translation memory on start or upload** at the top of the **Translation memory** card and **Fill in machine translation on start or upload** at the top of the **Machine translation** card, decide whether these run. While the job is on its first step, they can also run later with **Pre-translate** (**Apply TM** or **Apply MT**): a manager from the selection bar on the project's **Tasks** tab, and a manager or the linguist assigned to the job from the editor toolbar, or on just the segments selected in the editor.

A run does not have to finish. While pre-translation is working on a job, a manager or the linguist assigned to the job can click **Stop**. Everything already filled in stays, and running **Apply TM** or **Apply MT** again continues from there, because neither touches a segment that already has a translation. See [Stop a running pipeline run](/docs/guides/workflows-and-tasks#stop-a-running-pipeline-run).

If pre-translation fails, the task takes no edits until it is re-run or the step is completed — see [Recover from a failed run](/docs/guides/workflows-and-tasks#recover-from-a-failed-run).

## Stage 4: Analysis

Now that the segments are filled, an analysis can count them. It sorts the words of a job into **bands** — Repetitions, 101%, 100%, 85–99%, 65–84% and New — by how much the translation memory helped.

![An analysis with its bands](assets/project-analysis.png)

The bands predict effort. A job that is mostly 100% and 101% is nearly done before anyone opens it; a job that is mostly New is real work. The analysis also reports how many words in each band were approved automatically. See [Analysis bands](/docs/concepts/analysis-bands) for what each band holds.

The analysis measures what pre-translation produced, so it cannot run before it. An analysis started too early is refused with a message naming the jobs that are not ready yet.

## Stage 5: Quality estimation and automatic post-editing

A **Hybrid** or **AI** workflow step points at an **AI router**, which picks the QE+APE engine for each job by its target language — from any connected provider that reviews translations. A router with no rule for that language and no default engine leaves the job unchecked; see [AI routers](/docs/guides/ai-engines-and-prompts#send-each-language-to-the-right-engine-with-an-ai-router). When a job reaches that step, the check looks at every **Pretranslated** segment and either approves it as it is, rewrites it, or leaves it for a human. Good translations, and rewrites the check is confident in, become **Auto-approved**; the rest wait as **Draft**. [Quality estimation](/docs/concepts/quality-estimation#the-four-outcomes) sets out the four outcomes in full.

The check sees a segment's inline tags as the same chips the editor shows, so it also judges where each tag sits, and a rewrite arrives with its tags in place. If it returns a tag that does not match the source, the segment gets a comment naming it, so a person can put the tags right.

Why a separate status called **Auto-approved**? A machine's approval and a person's approval are not the same thing. Keeping them apart shows at a glance which segments a human has seen, and keeps machine output out of your translation memory — see [Why auto-approved text never enters the translation memory](/docs/concepts/quality-estimation#why-auto-approved-text-never-enters-the-translation-memory).

While the check works on a step, the task is locked and opens as soon as it finishes; a failed check leaves the task in error — see [When the run fails](/docs/concepts/quality-estimation#when-the-run-fails). **Stop** works here too: the verdicts already written stay, and **Re-run AI step** reviews the rest.

## Stage 6: Human editing

A linguist opens the task and sees every segment with one of five statuses: **Untranslated**, **Pretranslated**, **Draft** (a person typed but has not confirmed), **Auto-approved** and **Approved** (confirmed by a person).

Typing saves the segment as a **Draft** right away. **Ctrl+Enter** (Cmd+Enter on a Mac) confirms it as **Approved**. Editing an **Approved** or **Auto-approved** segment moves it back to **Draft**, because the text no longer matches what was approved.

A TM match or MT suggestion inserted from the **Resources** panel is saved as a **Draft** and keeps its origin in **Match**, as after pre-translation; see [Origins and other marks](/docs/reference/segment-statuses#origins-and-other-marks).

Selected segments can also be confirmed together from the editor's selection bar; see [Select several segments](/docs/guides/cat-editor#select-several-segments-and-act-on-them) for what it leaves out.

Saving protects work; confirming states that the translation is final. Only confirmed segments count as done and feed the memory. See [Segment statuses](/docs/reference/segment-statuses).

One person edits a segment at a time: a segment someone else has open is read-only for you, and bulk actions skip it ([how it works](/docs/guides/cat-editor#when-someone-else-is-editing-a-segment)).

## Stage 7: Workflow steps

A job moves through the steps defined in the project, for example **Translation** then **Review**. Each step has a type — **Human**, **Hybrid** or **AI** — or is the built-in **Pre-translation** step. A Human or Hybrid step has an optional assignee and an optional deadline; an AI or Pre-translation step runs by itself and has neither. The assignee of the current step sees the job on their **Tasks** page.

Choosing **Raw MT**, not **Workflow**, on the **Workflow** card when a project is set up or in a template gives each job that single Pre-translation step, with machine translation always on. Its work is Stage 3: the task takes no edits while pre-translation runs, and when it finishes the step and the job complete themselves, with no person involved — unless the MT router has no engine for the job's language (see [Pre-translate only](/docs/guides/workflows-and-tasks#pre-translate-only-with-no-linguist)).

When done, the assignee clicks **Complete step**. If segments are still unconfirmed, a dialog says how many; the person can go back, approve them all and continue, or continue as they are. OpenLoc warns but does not block, because partial deliveries are sometimes intended. A reviewer can instead click **Reject** with a reason, and the job returns to the previous step with that reason shown in the editor.

Why steps on the job and not on the segment? A reviewer judges the whole delivery. Sending the job back keeps confirmed segments intact and says what to fix, instead of resetting hundreds of rows.

A completed job can still be corrected: an owner, admin or project manager who edits one of its segments [reopens its last step](/docs/guides/workflows-and-tasks#reopen-a-completed-task), and completing it again repeats the memory write of Stage 8 with the corrected text.

## Stage 8: Confirmed translations go to memory

Each confirmed segment is written to the project's write-mode translation memories at once. Completing a step, or the job, writes every **Approved** segment again, so nothing is missed; **Auto-approved** segments stay out. If a translation cannot be saved to every write memory, completing the job fails and the job stays open, so completing it again retries the write. A segment no memory can ever store, such as one too long to index, is skipped instead.

The next file with the same sentence gets a 100% match at Stage 4 and skips every stage in between. See [TM matching](/docs/concepts/tm-matching).

## Stage 9: Export

**Translated file** rebuilds the original document with the translations in place: same Word layout, same JSON keys, same XLIFF structure — the formatting set aside at Stage 1 comes back exactly as it was. **Bilingual XLIFF** exports every segment with source and target for use in another tool. **Source file** returns the upload unchanged.

When a file cannot be rebuilt, **Translated file** stops with a message that says what to do instead. A PDF has no translated version, and an Excel workbook whose row 1 has language codes but no column for the job's language cannot be filled in; see [What you download](/docs/reference/file-formats#what-you-download).
