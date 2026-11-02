---
title: "Keyboard shortcuts"
summary: "Every shortcut in the CAT editor."
order: 30
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-30
sources:
  - web/src/pages/CATEditor.tsx
  - web/src/hooks/usePanelResize.ts
  - web/src/components/SegmentEditor.tsx
  - web/src/components/SegmentRow.tsx
  - web/src/components/SegmentEditorToolbar.tsx
  - web/src/components/MissingTagsBar.tsx
  - web/src/hooks/useSourceInserts.ts
  - web/src/components/EditorSidebar.tsx
  - web/src/components/EditorSidebarTMSection.tsx
  - web/src/components/FindReplaceModal.tsx
  - web/src/hooks/useSegmentSelection.ts
related:
  - guides/cat-editor
  - reference/segment-statuses
---
The CAT editor is built for the keyboard. Most work is: type, press Ctrl+Enter, type the next segment. On Windows and Linux use **Ctrl**; on Mac use **Cmd** (⌘) instead. The keyboard icon at the right end of the search and filter row, above the segment grid, shows the main shortcuts.

## Navigation

| Shortcut | What it does |
|---|---|
| Tab | Go to the next segment without confirming. The draft of the segment you leave is saved. |
| Shift+Tab | Go to the previous segment without confirming. |
| Down arrow, Up arrow | Move a line down or up in the translation. On its last line, Down goes to the next segment; on its first line, Up goes to the previous one. The cursor lands on the line you enter, at the same place across. A run of presses keeps the column it started in: a shorter line puts the cursor at its end, and the next longer line brings it back to that column. In a locked segment, Down and Up go straight to the next or previous one. |
| Ctrl+G (Cmd+G on Mac) | Open **Jump to segment**. Type the number the grid shows, such as 12 or 12.1, and press Enter. A number that a join removed opens the row that now holds it. |
| Escape | Leave editing mode. Your text stays; the last saved draft is kept. Also closes open dialogs and the find and replace bar. When no segment is open for editing, it clears the segment selection. |

## Selecting segments

| Shortcut | What it does |
|---|---|
| Ctrl+Click (Cmd+Click on Mac) on a row | Add the segment to the selection, or take it out. Outside the open editor's text. |
| Shift+Click on a row's **#** checkbox | Select every segment from the last one you ticked to this one. |

## Editing

| Shortcut | What it does |
|---|---|
| Ctrl+Enter (Cmd+Enter on Mac) | Confirm the segment. Status becomes **Approved**, the translation is written to the translation memory, and the editor moves to the next segment. Refused when the segment is locked or read-only (a segment someone else is editing is read-only), or when a QA rule at the **Error** level fails. |
| Shift+Enter | Insert a line break inside the segment. Plain Enter does nothing in the translation. |
| Ctrl+Shift+S (Cmd+Shift+S on Mac) | Copy the source text, with its tags, into the translation. |
| Ctrl+Z (Cmd+Z on Mac) | Undo the last change in the editor. |
| Ctrl+B (Cmd+B on Mac) | Bold. |
| Ctrl+I (Cmd+I on Mac) | Italic. |
| F8 | Insert the next missing tag, [placeable or term](/docs/guides/cat-editor#insert-placeables) at the cursor, in source order. A placeable goes in exactly as the source writes it, a term as its target term. |
| Alt+F8 (Option+F8 on Mac) | Insert all missing tags, placeables and terms at the cursor, in source order, with a space beside each placeable and term. |
| Ctrl+Shift+Backspace (Cmd+Shift+Backspace on Mac) | Clear the translation of the open segment. With a saved translation, a confirmation dialog opens first; with nothing saved, it only empties the editor. Available to anyone who can edit the task up to and including the workflow's first human or hybrid step (automated steps before it do not count), and not on locked segments or ones someone else is editing. |
| Alt+Shift+T (Option+Shift+T on Mac) | Split the open segment where you last clicked in its source text. Works while the cursor is in the translation. |
| Alt+Shift+J (Option+Shift+J on Mac) | Join the open segment with the next row: the next piece of a split, or the next sentence of the same paragraph. Works while the cursor is in the translation. |
| Alt+Shift+G (Option+Shift+G on Mac) | Open **Add term** to add a term to the term base. The words selected in the open segment's source and translation are filled in. Works while the cursor is in the translation, and the selected source words stay lightly marked until the dialog closes. The **+** on the **Terminology** panel does the same. |

The split and join shortcuts do nothing on a segment you cannot edit. See [The CAT editor](/docs/guides/cat-editor#split-and-join-segments).

Ctrl+Enter and Ctrl+Shift+S work even when your focus is on a sidebar button, for example right after you clicked a TM match or a term. Bold, italic and undo only work while the cursor is inside the translation text.

## Sidebar

| Shortcut | What it does |
|---|---|
| Ctrl+1 to Ctrl+5 (Cmd+1 to Cmd+5 on Mac) | Insert TM match number 1 to 5 from the **TM matches** list, whether or not the **Resources** sidebar is open. The match replaces the whole translation and is saved as a **Draft**, as when you click it; an exact match (the same source text, with no penalty) is then approved. Tags in the match are matched to your current source. |
| Left and right arrows on the sidebar's resize handle | Widen (left) or narrow (right) the **Resources** sidebar, one small step per press. A right-arrow press when the sidebar is already at its narrowest closes it, the same as clicking its close button. The handle is the thin line along the sidebar's left edge; press Tab until it is focused. |

## Search

| Shortcut | What it does |
|---|---|
| Ctrl+K (Cmd+K on Mac) | Open **Concordance search**. If text is selected in a source cell or in the translation, that phrase is searched right away. |
| Ctrl+F (Cmd+F on Mac) | Put the cursor in the **Search segments...** box, which searches every segment of the file. |
| Ctrl+H (Cmd+H on Mac) | Open the **Find and replace** bar. |
| Enter in the **Find...** field | Go to the next match and select it in the editor. |
| Enter in the **Replace...** field | Replace the selected match and go to the next one. Only works after **Find** has selected a match. |
| Enter in the **Search segments...** box | Apply the segment filter. |
| Enter in the **Concordance search** box | Run the search. |

## Notes

- Shortcuts do not fire inside text boxes such as the find field or the comment box, so Enter and Tab keep their normal meaning there.
- Clicking another row, clicking outside the grid, or moving with Tab all save the draft of the segment you leave. See [Segment statuses and origins](/docs/reference/segment-statuses) for what a draft means.
- There is no shortcut for **Spell check**, **Run QA** or **Complete step**. Use the toolbar buttons. See [The CAT editor](/docs/guides/cat-editor).
