---
title: "The CAT editor"
summary: "Translate, confirm and review segments; use the TM and terminology sidebar, tags, find and replace, and spell check."
order: 20
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-18
sources:
  - web/src/pages/CATEditor.tsx
  - web/src/components/TaskControls.tsx
  - web/src/components/CompleteStepDialog.tsx
  - internal/job/handler_skip_ai_step.go
  - web/src/lib/current-task.ts
  - web/src/lib/task-edit.ts
  - internal/taskedit/**
  - internal/job/handler_rerun.go
  - web/src/hooks/usePanelResize.ts
  - web/src/components/layout/NavRail.tsx
  - web/src/components/SegmentRow.tsx
  - web/src/components/SegmentRow.shared.tsx
  - web/src/components/SegmentEditor.tsx
  - web/src/components/SegmentEditorToolbar.tsx
  - web/src/components/SegmentFilterBar.tsx
  - web/src/components/SegmentSelectionBar.tsx
  - web/src/hooks/useSegmentSelection.ts
  - web/src/lib/segment-selection.ts
  - web/src/lib/segment-bulk.ts
  - web/src/lib/find-replace.ts
  - internal/segmentbulk/**
  - web/src/components/JobLeverageStatusBar.tsx
  - web/src/lib/completion-copy.ts
  - web/src/components/JobPipelineActions.tsx
  - web/src/hooks/useJobPipelineActions.ts
  - web/src/hooks/useClearTranslations.ts
  - web/src/components/ClearTranslationsDialog.tsx
  - web/src/lib/project-logic.ts
  - web/src/components/project/task-matrix/TaskMatrix.tsx
  - web/src/components/project/task-matrix/TaskStepSections.tsx
  - web/src/components/EditorSidebar.tsx
  - web/src/components/EditorSidebarCustomizer.tsx
  - web/src/components/EditorSidebarSection.tsx
  - web/src/lib/term-status.ts
  - web/src/components/EditorSidebarTMSection.tsx
  - web/src/lib/panel-insert.ts
  - web/src/hooks/useTMMatches.ts
  - internal/translationmemory/handler_concordance.go
  - web/src/lib/tm-diff.ts
  - web/src/lib/text-diff.ts
  - web/src/lib/track-changes.ts
  - web/src/lib/text-ranges.ts
  - web/src/lib/segment-decorations.tsx
  - web/src/lib/sidebar-sections.ts
  - web/src/components/ConcordanceModal.tsx
  - web/src/components/FindReplaceModal.tsx
  - web/src/components/SpellCheckDialog.tsx
  - web/src/components/SpellFixFields.tsx
  - web/src/components/CommentPanel.tsx
  - web/src/components/QAFindingsSection.tsx
  - web/src/components/SegmentHistorySection.tsx
  - web/src/lib/segment-history-group.ts
  - web/src/components/QASummaryDot.tsx
  - web/src/lib/editor-link.ts
  - web/src/lib/segment-columns.ts
  - web/src/hooks/useColumnBorderDrag.ts
  - web/src/components/layout/Breadcrumbs.tsx
  - web/src/components/ConflictBanner.tsx
  - web/src/components/MissingTagsBar.tsx
  - web/src/components/RichTextCellEditor.tsx
  - web/src/components/RegexSearchHelp.tsx
  - web/src/components/SearchOptions.tsx
  - web/src/lib/regex-help-patterns.ts
  - web/src/lib/segment-status.ts
  - web/src/lib/segment-filters.ts
  - web/src/lib/text-search.ts
  - web/src/lib/qa-findings.ts
  - web/src/lib/qa-block.ts
  - web/src/hooks/use-qa-finding-ignore.ts
  - internal/translation/**
  - internal/segment/**
  - internal/spellcheck/**
  - web/src/lib/spellcheck-scope.ts
  - web/src/api/spellcheck.ts
  - internal/parsers/android_xml/android_xml_content.go
  - internal/comments/**
  - internal/api/api.go
  - internal/hold/**
  - web/src/hooks/useSegmentHold.ts
  - web/src/hooks/useHoldStream.ts
  - web/src/components/SegmentHoldIndicators.tsx
limitations:
  - tm-diff-splits-words
related:
  - reference/keyboard-shortcuts
  - reference/segment-statuses
  - guides/translation-memory
  - guides/terminology
  - guides/quality-assurance
  - guides/workflows-and-tasks
screenshots:
  - cat-editor.png
  - editor-sidebar.png
  - segment-history.png
  - spell-check-dialog.png
---
The CAT editor is where you translate. It shows the source text and your translation side by side, one segment (a sentence or paragraph) per row. Your typing is saved as a draft; you confirm each segment when it is done. See also [Keyboard shortcuts](/docs/reference/keyboard-shortcuts) and [Segment statuses and origins](/docs/reference/segment-statuses).

![The CAT editor with the sidebar open](assets/cat-editor.png)

## Open the editor and find your way around

1. Open a project; it opens on the **Tasks** tab. On the job's current step, click **Show N tasks**; each row is one job (one file in one target language). Click the row. While the job's QE+APE step runs, the row does not open; point at the icon at its end to see why. You can also click a job in **Tasks** in the left menu. Either way the editor opens in a new browser tab, so the list stays where you left it.
2. The grid has six columns: segment number (with a selection checkbox), **Source**, **Translation**, **Match**, **Status**, and a last column without a name. **Match** shows where the text came from (TM percentage, **MT**, **APE**, **File**, repetition). **Status** shows a badge for **Draft**, **Auto-approved** or **Approved**. The last column holds a lock icon, the quality estimation score, one QA dot and a comment count, when the segment has them. Inactive rows show up to ten lines and end with **…**; click the row to see the rest.
3. To change a column's width, drag the border between two column headers. **#**, **Match** and **Status** can be made wider but not narrower than their standard widths. Double-click a border to go back to the standard widths. The widths are not saved: reloading the page restores them.
4. Above the grid: a progress bar and the toolbar with **Pre-translate**, **Concordance**, **Spell check** and **Run QA**. While a run is filling the job, a pulsing badge at the start of the toolbar row reports its progress (for example **Translating 40 / 240**) and the toolbar shows **Stop**. A bar along the bottom of the page gives the job's size (segments and words) and, in the middle, its leverage: how many words fall in each match band.
5. The breadcrumb at the top names the project and the file (a long name is cut short) and ends with the job's target language and, if the job has a workflow, a chip naming its current task: the step in progress, else the last step completed. The chip is blue while the task is in progress, green once it is completed, red when its automation failed, and grey, naming the first step, before the workflow starts. Point at it for the task's state; while it is in progress that includes who it is assigned to, and on the final step what completing it does. While the task is in progress, a pill after the chip gives its due date for this file and this target language, or **No due date** — see [Read deadlines](/docs/guides/workflows-and-tasks#read-deadlines). At the right end of the same line is **Download**, and before it what you can do with the task — see [Read the task controls](#read-the-task-controls).
6. The **Resources** sidebar on the right holds the memory matches, terms and findings for the open segment. If it is closed, click the vertical **Resources** tab.
7. To give the grid more room, click the panel button at the right end of the left menu's top row, which shrinks the menu to a strip of icons — see [The left menu](/docs/getting-started/interface-tour#the-left-menu).

## Move between segments

1. Click a row to open it.
2. Press **Tab** for the next segment and **Shift+Tab** for the previous one. This does not confirm anything.
3. Press **Ctrl+G** (Cmd+G on Mac) to open **Jump to segment**, type a number and press Enter.
4. Press **Escape** to stop editing. This does not undo your text; use **Ctrl+Z** (Cmd+Z) for that.

## Edit a segment

1. Type in the **Translation** cell. An empty cell reads **Click to translate**.
2. Pause for about three seconds, or move to another segment, and the draft is saved.
3. The status becomes **Draft**: safe, but not finished. Editing an **Approved** or **Auto-approved** segment also drops it to **Draft** until you confirm again.
4. To start from the source text, press **Ctrl+Shift+S** (Cmd+Shift+S). The source, with its tags, replaces your translation.
5. To empty the translation, click **🧹 Clear** on the top border of the editor, or press **Ctrl+Shift+Backspace** (Cmd+Shift+Backspace). Owners and admins get it on the job's first workflow step, on any open segment that is not locked and that nobody else is editing. When the segment has a saved translation, a dialog asks first ([what a clear removes](/docs/reference/segment-statuses#rules-to-remember)). With nothing saved, the editor is simply emptied.

6. A segment is one sentence or paragraph: **Enter** does nothing. Press **Shift+Enter** for a line break inside it.

A linguist works only in the target languages whose job they hold; see [Roles and permissions](/docs/reference/roles-and-permissions).

## Confirm a segment

1. Press **Ctrl+Enter** (Cmd+Enter). To confirm several segments at once, see [Select several segments](#select-several-segments-and-act-on-them).
2. The status becomes **Approved**, the translation is written to the translation memory (a **TM updated** toast appears), QA re-runs, and the editor moves to the next segment.
3. Every segment with exactly the same source text, in this file and this target language, gets your translation and becomes **Approved** too. The copy replaces whatever was there — including rows that were already approved, but never a locked one — and clears their QA findings. A message says how many rows were updated. The first such row shows **→R**; the copies show **⇢R**.
4. If the segment breaks a rule that the project's QA profile sets to **Error**, the confirm is refused with **Fix QA error before confirming: …**, or **Fix N QA errors before confirming (e.g. …)** when more than one rule fails. Warnings never block, and neither do findings you have ignored.

Only confirmed segments feed the translation memory. Drafts and auto-approved machine translation never go there.

A segment the [AI assistant](/docs/guides/ai-assistant#segments-the-assistant-confirms) confirmed shows a small grey **Confirmed by assistant** chip under **Approved** in the **Status** column. It is not in the translation memory yet. Press Ctrl+Enter on it to confirm it yourself: the chip goes away and the translation is written to the memory.

## Use the sidebar

![The Resources sidebar: TM matches, MT suggestion, Terminology, History, QA findings and Comments](assets/editor-sidebar.png)

1. **TM matches** lists up to ten translation memory hits for the open segment, in the order set by the project's [TM lookup order](/docs/concepts/tm-matching#several-memories), each with the percentage the grid's **Match** column uses (100% and 101% green, anything lower yellow) and the TM name. Click a hit to insert it, or press **Ctrl+1** to **Ctrl+5** (Cmd on Mac) for the first five; its tags are matched to your current source. An insert is saved as a **Draft** at once (a confirmed segment drops back to **Draft**), and **Match** shows the hit's percentage, as after pre-translation; later edits keep it. A source shorter than three characters is not looked up. The expand icon in the header shows hits in full.
   When a hit is not a 100% match, what it says that your segment does not is highlighted in yellow, and what your segment says that the hit does not is highlighted the same way in the **Source** column. The **Source** column compares against the best hit; point at another card, or move to it with the keyboard, to compare against that one instead.
2. **MT suggestion** shows the machine translation the pipeline already produced for this segment; a segment that has none is translated on demand. Click to insert; it is saved and shown in **Match** as **MT**, like a TM insert.
3. **Terminology** lists glossary terms found in the source as **source → target**, with a status icon and the domain; point at the icon to read the status (approved, pending or forbidden). An applied term shows a check in place of its status icon — see [Terminology while translating](/docs/guides/terminology#terminology-while-translating). Matching words are underlined in the source: red while the approved target term is missing from your translation, green once it is there. Click a term to insert it, or select a word in the editor first to replace only that word. A forbidden term in your translation shows a red warning.
4. **References** appears when the segment has a cross-reference chip. Click an entry to jump to the segment that defines it.
5. **QA findings** lists every quality check that fired on the open segment, with the full message next to **Error**, **Warning** or **Info**. The header counts them, for example **2 + 1 ignored**. With no segment open it reads **Select a segment**; on a clean segment, **No issues**.

To arrange the sidebar, click **Customize panel** (the sliders icon next to **Resources**). Drag a section by its grip to move it, or untick it to hide it; one section always stays on.

Drag the sidebar's left edge to make it wider or narrower; the grid takes whatever is left. The sidebar stops widening at about 640 pixels; past that the grid is the one that suffers. Drag it all the way to the right and the panel closes, the same as clicking its close button. Clicking the vertical **Resources** tab reopens the panel narrow; dragging that tab leftwards pulls it back out at the width you drag to. Double-click the edge to go back to the standard width, or put the keyboard focus on it and use the left and right arrow keys. Both the arrangement and the width are saved in this browser and are there the next time you open the editor.

## See a segment's history

**History** in the sidebar lists the recorded changes to the open segment, newest first. A segment nothing has touched yet reads **No recorded changes yet**.

![A segment's history: an approved APE rewrite with its changes marked against the machine translation below it, removed words struck through in red and added words in yellow](assets/segment-history.png)

1. Each row carries a chip for what happened: a memory match shows its percentage, the rest read **MT**, **APE**, **Edit**, **Propagated**, **File**, **Cleared**, **Approved**, **Auto-approved** or **Unapproved**. Next to it is the memory, engine or person behind the change, and how long ago it was. An **APE** rewrite the quality check also approved carries an **Auto-approved** chip beside it.
2. Rows that changed the text show that text. The last line of a row says **Approved** when somebody confirmed it, and names the workflow step it happened in.
3. A row's text is compared, word by word, with the older text below it; Japanese and Chinese text is split into words as well. Added words are highlighted in yellow, and removed words stay where they were, struck through and highlighted in red, just before any words that replaced them. A change that is only spacing, such as two spaces made into one, is marked too. Point at a mark to see **Added** or **Removed**. Nothing is marked on the first text the segment got, or on the first text after it was cleared.
4. Confirming a segment without changing a word is recorded as **Approved**. The quality check approving a translation without rewriting it is recorded as **Auto-approved** — see [the four outcomes](/docs/concepts/quality-estimation#the-four-outcomes). Editing a segment that was approved is recorded as **Unapproved**.
5. Your typing is not recorded while it is a draft. Completing a workflow step records the text of segments somebody typed and never confirmed, credited to the person who typed it. Those rows have no **Approved** on them, which is how you tell them from an edit somebody confirmed.
6. Consecutive changes by one person in one step are one row: it shows their last text, and its time and **Approved** come from their last change. Everything else keeps its own row. A result repeated exactly, such as the same memory match from a second pre-translation run, is shown once, at the time it first appeared. **Unapproved** shows only at the top, until the next change.

## Work with tags

Inline tags from the file show as chips such as **<1>…</1>** or **{1}**. You can move a chip but not type inside it. Hover to see the original markup; **¶** at the right end of the search and filter row shows hidden characters and full tag names.

1. When your translation lacks a tag from the source, an amber **Missing tag:** bar appears above the editor with one chip per missing tag.
2. Click a chip to insert it at the cursor, or press **F8** for the first missing tag.
3. Click **Insert all** or press **Alt+F8** (Option+F8 on Mac) to insert all remaining tags in source order.

## Format text

The small toolbar on the top border of the open segment's editor has **Bold** (Ctrl+B), **Italic** (Ctrl+I), **Superscript** and **Subscript**. HTML files keep this formatting on download; every other format, generic XML included, drops it (the file's own tags still come back as they were). An Android resource string keeps a tag only when its source string has that exact tag, so bold or italic you add to a string that had none comes out as literal text such as `<b>`. Formatting from the file itself appears as tag chips instead. In a JSON, PO, TXT or Excel file uploaded with **Strings contain HTML** off, segments have no tags: text such as `<b>` shows and saves exactly as typed, and the formatting buttons save nothing ([Settings](/docs/reference/settings)).

## Filter segments

1. Click a status pill (**Untranslated**, **Pretranslated**, **Draft**, **Approved**, **Auto-approved**; each shows in brackets how many segments it holds), an origin pill (**TM**, **MT**, **APE**, **File import**), a QA pill (**Has errors**, **Has warnings**, **No issues**, **Ignored**) or **Commented**. You can pick several status and several origin pills at once, and they narrow each other. The QA pills work one at a time: clicking another replaces it. **Has warnings** also lists segments that have errors. The QA pills treat an ignored finding as no issue, so a segment whose only finding is ignored counts as **No issues** and appears under **Ignored**.
2. Type in **Search segments...** and press Enter. The **✕** clears only the text.
3. Click the sliders icon for **Search in** Both, Source or Translation, **Case sensitive**, and **Regex** (regular expressions, a pattern language for text). The **?** icon opens a cheat sheet. An invalid pattern shows **Invalid regular expression** and matches nothing.
4. A badge shows the match count, for example **12 / 240**. Click **Clear** to reset.

## Select several segments and act on them

1. Tick the checkbox in a row's **#** cell; the whole cell is the click target. **Shift+click** a second checkbox to select every row between the two. **Ctrl+click** (Cmd+click) on a row adds or removes it without opening it. The checkbox in the **#** header selects every row the filter shows. A plain click on a row opens it and keeps the selection.
2. A bar appears above the grid with the count, for example **12 segments selected · 3 hidden by filter**. The buttons act only on the selected rows the filter shows; when the filter hides all of them, the buttons are disabled. Click **✕** in the bar, or press **Escape** (not while typing in a segment, a text field or a dialog), to clear the selection.
3. The bar offers:
   - **Confirm**, when the task can be edited. It writes each segment to the translation memory like Ctrl+Enter, but does not copy it to identical segments. It leaves alone locked segments, ones someone else is editing, empty ones, ones already confirmed, auto-approved ones (confirm those one by one) and ones with a QA error, whose findings then show in the editor, and, in JSON, PO, text and Excel files whose strings are HTML, ones with a tag the source does not have (no finding is stored; confirm the segment on its own to see which tag). A toast counts each reason.
   - **Lock** for project managers, admins and owners, and **Unlock**. See [Lock and unlock segments](#lock-and-unlock-segments).
   - **Run QA**, **Spell check** (the dialog opens on **Selected segments**) and **Find & replace** (the bar opens with **In selection** ticked).
   - **Pre-translate**, with **Apply TM** and **Apply MT**, on the first workflow step when the task can be edited and no run is in progress. It fills only what the toolbar's **Pre-translate** would fill — see [Pre-translation](/docs/concepts/how-a-translation-flows#stage-3-pre-translation) — and never locked segments. A run on a selection does not complete a **Pre-translation** step; only a run on the whole job does.
   - **Clear translation**, for project managers, admins and owners on the first step. A dialog first says how many of the segments are confirmed and how many locked ones are skipped; empty segments are skipped too.

## Find and replace

1. Press **Ctrl+H** (Cmd+H). A bar opens above the grid with **Find...**, **Replace...** an **Aa** checkbox for case-sensitive matching and an **Include locked** checkbox. **Include locked** is off each time the bar opens; hover it to see what applies to you. While segments are selected, an **In selection (N)** checkbox limits the search to them. If the selection is cleared, or the filter hides every selected row, while it is ticked, **Find** and **Replace all** refuse until you untick it; they never widen to the whole job.
2. Click **Find** (or press Enter in the Find field) to jump to the next match. **Find All** highlights every match in yellow.
3. Click **Replace** to replace the selected match and move on; it needs a match selected by **Find** first. **Replace all** replaces every match.
4. A replacement is saved as a draft, so a replaced **Approved** segment becomes **Draft**. Inline tags stay in place. Locked segments are neither found nor replaced unless **Include locked** is ticked. Then **Find** lists them for everyone, but only owners and admins can replace text in them, and a replace unlocks them; for anyone else they are left as they are. Segments where the match runs across a tag, and segments someone else is editing, are also left as they are. The toast says how many and why, including replacements that would leave a segment empty and ones that could not be saved (try again).
5. Press **Escape**, or the **✕** at the right-hand end of the bar, to close it. The **✕** inside a field only empties that field.

## Search the TM with concordance

1. Select a phrase in a source cell or in your translation, then press **Ctrl+K** (Cmd+K) or click **Concordance**. The dialog opens with the phrase already searched. Without a selection the box is empty.
2. Switch between **TM matches** and **Terminology**. Both source and target are searched.
3. Click a result to insert its full target into the open segment, or select part of it first to insert only that part. Tick **Keep open** to keep the dialog open. A concordance insert is an ordinary edit: it is autosaved like typing and adds no percentage to **Match**.
4. Drag the dialog by its title to push it off the text you are reading. It stays inside the window, and opens in the middle again next time.

## Comment on a segment

1. Open a segment and find **Comments** in the sidebar.
2. Type in **Add a comment...** and click **Add**. A comment count appears in the row; the **Commented** filter finds all commented segments.
3. Click **✕** to delete your own comments or comments written by **AI** (an AI review step can leave notes).

## Check spelling

Misspelled words in the open segment get a red wavy underline. Words in the project dictionary are not flagged.

1. Click an underlined word. A **Spell check** panel shows the source, the flagged text, the reason, suggestion chips and an editable **Change to** field.
2. Under **Replace**, click **In this segment**, **In non-locked segments (N)** or **In all segments (N)**.
3. Click **Ignore in this project** to add the word to the project dictionary. This needs the project manager, admin or owner role.
4. For a whole job, click **Spell check** in the toolbar. Choose **Non-locked segments** (selected when the dialog opens), **All segments (includes locked)**, **Matches current filter** or **Selected segments**, then **Check spelling**. The dialog walks through **Error 1 of N**. Click **Ignore once**, **Ignore all**, **Change** or **Change all**. **Ignore all** also adds the word to the project dictionary. Drag the dialog by its title to uncover the segment behind it.

   ![The spell check dialog part-way through a job: the flagged word in its sentence, a suggestion to accept, and the Ignore once, Ignore all, Change and Change all buttons.](assets/spell-check-dialog.png)
5. Corrections are saved as drafts, so a corrected **Approved** segment becomes **Draft**. Locked segments are checked and changed only by **All segments (includes locked)** in the dialog and **In all segments** in the panel, which only owners and admins can choose; a corrected locked segment is unlocked, since a draft is never locked. Every other choice skips them.

## Run QA

Click **Run QA** to check every segment for tag, number, terminology and punctuation problems. A segment with findings gets one dot in the last column, in the colour of its most severe open finding: red for an error, yellow for a warning, blue for information. When every finding on it is ignored, the dot is a hollow grey ring. Hover the dot to read all of the segment's findings, ignored ones included.

Findings from the last check are already there when you open the job, so you see the dots before you run anything. While open errors and warnings remain, the toolbar button reads **QA (3)** with the count; with none left it reads **Run QA**. After a run a toast reports **QA completed: N issue(s) found**, plus the number ignored if there are any. See [Quality assurance](/docs/guides/quality-assurance).

## Ignore a QA finding

1. Open the segment and find the finding in the sidebar's **QA findings** section.
2. Click **Ignore**. The finding stays listed, greyed out, with **Ignored by …** and the date. The row's dot takes the colour of the next most severe open finding, or becomes a hollow grey ring when none is left.
3. An ignored finding does not block confirming the segment, is not counted in **QA (N)**, and counts as no issue for the QA filter pills. The **Ignored** pill lists the segments that carry one.
4. To bring it back, click **Un-ignore** on the same entry.

Anyone who can edit the segment can ignore and un-ignore. For when an ignore survives an edit and when it comes back as a new finding, see [Ignore a false positive](/docs/guides/quality-assurance#ignore-a-false-positive).

## Lock and unlock segments

Locking marks a translation as final. Project managers, admins and owners can lock; anyone who can edit the segment can unlock it. A project manager cannot lock a segment [someone else is editing](#when-someone-else-is-editing-a-segment); owners and admins can.

1. Open a translated segment and click **🔓 Lock** on the top border of the editor. The segment becomes **Approved** and read-only, and shows **🔒** in the last column. The approval counts as a confirm: it is written to the translation memory under your name. An **Auto-approved** segment, or one auto-approved in an earlier step that nobody has edited, stays **Auto-approved**. An empty segment cannot be locked. To lock many at once, use the [selection bar](#select-several-segments-and-act-on-them); it skips empty segments.
2. Nothing changes a locked segment: not typing, pre-translation, spell check or copying to repetitions. The one exception is [find and replace](#find-and-replace) with **Include locked**: an owner or admin can replace text in locked segments, and the replace unlocks them. A locked segment keeps its status when the job moves to the next workflow step, or back with **Reject**.
3. To change a locked segment, click **🔒 Unlock**. It stays **Approved** until you edit it. If the task is not yours to edit, you see **🔒 Locked** instead.

## When someone else is editing a segment

Opening a segment you may edit holds it for you, so nobody else can change it while you work. Anyone else who opens it sees **Name is editing this segment** above the editor and a small person-with-pen icon in the row's **#** cell; point at the icon to read the same text. For them the segment is read-only: they can still read it, its matches and its comments.

1. Your hold moves with you to the next segment. It ends when you close the segment, leave the editor or close the browser tab. After 5 minutes without typing or clicking, the editor closes your segment. If a tab crashes, its segment frees after about 90 seconds and shows as free to others within another 30 seconds.
2. A segment shows as held in every other editor of the job as soon as someone opens it, and as free as soon as they move on. Anyone who has it open then sees the saved text and can edit.
3. Your own hold never blocks you: your other browser tab, the [AI assistant](/docs/guides/ai-assistant), access keys, and any **Apply TM**, **Apply MT** or **Clear** you start act as you.
4. A save the server refuses shows **Name is editing this segment. Try again when they have moved on.**
5. Bulk actions leave such segments alone and say how many. The selection bar's toast reads, for example, **Confirmed 7 segments · 2 being edited by someone else**; find and replace adds **N being edited by someone else**; copying a confirm to identical segments warns **N repetitions were skipped because someone else is editing them.** Spell-check corrections, **Apply TM**, **Apply MT**, **Clear** on a whole job or project, and **Import XLIFF** each warn **N segments were skipped because someone else is editing them.** **Clear translation** on a selection skips them too, and so does **Lock** for project managers. **Approve all and complete** approves the rest and does not complete the step (see [Complete the step](#complete-the-step)).

## Resolve a conflict with another user

A segment someone else is editing is read-only for you, so a clash is rare. It can still happen when the same person saves from another tab or from outside the editor, or when a hold ran out meanwhile. If somebody else saves the same segment before you, an amber banner appears: **Name saved a different version** (or **Saved from another tab or session** when it was you). It shows **Their translation** and **My edit**. Click **Use theirs (discard mine)** or **Use mine (overwrite theirs)**.

A newer version of the open segment never replaces typing you have not saved. The banner appears when you save.

## Stop a running job

While a pre-translation run (**Apply TM** or **Apply MT**) or an automated quality step is live, **Stop** in the toolbar is the only pipeline action you can click. Everything done so far is kept, a note replaces the badge, and the grid becomes editable again. Project managers, admins, owners and the linguist assigned to the job can stop a run.

For what the note says and how to resume, see [Stop a running pipeline run](/docs/guides/workflows-and-tasks#stop-a-running-pipeline-run).

## Read the task controls

The end of the breadcrumb line, before **Download**, shows the state of the job's current task and what you can do with it. A job without a workflow shows nothing there and is always editable.

- **← Reject** and **Complete step →** (or **Complete final step ✓**): the task is in progress. **← Reject** is not shown on the first step. An AI step (quality estimation and automatic post-editing) and a **Pre-translation** step have nobody assigned, carry no due-date pill, and complete themselves. A **Pre-translation** step completes when the file's pre-translation finishes, and nothing can be edited while it runs. If the MT router has no engine for the job's language, the step stays open — see [Pre-translate only](/docs/guides/workflows-and-tasks#pre-translate-only-with-no-linguist).
- **Completed** and **Edit a segment to reopen this task.**: what owners, admins and project managers see once the task is completed. The first change you save reopens it — see [Reopen a completed task](/docs/guides/workflows-and-tasks#reopen-a-completed-task).
- **Completed** and **Read-only**: what everyone else sees.
- **Re-run** and **Complete step →**, with the error message in a row under the toolbar: the job's pre-translation or AI step failed, and nobody can edit until it is re-run or the step is completed — see [Recover from a failed run](/docs/guides/workflows-and-tasks#recover-from-a-failed-run). When the AI check of a **Hybrid** step failed, owners, admins and project managers also see **Skip AI step** in the error row: the error clears and the step stays in progress, so the translation is worked without the AI check.
- **Not started**: the workflow has not begun, so nothing can be edited yet.

When a later step sent the job back, a red row headed **Sent back from** and the step's name gives the reason. While the task is in progress, a blue row headed **Comment from the previous step** shows the note left when that step was completed — see [Complete a step](/docs/guides/workflows-and-tasks#complete-a-step). Click either heading to hide or show its text; both are shown again each time you open the editor.

While the task cannot be edited, nothing in the editor changes text. You cannot type, insert a memory match, the MT suggestion, a term or a concordance result (**Ctrl+1** to **Ctrl+5** included), or correct spelling: the **Spell check** dialog offers only **Ignore once** and **Ignore all**, and **Ctrl+H** opens a **Find** bar with no replace field or buttons. **Pre-translate** and **Clear** are hidden. If the task changes while you work, for example someone completes it, your next save is refused with a message saying why, and the controls show the new state.

## Complete the step

1. Click **Complete step →** (or **Complete final step ✓**). A dialog opens.
2. If segments are left unapproved, it says what is left, for example **There are 3 drafts and 2 untranslated segments.** Drafts are all segments with a translation that is not approved yet, pretranslated ones included.
3. Optionally write a **Comment for next assignee** (the final step has no comment box). Then choose:
   - **Approve all and complete** confirms every draft like Ctrl+Enter — each goes to the translation memory — and then completes the step. Untranslated segments cannot be approved and stay as they are. If a QA error or a tag the source does not have blocks a segment, or someone else is editing one, the others are approved, the step is not completed, and the dialog lists the segment numbers: the ones to fix, and the ones to try again when they are free.
   - **Complete step** completes the step and leaves the segments as they are.

   The final step marks the job completed and saves human-approved segments to the translation memory.
4. To send the job back, click **← Reject** and give a reason. The previous assignee sees **Sent back from …** with your reason.

Completing a step makes the next one the current task; after the final step the controls read **Completed** — see [Read the task controls](#read-the-task-controls). While an AI step runs, a waiting screen replaces the grid; when it finishes, the job moves to the next step on its own and the grid loads again. See [Workflows and tasks](/docs/guides/workflows-and-tasks).
