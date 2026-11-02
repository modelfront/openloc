---
title: "Segmentation profiles"
summary: "Choose where files are split into segments: a segmentation profile per project and template, with break and no-break rules, abbreviation lists, a live preview, and SRX import and export."
order: 85
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-29
sources:
  - web/src/pages/segmentation/**
  - web/src/pages/file-configurations/**
  - web/src/components/segmentation/**
  - web/src/components/settings/FileConfigurationsSection.tsx
  - web/src/hooks/useSegmentationProfiles.ts
  - web/src/lib/segmentation.ts
  - internal/segmentation/**
  - internal/segmentationapi/**
limitations:
  - segmentation-patterns-re2
  - segmentation-context-window
  - segmentation-new-uploads-only
related:
  - reference/file-formats
  - guides/projects-and-files
  - reference/settings
  - reference/roles-and-permissions
---

## How a file is split

A file is split in two steps. First its structure gives the blocks: paragraphs,
table cells, list items, JSON values, XML elements. Then a **segmentation
profile** splits the text inside each block into sentences. A profile never
joins two blocks. [File formats](/docs/reference/file-formats) says which
formats are split into sentences.

Each project and template has a segmentation profile, and a file is split with
its project's profile when it is uploaded. Each file keeps the rules it was
uploaded with: editing or deleting a profile, or picking another one for the
project, changes how files uploaded afterwards are split. Files already in a
project keep their segments, and their translated files are built with the same
rules. A draft is the one exception: if you change its source language or its
profile before you publish it, its files are split again with the profile it
has then.

## Pick a project's profile

**Segmentation profile** is in the **File configurations** section of the New
project page, of a project's **Settings** tab and of the template editor, next
to the QA profile. Owners, admins and project managers pick it there. The icon
after the field opens the selected profile's page in a new tab.

- Every project and template has one. The list shows the profiles that suit the
  source language: those that list the language or its base language (`fr`
  for a `fr-CA` project), and those for any language.
- A new project or template starts with the built-in profile for its source
  language, or **Default** when there is none. A project made from a
  template takes the template's profile when it suits the project's language,
  else the built-in profile for the project's language. A template without a
  source language that keeps **Default** passes on nothing: each project
  made from it starts with the profile for its own language.
- When you change the source language and the profile no longer suits it, the
  field moves to the built-in profile for the new language.
- A project keeps its profile when someone later changes that profile's
  language. Its **Settings** tab then shows the profile marked
  **(not for this language)** until you pick another.

## The built-in profiles

**Default**, **Conservative**, **English**, **German**,
**Spanish**, **French**, **Italian**, **Portuguese**, **Russian** and **Thai**
come with OpenLoc. They are read-only: to change one, copy it.

- The language profiles end a sentence at `.`, `!`, `?` and `…` (with any
  closing quote or bracket) followed by a space and a capital letter, a digit or
  an opening quote or bracket. Each has a list of the language's abbreviations.
  Full-width `。`, `！` and `？` end a sentence without a space.
- **Default** suits any language. It has the same rules without an
  abbreviation list, and also ends a sentence at the Arabic, Urdu and
  Devanagari full stops.
- **Thai** keeps each block as one segment: Thai has no sentence-ending
  punctuation.
- **Conservative** also ends a segment at `:` and knows only a few
  abbreviations, so it makes fewer, longer segments. It suits technical
  documentation.

## Open the Segmentation profiles tab

Open **File configurations** in the left menu; it opens on the **Segmentation
profiles** tab. Owners, admins and project managers see it and may create,
edit, copy, delete and import profiles. Linguists do not see it.

The list shows every profile with its languages (**Any** for the
language-neutral ones) and its mode. A built-in profile has **(built-in)**
after its name, here and in the profile list of a project or template.
Click a row to open it.

## Create a profile

1. Click **Copy** on the row of the profile to start from, or **New profile**
   to start empty.
2. Enter a **Name** and pick the **Source languages**: one or more languages,
   or **Any language**. Picking **Any language** clears the languages, and
   picking a language clears **Any language**.
3. Change the rules, the mode and the abbreviation lists (below), and watch the
   **Preview**.
4. Click **Create**. The profile list opens again, with the new profile in it.

A project or template uses the new profile once you pick it in its settings.
You cannot change a profile's language to one that a template using it does
not suit: pick another profile for the template first.

## Edit the rules

- **Mode.** **Sentence** splits each block into sentences. **Paragraph** keeps
  each block as one segment, so the page hides the rules and both abbreviation
  lists. They are kept and come back when you switch to **Sentence**.
- **Rules.** Each rule has a pattern for the text **before the break** and one
  for the text **after the break**, and an action: **Break** or **No break**.
  The rules are tried from the top, and the first rule whose two patterns both
  match a position decides it. Where no rule matches, the text is not split.
  Put an exception (**No break**) above the rule it overrides. Drag a row by its
  handle to move it, **Add rule** adds one at the end, and the trash icon
  removes one. An empty pattern matches anything.
- **Patterns** are regular expressions. Look-ahead, look-behind and
  back-references are not supported. When you click **Create** or **Save**, a
  pattern that cannot be used is marked red with the reason, and the profile
  is not saved until you fix it.
- **Abbreviations.** A full stop after one of these words never ends a
  sentence. Type the word without the full stop (`Dr`, `e.g`). The match is
  exact: `Dr` does not cover `dr`.
- **Abbreviations before a number.** A full stop after one of these words does
  not end a sentence when a number follows, as in "p. 12" or "No. 5"; before
  anything else it ends one as usual.

The abbreviation lists are checked before the rules. Click **Save** to keep
your changes; the profile list opens again. If someone else saved the profile while you were editing,
OpenLoc says so: click **Reload** to see their version, then make your changes
again.

## Check the rules in the preview

The **Preview** splits the sample text with the rules as they are on screen,
saved or not. Type or paste your own text into the box. The numbered list shows
the segments. In the text above it, a scissors mark shows each break and a link
mark each place a rule or a list kept whole; hover over a mark to see which
rule (by its number) or which abbreviation decided it.

## Delete a profile

Click the trash icon on the profile's row. Files already uploaded keep their
segments. Projects that use the profile lose it: their **Settings** tab asks
for another profile, and an upload is refused with "This project has no
segmentation profile" until someone picks one. A profile that a template uses
cannot be deleted: pick another profile for the template first.

## Import and export SRX

SRX is the segmentation rules format other translation tools read and write.

- **Import SRX** reads a file and shows one profile per language rule in it,
  with its rules, ready to save. Name each one you keep, pick its source
  languages (filled in when the file names them) and click **Save profile**
  (**Save 2 profiles** for two, and so on). A rule whose pattern cannot be used
  here is left out and listed with the reason, and so are file settings that do
  not apply. When the file lets languages share rules, each profile gets the
  shared rules after its own.
- **Export SRX** downloads a profile as an SRX file. The abbreviation lists go
  in as rules, so other tools apply them too, and a file exported here imports
  back as the same profile.
