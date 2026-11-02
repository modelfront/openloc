---
title: "Errors and limits"
summary: "What an error response looks like, the codes to handle, and the rate limit."
order: 20
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-26
sources:
  - internal/httputil/errors.go
  - internal/httputil/response.go
  - internal/httputil/ratelimit.go
  - internal/auth/ratelimit.go
  - internal/auth/pat_gate.go
  - internal/auth/middleware.go
  - internal/api/api.go
  - internal/publicapi/*.go
  - internal/hold/errors.go
  - internal/segmentedit/errors.go
related: [getting-started/authentication]
---
## Error shape

Every error has the same body:

```json
{
  "error": {
    "code": "read_only_key",
    "message": "This access key is read-only."
  }
}
```

`code` is for your code to check. `message` is for people and may change. Some errors also carry `field`, the request field that was wrong.

## Status codes

| Status | Meaning |
|---|---|
| `400` | The request is wrong, or an upload is too large. `message` says what to fix. |
| `401` | The key is missing, expired or revoked. |
| `403` | Your role may not call this endpoint, or the key is **Read only** (`read_only_key`). |
| `404` | The item does not exist in your organization. |
| `409` | The item changed since you read it, or its state does not allow the action. Read it again, then retry. With `segment_held`, someone has the segment open in the editor; `message` names them. Try again later, not at once. |
| `422` | Quality checks block a segment confirm (`QAErrorsBlocked`). `qa_issues` lists each failed check with its `check_type`, `severity` and `message`. Fix the translation, then confirm again. |
| `429` | Too many requests (`rate_limited`). |
| `500` | Something failed on our side. A create that answers 500 has saved nothing, so you can send it again. |
| `503` | The key could not be checked right now (`unavailable`, "The service is busy. Try again in a moment."). The key is still valid. Retry shortly; do not discard the key. |

## Split and join errors

`POST /api/v1/jobs/{jobID}/segments/{translationID}/split` and `POST /api/v1/jobs/{jobID}/segments/join` refuse with these codes.

| Status | `code` | When |
|---|---|---|
| `400` | `split_position` | `offset` is not a valid cut: it is inside a tag pair or `{…}`, or not between two words. |
| `400` | `split_unsupported` | The segment cannot be split, for example a PO plural form. |
| `400` | `split_tags` | The segment's tags cannot be split, for example XLIFF inline codes with escaped markup. |
| `400` | `join_count` | Fewer than two segments. |
| `400` | `join_not_adjacent` | The segments do not follow each other. |
| `400` | `join_other_unit` | The segments are not sentences of one paragraph or pieces of one split segment. |
| `400` | `join_partial` | The join leaves out a piece of a split sentence. Include every piece. |
| `400` | `join_unsafe` | The sentences cannot be joined in this file format. |
| `409` | `segment_locked` | A segment is locked. |
| `409` | `segment_in_flight` | A segment is being pre-translated. |
| `409` | `pipeline_running` | Pre-translation is running on the job. |
| `409` | `conflict` | `expected_updated_at` no longer matches. Read the segment again. |
| `409` | `segment_held` | Someone has a segment open in the editor. |

Retry the `409` codes when the cause has passed.

## Rate limits

Each key has its own limit: 10 requests per second, with short bursts of up to 40. Over the limit, the API returns `429` with a `Retry-After` header that gives the seconds to wait.
