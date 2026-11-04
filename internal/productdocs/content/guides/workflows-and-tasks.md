---
title: "Workflows and tasks"
summary: "Define steps and workflows in the library, pick a workflow for a project, follow and assign its tasks, and work from the Tasks page."
order: 60
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-11-03
sources:
  - internal/autostep/**
  - web/src/pages/TasksPage.tsx
  - web/src/components/task/**
  - web/src/lib/task-filters.ts
  - web/src/pages/workflows/**
  - web/src/components/WorkflowStepsEditor.tsx
  - web/src/components/workflow/**
  - web/src/components/TaskControls.tsx
  - web/src/components/RejectStepDialog.tsx
  - web/src/lib/rollback.ts
  - internal/workflow/service_reject.go
  - web/src/components/CompleteStepDialog.tsx
  - web/src/components/editor/ScopeCompleteDialog.tsx
  - web/src/components/project/project-event-render-workflow.tsx
  - web/src/lib/current-task.ts
  - web/src/lib/task-edit.ts
  - internal/taskedit/**
  - web/src/components/project/ProjectWorkflowTab.tsx
  - web/src/components/project/ProjectCreateWorkflowCard.tsx
  - web/src/components/AssignmentSection.tsx
  - web/src/components/JobPipelineActions.tsx
  - web/src/lib/deadline.ts
  - web/src/components/ui/deadline-picker.tsx
  - web/src/components/ui/date-picker.tsx
  - web/src/components/ui/time-input.tsx
  - web/src/lib/time-zone.ts
  - web/src/components/project/BulkJobActionsBar.tsx
  - web/src/components/project/task-matrix/**
  - web/src/components/project/BulkDeadlineButton.tsx
  - web/src/components/project/UploadConfirmDialog.tsx
  - web/src/lib/completion-copy.ts
  - web/src/lib/project-logic.ts
  - web/src/hooks/useJobPipelineActions.ts
  - internal/workflow/**
  - internal/job/**
  - internal/stepdef/**
  - internal/workflowlib/**
  - internal/settings/workflow_steps.go
  - internal/settings/step_deadlines.go
limitations:
  - deadline-zone-outside-picker
  - workflow-draft-lost-on-navigation
related:
  - guides/projects-and-files
  - guides/cat-editor
  - guides/notifications
  - guides/templates
  - concepts/quality-estimation
  - guides/ai-engines-and-prompts
  - concepts/how-a-translation-flows
  - reference/roles-and-permissions
  - reference/segment-statuses
screenshots:
  - workflow-steps.png
  - workflow-step-dialog.png
  - tasks.png
---

A workflow is the list of steps a job goes through, for example Translation → Review → Proofread. Every job (one file in one target language) walks through the steps in order. Steps are defined once in a **step library**, and workflows are built from them in a **workflow library**; both live on the **Workflows** page. A project and a template take their steps from a workflow in that library. Each step is **Human**, **Hybrid** (an AI engine checks the translations first, then a person reviews) or **AI** (the AI engine checks them and the step completes by itself). People find the work assigned to them on the **Tasks** page and finish it in the CAT editor with **Complete step**. Managers follow every step of every job on the project's **Tasks** tab.

## Define steps in the step library

Open **Workflows** in the left menu and click **Steps**. The list shows every step with its type badge, its AI router (Hybrid and AI steps) and how many workflows use it. Every organization starts with two Human steps, **Translation** and **Review**. They are ordinary steps: rename them, change their type or delete them like any other. The Workflows page is for owners, admins and project managers.

1. Click **New step**.
2. Pick the step type:
   - **Human** — a linguist works and completes the step.
   - **Hybrid** — AI runs first, a human reviews and completes.
   - **AI** — AI runs and completes the step itself.
3. Type a **Name**, for example Review.
4. For a Hybrid or AI step, choose the **AI router**. The router picks the QE+APE engine for each job by its target language; see [AI routers](/docs/guides/ai-engines-and-prompts#send-each-language-to-the-right-engine-with-an-ai-router) for how to set one up. **New AI router** at the bottom of the list opens the AI page in a new browser tab.
5. Click **Create step**.

![The New step dialog: the three step types with a short description each, the step's name, and the AI router picker that a Hybrid or AI step needs.](assets/workflow-step-dialog.png)

Use the pencil on a row to change a step's name, type or AI router. Projects and templates that already use the step keep their own copy, so the change does not reach them. A step can be deleted only when no library workflow uses it.

## Create a workflow

Open **Workflows** in the left menu. Each workflow is listed with its steps numbered in order, with the step type, the AI router (Hybrid and AI steps; its name opens the AI page at that router) and the default assignee. Every organization starts with one workflow, **Default**, built from those two steps. It is an ordinary workflow — rename it, reshape it or delete it like any other.

1. Click **New template**. The **New workflow** dialog opens with Translation → Review filled in. It is the same dialog the **New project** form opens.
2. Type a **Name**.
3. In each row, pick a step from the library. The row shows a **Default assignee** picker; leave **No default** to assign per job later. An AI step shows **Automated** instead.
4. Click **Add step** for more rows, drag the handle on the left to reorder, and use the trash icon to remove a row. A workflow can have up to 9 steps.
5. Click **Create**.

The pencil on a row opens the same dialog on the saved workflow, with **Save** instead. Projects and templates that use a workflow get a copy of its steps, so editing or deleting the workflow does not change them.

## Choose a project's workflow

Open the project and go to the **Workflow** tab. The **Workflow** dropdown lists the library workflows, each with its steps. The same card sits on a template's **Workflow** tab. When a library workflow is chosen, an icon at the end of the dropdown's line opens the **Workflows** page in a new browser tab with that workflow's **Edit workflow** dialog open; hover it to read **Open workflow**. A workflow that is not in the library, such as Raw MT's **Pre-translation**, has no icon.

Pick a workflow and confirm with **Use workflow**. The project gets a copy of the steps and lists them under the dropdown, numbered, each with who picks it up — **Automated** on an AI step — and when it is due. When machine translation fills in files on upload and the project has an MT router, an unnumbered **Pre-translation** row heads the list; the router's name there takes you to the **Machine translation** card, where the router is chosen, and highlights it. The copy belongs to the project. On a running project, steps that jobs have reached keep their place: a workflow that swaps or drops one is refused with a message naming the step.

![A project's Workflow tab: the workflow picker over the project's steps, each with who picks it up and when it is due](assets/workflow-steps.png)

In the **New project** form the same card applies your choice straight away, because nothing is running yet. A fresh form has no workflow chosen, and a project cannot be created without one: **Create project** points at the empty dropdown and frames it red. Its dropdown also has **New template** at the top, which builds a workflow on the spot: type a **Name**, edit the steps (they start as the ones on screen, if any) and click **Create and use** — the workflow is added to your library and used for this project. Steps that arrive with a [template](/docs/guides/templates) or a duplicated project are listed under an empty field until you pick a workflow to replace them.

## Change a running project's steps

On a project's **Workflow** tab the steps are listed with who picks each one up and when it is due. Click **Edit steps** to change them: pick another step from the library, drag a row by its grip to move it, remove it with the trash icon, click **Add step** (up to 9 steps), or change a step's default assignee or deadline.

Steps that jobs have already reached are locked. A lock takes the place of the grip; hover it to see how many jobs reached the step. A locked step can't be swapped, moved or removed, and nothing goes in front of it, but its default assignee and its deadline still change. A step stays reached when a rejection or a clear sends the job back past it.

Nothing is saved until you say so: **Save changes** and **Discard** sit under the list while you edit, and the **Workflow** dropdown stays shut until you use one of them. **Discard** puts the steps back as they were. Changes you have not saved are lost when you go to another tab or page; your browser asks first only when you reload or close it. **Save changes** first says what the save does — how many steps it adds, changes or removes, how many jobs are in progress, and which jobs start a step at once. A job that had finished every step starts the first added step right away, and an added AI step then runs, which may cost money. Click **Save** to apply it. If a job reaches a step while you edit, the save is refused with a message naming the step, and the locks update.

After the save, every job that is not completed follows the new steps when it gets to them, with each step's default assignee. A new default assignee also goes to the jobs sitting on that step. Once a save adds, changes, removes or moves a step, the dropdown shows **Edited** beside the workflow's name; a change to assignees or deadlines alone does not. Choosing a workflow there replaces the steps with its copy and clears the mark.

## Pre-translate only, with no linguist

For files that need machine translation and nothing else, switch the **Workflow** card to **Raw MT**; the other choice is **Workflow**. The toggle heads the card in the **New project** form and on a template's **Workflow** tab, where you confirm with **Use Raw MT**. A project gets it when it is set up: once created, a project cannot switch to it or away from it. The steps become one **Pre-translation** step. Each uploaded file is pre-translated (translation memory as the project sets it, then machine translation), and the step then completes by itself, which completes the job. Nobody is assigned to the step and it has no due date. When the MT router has no engine for a job's target language, the step stays open and the file reads [**No MT**](/docs/guides/projects-and-files#upload-files) instead of completing with empty targets; once the router covers the language, **Pre-translate** > **Apply MT** completes it.

Under Raw MT the card shows the **Pre-translation** row alone, with no dropdown. The row names the project's MT router (**No MT router yet** until one is chosen); click it to go to the **Machine translation** card, where the router is chosen. **Fill in machine translation on start or upload** stays on. The project needs an **MT router**: it cannot be created, or clear its router, without one. A draft or a template may leave it empty until then. Switching back to **Workflow** applies the workflow you had chosen before. With none, the **New project** form and the **New template** page leave the field empty for you to pick one, and a saved template's **Workflow** tab takes the first one in the library. The workflow is built in, so it is not listed on the **Workflows** page.

Nobody can edit the file while it is being pre-translated. After that, an owner, admin or project manager can still [reopen the task by editing a segment](#reopen-a-completed-task). If pre-translation fails, a successful [**Re-run**](#recover-from-a-failed-run) completes the step.

## Set deadlines

Each step carries its own deadline, shown as a pill at the right-hand end of the step's row — on the project's **Workflow** tab and in the **New project** form alike. An **AI** or **Pre-translation** step runs by itself, so its row has no date. On a project, click **Edit steps** first; until then the globe beside a pill lists the languages that have a date of their own.

1. Click the pill beside a step. A step with no date yet reads **Set due date**. Pick a day in the calendar and a time below it — choose from the list in 15-minute steps, or type any time such as 17:40 (a new date starts at 17:00), then click **Done** or anywhere outside. Or click a quick pick on the left — **1 day**, **2 days** or **1 week** from now, at 17:00 — which sets the date and closes the calendar. **Clear** removes the date. The date counts for every target language.
2. For a language due on a different date, click the globe beside the pill. Pick the languages and a date, and **Add override per language** for another set. A language with no override of its own uses the step's date. The globe is highlighted while a step has any, so a closed row still shows there are some.
3. On a project, the dates are saved with your other changes when you click **Save changes**. In the **New project** form they are kept with the rest of the form.

Deadlines show in your own browser's time zone. Hover a pill, or look at the project's **Deadline** field, for the exact time with its offset, for example *Sep 30, 2026, 5:00 PM GMT+2*. To enter a time given in another zone, such as 17:00 New York, pick that zone in the list under the calendar before you close it. The date is saved as that moment; the zone is not kept, so the next time you open it, it shows in your own zone.

A step nobody has dated falls back to the project's own deadline, so one date on the project gives every task a date. Most specific wins: a language's override, then the step's date, then the project's.

Files uploaded later inherit these dates — the upload itself asks nothing about them. On a project that is already running, saving also re-dates the jobs sitting on a step: a step still waiting or in progress gets the new date, a step someone already finished keeps the date it had, and a date set on a single job with **Set deadline** is replaced.

The dates belong to the project: a library workflow and a [template](/docs/guides/templates) carry none. Picking a different workflow replaces the steps, and the dates on them with it.

## Hybrid and AI steps

When a job reaches a Hybrid or AI step, the step's [AI router](/docs/guides/ai-engines-and-prompts#send-each-language-to-the-right-engine-with-an-ai-router) picks the QE+APE engine (quality estimation and automatic post-editing) for the job's target language and runs it. The engine checks every machine-translated segment and approves the good ones automatically. A target language the router has no rule and no default engine for is not checked: on a Hybrid step the assignee reviews everything, and an AI step completes itself with nothing approved. **Re-run AI step** on such a job says the router has no engine for the job's target language.

- On a **Hybrid** step, the rest go to the assignee for review. While the AI runs, the task is locked: its card on the **Tasks** page shows a **QE+APE running** badge, and the job's own badge counts the batches, for example **AI step (QE+APE): 30 / 880**. It unlocks by itself when the check is done — no need to reload.
- On an **AI** step, nobody is assigned. The step's tasks on the project's **Tasks** tab and the step list show **Automated**, and in the CAT editor the current-step chip's tooltip says the step completes itself. When the check is done, the step completes itself and the job moves on to the next step. The next step starts with every segment unconfirmed; the ones the AI approved show a grey check with sparkles in the status column, as the [segment statuses](/docs/reference/segment-statuses) page describes. An AI step can also be the last step; the job is then completed.

An engine that uses a **Comment only** prompt (see [AI engines and prompts](/docs/guides/ai-engines-and-prompts)) can run on either step type, but it works differently. It only adds AI comments and approves nothing, so a Hybrid step still sends everything to the assignee, and an AI step completes itself with nothing approved.

Managers can run the check again with **Re-run AI step** in the CAT editor toolbar, or halt a run that is still going with **Stop**. When the run ends, the toast reads **AI step complete**. Read more in [Quality estimation](/docs/concepts/quality-estimation).

## Stop a running pipeline run

While a job is being pre-translated (**Apply TM** or **Apply MT**) or its QE+APE step is running, a **Stop** button sits with the other job actions in the CAT editor toolbar. It is also in the [selection bar](/docs/guides/projects-and-files#select-tasks-and-act-on-them) on the project's **Tasks** tab while a selected job is running — handy for a run that started by itself after an upload.

Click **Stop**. The run first finishes the chunk of segments it is working on, so the button reads **Stopping…** and the job's badge turns amber with the same word. When the chunk is done:

- Every segment translated (or checked by the AI) before the stop is kept.
- The job ends its run and the CAT editor header shows an amber note. After pre-translation it reads *Stopped — 1230 / 22857 segments translated; run Pre-translate again to resume*. After a QE+APE step it reads *Stopped — 1230 / 880 segments reviewed; use Re-run AI step to resume*.
- To carry on, run **Pre-translate** again (or **Re-run AI step**). It picks up where the stopped run left off; finished segments are not done a second time.

The assigned person can stop a run, and so can any owner, admin or project manager. If nothing is running any more, or a stop is already on its way, you get a message saying so. Both the stop request and the end of the run are recorded in the project's **History** tab.

## Recover from a failed run

When pre-translation or an AI step fails, the task the job is on shows **Error**: red on the project's **Tasks** tab and in the CAT editor's step chip, with the error message in a row under the editor's toolbar. While the system is still retrying the run by itself, the job shows as running; the error appears once the last attempt fails. An AI step is tried at most three times. An error that no retry can fix appears at once, such as a machine translation engine that does not translate the job's language pair.

Nobody can edit a task with an error. The grid is read-only, **Pre-translate**, **Re-run AI step** and **← Reject** are hidden, and the end of the editor's breadcrumb line offers two buttons:

- **Re-run** runs the failed automation again, and **Re-run queued** confirms it. A failed pre-translation runs TM and MT again, skipping segments that already have a translation; like **Pre-translate**, it is offered only while the job is in its first step. Owners, admins and project managers can re-run either kind; the linguist assigned to the job can re-run a pre-translation.
- **Complete step →** (or **Complete final step ✓**) accepts the error: the step is completed as it is and the job moves on without the error. The project's **History** tab notes the error that was dismissed. The usual rule applies for [who may complete](#complete-a-step).

When the AI run of a **Hybrid** step fails, owners, admins and project managers also see **Skip AI step** in the error row. It clears the error and leaves the step in progress, so the assignee works the machine translation without the AI check. The project's **History** tab records the skip and the error it cleared.

To re-run several jobs, or skip the AI step on several Hybrid jobs, select their tasks in error on the project's **Tasks** tab and click **Re-run** or **Skip AI step** in the [selection bar](/docs/guides/projects-and-files#select-tasks-and-act-on-them).

## Follow a project's tasks

The project's **Tasks** tab lists each job under the workflow step it is at, with who has the task, when it is due, and its state as a colour. **Expand** shows every step of every job in one grid. See [The Tasks tab](/docs/guides/projects-and-files#the-tasks-tab).

## Assign a person to a step

When a file is uploaded, every Human and Hybrid step of each job gets that step's default assignee. To change who does a step, or when it is due:

1. On the project's **Tasks** tab, [select the tasks](/docs/guides/projects-and-files#select-tasks-and-act-on-them). The tab shows each job's current task; to pick a step that has not started yet, click **Expand** and select it there.
2. In the bar that appears, click **Assign** and pick a member.
3. Optional: click **Set deadline** to pick a due date, in the same calendar as a step's own date, then click **Done**, or click a quick pick to apply it at once. **Clear** removes the due date instead. Closing the calendar any other way changes nothing.

A step that has not started can get its person and due date ahead of time. The person keeps the step when the job reaches it, and is notified then; the default assignee fills only a step nobody is on.

**Assign** and **Set deadline** act on exactly the steps you selected, including steps that have not started yet. **Assign** skips steps that are already completed. Both skip AI and Pre-translation steps, which take neither a person nor a date. Assigning needs the owner, admin or project manager role.

## Work from the Tasks page

Open **Tasks** in the left menu. Each card is one job where you are the assignee of the current step. Owners and admins see every assigned task in the organization, and each card names the person after **Assignee:**; everyone else sees only their own. Linguists see only this page in the menu.

A card shows:

- The file name, and the due date of the step as plain text, for example **Due in 3d · Sep 15, 5:00 PM**. It is red when overdue and amber within 24 hours, grey otherwise; see [Read deadlines](#read-deadlines). A **QE+APE running** badge beside the name means the task is locked for a moment.
- The project, the customer if there is one, and the languages (source → target).
- **Step:** with the step you are assigned to, and the progress: translated segments out of the total, such as **1,500 / 2,400 segments**, or the word count.

A name too long for the card is cut short; hover it to read it in full.

Click a card to open the job in [the CAT editor](/docs/guides/cat-editor) in a new browser tab; the task list stays open in the first one. The file name is a link, so middle-click and Cmd/Ctrl+click work too.

To work through several files in one editor grid, tick the checkbox on each card and click **Open in editor** in the bar that appears. The files must be of one project and one target language, and at most 500 can open together; the button is greyed out with the reason in its tooltip otherwise. **Complete step** in that editor [completes each file's step](#complete-a-step) on its own.

The nearest deadline comes first, and tasks with no deadline come last. The page shows 50 tasks at a time; **Load more** shows the next 50.

To narrow the list, use the bar above it:

- The search box finds a file name or a project name.
- **Step** shows only the tasks on one step. Type in the box at the top of its list to find a step by name.
- **Deadline** shows **Overdue**, **Due within 24h**, **Due within 7 days** or **No deadline**.

**Clear filters (N)** removes them all; the number counts the search and each active filter. The filters are kept in the page address, so a reload keeps them and you can bookmark a filtered view.

![The Tasks page listing assigned jobs](assets/tasks.png)

## Complete a step

A chip at the top of the CAT editor names the job's current task and shows its state — see [Open the editor](/docs/guides/cat-editor#open-the-editor-and-find-your-way-around).

1. Finish your work. Confirm each segment with Cmd/Ctrl+Enter.
2. Click **Complete step →** (or **Complete final step ✓** on the last step).
3. A dialog opens. If segments are left unapproved, it counts the drafts and untranslated segments and offers **Approve all and complete** next to **Complete step** — see [Complete the step](/docs/guides/cat-editor#complete-the-step). Completing with segments left is a choice, not a block. Text that was typed but never confirmed is handed over as it is, and each of those segments gets an entry in its **History**, under the name of the person who typed it. Those entries are not marked **Approved**, because nobody confirmed them.
4. Optional: write a **Comment for next assignee**. Whoever works the next step sees it under the editor's toolbar, headed **Comment from the previous step** (see [Read the task controls](/docs/guides/cat-editor#read-the-task-controls)); the project's **History** tab quotes it too, the next assignee's [assignment notification](/docs/guides/notifications#know-which-events-create-a-notification) carries it, and the project's [**Tasks** tab](/docs/guides/projects-and-files#the-tasks-tab) marks the task it was handed to. Completing without a comment leaves the next step with none. The final step has no next assignee, so its dialog has no comment box.
5. Click **Complete step** or **Approve all and complete**.

The job moves to the next step and its assignee gets a notification: the person already put on that step, or else the step's default assignee. If the next step is an AI step, it runs and completes itself, and your comment goes on to the step after it. Completing the final step does more. The dialog lists it:

- Mark the job as completed.
- Save the human-approved segments to the translation memory.

If the segments cannot be saved to a translation memory the project writes to, the final step is marked done but the job stays open. Complete the job again to retry; a segment no memory can ever store is skipped.

The assigned person can complete a step, and so can any owner, admin or project manager. A linguist who is not the assignee cannot complete it, even when nobody is assigned.

You can also complete several jobs at once: on the project's **Tasks** tab, [select their current steps](/docs/guides/projects-and-files#select-tasks-and-act-on-them) and click **Complete step** in the bar.

In an editor grid with several files, **Complete step** lists every open file you can complete, each with how many segments it would leave unapproved. The dialog takes one comment for all the next assignees and, when some file still has drafts, offers **Approve all and complete**. Each file's step is completed on its own. A file that could not be completed stays open, and the dialog lists it with the reason, for example segments that could not be approved.

## Reopen a completed task

When a job's workflow is done, the editor opens on its last step, marked **Completed**. Linguists see it with a **Read-only** badge. Owners, admins and project managers see *Edit a segment to reopen this task.*

1. Edit a segment. When the change is saved, **Task reopened** appears and the step is in progress again. The job is open again too, and a project that [completes itself](/docs/guides/projects-and-files#project-status) goes back to **In progress**.
2. When you are done, click **Complete final step ✓**, as for any [step](#complete-a-step).

The project's **History** tab records that you reopened the step by editing it.

Splitting or joining a segment does not reopen a completed task. Both need a task that takes edits, so reopen it by editing a segment first.

## Send a step back (reject)

If the work from an earlier step is not good enough, you can send the job back to it. The **← Reject** button sits next to **Complete step** at the right end of the CAT editor's top line. It is not shown when there is no earlier step you can send the job to, nor on a task with an [error](#recover-from-a-failed-run).

![Completing a step moves the job to the next one. Rejecting sends it back to an earlier step you pick.](assets/workflow-step-lifecycle.svg)

1. Click **← Reject**. The dialog shows the whole workflow, with the state of each step.
2. Pick the step to send the job back to. The nearest earlier Human or Hybrid step is picked for you. The current step, later steps and Pre-translation cannot be picked; point at a greyed-out step to see why.
3. Type a **Rejection reason** (optional but helpful).
4. Click **Send back to** and the step's name.

The step you picked opens again and its assignee gets a notification. The editor shows a red banner headed **Sent back from Review** with your reason. The steps between the two start again, in order, when the job moves forward. Each of them gets back the statuses it ended with on every segment nobody has changed since; a changed segment starts unconfirmed. The same rule applies as for completing: the assigned person or a manager. As when [completing a step](#complete-a-step), text that was typed but never confirmed gets a **History** entry under the name of the person who typed it, not marked **Approved**.

The reopened step gets back the segment statuses it ended with. A segment whose text changed after that step comes back unconfirmed, without its AI score, and so does a segment an AI step after it scored. A segment you split, or the result of a join, also comes back unconfirmed: a status the earlier text earned is not restored.

### Run the AI again

Sending the job back to an **AI** step runs that step again. On a **Hybrid** step, tick **Run the AI again** to do the same, for example after you changed the engines its AI router picks. Only owners, admins and project managers can run the AI again.

The AI then reviews every translated segment that is not locked, on its current text — text people wrote included. It may rewrite that text, and its verdict replaces their approval. An AI step then completes itself as usual; on a Hybrid step the assignee reviews the result.

## Read deadlines

A project has one deadline. Each step assignment has its own deadline too. Both show as a colored pill, on the project list and the project's **Tasks** tab. The **Tasks** page shows the same wording as plain text, coloured only when overdue (red) or due within 24 hours (amber); every other deadline is grey.

| Pill | Meaning |
|---|---|
| **Overdue 3d** (red) | The deadline has passed. |
| **Due in 5h** (amber) | Less than 24 hours left. |
| **Due in 3d** (blue) | Between one and seven days left. |
| **Due Sep 20** (grey) | More than a week away, or the work is finished. A completed project or job always shows its due date this way, never as **Overdue**. |
| **No due date** | No deadline set. |

Hover a pill to see the exact date and time in your time zone, with its offset. Deadline reminders are sent as notifications; see [Notifications](/docs/guides/notifications).
