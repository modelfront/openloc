---
title: "Translation memory"
summary: "Create translation memories, group them in TM profiles, assign them to projects, and import or export TMX."
order: 30
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - web/src/pages/TMList.tsx
  - web/src/pages/TMBrowser.tsx
  - web/src/hooks/useTMEntrySearch.ts
  - web/src/components/SearchOptions.tsx
  - web/src/components/RegexSearchHelp.tsx
  - web/src/pages/TMProfileDetail.tsx
  - web/src/components/tm/**
  - web/src/components/settings/TMProfilePicker.tsx
  - web/src/components/settings/TMScoringFields.tsx
  - web/src/components/CreateTMDialog.tsx
  - web/src/components/TMBulkFindReplaceModal.tsx
  - web/src/components/TMEntryHistoryDialog.tsx
  - web/src/components/project/TMAssignmentSection.tsx
  - web/src/components/SettingsEditor.tsx
  - internal/translationmemory/**
  - internal/tmprofile/**
  - internal/tmx/**
related:
  - concepts/tm-matching
  - guides/projects-and-files
  - guides/templates
  - guides/cat-editor
screenshots:
  - tm-list.png
  - tm-browser.png
  - tm-assignments.png
---
A translation memory (TM) is a database of sentences you have already translated. Each entry stores a source sentence and its approved translation. When a new project contains the same or a similar sentence, the TM offers the old translation, so nobody translates the same thing twice. For the theory behind match scores, see [How TM matching works](/docs/concepts/tm-matching).

## Create a translation memory

1. Open **Translation memories** in the left menu. The **Memories** tab is selected.
2. Click **New TM**.
3. In the **Create translation memory** dialog, enter a **Name**, the **Source language**, one or more **Target languages**, and optionally a **Customer** so the TM is easy to find later.
4. Click **Create**.

The new TM appears in the list with its languages, customer, entry count, and who created it. The **Created by** filter shows only one colleague's TMs. The trash icon deletes a TM with all its entries; only owners and admins can do this.

![The translation memories list](assets/tm-list.png)

## Browse and edit entries

Click a TM in the list to open the TM browser.

- **Rename** the TM by clicking its name in the breadcrumb.
- **Filter by language** with the target-language buttons above the table. **All** shows every entry.
- **Search** with the **Search entries...** box. It finds entries that contain your text, in any case, and tints the hits yellow. The options sit next to the box: **Search in** Both, Source or Target (Both by default), **Exact** (the whole text equals your search), **Case sensitive**, and **Regex** (regular expressions, a pattern language for text). The **?** icon opens a cheat sheet. An invalid pattern, or a search that takes too long, shows a message and no entries.
- **Edit an entry.** Click a source or target cell, change the text, and press Enter to save or Escape to cancel. Saving is immediate and has no undo. If a colleague saved a different version of the same cell in the meantime, a banner shows both versions and lets you pick one.
- **Tags.** If the target is missing tags from the source, a bar above the table lists them. Click a tag to insert it, or press F8 for the next one.
- **View edit history.** The clock icon opens the **Edit history** dialog: the current text, then every past edit, newest first, with who made it.
- **Delete an entry.** The trash icon asks for confirmation first.

The table also shows each entry's source language (SL), target language (TL), who last edited it, and when it was added. Long source and target text wraps up to 10 lines and ends with "…"; the entry you are editing shows its full text.

The **Edit history** and **Find & replace** dialogs can be moved: drag one by its title to see the entries behind it. It stays inside the window, and opens in the middle again next time.

![The TM browser with entries](assets/tm-browser.png)

### Replace text in many entries at once

1. In the TM browser, click **Find & replace**.
2. Type the text to find and the replacement. Leave **Replace with** empty to delete the found text.
3. Choose **Case sensitive** if needed, and pick **Both**, **Source** or **Target**. An active language filter limits the search to those entries.
4. Click **Find**. A preview says how many occurrences in how many entries will change; nothing is changed yet.
5. Click **Replace all** and confirm.

All changes are applied together and recorded in each entry's edit history. There is no undo, so check the preview first.

## Import and export TMX

TMX (Translation Memory eXchange) is the standard file format for moving TMs between translation tools.

**Import.** Open the TM in the browser, click **Import TMX** and pick a `.tmx` file. A progress bar shows the upload, then the import runs in the background with a banner counting entries. A final message tells you how many were imported and how many were skipped. Regional variants of the file's declared source language (for example "en-US" in a file declared as "en") are imported too. Characters written as entities in the file (such as `&amp;`) are imported as the characters themselves. A file that is not XML or has no entries is rejected at once. A file whose XML breaks further on shows as a failed import with a message naming the entry where it breaks; nothing from that file is imported, so you can fix it and upload it again. If the import stops before it finishes, for example because the server restarts, it is marked failed within about 45 minutes, with a message. The entries imported so far stay in the TM.

**Export.** Click **Export TMX** to download every entry as one TMX file. Inline tags are kept as TMX codes. The button is disabled while the TM is empty.

## Assign translation memories to a project or template

TMs are attached in the **Translation memory** card of the project's **Settings** tab, of a template, or in the **New project** wizard.

![The Translation memory card: TM profile picker, then the match scoring fields and the numbered TM rows in one section](assets/tm-assignments.png)

Under the **TM profile** dropdown, one section holds the match scoring and the memories. The dropdown says where they come from: **From scratch** means this project keeps its own list (see the next section).

1. The match scoring — three percentages:
   - **Fuzzy match threshold** — a fuzzy match is a TM entry that is similar to, but not the same as, the new sentence. Matches below this score are not offered. Set 100% for exact matches only.
   - **Language-variant mismatch penalty** — points taken off when only the region differs, for example an "en" entry on an "en-US" project.
   - **Near-exact penalty** — points taken off when only punctuation, capitalisation or spacing differs.
2. **Translation memories** — the TMs to look up, in order.
3. **TM lookup order** — shown once there are two or more TMs. It decides which memory fills a segment when several have a match. **By match percentage** (the default) takes an exact match from any TM before a fuzzy one. **By TM order** gives each TM its exact and fuzzy search before the next TM is asked, so a fuzzy match from the first TM is kept over an exact one further down. The editor's matches list follows the same setting. See [Several memories](/docs/concepts/tm-matching#several-memories).

In the **New project** wizard the dropdown starts empty. The match scoring and the memories table appear once you pick a profile or **From scratch**, and **Apply translation memory on start or upload** stays on but cannot be changed until then.

To add a TM, pick it in the **Add translation memory** field at the top right of **Translation memories**, or choose **Create new TM...**. Only TMs whose source language matches the project are offered. Each row has:

| Column | Meaning |
| --- | --- |
| Sequence | Rows are numbered. The first is marked **Master**: it is checked first, and ties in score go to the earlier TM. Drag the grip to reorder (keyboard: Space, arrows, Space). |
| Read | Look up matches in this TM. |
| Write | Save confirmed translations from this project into this TM. |
| Penalty | Points subtracted from every match out of this TM, for a memory you trust less. Any penalty means its matches are filled in for review instead of being approved. |

Changes save as you make them. A project created from a template starts with the template's TM setup.

## Share a setup with TM profiles

A TM profile bundles a TM list — with read/write modes, order and penalties — together with the match scoring. Projects and templates *follow* a profile live: add a TM to the profile once and every project that follows it gets it at the same time.

### Create a profile

1. Open **Translation memories** and switch to the **TM profiles** tab.
2. Click **New TM profile**, give it a name and an optional description, and click **Create**.
3. On the profile page, set the match scoring and add TMs in **Translation memories**, exactly like in project settings.

The profile page says how many projects or templates follow it; the tab lists each profile's TM count, threshold and usage.

### Follow a profile from a project

In the **Translation memory** card, pick the profile in the **TM profile** dropdown. The project's own TM list is cleared and the profile's values apply. The profile's name becomes a link to its page. Its scoring and TM rows sit behind a **Show profile settings** toggle with a one-line summary, collapsed by default.

**When the rows are editable.** While nothing else follows the profile, you can still edit the scoring and TM rows right here; the edits go to the profile. Once a second project or template follows it, the block becomes read-only and you edit the profile on its own page. If someone starts following it while you edit, your change is refused with a message.

### Stop following: Detach and customise

Click **Detach and customise** next to the profile link (or pick **From scratch** in the dropdown) and confirm. The profile's TMs and scoring are copied into the project, then the link is removed. The project keeps translating the same way, but later profile changes do not reach it.

### Turn a project's setup into a profile: Save as TM profile

While **From scratch** is selected, a **Save as TM profile…** link sits beside the dropdown. Click it, name the profile, and use the checkbox **Switch this project to follow the new profile** to choose between following it and keeping the project's own copy.

### Delete a profile

Only owners and admins can delete a profile, from the **TM profiles** tab. The delete is refused while any project or template still follows it.

## What gets written to a TM

Every **Approved** segment is written to the project's **Write** TMs for that language pair: when a linguist confirms it in the editor, and again when a workflow step or the job is completed. Segments an AI step auto-approved are never saved — see [why](/docs/concepts/quality-estimation#why-auto-approved-text-never-enters-the-translation-memory). If the project has no Write TM for the language pair, the completion message warns you and nothing is saved.

For the full rule, including what happens to exact matches that pre-translation confirmed on its own, see [What is written back, and when](/docs/concepts/tm-matching#what-is-written-back-and-when).

## Use TM matches while translating

In the CAT editor, the **TM matches** section of the sidebar lists matches for the active segment with their scores; click one (or press Ctrl/Cmd+1 to 5) to insert it. The insert is saved as a **Draft** and **Match** shows the card's percentage, as after pre-translation. Press Cmd/Ctrl+K (or click **Concordance**) to search the project's TMs for a selected phrase. See [The CAT editor](/docs/guides/cat-editor).
