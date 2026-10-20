---
title: "Errors and limits"
summary: "What an error response looks like, the codes to handle, and the rate limit."
order: 20
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-18
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

## Rate limits

Each key has its own limit: 10 requests per second, with short bursts of up to 40. Over the limit, the API returns `429` with a `Retry-After` header that gives the seconds to wait.
