---
title: "Projects and files"
summary: "Create projects, upload files, assign and track tasks, read the analysis and download translations."
order: 10
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - web/src/pages/ProjectList.tsx
  - web/src/pages/CATEditor.tsx
  - web/src/lib/editor-link.ts
  - web/src/pages/ProjectCreate.tsx
  - web/src/pages/ProjectDetail.tsx
  - web/src/lib/task-matrix.ts
  - web/src/lib/current-task.ts
  - web/src/lib/file-readiness.ts
  - web/src/hooks/useProjectCreateSettings.ts
  - web/src/hooks/useProjectDraft.ts
  - web/src/hooks/useUploadQueue.ts
  - web/src/lib/xlsx-columns.ts
  - web/src/components/project/**
  - web/src/components/ClearTranslationsDialog.tsx
  - web/src/hooks/useClearTranslations.ts
  - web/src/components/SettingsEditor.tsx
  - web/src/components/settings/TMProfilePicker.tsx
  - web/src/components/workflow/WorkflowTab.tsx
  - web/src/components/workflow/WorkflowDeadlines.tsx
  - web/src/components/workflow/WorkflowPicker.tsx
  - web/src/components/workflow/WorkflowFormDialog.tsx
  - web/src/components/workflow/WorkflowStepChips.tsx
  - web/src/components/ui/deadline-picker.tsx
  - web/src/components/workflow/WorkflowModeToggle.tsx
  - web/src/components/workflow/RawMTPane.tsx
  - web/src/components/ProjectAnalysesTab.tsx
  - web/src/components/ProjectSettingsTab.tsx
  - internal/project/**
  - internal/label/**
  - internal/file/**
  - internal/parser/**
  - internal/projectevents/**
  - internal/pipeline/outcome.go
  - internal/pipeline/export_events.go
  - internal/taskedit/**
limitations:
  - completed-upload-ui-only
  - draft-cleanup
related:
  - getting-started/first-project
  - guides/workflows-and-tasks
  - guides/cat-editor
  - guides/templates
  - reference/file-formats
  - reference/roles-and-permissions
  - concepts/analysis-bands
screenshots:
  - project-create.png
  - project-files.png
---

A project holds everything for one translation order: the languages, the files, the translation memories and MT router to use, the workflow steps, and the people. Each uploaded file becomes one job per target language, and each job has one task per workflow step.

## Create a project

1. Open **Projects** in the left menu and click **New project**.
2. Choose **From template** or **From scratch**. The form appears once you have chosen. Starting from a template — or from another project — also puts a line at the top saying where the settings came from. **From template** opens a **Choose a template** dialog; click a template and the form fills with its project name, languages, domain, customer, workflow and settings. The line then reads **Using template …**. Once the form has saved a draft, a red **Discard** button appears at the top right, next to the **Draft** badge. It throws the draft away: it asks you to confirm, then the form empties, any files you had already uploaded into it go with it, and the two options come back so you can pick another template. See [Start a project from a template](/docs/guides/templates#start-a-project-from-a-template).
3. In the first block, fill in the basics:
   - **Project name** (required) and **Deadline (optional)**, side by side. The deadline opens a calendar with a time field and a time zone selector — see [Set deadlines](/docs/guides/workflows-and-tasks#set-deadlines).
   - **Source language** and **Target languages** (required, one or more targets)
   - Under **Labels**: **Customer** and **Domain**, each a pick from the values your organization has defined
   - **Add label** adds one of your organisation's own labels — a name such as "Country" with a fixed list of values, of which a project takes one. Labels classify projects; they change nothing about how the files are translated.
   - **Manage labels**, at the right of the group, opens a dialog to define them. **Add label** there, type a name and click **Add**; then type each allowed value into the label's values field. A label's name is fixed once it is added, and so is each value's. Owners, admins and project managers add labels and values. Deleting needs an owner or an admin: untick a value to delete it, or use the bin icon to delete a whole label.
   - A value marked **In use** can still be deleted: you confirm, and it is removed from the projects and templates that carry it. A label's bin stays greyed out while any of its values is in use. See [Delete a value or a label](/docs/guides/customers-and-vendors#delete-a-value-or-a-label-owner-or-admin).
4. Choose the **Workflow**. A **Workflow** / **Raw MT** toggle heads the card and starts on **Workflow**. The dropdown offers the workflows in your library, each with its steps; type to search. The form opens on the first one (they are sorted by name; every organisation has one called **Default**) and lists its steps read-only, numbered down a rail, each with its type, its AI router and who picks it up. Pick another and its steps take their place. See [Workflows and tasks](/docs/guides/workflows-and-tasks).
   - To build a new one, open the dropdown and choose **New template** at the top. The dialog opens with the steps on screen: type a **Name**, reorder, add or remove steps from the step library, and set who picks each one up by default. **Create and use** adds the workflow to your library and uses it for this project.
   - While machine translation runs on upload and an MT router is chosen, an unnumbered **Pre-translation** row heads the steps. Its router name is a link: it scrolls to the **Machine translation** card and rings it.
   - Choose **Raw MT** for a workflow of one automated **Pre-translation** step: machine translation fills in each file and the job completes itself — see [Pre-translate only, with no linguist](/docs/guides/workflows-and-tasks#pre-translate-only-with-no-linguist). The card then shows the **Pre-translation** row alone, with no dropdown; choose **Workflow** to go back to the workflow chosen before.
   - Steps that came in with a template or a duplicated project that followed no workflow are listed under an empty field. They are kept as they are and cannot be edited here; pick a workflow to replace them.
   - Each step's deadline sits at the right of its row; files uploaded to the project inherit them. See [Set deadlines](/docs/guides/workflows-and-tasks#set-deadlines).
   - Who picks a step up and when it is due is then set per task, on the **Tasks** tab — see [Select tasks and act on them](#select-tasks-and-act-on-them).
5. Under the basics sits **Complete the project when every task is done** — on by default. See [Project status](#project-status) below. **Strings contain HTML**, just under it, is on by default and decides how JSON, PO, TXT and Excel strings are read; see [Where each project option lives](/docs/reference/settings#where-each-project-option-lives).
6. Set up **Translation memory**, **Machine translation**, **Terminology** and **Quality assurance**, each its own block. **Translation memory** opens with an empty **TM profile** field: pick a profile to follow, or **From scratch** for a list that belongs to this project alone. The memories and the match scoring appear once you have chosen — see [Assign translation memories to a project or template](/docs/guides/translation-memory#assign-translation-memories-to-a-project-or-template). **Machine translation** asks for an **MT router**. Each of the two blocks starts with a ticked checkbox — **Apply translation memory on start or upload** and **Fill in machine translation on start or upload** — that decides what runs automatically on new files; it is greyed out until that block has its source. With **Raw MT**, the machine translation one stays ticked and locked, and **Create project** asks for an MT router first and takes you to that field. **Quality assurance** holds the **QA profile** — the checks that run on this project; see [Quality assurance](/docs/guides/quality-assurance).
7. Optional: drop files into **Files (optional)**. Each file uploads right away and is listed with its segment count; use the bin icon to remove one. An Excel file without language codes in row 1 first asks which columns to translate; see [Choose the columns of an Excel file](#choose-the-columns-of-an-excel-file). Keep **Run analysis on upload** ticked if you want a word-count analysis as soon as the project is created.
8. Click **Create project**. The required fields are checked — the first one that fails is scrolled to and ringed — the project becomes **In progress**, and its files get their jobs — translation memory and machine translation run on them now, according to the settings above. **History** already shows the creation, from the first time the form saved, in the name of whoever started it, followed by the uploads; if someone else clicks **Create project**, a **published the project** entry names them. **Save as template**, next to it, saves the form as a template instead — see [Templates](/docs/guides/templates#save-a-new-projects-setup-as-a-template).

The form saves itself as a **Draft** project from your first edit: a grey **Draft** badge appears next to the title — the same one the project list shows — with **Saving…** and then **Saved** beside it as each change goes out, or a red **Draft not saved** if one fails. Leaving a field saves at once, so leaving the page loses nothing. The title, the badge, the save state and **Discard** stay in view while you scroll the form. The draft waits in the **Projects** list and reopens in this form where you left it. It stays in the list until you create it or delete it.

![The New project form after choosing From scratch](assets/project-create.png)

## Find and filter projects

The **Projects** page lists projects newest first, 50 at a time. Click **Load more** to see older ones.

- Type in **Search projects...** to search by name.
- Use the filter chips: **Status**, **Customer**, **Source**, **Target** and **Created by**. **Created by** only lists people who created at least one project.
- **Clear filters (N)** empties the search box and resets the chips. The number counts the search and each active chip, and the button only appears once at least one is set. Filters are part of the page address, so you can bookmark or share a filtered view.

Each row shows the status, deadline, customer and creator. A row with the **Draft** badge is a New project form still being filled in — an unnamed one reads **Untitled draft** — and clicking it opens that form. Two icons sit at the right end of each row: **Duplicate** (owners, admins and project managers; a new project with the same name, details and settings, no files, and not offered on a draft — see [Use as template](#the-project-page)) and **Delete** (owner and admin only).

## The project page

The project name is a breadcrumb. Click the pencil next to it to rename the project. Under the name you see the language pair and, for managers, a **Processing time** button (a gauge icon) that opens a breakdown of how long the work took. On the right, owners, admins and project managers get **Use as template**: it opens the New project page with this project's name, labels, languages, workflow and settings filled in, and no files. The line at the top reads **Settings copied from project …**, and, once you have changed something, the red **Discard** button at the top right clears it all. Below the language pair are the project's status and who created it, then a card with the project's labels — **Customer**, **Domain** and any others it carries — above a divider, then **Deadline** and **Status**. Managers edit them in place — a deadline change saves when its calendar closes; everyone else sees them read-only.

The page has six tabs:

| Tab | What it holds |
|---|---|
| **Files** | The uploaded files and how far each one is, plus upload, download and delete. |
| **Tasks** | Every job under the workflow step it is at, with every step of every job one click away in **Expand**. Here you assign people, set deadlines and act on jobs. See [The Tasks tab](#the-tasks-tab). |
| **Analyses** | Saved word-count analyses. Click **New analysis** to make one. See [Analysis bands](/docs/concepts/analysis-bands). |
| **Workflow** | Which workflow the project follows and its steps. See [The Workflow tab](#the-workflow-tab) below. |
| **Settings** | Project settings, translation memory, machine translation and terminology — the same as in the wizard. A project made from a template says **Based on template** at the top. |
| **History** | Who did what and when in this project. |

A project opens on **Tasks**. Until it has a file, **Tasks** is greyed out and the project opens on **Files**.

## The Workflow tab

The **Workflow** tab is one card. The **Workflow** dropdown lists the workflows in your library, each with its steps, and shows the one this project follows. Under it, the project's steps are listed read-only, numbered in order, with their type, the [AI router](/docs/guides/ai-engines-and-prompts#send-each-language-to-the-right-engine-with-an-ai-router) if the step has one, and who picks the step up. An AI or Pre-translation step shows **Automated** instead of a person. A router's name is a link to its card on the **AI** page. While machine translation runs on upload and an MT router is chosen, an unnumbered **Pre-translation** row heads the list; its router name opens the **Settings** tab and rings the **Machine translation** card.

To change the steps, pick another workflow. A dialog asks **Use workflow "…"?** and explains that the current steps are replaced with a copy of the workflow's steps. Steps that running jobs have already reached keep their place; only steps nobody has got to yet can change, and new steps are added at the end. Click **Use workflow** to confirm.

[**Raw MT**](/docs/guides/workflows-and-tasks#pre-translate-only-with-no-linguist) is chosen in the [New project form](#create-a-project). A created project cannot switch to it or away from it, so this tab has no **Workflow** / **Raw MT** toggle and, on a Raw MT project, the dropdown is locked.
- **Set deadlines.** Each step carries its deadline at the right of its row. See [Set deadlines](/docs/guides/workflows-and-tasks#set-deadlines).

Steps with no library workflow behind them — a project that started from a template carrying its own steps — sit under an empty **Choose a workflow…** field. Pick a workflow to replace them.

A project always gets a copy of the workflow's steps. Editing or deleting the library workflow later never changes a project that copied it. Build and change workflows themselves on the **Workflows** page in the left menu; see [Workflows and tasks](/docs/guides/workflows-and-tasks).

## Upload files

1. On the **Files** tab click **Upload files**, or drag files anywhere onto the page. You can pick several files at once.
2. A dialog opens: **Upload N files**, listing what you picked. Deadlines are not asked for here — the new jobs take them from the project's workflow, see [Set deadlines](/docs/guides/workflows-and-tasks#set-deadlines).
3. Keep **Auto-analyze project files** ticked to get one analysis of the whole project after the upload settles.
4. Click **Upload**.

Each file is one row. Its **Status** sits beside a count of the target languages that are done — for example **2 of 3 ready**; hover it to see which languages are in which state.

| Status | Meaning |
|---|---|
| **Processing** | A target language is being parsed, pre-translated or checked by a QE+APE step. |
| **Stopped** | A run on a target language was stopped before it finished. It goes on once someone starts it again — see [Stop a running job](#stop-a-running-job). |
| **In progress** | Translation or review is under way. |
| **No MT** | Pre-translation found no usable MT engine for a target language: the project's MT router has no route or default for it, or the AI engine it points to cannot translate that language pair. People can still translate it by hand; once its workflow is done it reads **Ready**. To machine translate it, fix the router, then choose **Pre-translate** > **Apply MT** — see [Machine translation](/docs/guides/machine-translation#choose-the-router-for-a-project-or-template). On a [Raw MT](/docs/guides/workflows-and-tasks#pre-translate-only-with-no-linguist) project the job waits on its **Pre-translation** step until then. |
| **Ready** | Every target language has finished its workflow. |
| **Failed** | A target language failed to process. |

Depending on the project settings, translation memory and machine translation run right after the upload. A JSON, PO, TXT or Excel file is read with **Strings contain HTML** as it is at upload and keeps that mode; changing the setting later affects new uploads only. Click a row to open the **Tasks** tab filtered to that file.

Translations already in an XLIFF or PO file are imported only into the project target language the file names; see [Formats](/docs/reference/file-formats#formats).

A file that cannot be read is refused with a message that says what to do. See [Limits](/docs/reference/file-formats#limits) for the cases.

The **Upload files** button is hidden while a project is **Completed**. Set its status back to **In progress** first.

Use **Search files…** or the **Status** filter chip to narrow the list. Tick files — or the checkbox in the header, for every file shown — and a bar appears with the number selected, **Download** and, for owners and admins, **Delete**.

![The Files tab: one row per file with its status and Download icon](assets/project-files.png)

### Choose the columns of an Excel file

For an Excel file whose row 1 has no language codes, the upload dialog also asks what to translate; the ⓘ next to the file's name explains the choices. Each sheet that holds text is a row showing its choice, such as **Column B**, **Columns B, D**, **All columns**, **Whole sheet** or **Not translated**; click it to change:

- **Columns to translate** — one or more columns whose cells become segments, each shown with its text in row 1, such as **B (English)**. Only columns that hold text are listed. On a sheet with more than one column, **Select all** at the right end of the label picks every column at once; it then turns into **Clear**, which empties the field. With no column chosen, every text cell of the sheet is translated. The dialog starts with the column that has the most text.
- **Row 1 is a header** — its cells are column titles, not text to translate.
- With several sheets, untick a sheet to leave it out.

The translated file has each translation in the cell its source came from. If a workbook could not be read, **Retry** in its block reads it again; **Upload** waits until it has been read.

In a project that is not created yet, files upload as soon as you add them, so the same choices open in an **Excel columns** dialog, with the ⓘ next to its title; **Cancel** skips the file. The columns icon on the file's row under **Files (optional)** opens it again, and the file is read again with the new choice. Once the project is created the choice is fixed: to translate other columns, upload the file again.

## Download translated files

Every file row on the **Files** tab has a **Download** icon, beside the row's other icons. It opens a menu, the same one the labelled **Download** button opens in the selection bar on the **Files** and **Tasks** tabs and at the right end of the CAT editor's breadcrumb line. It offers three things:

| Option | What you get |
|---|---|
| **Translated file** | The deliverable, rebuilt in the original format. Available once the job has at least one **Approved** or **Auto-approved** segment. |
| **Bilingual XLIFF** | A two-language XLIFF 1.2 file for other CAT tools. Available as soon as the file has segments, even if nothing is translated yet. |
| **Source file** | The document exactly as it was uploaded. |

If several jobs are in scope the labels count them — **Translated files (3)** — and you get one `.zip` file. An option that is not available tells you why: **No approved segments yet**, **No segments to export**, or **No file selected**.

When a file cannot be rebuilt — a PDF, or an Excel workbook whose columns do not fit the job's language — **Translated file** stops with a message that says why. With several jobs the message names the file and nothing downloads. Which column fits a job is set out in [What you download](/docs/reference/file-formats#what-you-download).

## The Tasks tab

The tab lists one section per workflow step, all closed at first — when there is only one section, it starts open. Each section holds the jobs whose current task is at that step. Its header counts them — **Show 3 tasks**, or **Hide 3 tasks** while open — and a click opens or closes it. A step with no current tasks reads **No tasks** and does not open. A step's header turns green once every task at that step is completed, and yellow while any of its tasks is in progress. The colours follow the filters: they count the jobs you are looking at. A job's current task is the one in progress, or else the last one completed; a job with nothing started yet sits under the first step, and a job with no workflow steps sits in a last section, **No workflow step**. Each job shows once, as a table row: target language, file, and its current task's assignee, deadline and word count. Click the row to open the job in a new browser tab. The row's colour is the task's state. An icon at the end of the row marks an error, a task sent back, a QE+APE step running (while it runs, the row does not open), or a speech bubble for a comment the previous step left for the task — see [Complete a step](/docs/guides/workflows-and-tasks#complete-a-step). Point at the icon to read the message, the reviewer's reason, or the comment under **Comment from the previous step**. A row with several of these shows one icon, the first in that order, and its tooltip lists them all. The table always fits the page; in a narrow window the word count drops out. Drag the right edge of the **Lang**, **File**, **Deadline** or **Words** header to make that column wider or narrower; **Assignee** takes the space left. Double-click the edge to put it back. The widths apply to every step and are kept in this browser.

**Expand** opens the full grid in a window that fills the screen: workflow steps run across it and the jobs run down it, one row per job, with every task of every job. Each cell is a task: one job at one step.

A task shows who has the task (**Automated** on an AI or Pre-translation step, which has no deadline), its deadline (or **Done** and the date once it is completed) and the job's word count. Its colour is its state: grey **Not started**, yellow **In progress**, green **Completed** or red **Error**. The legend above the tasks counts each state — on the tab, the current tasks; in the expanded view, every task. Click a state there to set the **Status** filter to it, and click it again to clear the filter. On the tab, the legend and the **Status** filter leave out **Not started**; the expanded view has it. In the expanded view, a failed job shows its error message in the red cell, the step that sent a job back reads **Sent back** (hover it for the reason), a task handed a comment — finished or not — reads **Comment** (hover it for the text) unless an error or **Sent back** takes that line, and each step's header counts its completed tasks, for example **3/8**.

- In the expanded view, the **Language** column shows each target language once, across all its rows. The **File** column shows the file name and an **Open** button that opens the job in [the CAT editor](/docs/guides/cat-editor), in a new browser tab, on its current task: the one in progress, or else the last one completed. While the job's QE+APE step runs, the button is greyed out; point at it to see why.
- **Search files, languages, people…** matches file names, language codes and assignees. Searching for a person keeps only their tasks.
- To filter, click the filter icon after the search box and pick a filter: **Status**, **File** (when there is more than one file), **Language**, **Assignee** (which includes **Unassigned**) or **Deadline** (**Overdue**, **Due in 7 days**, **No deadline**). Its options open right away; pick one and the filter's chip appears on the bar. To change it, click the chip. To clear it, click its **×** or pick **All** (**Any** on **Deadline**) — the chip goes with it. Once every filter is set, the icon goes away. The expanded view always shows every filter chip, without the filter icon or the **×**; there, pick **All** in a chip to clear it. Both views apply the same filters. On the tab, the filters apply to each job's current task: a job shows if its current task matches. In the expanded view, a cell the filters leave out stays in its row, dimmed, and cannot be selected. Search and filter values are part of the page address, and the search text is shared with the **Files** tab.
- In the expanded view, Esc first clears the selection, then closes the window. The filters and the selection carry over between the tab and the expanded view; on the tab, only the current tasks in the selection count.
- In the expanded view, drag the right edge of a column header other than **#** to make the column wider or narrower. Double-click the edge to put it back. Your widths are kept in this browser.

## Select tasks and act on them

On the tab, click a row to open the job in [the CAT editor](/docs/guides/cat-editor), in a new browser tab, on its current task (not while its QE+APE step runs). Tick a row's box to select its current task, or untick it to drop it; the box in a step's header selects every task in that step. Use **Expand** to select tasks further along the workflow: there, click a task to select it, and Ctrl+click (Cmd+click on a Mac) adds or removes one. Click a row number or row header to select the whole row, or a step header to select that step in every row. Clicking a language selects every row in that language. Esc clears the selection.

A bar appears. It starts with the number of tasks selected and an **✕** that clears the selection, then **Download**, **Import XLIFF**, **Pre-translate** (Apply TM or Apply MT), **Complete step**, **Assign**, **Set deadline**, **Clear** and **Delete**. **Stop** joins the bar whenever at least one selected job is running — see [Stop a running job](#stop-a-running-job) — and **Re-run** whenever a selected task is in error. For owners, admins and project managers, **Skip AI step** follows **Re-run** whenever a selected Hybrid step task is in error because its AI run failed.

- **Assign** and **Set deadline** act on exactly the steps you selected, including steps that have not started. **Assign** skips completed steps. Both skip AI and Pre-translation steps, which run by themselves.
- **Complete step** acts only on tasks that are their job's current step.
- **Download**, **Pre-translate**, **Stop**, **Clear** and **Delete** act on the jobs the selected tasks belong to. **Import XLIFF** needs exactly one selected task. **Delete** removes those whole jobs, with every step.
- **Re-run** runs the failed automation again, once for each job with a selected task in error, and the toast counts the jobs queued — see [Recover from a failed run](/docs/guides/workflows-and-tasks#recover-from-a-failed-run).
- **Skip AI step** clears the AI error once for each of those jobs and leaves the Hybrid step in progress, so the assignee works the machine translation without the AI pass. See [Recover from a failed run](/docs/guides/workflows-and-tasks#recover-from-a-failed-run).
- **Pre-translate** and **Clear** leave alone segments someone else has [open in the editor](/docs/guides/cat-editor#when-someone-else-is-editing-a-segment) and say how many they skipped.

Tasks that do not fit an action are skipped, and the summary toast says how many. Tasks a filter hides are left out of every action.

Do not confuse the "clears" on this tab: the **✕** in the bar drops your selection, the **✕** inside the search box only empties it, a filter chip's **×** clears that filter, and **Clear** throws away the translations of the selected jobs.

## Stop a running job

To end a pre-translation or AI step early, select any of the job's tasks on the **Tasks** tab and click **Stop** in the selection bar. The button is there only while something in your selection is running, and it acts on those jobs alone — the rest are skipped and listed in the summary toast. There is no confirmation step. Nothing translated is thrown away; see [Stop a running pipeline run](/docs/guides/workflows-and-tasks#stop-a-running-pipeline-run) for how to pick it up again.

## Import an edited XLIFF

If someone edited a task's bilingual XLIFF in another CAT tool, you can bring the changes back into that task. Every role can do this; a linguist only on their own tasks.

1. Select one task on the **Tasks** tab and click **Import XLIFF** in the bar, or click **Import XLIFF** next to **Download** in the CAT editor.
2. Pick the file (`.xliff`, `.xlf`, `.sdlxliff`, `.mxliff` or `.mqxliff`). It imports at once into the task's language, and a toast says how many segments changed.

Segments are matched by their trans-unit ID. Locked segments are never changed, and neither are segments someone else has [open in the editor](/docs/guides/cat-editor#when-someone-else-is-editing-a-segment); a toast says how many of those were skipped. If the file would change approved segments, nothing is imported until you click **Overwrite**.

The import writes into the task's current step. While that task is in error, or its AI step is running, the import is refused with a message saying why. On a completed task, an owner, admin or project manager puts the task back in progress, and a completed job with it; for a linguist the import is refused.

An import replaces the target text of the segments it changes, so the QA findings on those segments — ignored ones included — are cleared. Use **Run QA** in the editor to check the new text. See [Quality assurance](/docs/guides/quality-assurance).

## Delete a file or a project

- The bin icon on a file's row on the **Files** tab removes the file with all its jobs, segments and translations. To remove several files, tick them and click **Delete** in the bar. Owner and admin only; while the project is still a **Draft**, project managers can remove its files in the New project form too.
- To remove one target language of a file, select its tasks on the **Tasks** tab and click **Delete** — see [Select tasks and act on them](#select-tasks-and-act-on-them).
- To delete a project, hover over it on the **Projects** page and click the trash icon. This deletes all its files and translations. Owner and admin only; project managers can throw away a **Draft** with **Discard** in the New project form.

## Project status

A project is **Draft**, **In progress**, **On hold** or **Completed**. It is a **Draft** while the New project form is being filled in and becomes **In progress** when you click **Create project**; it never goes back to **Draft**. Switch between the other three in the **Status** field on the project page. The **Status** filter on the list covers all four.

When **Complete the project when every task is done** is on (the default), the project marks itself **Completed** once every job in it is done. It also reopens itself to **In progress** whenever a job stops being finished — you upload another file, delete files, a reviewer sends a step back, or a manager edits a completed job. A project **On hold** is never changed automatically, in either direction. Templates remember this setting, so you can turn it off once for a whole customer.

## The History tab

The **History** tab is a table with **Type**, **Action**, **Changes**, **User** and **Date**, newest first. It records:

- **the project** — creation (and, when someone else finishes a draft, who published it), renames, status and deadline changes, settings changes, and the automatic completing and reopening described above;
- **resources** — translation memory and terminology assignments, the MT router, applying a template, and saving the project as a template;
- **the workflow** — using a library workflow, who a step was assigned to, and step deadline changes;
- **files and jobs** — uploads, renames, deletions, XLIFF re-imports, clearing translations, and downloads: the source file, the final file and a bilingual XLIFF;
- **runs** — pre-translation, **Apply TM**, **Apply MT** and AI steps starting, finishing and failing, plus stop requests and the stopped runs that follow them, with the counts they reached, and re-runs after a failure. A TM or MT run names the memories that gave matches and the MT engine it used, or says why it did nothing, for example **TM skipped: no TM for this language** or **no TM matches**;
- **steps** — completions and rejections, including AI and Pre-translation steps that complete themselves, completed tasks a manager reopened by editing them, and a failed AI run a manager skipped on a hybrid step. A completion quotes the comment left for the next assignee. Completing, sending back or skipping the AI step of a task in error records the error it dismissed;
- **analyses** — runs and deletions.

Narrow the table with the **Type** and **User** filter chips; **Clear filters (N)** resets both.

Automatic actions show **System** as the user. Hover a long entry in **Changes** to read it in full, or click the expand button to widen the column.
