# OpenLoc — ChatGPT / Codex plugin bundle

This bundle lists OpenLoc in ChatGPT and Codex. `.mcp.json` points at the
hosted MCP server, `https://app.openloc.com/mcp`; for your own deployment,
change it to `<your BASE_URL>/mcp`. The server lives in `internal/mcp`. Nothing
in this directory is executable: it is listing metadata, the server reference
and bundled skills.

## Contents

- `.codex-plugin/plugin.json` — the manifest (identity, listing copy, assets,
  component references).
- `.mcp.json` — the remote MCP server reference. Authentication is NOT
  declared here: ChatGPT discovers the OAuth authorization server through the
  MCP handshake (401 → `WWW-Authenticate` → RFC 9728 protected-resource
  metadata → RFC 8414), all served by the backend.
- `skills/` — packaged workflows over the MCP tools, in the Agent Skills
  format (SKILL.md with frontmatter).
- `assets/` — icon, logo and screenshots. The image files are not in the
  repository; `assets/README.md` has their specs.
