---
title: "Filter profiles"
summary: "Copy a built-in filter profile, change its import options, and use it for a project or a single upload."
order: 20
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-11-02
sources:
  - internal/publicapi/filter_profiles.go
  - internal/publicapi/projects.go
  - internal/publicapi/openapi/filter-profiles.yaml
  - internal/publicapi/openapi/projects.yaml
  - internal/publicapi/openapi/files.yaml
  - internal/filterprofile/service.go
  - internal/filterprofile/pick.go
  - internal/filterprofile/options.go
  - internal/filterprofile/defaults.go
related: [getting-started/first-project, guides/segmentation-profiles, reference/errors]
---
Every member can list and read filter profiles. Creating, replacing,
deleting and setting the organization's default need the **Owner**, **Admin**
or **Project manager** role.

A filter profile holds the import options of one `format`: `html`, `xlsx`,
`json_i18n`, `po` (`.po` and `.pot`), `txt` or `xml` (generic XML, not Android
string resources). Other formats have no filter profiles. Each project names one
profile per format; a format it names none for uses its built-in `Default`. A
file keeps the options it was uploaded with.

## 1. Find a profile to start from

```bash
curl "{app_url}/api/v1/filter-profiles?format=xlsx" \
  -H "Authorization: Bearer $OPENLOC_KEY"
```

Built-ins have `"builtin": true` and a `builtin_key` such as
`builtin:xlsx-default@1`. Every format has a `Default`; `json_i18n`, `po`, `txt`
and `xlsx` also have `Plain text strings`. Built-ins are read-only: a `PUT` or a
`DELETE` answers `409`.

## 2. Copy it

```bash
curl {app_url}/api/v1/filter-profiles \
  -H "Authorization: Bearer $OPENLOC_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name": "Product sheet", "from_id": 12, "options": {"inline_html": true, "xlsx": {"sheets": [], "header_row": true, "columns": ["B"]}}}'
```

A copy takes the format of the profile it copies, and its options when you
leave `options` out. Send `format` and `options` instead of `from_id` to start
from scratch; `options` is required then, and an `html` or `xml` profile with
empty options (`{}`) starts from the defaults. Names are unique per format: a name in use is `409`.

The response holds only the options of the profile's format:

- `inline_html` — `json_i18n`, `po`, `txt`, `xlsx`: tags in strings become
  protected tags; `false` reads them as plain text. Tags only: attribute text
  is not translated, and `html` options do not apply to these strings.
- `xlsx` — `sheets` (names; empty: every visible sheet), `header_row`,
  `columns` (letters; empty: every text cell) and `target_columns`
  (`[{"language": "fr", "column": "C"}]`; empty: each translation replaces its
  source cell). With `target_columns`, `columns` must hold exactly one letter,
  the source column, which keeps its text, and each language's export writes
  into its own column on the same row. A target column that is the source
  column or another language's, a language listed twice or a code that is not
  a language is `400`. Exporting a language with no column fails with a message
  that names the columns; a regional job (`fr-CA`) uses its base language's
  column. Empty rules leave the column choice to the upload. A workbook with
  language codes in row 1 ignores them.
- `html` — `attributes` (element name to the attributes translated),
  `meta_description` and `skip_pre`. Only text attributes can be translated:
  `alt`, `title`, `placeholder`, `abbr`, `label`, `summary`, the text `aria-*`
  attributes such as `aria-label`, and `value` on an `input`; any other, or
  any attribute of `meta`, `link`, `base`, `html` or `head`, is `400`. Send the
  whole object: a field you leave out is empty, not the default.
- `xml` — `inline_elements`, `no_translate` (`[{"name": "translate", "value": "no"}]`;
  a marker needs a value), `ref_elements`, `ref_attributes` and
  `skip_elements`. Send the whole object, as for `html`.

## 3. Change it

`PUT` replaces the name and the whole `options`; the format never changes. Send
the `updated_at` you read; when the profile changed since, the answer is `409`
and you read it again.

```bash
curl -X PUT {app_url}/api/v1/filter-profiles/42 \
  -H "Authorization: Bearer $OPENLOC_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name": "Product sheet", "updated_at": "2026-10-29T09:30:12.512304Z", "options": {"inline_html": false, "xlsx": {"sheets": ["Products"], "header_row": true, "columns": ["B", "C"]}}}'
```

Projects that use the profile read their next uploads with the change; files
already uploaded keep their options.

## 4. Use it

Name it per format when you create a project. A format you leave out takes the
template's profile; without a template, the organization's default, else the
format's `Default`. `null` picks the `Default`.

```bash
curl {app_url}/api/v1/projects \
  -H "Authorization: Bearer $OPENLOC_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name": "Catalog", "source_language": "en", "target_languages": ["de"], "workflow_id": 7, "filter_profile_ids": {"xlsx": 42}}'
```

Or pick it for one upload with the `filter_profile_id` form field. It must be a
profile of the file's format, else the answer is `400`; an `xml` pick on a file
OpenLoc reads as Android string resources is ignored. An Excel profile with
column rules lays the workbook out when you send no `xlsx_layout`.

```bash
curl {app_url}/api/v1/projects/310/files \
  -H "Authorization: Bearer $OPENLOC_KEY" \
  -F "file=@catalog.xlsx" \
  -F "filter_profile_id=42"
```

## 5. Make it the organization's default

```bash
curl -X PUT {app_url}/api/v1/filter-profiles/defaults \
  -H "Authorization: Bearer $OPENLOC_KEY" \
  -H "Content-Type: application/json" \
  -d '{"format": "xlsx", "filter_profile_id": 42}'
```

Projects and templates created without a template then start with it for that
format; existing ones keep their picks. `"filter_profile_id": null` goes back
to the built-in `Default`. The answer lists the formats that have a default, and
`org_default` is `true` on that profile when you list or read it.

A profile that a template uses, or that is the organization's default, cannot
be deleted (`409`). Deleting a profile
puts the projects that use it back on the format's `Default`. The API sets a
project's profiles only when it creates the project: to pick others for an
existing project, use the project's **Settings** tab in the app or the MCP
tools.
