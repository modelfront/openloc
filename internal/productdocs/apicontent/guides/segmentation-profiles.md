---
title: "Segmentation profiles"
summary: "Copy a built-in segmentation profile, change it, and create projects that split their uploads with it."
order: 10
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-27
sources:
  - internal/publicapi/segmentation.go
  - internal/publicapi/projects.go
  - internal/publicapi/openapi/segmentation.yaml
  - internal/publicapi/openapi/projects.yaml
  - internal/segmentationapi/service.go
  - internal/segmentationapi/pick.go
related: [getting-started/first-project, reference/errors]
---
Every member can list, read and download segmentation profiles. Creating,
replacing and deleting need the **Owner**, **Admin** or **Project manager** role.

Each project names the profile its uploads are split with, and a file keeps the
rules it was uploaded with. A profile's `source_languages` list the languages it
suits, each with its regional variants; an empty list suits any language. A
profile suits a project's source language when it lists that language or its
base language, or lists none.

## 1. Find a profile to start from

```bash
curl "{app_url}/api/v1/segmentation-profiles?source_language=en" \
  -H "Authorization: Bearer $OPENLOC_KEY"
```

With `source_language`, the list holds the profiles that suit it. Built-ins have
`"builtin": true` and a `builtin_key` such as `builtin:en@1`. They are read-only:
a `PUT` or a `DELETE` answers `409`.

## 2. Copy it

```bash
curl {app_url}/api/v1/segmentation-profiles \
  -H "Authorization: Bearer $OPENLOC_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name": "English – legal", "source_languages": ["en"], "from_id": 3}'
```

`from_id` copies the definition of any profile you can read. Send `definition`
instead to start from your own rules.

## 3. Change it

`PUT` replaces the name, the language and the whole `definition`. Send the
`updated_at` you read; when the profile changed since, the answer is `409` and
you read it again. New `source_languages` that a template using the profile
does not suit is `409` too: move the template to another profile first.

```bash
curl -X PUT {app_url}/api/v1/segmentation-profiles/42 \
  -H "Authorization: Bearer $OPENLOC_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "English – legal",
    "source_languages": ["en"],
    "updated_at": "2026-10-21T09:30:12.512304Z",
    "definition": {
      "mode": "sentence",
      "rules": [
        {"before": "\\bArt\\.", "after": "\\s", "break": false},
        {"before": "[.!?]", "after": "\\s+\\p{Lu}", "break": true}
      ],
      "abbreviations": ["Dr", "e.g"],
      "number_abbreviations": ["No", "p"]
    }
  }'
```

- Rules are tried in order; the first whose `before` matches the text ending at
  a position and whose `after` matches the text starting there decides it.
  `"break": false` keeps the text whole. No match means no break.
- Patterns are RE2 regular expressions. A pattern with look-around or a
  back-reference answers `400` with a message that names the rule, such as
  `Rule 1: the before pattern is not valid: look-behind is not supported.`
- `abbreviations` are words, without the full stop, after which a full stop never
  ends a sentence; `number_abbreviations` only before a number. Both are
  case-sensitive and are tried before the rules.
- `"mode": "paragraph"` keeps each block as one segment.

Projects that use the profile split their next uploads with the change; files
already uploaded keep their segments.

## 4. Create a project with it

```bash
curl {app_url}/api/v1/projects \
  -H "Authorization: Bearer $OPENLOC_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name": "Contracts", "source_language": "en-GB", "target_languages": ["de"], "workflow_id": 7, "segmentation_profile_id": 42}'
```

The project needs a workflow too, as in [your first project](/api-docs/getting-started/first-project). A profile that does not suit the source language answers `400`. Leave
`segmentation_profile_id` out and the project takes its template's profile when
it suits the language, else the built-in profile for the language.

## SRX

`GET /api/v1/segmentation-profiles/{profileID}/srx` downloads a profile as an SRX
2.0 file for other translation tools. Reading an SRX file into profiles is done
in the app, on the **Segmentation profiles** tab of **File configurations**.

A profile that a template uses cannot be deleted (`409`). Deleting a profile
takes it off the projects that use it, and an upload to them answers `400`
until they name another profile. The API sets a project's profile only when it
creates the project: to pick another for an existing project, use the
project's **Settings** tab in the app or the MCP tools.
