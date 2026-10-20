import { ApiError } from '../api/request'

/**
 * A single QA issue surfaced in a 422 confirm-block response.
 *
 * Distinct from `QaResult` (in `types/generated.ts`) which is the
 * persisted form returned by the on-demand QA-run endpoint — that one
 * carries `id`, `translation_id`, `created_at` from the DB row. The
 * 422-response form is the transient backend computation against the
 * proposed text and has none of those fields. Mirrors `qa.Issue` from
 * `internal/qa/checker.go`.
 */
export interface QaIssue {
  check_type: string
  severity: string
  message: string
  /**
   * True when a human marked this finding as acceptable. The 422 body carries
   * ignored findings too — `HasErrors` only guarantees that at least one
   * NON-ignored error exists — so every consumer must filter them out.
   */
  ignored?: boolean
}

/**
 * True when a thrown save error is a server-side QA block (HTTP 422 +
 * `error.code === "QAErrorsBlocked"`). Both checks matter:
 *
 *   - 422 alone is not enough: the codebase uses 422 elsewhere (e.g.
 *     terminology MT-engine-unavailable, see internal/terminology/handler.go).
 *     A generic "any 422 is a QA block" classifier would route those
 *     errors to the QA toast formatter — wrong message, no issue list.
 *
 *   - error.code is the identity contract. The handler
 *     emits `{ error: { code: "QAErrorsBlocked", ... }, qa_issues: [...] }`.
 *     Future 422s from HandleUpdateHuman with a different code would
 *     correctly NOT route through this branch.
 *
 * Centralised so callers don't reinvent the dual check.
 */
export function isQABlockError(err: unknown): boolean {
  if (!(err instanceof ApiError) || err.status !== 422) return false
  const body = err.body as { error?: { code?: string } } | undefined
  return body?.error?.code === 'QAErrorsBlocked'
}

/**
 * Extract the QA issue list from a 422 ApiError body. Returns an empty
 * array for a non-QA 422 or one without a `qa_issues` field. Callers should still
 * surface a generic "fix QA errors" message in that case.
 */
export function getQAIssuesFromError(err: unknown): QaIssue[] {
  if (!isQABlockError(err)) return []
  const body = (err as ApiError).body as { qa_issues?: QaIssue[] } | undefined
  return body?.qa_issues ?? []
}

/**
 * Format an error-issue list for a toast. Caps the visible message
 * count so a translation with many tag mismatches doesn't blow up
 * the toast layout. The toast carries the count plus a sample.
 *
 * Ignored findings are excluded: they neither block the confirm nor belong in
 * the count, and quoting one as the sample would name a finding the user has
 * already accepted.
 */
export function formatQAErrorToast(issues: { check_type: string; severity: string; message: string; ignored?: boolean }[]): string {
  const errors = issues.filter(i => i.severity === 'error' && !i.ignored)
  if (errors.length === 0) return 'Fix QA errors before confirming'
  const sample = errors[0].message
  if (errors.length === 1) return `Fix QA error before confirming: ${sample}`
  return `Fix ${errors.length} QA errors before confirming (e.g. ${sample})`
}
