---
title: "Quality estimation and automatic post-editing"
summary: "How a quality check decides which machine translations a person still has to read, and the four outcomes."
order: 40
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - internal/pipeline/**
  - internal/airouter/**
  - internal/worker/enqueue.go
  - web/src/components/WorkflowStepsEditor.tsx
  - web/src/components/workflow/StepDefinitionDialog.tsx
  - web/src/components/JobPipelineActions.tsx
  - internal/workflow/service.go
  - internal/worker/handler.go
  - internal/worker/stop.go
  - internal/job/handler_stop.go
  - internal/job/handler_rerun.go
  - internal/job/handler_skip_ai_step.go
  - web/src/components/TaskControls.tsx
  - web/src/components/project/BulkJobActionsBar.tsx
  - web/src/components/ai/vendor-catalog.ts
related:
  - concepts/how-a-translation-flows
  - concepts/tm-matching
  - concepts/analysis-bands
  - guides/workflows-and-tasks
  - guides/machine-translation
  - guides/ai-engines-and-prompts
  - reference/segment-statuses
screenshots:
  - workflow-steps.png
---
**Quality estimation (QE)** is an AI model that reads a source sentence and its translation and decides whether a professional would accept it as it is. **Automatic post-editing (APE)** goes one step further and proposes a corrected translation. Together they let the reviewer spend their time on the sentences that need it.

A language model does the work with your own prompt. You choose per workflow step and target language.

## What gets checked

The check runs over every segment that automation filled in and no human has touched yet: machine translation output, fuzzy matches from the translation memory, and any memory match that was not confirmed automatically — one from another language variant, for example. Segments a linguist already edited are left alone, and so are clean exact matches, which are already **Approved**. See [How TM matching works](/docs/concepts/tm-matching).

A language model gets segments in batches of 30, and each batch carries only what the prompt asks for: the source, the translation, and — if you tick them — a translation memory match and the glossary terms that apply. See [Choose what each segment carries](/docs/guides/ai-engines-and-prompts#choose-what-each-segment-carries).

Inline tags travel with the text: the check reads the same numbered chips the editor shows, in the source and in the translation, so it can judge where a tag sits and a rewrite can move it. If it returns a tag that matches nothing in the source, the segment gets an AI [comment](/docs/guides/cat-editor#comment-on-a-segment) naming that tag, so somebody checks the tags before delivery.

## The four outcomes

For each segment the check decides three things: is the translation good enough as it is; if not, is there a rewrite at all; and if there is, is the rewrite good enough? The answers sort the segment into one of four outcomes. Which of them a step can reach depends on what its prompt does: a QE prompt only judges, an APE prompt only post-edits and leaves every segment for a person, and a QE+APE prompt does both.

![Three yes/no questions produce the four outcomes: Approved and Fixed and approved both end as Auto-approved, while Rejected and Fixed, needs review end as Draft for a person to look at.](assets/qe-ape-outcomes.svg)

| Outcome | What the check decided | What happens to the segment | Status |
|---|---|---|---|
| **Approved** | A professional would accept the translation as it is | Text unchanged | Auto-approved |
| **Fixed and approved** | Not acceptable, but the rewrite is | Text replaced by the rewrite | Auto-approved |
| **Fixed, needs review** | Not acceptable, and the rewrite is not certain either | Text replaced by the rewrite, left for a human | Draft |
| **Rejected** | Not acceptable, and no rewrite offered | Original text kept, left for a human | Draft |

Whenever the check offers a rewrite, the rewrite replaces the text, and the verdict on the rewrite decides the outcome. The segment's history keeps the original machine output. Every auto-approval is recorded there as well: an **Auto-approved** entry when the text was accepted untouched, and the same mark on the **APE** entry when it was the rewrite that was approved.

"Auto-approved" is its own status, distinct from a human's "Approved". The editor shows the difference, filters can select one or the other, and the analysis reports the auto-approved share of every band. See [Segment statuses](/docs/reference/segment-statuses).

## Running it as a workflow step

Quality estimation is not a background job that fires on upload. It runs as a workflow step. Steps are defined once in the step library (**Workflows** in the left menu, then **Steps**) and every step has a type:

- **Human** — no AI. A linguist works and completes the step.
- **Hybrid** — the AI runs when the step starts; a person reviews and completes the step.
- **AI** — the AI runs when the step starts and the step completes itself. Nobody is assigned to it; wherever an assignee would show, the step reads "Automated".

A Hybrid or AI step needs an **AI router**. Pick one in the step dialog and every workflow that uses the step gets the check at that point. The router picks the QE+APE AI engine for each job by its target language: the engine of the rule for that language (a rule for French also covers Canadian French), or the router's default engine for every language without a rule. Routers, engines and the prompts behind them are set up on the **AI** page — see [AI routers](/docs/guides/ai-engines-and-prompts#send-each-language-to-the-right-engine-with-an-ai-router). A project's **Workflow** tab lists the steps picked from the library, so giving a project a quality check means putting a Hybrid or AI step into its workflow.

![The workflow steps of a project: Translation and Review, each assigned to a linguist](assets/workflow-steps.png)

The check runs when a job reaches the step: after pre-translation on upload if the Hybrid or AI step is the first one, or when the job is advanced into it. While it runs, the job's badge shows the progress — **Running AI step** at first, then a count such as **AI step (QE+APE): 30 / 880** — and the editor is locked, so nobody edits sentences the model is about to rewrite. On the **Tasks** page the card is marked **QE+APE running**.

What happens when the check finishes depends on the step type. On a Hybrid step the editor opens on its own and the assignee reviews. An AI step completes itself: the job moves to the next step with the [comment](/docs/guides/workflows-and-tasks#complete-a-step) the previous person left. There every segment starts unconfirmed for the next person, and the ones the AI approved show a grey **Auto-approved** chip. If the AI step is the last step, the job is completed; a manager who then edits a segment reopens the step, and completes it again by hand. A run that fails keeps the job on the step — see [When the run fails](#when-the-run-fails).

The pipeline actions menu offers **Re-run AI step** to managers when a job sits on a Hybrid or AI step that has not failed and nothing else is running. The re-run checks only segments that have not been checked yet, for example segments filled after the first pass. Clearing a job removes the earlier verdicts, so its segments are checked afresh once they are filled again.

A long run does not have to be waited out. While the check is running, **Stop** is the one action that stays clickable, and a manager or the job's assigned linguist can use it. Every segment the check answered for keeps its text and its verdict, and **Re-run AI step** picks up exactly the segments that were never checked. See [Stop a running pipeline run](/docs/guides/workflows-and-tasks#stop-a-running-pipeline-run).

## Choose a provider

Providers are connected under **AI settings → Providers**, each with its own key — none of them works before somebody adds one. A language-model provider (OpenAI or Claude) runs this check with a prompt you write, so you can name your style guide, your customer, or the mistakes you keep seeing, and returns a score as well as a verdict, shown as the blue **QE** chip. A machine translation provider connected there only translates.

See [AI engines and prompts](/docs/guides/ai-engines-and-prompts) to set it up, [Integrations](/docs/guides/integrations#check-that-an-ai-provider-is-connected) for what a provider does with what you send it, and [Workflows and tasks](/docs/guides/workflows-and-tasks) for the step.

## When the check approves nothing

The step can finish without touching a single segment, and it says so rather than failing. That happens when the step's provider is not connected under **AI settings → Providers**, when the step's AI router has no rule for the job's target language and no default engine, or when nothing was pre-translated — there is simply nothing to check. The job still opens for the person assigned to it, with every segment as it was.

## When the run fails

If the run fails, the job stays on the step and its task is in error: nobody can edit its segments. A row under the CAT editor toolbar shows the provider's message, for example *The AI step could not complete: … Check AI settings → Providers and try again.*, and the task shows as **Error** on the project's **Tasks** tab.

A failed attempt is tried again on its own, up to three attempts in all. Until the last attempt fails, the job's badge reads **Retrying AI step after an error** and the editor stays locked, as during the run.

A manager can **Re-run** it, complete the step to accept the error, or, on a Hybrid step, **Skip AI step** so the assignee works the machine translation without the check — see [Recover from a failed run](/docs/guides/workflows-and-tasks#recover-from-a-failed-run). A re-run goes like an automatic one: an AI step completes itself when it succeeds.

## Why auto-approved text never enters the translation memory

Text an AI step approved, and text it rewrote, stay out of the memory, however confident the verdict. The memory should hold only text a person checked. If machine output were saved, it would come back in the next project as a 100% match, be approved without a look, and skip the check entirely. Over time the model would also learn from its own output instead of from human corrections, and its judgement would get worse.

The moment a linguist edits or confirms an auto-approved segment, its status becomes Approved and it is written to the memory like any other human work. An AI step that completes itself writes nothing to the memory, for the same reason.

## What the numbers mean in the analysis

The saved analysis shows, under every band, how many segments and words were auto-approved: the sentences nobody had to read. On the **Settings** tab of the **Organization** page, the analysis weights include a **QE approved** band with a default weight of 0%, the same as an exact match. See [Analysis bands and word counts](/docs/concepts/analysis-bands).

## What a human should still check

Quality estimation judges fluency and meaning. It does not know your glossary or your style guide. The project's QA checks run when a linguist saves a segment, so an auto-approved segment nobody opened has not been checked against them. A QA finding describes one exact translation, so when a rewrite replaces the text the segment's findings are dropped, including any a reviewer had ignored; the checks run again the next time somebody saves the segment. A reviewer should still look at:

- terminology the glossary marks as forbidden or preferred;
- numbers, units and tags, which a rewrite can move;
- consistency across a document, which a sentence-level model cannot judge.

A practical order: filter the editor to **Draft** first, then skim the auto-approved segments. See [Quality assurance](/docs/guides/quality-assurance).

## A worked example

A 1,000-segment manual is uploaded into a project whose first step is a Hybrid step named "Translation" that points at an AI router. Pre-translation fills 120 segments from the memory as exact matches and 880 from machine translation. The check looks at the 880: it approves 500 as they are, fixes and approves 150, offers an uncertain rewrite for 100, and rejects 130. The translator opens a task with 230 segments needing review and 650 auto-approved segments to skim. When the step is completed, the 230 confirmed segments go into the memory. The 650 auto-approved ones go nowhere until a human touches them.

Had that step been an AI step, it would have completed itself as soon as the check finished, moving the job to "Review" with those 230 segments still at **Draft** and writing nothing to the memory.
