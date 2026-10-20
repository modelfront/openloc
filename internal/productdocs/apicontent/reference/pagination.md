---
title: "Lists and pagination"
summary: "Every list returns one page and a cursor for the next."
order: 10
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-13
sources:
  - internal/httputil/pagination.go
  - internal/publicapi/respond.go
related: [reference/errors]
---
## Response shape

Every list endpoint returns the same shape:

```json
{
  "data": [ ... ],
  "cursor": 1234,
  "has_more": true
}
```

## Next page

When `has_more` is `true`, send `cursor` back as a query parameter to get the next page:

```bash
curl "{app_url}/api/v1/projects?cursor=1234" \
  -H "Authorization: Bearer $OPENLOC_KEY"
```

Use `limit` to choose the page size. The default and the maximum are listed on each endpoint.

## Stable fields

Fields may be added to a response at any time. Ignore fields you do not know, and accept enum values you have not seen before. A field is never removed or renamed within `/api/v1`.
