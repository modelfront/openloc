import { ApiError } from '../api/request'
import { SEGMENT_HELD_CODE } from '../api/holds'
import { isTaskLockedError } from './task-edit'

/**
 * True when a write was refused because another user has the segment open in
 * the editor (409 `segment_held`). The message names them; there is no newer
 * row to show, so it is not a version conflict.
 */
export function isSegmentHeldError(err: unknown): boolean {
  return err instanceof ApiError && err.status === 409 &&
    (err.body as { error?: { code?: unknown } } | undefined)?.error?.code === SEGMENT_HELD_CODE
}

/**
 * True when a thrown save error represents an optimistic-locking conflict
 * (HTTP 409) — the backend rejected the write because another user updated
 * the row in the meantime.
 *
 * A 409 from the task-edit guard (`error.code` task_errored, ai_step_running,
 * pretranslation_running, task_not_started) is not a version conflict: there is no newer row to show,
 * so it goes to `handleTaskLocked` (lib/task-edit.ts) instead of the conflict
 * banner. Neither is `suggestion_gone` (see `isSuggestionGoneError`).
 *
 * Centralised so callers don't reinvent the `instanceof ApiError && status === 409`
 * check (and so a refactor that changes status from number to string can't
 * silently break the conflict-detection branch in SegmentRow).
 *
 * Used by `SegmentRow.tsx` for both `updateTranslationHuman` and
 * `saveTranslationDraft` save paths.
 */
export function isSaveConflictError(err: unknown): boolean {
  return err instanceof ApiError && err.status === 409 && !isTaskLockedError(err) &&
    !isSegmentHeldError(err) && !isSuggestionGoneError(err)
}

/**
 * True when a Resources-panel insert was refused because the TM entry or
 * engine behind it was deleted after the panel listed it (409
 * `suggestion_gone`). Nothing newer to show: the text stays in the editor.
 */
export function isSuggestionGoneError(err: unknown): boolean {
  return err instanceof ApiError && err.status === 409 &&
    (err.body as { error?: { code?: unknown } } | undefined)?.error?.code === 'suggestion_gone'
}

/**
 * Server-side state of the conflicting row. Mirrors the `conflict` object
 * the backend returns in its 409 body (see `internal/translation/handler.go`).
 * Drives the side-by-side conflict banner in SegmentRow.
 */
export interface ConflictPayload {
  id: number
  // The banner shows the current visible target.
  active_text: string | null
  active_rich_text: string | null
  updated_at: string | null
  edited_by: number | null
  edited_by_name: string
}

/**
 * Extract the typed conflict payload from a 409 ApiError. Returns null if the
 * error doesn't carry a conflict body; the caller then falls back to a generic
 * error toast.
 */
export function getConflictPayload(err: unknown): ConflictPayload | null {
  if (!(err instanceof ApiError) || err.status !== 409) return null
  const body = err.body as { conflict?: ConflictPayload } | undefined
  return body?.conflict ?? null
}

/**
 * Server-side state of a conflicting TM entry. Mirrors the `conflict` object
 * the backend returns from `PATCH /api/tms/.../entries/...` on a 409
 * (`internal/translationmemory/handler.go::writeTMConflictResponse`). Drives
 * the same side-by-side conflict banner used in the CAT editor — single field at a
 * time (the cell being edited), but both source + target are returned so
 * the frontend can surface either.
 *
 * `updated_by` is the raw label stored in the row (typically the editor's
 * email). `updated_by_user_id` is null when the label can't be resolved to
 * a user record — e.g. machine writers like "tmx-import". The banner uses
 * the resolved id for same-user-vs-different-user detection.
 */
export interface TMConflictPayload {
  id: number
  source_text: string
  target_text: string
  source_rich_text: string | null
  target_rich_text: string | null
  updated_at: string | null
  updated_by: string
  updated_by_user_id: number | null
  updated_by_name: string
}

/**
 * Extract the typed TM conflict payload from a 409 ApiError. Same shape as
 * `getConflictPayload` but returns the TM body. Returns null if the error
 * doesn't carry a TM conflict body (legacy backend, deleted-row 409, etc.).
 */
export function getTMConflictPayload(err: unknown): TMConflictPayload | null {
  if (!(err instanceof ApiError) || err.status !== 409) return null
  const body = err.body as { conflict?: TMConflictPayload } | undefined
  return body?.conflict ?? null
}
