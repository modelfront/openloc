---
title: "Terminology"
summary: "Build term bases, mark terms as preferred or forbidden, import CSV or TBX, and see terms while translating."
order: 40
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - web/src/pages/TerminologyList.tsx
  - web/src/pages/TerminologyBrowser.tsx
  - web/src/pages/terminology-browser/**
  - web/src/components/CreateTermBaseDialog.tsx
  - web/src/components/EditorSidebar.tsx
  - web/src/components/QAFindingsSection.tsx
  - web/src/components/SettingsEditor.tsx
  - internal/terminology/**
  - internal/tbx/**
limitations:
  - termbase-mode-inert
related:
  - guides/quality-assurance
  - guides/cat-editor
  - guides/projects-and-files
  - guides/templates
screenshots:
  - terminology-browser.png
  - editor-terminology.png
  - project-settings.png
---
A term base is a glossary: a list of the words and phrases your customer wants translated in one fixed way. In this app a term base is built from *concepts*. A concept is one idea — for example "password" — and it holds one or more terms in each language, such as "password" in English and "Passwort" in German. Each term has a status that says whether translators should use it or avoid it. Assigned term bases show up in the CAT editor while translating and feed the terminology QA checks.

## Create a term base

1. Open **Terminology** in the left menu.
2. Click **New term base**.
3. Enter a **Name** and pick the **Source language**. Target languages are not chosen here; they come from the terms you add or import.
4. Click **Create**. The new term base opens right away.

The list shows each term base with its languages (source first, then the targets), the number of concepts, and who created it. Use the **Created by** filter to see only one colleague's term bases. The trash icon deletes a term base with all its concepts. Deleting a term base also removes it from the terminology settings of every project and template.

## Add a concept and its terms

Open a term base and click **New concept**. The **New concept** dialog has:

- **Domain** — an optional subject area such as "software" or "legal".
- **Definition** — an optional short explanation of the concept.
- **Terms** — one card per term. Click **Add term** for more. Each card has a **Language**, the **Term** itself, a **Status**, an optional **Part of speech** and **Notes**.

Click **Save**. Every term needs a language; the dialog reminds you if one is missing. A concept can hold several terms in the same language (synonyms) and terms in as many languages as you need.

### Term status

| Status | Meaning | In the editor |
| --- | --- | --- |
| **Approved** | Use this translation. | Shown as a suggestion you can click to insert. |
| **Forbidden** | Do not use this translation. | Shown with a warning when it appears in your text. |
| **Pending** | Suggested, not yet reviewed. | Shown as a suggestion, but not enforced by QA. |

Terms you add by hand start as **Pending** until someone approves them. Imported terms are different: a CSV or TBX row with no status arrives as **Approved**, and so does every source term in a CSV. An imported term base is therefore enforced by the terminology QA check straight away — look through an import before you assign it to a project.

## Edit terms in the browser

The term base page lists concepts in a table. The first column shows every term grouped by language with its status badge; the other columns are **Definition**, **Domain** and **Info** (created and updated stamps).

- **Change a term.** Click the term text, edit it, and press Enter. Escape cancels.
- **Change a status.** Click the status badge and pick a new one. Forbidden terms are shown struck through.
- **Add a term to a concept.** Click **Add term** under the concept, choose the language, type the term, set the status, and press Enter or the check mark.
- **Edit definition or domain.** Click the text in the column.
- **Open the full concept.** Click the pencil in the **Info** column, or the expand icon next to a term, to open the **Edit concept** dialog. Deleting a term or the whole concept happens here, with a confirmation.

Use **Search concepts...** to find a term in any language. The language filter next to it limits the view to the languages you pick: type to narrow the list, click a language to add or remove it (the list stays open), or click the × on its chip in the field. Chosen languages show as code chips in the field; with none chosen it reads **All languages**, and **All languages** at the top of the list clears the filter. By default the source language is selected. Both the search and the filter are kept in the page address, so you can share a link to a filtered view. Click the term base name in the breadcrumb to rename it, and the description line under it to edit the description.

![The terminology browser](assets/terminology-browser.png)

## Import and export

Both buttons sit at the top right of the term base page. **Import** takes a CSV or a TBX file and picks the right importer from the file extension. **Export** is a dropdown with **CSV** and **TBX**.

### CSV

CSV is a plain spreadsheet file. The first row is a header. Recognised columns are `source_term`, `target_term`, `status`, `domain`, `notes`, and optionally `source_language` and `target_language`. Columns can be in any order, and `source` / `target` work as short names. The first row counts as a header when it names both a source and a target column; a UTF-8 byte order mark in front of it, as Excel adds, is ignored. A file without a header is read in the fixed order source, target, status, notes, domain. Each row becomes a concept with one source term and one target term. Rows without a source language column use the base's source language. Rows without a target language column use the base's target language when it has exactly one; otherwise the row is skipped, so add a `target_language` column for multilingual bases. Statuses are the same words as in the UI: approved, forbidden or pending.

The CSV export writes every language in the base, and you can import the exported file again. To move a term base between systems with its full structure, use **TBX**.

### TBX

TBX (TermBase eXchange) is the industry standard for glossaries, read and written by most translation tools. Import accepts both the older TBX version 2 and the current TBX version 3 (TBX-Basic). Subject fields become the domain, and the administrative status becomes the term status: deprecated, superseded and obsolete terms become **Forbidden**, and a status the importer does not recognise becomes **Pending**. Part of speech goes into the term's own **Part of speech** field; definitions, context and remarks are kept in the term's notes. Export always writes TBX-Basic version 3.

One detail: TBX has no "pending" status. Pending terms export as preferred terms and come back as **Approved** if you re-import the file.

After an import a message tells you how many terms were imported and how many were skipped. If nothing was imported — for example because the file's languages do not match the base — the message says so instead of reporting success. Large files can take a minute or two to process after the upload finishes.

## Assign a term base to a project or template

1. Open the project's **Settings** tab (or the template).
2. Scroll to the **Terminology** card.
3. Under **Add Term base**, pick a term base, then the source and target languages the assignment applies to. Choose **Create new term base...** to make one without leaving the page.
4. Save the row.

You can assign several term bases to the same language pair. Drag rows to set their order; terms from the first term base are listed first in the editor. A project created from a template inherits the template's terminology assignments. If the project has a domain, concepts with the same domain are preferred when several concepts match.

![The project settings page](assets/project-settings.png)

## Terminology while translating

When a translator opens a segment in the CAT editor:

![A segment whose source contains a glossary term: the term is underlined in the source, and the sidebar's Terminology section shows the approved target term](assets/editor-terminology.png)

- **Terms are underlined in the source text.** Longer terms win over shorter ones, so "email address" is matched as one term, not as "email" and "address". The underline is not limited to whole words, so a short term such as "cat" also appears underlined inside "category". The terminology QA check only counts whole words, so on a short term the underline and the check can disagree. Terms that contain punctuation, such as "R&D", "Node.js", "C++" or "TCP/IP", are found in the sidebar list too.
- **The Terminology section of the sidebar** lists every matching concept as source term → target term, with notes, a status icon (hover it to see the status) and the domain. The number next to the heading is the count.
- **Hover a term** to select the matching word in your translation. **Click it** to replace that word with the approved term, or to insert the term at the cursor if there is no match. Terms are never inserted automatically; the translator decides.
- **Applied terms turn grey, and the status icon becomes a check mark,** once the target term is in your text. Editing the text back makes the term active again.
- **Forbidden terms** cannot be clicked. If the forbidden word is present in your translation, a red warning triangle appears with the hint "Forbidden term is used in the translation — replace it".

The concordance dialog (Cmd/Ctrl+K) also has a **Terminology** tab, so you can search the assigned term bases for any phrase. Term lookups are live: a change in the term base shows the next time a segment is opened.

## Enforce terms with QA

Two QA checks read the assigned term bases. **Terminology** flags a segment where an approved translation of a matched concept is missing from the target. **Forbidden terms** flags a segment that contains a forbidden term. With the severity set to **Error**, a translator cannot confirm the segment until it is fixed or the finding is ignored. See [Quality assurance](/docs/guides/quality-assurance) for how to set this up.

A term check can be wrong about a single segment — a brand name that stays in English, a term the customer waived just here. Click **Ignore** on that finding in the sidebar's **QA findings** section. Ignoring is per segment, so the term base and the QA profile stay as they are for everyone else. See [Ignore a false positive](/docs/guides/quality-assurance#ignore-a-false-positive).
