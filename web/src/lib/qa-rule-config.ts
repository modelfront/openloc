/**
 * Parse a QA rule's `config` field into a plain object.
 *
 * Accepts all three shapes the field can arrive in, so the QA profiles UI
 * never loses the user's tag_checks sub-config:
 *
 *   1. Already-parsed object (the backend's raw JSON)
 *   2. Raw JSON string (alternative serializer)
 *   3. Base64-encoded JSON string (Go's `[]byte` marshalling)
 *
 * Returns `{}` for null/undefined/garbage so the caller can use `?? defaults`
 * cleanly without a try/catch.
 */
export function parseQARuleConfig(raw: unknown): Record<string, unknown> {
  if (raw == null) return {}
  if (typeof raw === 'object') return raw as Record<string, unknown>
  if (typeof raw !== 'string') return {}
  // Try plain JSON first
  try { return JSON.parse(raw) } catch { /* not JSON */ }
  // Then base64 → JSON
  try { return JSON.parse(atob(raw)) } catch { /* not base64 JSON */ }
  return {}
}
