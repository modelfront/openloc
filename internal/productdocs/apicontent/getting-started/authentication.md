---
title: "Authentication"
summary: "Every request carries an access key. The key acts as you, with your role, in one organization."
order: 10
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-18
sources:
  - internal/auth/middleware.go
  - internal/auth/pat.go
  - internal/auth/pat_gate.go
  - web/src/components/ai/APITokensSection.tsx
related: [getting-started/first-project, reference/errors]
---
## Create an access key

Open **AI settings → Access keys**. Fill in a **Name**, how long the key lasts (**Expires**) and an **Access** level, then click **Create access key**:

- **Read only** — the key may only send `GET` requests. Any other method returns `403` with the code `read_only_key`.
- **Read and write** — the key may call every endpoint your role allows.

The key is shown once. Store it in a secret manager. Never put it in browser code or a public repository.

## Send the key

Send the key as a bearer token on every request. All endpoints live under `/api/v1` on the same address you use to open the app.

```bash
curl {app_url}/api/v1/projects \
  -H "Authorization: Bearer $OPENLOC_KEY"
```

## What the key can do

The key acts as you, in the organization where you created it. It has your role, checked on every request, so if your role changes, the key's access changes with it. Each endpoint under **API documentation** lists the roles that may call it.

A key that has expired or was revoked returns `401`. Create a new one under **AI settings → Access keys**.

When the server cannot check the key at all, it returns `503` with the code `unavailable`, not `401`. The key is not at fault: keep it and retry. See [errors](/api-docs/reference/errors).
