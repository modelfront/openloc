---
title: "Filter profiles"
summary: "Choose how each file format is read: whether strings contain HTML, which Excel sheets and columns to translate, which HTML attributes and XML elements count. A profile per format for each project and template, and another one for a single upload when you need it."
order: 86
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-11-03
sources:
  - web/src/pages/filter-profiles/**
  - web/src/components/filter-profiles/**
  - web/src/components/settings/FileConfigurationsSection.tsx
  - web/src/components/project/files/UploadFilterProfile*.tsx
  - web/src/hooks/useFilterProfiles.ts
  - web/src/hooks/useUploadFilterPicks.ts
  - web/src/lib/filter-options.ts
  - web/src/lib/filter-formats.ts
  - internal/filterprofile/**
  - internal/parser/xlsx_targets.go
related:
  - reference/file-formats
  - guides/segmentation
  - guides/projects-and-files
  - reference/settings
  - reference/roles-and-permissions
---

## What a filter profile is

A filter profile holds the import options of one file format: what in a file
of that format is text to translate, and how its tags are read. These formats
have filter profiles:

| Format | Files | Options |
|---|---|---|
| HTML | `.html`, `.htm` | Attributes to translate, meta description, preformatted text |
| Excel | `.xlsx` | Strings contain HTML, sheets, header row, columns, target columns |
| JSON | `.json` | Strings contain HTML |
| PO | `.po`, `.pot` | Strings contain HTML |
| Plain text | `.txt` | Strings contain HTML |
| XML | `.xml` (not Android string resources) | Elements to skip, do-not-translate markers, inline elements, references |

Other formats are read the same way every time; [File formats](/docs/reference/file-formats)
says how.

Every format has a built-in **Default** profile, which reads files the way
OpenLoc always has. JSON, PO, plain text and Excel also have a built-in
**Plain text strings** profile. Built-in profiles cannot be changed; copy one
to make your own.

## Pick a project's profiles

Each project and template names one filter profile per format, in the
**File configurations** card of its settings, under **Filter profiles**. The
list is closed at first; its heading names the formats that have a pick, or
says **All Default**. Click it to see one picker per format. A
format shows **Default (built-in)** until you pick another profile, and
picking it again goes back to it. Your organization's default carries an
**Org default** label in the picker. The same card is on the **New project**
page and on a template.

A project made from a template takes the template's profiles. A new project
or template without a template starts with your organization's default for
each format (below).

Each file keeps the options it was read with. A change of the project's
profile, or of the profile itself, applies to files uploaded afterwards;
files already in a project stay as they are.

### Your organization's default

The **Filter profiles** tab lists the profiles by format, each format closed
at first with its organization default in the heading; click a format, or
**Expand all**, to see its profiles.

**Set as org default** on a profile makes it the starting pick of its format
for new projects and templates made without a template; the profile shows an **Org default** label. A built-in profile can
be the default too. **Remove org default** goes back to the built-in
**Default**. Changing the default leaves existing projects and templates as
they are.

## Pick another profile for one file

The upload dialog on a project's **Files** tab shows a **Filter profile** row
for each file whose format has profiles, set to the project's profile for that
format. Pick another one to read just that file with it. A long list of files
scrolls. An XML profile picked for a `.xml` file that turns out to be Android
string resources is ignored.

On the **New project** page, a file you drop asks for its filter profile when
its format has a profile of your organization. A format with only the
built-in profiles uploads straight away, with the form's pick. The form's
picks are saved to the draft before the upload; if that fails, **Draft not
saved** appears with the reason and nothing is uploaded.

## Create or change a profile

Owners, admins and project managers manage filter profiles, set the
organization's defaults and pick profiles for projects and templates. [Roles and permissions](/docs/reference/roles-and-permissions)
says who else can read them.

1. In the left menu, open **File configurations**, then the **Filter
   profiles** tab.
2. Click **New filter profile**, or **Copy** on a profile to start from it.
3. Choose the **Format** and enter a **Name**. A copy keeps the format of the
   profile it copies; the format of a saved profile never changes.
4. Set the **Import options** (below) and save.

A name is used once per format. If someone else saved the profile while you
were editing it, the save is refused: reload it and make your changes again.

Deleting a profile is refused while a template uses it or while it is your
organization's default: pick another profile for those templates, or another
default, first. Projects that use it go back to the format's **Default**.

## Import options

### Strings contain HTML

JSON, PO, plain text and Excel. When on, tags in the strings, such as `<b>`,
become protected tags. Turn it off when the strings are plain text: a `<b>`
is then ordinary text, and the translation is written exactly as typed. It
covers tags only: text in attributes, such as `title` or `alt`, is not
translated, and the options of HTML profiles do not apply to these strings.

### Excel sheets and columns

These rules choose what to translate in a workbook whose row 1 does not hold
language codes:

- **Sheets** — the sheet names to translate. Empty: every visible sheet.
- **Row 1 is a header** — row 1 names the columns and is not translated.
- **Columns** — the column letters to translate, such as B. Empty: every
  text cell.
- **Target columns** — for a multilingual workbook: one row per language,
  each with the column its translation goes into, such as French in C and
  German in D. Choose exactly one column under **Columns**: it holds the
  source text, which stays as it is. Empty: each translation replaces its
  source text in the same cell.

With rules, the upload dialog's column step starts from them, so a workbook of
the same shape needs no choices. With no sheets, no columns, no header row and
no target columns, the upload asks which columns to translate. A workbook with
language codes in row 1 is read by its languages and ignores the rules.

With target columns, text already in a language's column is imported as that
language's translation, to review, and each language's translations go back into
that column, on the source text's row. A **Translated file** download of several of
the file's languages, such as from the file's row on the **Files** tab, is one
workbook with each of them in its column; one language alone fills only its
column. Two languages that would share a column, such as French and French
(Canada) when only French has one, cannot be downloaded together: download
them one at a time, or give each its own column. A job in a regional variant,
such as French (Canada), uses the column of its base language when it has
none of its own. A language with no column cannot be exported to the workbook:
the export says so, and the bilingual XLIFF export still works. A file keeps
the columns it was uploaded with, so a language added to the profile later
applies to files uploaded after the change.

### HTML

- **Attributes to translate** — per element, the attributes whose text is
  translated. The text between tags is always translated. The Default
  translates `alt` and `title` of `img`, `title` of `a` and `abbr`, `alt`,
  `value`, `title` and `placeholder` of `input`, and `abbr` of `td` and `th`.
  Only text attributes can be added: `alt`, `title`, `placeholder`, `abbr`,
  `label`, `summary`, text `aria-*` attributes such as `aria-label`, and
  `value` on an `input`. Others, such as `href`, `src`, `onclick` or
  `data-*`, and every attribute of `meta`, `link`, `base`, `html` and `head`,
  cannot: a translation must not change what a page does or loads.
- **Translate the meta description** — the page summary in
  `<meta name="description">`. On by default.
- **Leave preformatted text as is** — text in `<pre>`, such as code samples,
  is not translated. On by default. When off, its text is translated and its
  spacing is kept.

### XML

Every element with text is translated, except what these lists leave out.
Names are case-sensitive, as in XML.

- **Elements to skip** — not translated, with everything inside them, and kept
  as they are in the translated file.
- **Do-not-translate markers** — `attribute=value` pairs, such as
  `translate=no`, each with a value: an element that has one is not
  translated. The Default has
  `translate=no` and `translatable=false`.
- **Inline elements** — formatting inside a sentence, such as `b` or `em`: a
  tag in the segment instead of a segment of its own.
- **Reference elements** and **Reference attributes** — cross-references
  whose text comes from elsewhere in the file: one tag in the segment, not
  translated. The Default has the `meta-ref` element and the `linkid`
  attribute.
