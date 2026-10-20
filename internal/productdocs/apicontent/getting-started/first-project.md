---
title: "Your first project"
summary: "Create a project, upload a file, wait for its jobs, and download the translation."
order: 20
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - internal/publicapi/**
related: [getting-started/authentication, reference/pagination]
---
Project management needs the **Owner**, **Admin** or **Project manager** role. A **Linguist** key can read and export the jobs assigned to it.

## 1. Create the project

```bash
curl {app_url}/api/v1/projects \
  -H "Authorization: Bearer $OPENLOC_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name": "Spring catalog", "source_language": "en", "target_languages": ["de", "fr"]}'
```

The response holds the new project under `data`. Keep its `id`. Pass `template_id` to set the project up from one of your templates.

The customer and the domain are labels. Find the value ids with `GET /api/v1/labels` (the built-in labels have `key` `customer` and `domain`), then set them:

```bash
curl -X PUT {app_url}/api/v1/projects/42/labels \
  -H "Authorization: Bearer $OPENLOC_KEY" \
  -H "Content-Type: application/json" \
  -d '{"value_ids": [17, 23]}'
```

## 2. Upload a file

Send the file as `multipart/form-data` in a field named `file`.

```bash
curl {app_url}/api/v1/projects/42/files \
  -H "Authorization: Bearer $OPENLOC_KEY" \
  -F "file=@catalog.docx"
```

Each upload makes one job per target language.

An Excel workbook without language codes in row 1 is translated cell by cell: every text cell of every visible sheet. To translate only some sheets, or some columns of a sheet, add an `xlsx_layout` field as JSON text:

```bash
curl {app_url}/api/v1/projects/42/files \
  -H "Authorization: Bearer $OPENLOC_KEY" \
  -F 'xlsx_layout={"sheets":[{"name":"Products","header_row":true,"columns":["B","D"]},{"name":"Notes"}]}' \
  -F "file=@catalog.xlsx"
```

`columns` holds column letters; a sheet without them is translated whole. `header_row: true` keeps row 1 out of the translation. A sheet you leave out is not translated, and each translation is written back into the cell its source came from. The file's `xlsx_layout` returns the layout as stored, and is `null` for any other file.

A layout the workbook cannot satisfy is a `400` whose message says what to fix, for example `The workbook has no sheet named "Products".`, `Sheet "Notes" is hidden. Show it in Excel to translate it.` or `The chosen sheets and columns hold no text to translate.` The same `400` answers an `xlsx_layout` sent with a file that is not an `.xlsx`, or with a layout that reads a language column of a workbook with language codes in row 1 (the whole first sheet, or a column headed with a code): that workbook is read by its codes.

## 3. Wait for the jobs

```bash
curl {app_url}/api/v1/projects/42/jobs \
  -H "Authorization: Bearer $OPENLOC_KEY"
```

A job's `status` is `completed` when every segment is approved. If its `mt_skip_reason` is set, pre-translation found no machine translation engine for the target language, and a job on a pre-translation-only workflow stays open until the project's MT router covers the language and MT runs. Check every few minutes, not every second: keys have a [rate limit](/api-docs/reference/errors#rate-limits).

## 4. Download the translation

```bash
curl -o catalog.de.docx {app_url}/api/v1/jobs/7/export \
  -H "Authorization: Bearer $OPENLOC_KEY"
```

The export returns the file in its original format, with the translations filled in.
