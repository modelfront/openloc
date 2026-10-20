---
title: "Your first project"
summary: "Create a project, upload a file, pre-translate it, edit in the CAT editor and download the result."
order: 20
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-16
sources:
  - web/src/components/SegmentFilterBar.tsx
  - web/src/pages/ProjectCreate.tsx
  - web/src/components/project/ProjectCreateWorkflowCard.tsx
  - web/src/components/workflow/WorkflowModeToggle.tsx
  - web/src/components/workflow/RawMTPane.tsx
  - web/src/components/workflow/WorkflowStepChips.tsx
  - web/src/pages/ProjectDetail.tsx
  - web/src/components/project/task-matrix/TaskMatrix.tsx
  - web/src/components/project/task-matrix/TaskMatrixToolbar.tsx
  - web/src/components/project/task-matrix/TaskStepSections.tsx
  - web/src/components/project/task-matrix/ProjectTasksTab.tsx
  - web/src/components/project/files/ProjectFilesTab.tsx
  - web/src/components/project/files/file-columns.tsx
  - web/src/lib/file-readiness.ts
  - web/src/pages/CATEditor.tsx
  - web/src/components/SettingsEditor.tsx
  - web/src/components/settings/TMProfilePicker.tsx
  - web/src/components/project/BulkJobActionsBar.tsx
  - web/src/components/project/ProjectLabelFields.tsx
  - web/src/components/project/ManageLabelsDialog.tsx
  - web/src/components/project/DownloadMenu.tsx
  - web/src/lib/source-file-formats.ts
  - internal/project/**
  - internal/pipeline/**
  - internal/file/**
related:
  - getting-started/welcome
  - getting-started/interface-tour
  - guides/projects-and-files
  - guides/cat-editor
  - guides/workflows-and-tasks
  - reference/file-formats
  - reference/keyboard-shortcuts
  - concepts/how-a-translation-flows
screenshots:
  - project-create.png
  - cat-editor.png
---
In this lesson you create a project, upload one file, let the system pre-translate it, fix a few segments in the editor, complete the two workflow steps and download the translated file. You need a Project manager, Admin or Owner role. Plan about ten minutes.

## Before you start

You need a short file to translate — a Word document (`.docx`), plain text (`.txt`), Markdown (`.md`) or XLIFF (`.xliff`, `.xlf`) all work. The full list is in [File formats](/docs/reference/file-formats).

It helps, but is not required, to have an MT router set up on the **MT** page. Without one, the pre-translation step fills nothing and you translate by hand. Both paths work for this lesson.

## Step 1: Sign in

Open OpenLoc in your browser. Depending on how your organization is set up, you sign in with Google, with Microsoft, or with your email and password. After sign-in you land on the **Projects** page.

## Step 2: Create the project

1. Click **New project** in the top right.
2. Choose **From scratch**. The form appears. **From template** fills it from a saved template instead — see [Start a project from a template](/docs/guides/templates#start-a-project-from-a-template).
3. In the **Project settings** block, fill in the basics:
   - **Project name** — for example "My first project". **Deadline (optional)** sits next to it.
   - **Source language** — the language of your file.
   - **Target languages** — one or more languages you want. Each one becomes a job.
   - Under **Labels**: **Customer** and **Domain**, plus any other label your organization has defined, through **Add label**. Leave them all empty for now.
4. Look at the **Translation memory** and **Machine translation** blocks. Each starts with a checkbox that decides what happens automatically after upload:
   - **Apply translation memory on start or upload** — reuse past translations first.
   - **Fill in machine translation on start or upload** — send the rest to machine translation.
   Both are on, and each stays locked until you tell its block where to work from. Leave them on.
5. Under each checkbox, choose that source. In **Translation memory**, the **TM profile** dropdown starts empty: pick a shared profile, or **From scratch** for a list of memories that belongs to this project alone. The TM list and the match scoring appear once you pick. In **Machine translation**, pick your **MT router** if you have one. A warning beside it names any target language the router has no engine for ([details](/docs/guides/machine-translation#choose-the-router-for-a-project-or-template)).
6. The **Workflow** card decides which steps each job goes through. Keep the toggle on **Workflow**: the dropdown shows a workflow from your organization's library, and its steps are listed under it in order. A new organization has one workflow, **Default**, with the steps **Translation** and **Review**. Each step carries its deadline at the right of its row, with a globe beside it for a target language due on a different date; files you upload later inherit them ([Set deadlines](/docs/guides/workflows-and-tasks#set-deadlines)). Once you have picked an MT router and left machine translation on, a **Pre-translation** row headed by the router's name opens the list; click the name to jump to the **Machine translation** card. **Raw MT** drops the dropdown and the steps for one automated **Pre-translation** step that machine-translates the file and completes the job by itself ([details](/docs/guides/workflows-and-tasks#pre-translate-only-with-no-linguist)); leave it for this lesson.
7. Under **Files (optional)**, drag your file into the box or click it to browse. It uploads right away and appears in the list with its segment count. Leave **Run analysis on upload** checked.
8. Click **Create project**.

The form saves itself as a draft while you fill it in — a grey **Draft** badge appears next to the title. If you leave the page, the draft waits in the **Projects** list (as **Untitled draft** if it has no name yet) and opens in the same form. See [Create a project](/docs/guides/projects-and-files#create-a-project).

![The New project form](assets/project-create.png)

You are taken to the project page, which opens on the **Tasks** tab. It has one section per workflow step, all closed at first unless there is only one; click **Translation** to open it. Each row is one job — your file in one target language — and its colour shows the state of the job's current task; the legend above the sections names each colour. See [The Tasks tab](/docs/guides/projects-and-files#the-tasks-tab).

## Step 3: Watch the automatic work

The file is already cut into segments (sentences or paragraphs) from the upload. Right after you click **Create project**, OpenLoc works in the background:

1. Looks each segment up in the translation memory. Exact matches are confirmed automatically. Close matches are filled in but left for you to check.
2. Sends the remaining empty segments to the machine translation engine.
3. Runs an analysis, because you left **Run analysis on upload** checked.

To follow it, open the **Files** tab. Your file's status reads **Processing** while the pre-translation runs, and **In progress** once the job is ready for you. **No MT** means the router has no engine for a target language, so that language was not machine translated ([what to do](/docs/guides/projects-and-files#upload-files)).

To interrupt the run — wrong file, wrong engine — tick the job's box in the **Translation** section of the **Tasks** tab, then click **Stop** in the bar that appears. Everything already translated is kept. See [Stop a running pipeline run](/docs/guides/workflows-and-tasks#stop-a-running-pipeline-run).

Open the **Analyses** tab to see how many words came from memory and how many are new. [Analysis bands](/docs/concepts/analysis-bands) explains the columns.

## Step 4: Open the editor

On the **Tasks** tab, open the **Translation** section and click your file's row. The CAT editor opens in a new browser tab; the project page stays open in the first one. "CAT" stands for computer-assisted translation. The editor shows the source text on the left and the translation on the right, one segment per row.

![The CAT editor](assets/cat-editor.png)

Each row has a status badge. The ones you see now are:

- **Pretranslated** — filled by translation memory or machine translation, not checked by a person yet.
- **Approved** — confirmed. Exact TM matches start here.
- **Untranslated** — empty, because nothing matched and no MT router was set.

The status filters above the grid show in brackets how many segments have each status. On the right, the sidebar shows TM matches, terms and, under **QA findings**, the quality issues found on the active segment.

## Step 5: Translate and confirm a segment

1. Click the target cell of the first row. It becomes editable.
2. Type or fix the translation. Your typing is saved as a **Draft** automatically.
3. Press **Ctrl+Enter** (**Cmd+Enter** on a Mac). This confirms the segment: its status becomes **Approved**, it is saved to the translation memory, and the cursor moves to the next segment.

Repeat for a few rows. Some useful keys while you work:

| Key | What it does |
|---|---|
| Ctrl/Cmd+Enter | Confirm the segment and go to the next one |
| Tab / Shift+Tab | Move to the next or previous segment without confirming |
| Ctrl/Cmd+Shift+S | Copy the source text into the target |
| Ctrl/Cmd+1 to 5 | Insert TM match 1 to 5 from the sidebar; it is saved as a **Draft** at once |
| Ctrl/Cmd+Z | Undo |
| Escape | Stop editing this row |

Editing an already approved segment moves it back to **Draft**. Press Ctrl/Cmd+Enter again to re-approve it. The full list is in [Keyboard shortcuts](/docs/reference/keyboard-shortcuts).

## Step 6: Complete the steps

At the top of the editor, the breadcrumb ends with the target language, the job's current step (**Translation**) and the step's due date. The buttons that finish the step sit at the right end of the same line.

1. Click **Complete step →**. A dialog opens. If some segments are not confirmed yet, the dialog tells you how many. You can go back and confirm them, or continue anyway. You can also leave a comment for the next assignee. Click **Complete step**. The job moves to the **Review** step.
2. Look through the segments once more. Then click **Complete final step ✓**. The dialog lists what happens when you confirm: the job is marked completed and your approved segments are saved to the translation memory. Click **Complete final step**.

The job status changes to **Completed**. If the project has only this one job, the project completes too.

## Step 7: Download the translated file

1. Switch to the browser tab with the project page and open the **Files** tab.
2. In your file's row, click the **Download** icon.
3. Choose **Translated file**. The file is rebuilt in its original format with your translations.

The same menu also offers **Bilingual XLIFF** (a two-language file for other translation tools) and **Source file** (your upload, unchanged). The **Download** menu is also at the top right of the editor, and in the bar that appears when you select tasks or files. A download that covers several jobs comes as one zip file.

Your confirmed segments are now in the translation memory, so the next project with the same sentences starts with them already filled in.
