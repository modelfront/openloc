-- name: CreateTranslation :one
INSERT INTO translations (segment_id, target_language, status)
VALUES ($1, $2, $3)
RETURNING *;

-- name: CreateTranslationWithOrigin :one
-- Sets active_text + origin directly. Used by test fixtures + integration
-- setup.
INSERT INTO translations (segment_id, target_language, status, active_text, origin)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetTranslation :one
-- Internal: used by pipeline. For user-facing handlers, use GetTranslationTenant.
SELECT * FROM translations WHERE id = $1;

-- name: GetTranslationTenant :one
-- Tenant-scoped: returns translation only if it belongs to the caller's tenant.
SELECT t.* FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE t.id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL;

-- name: GetTranslationInFileTenant :one
-- Tenant- AND file-scoped. The batch-draft endpoint receives translation IDs
-- from the client, so it must verify each one belongs to the file named in the
-- route — that file is what RequireFileOwnership / RequireLinguistFileAccess
-- actually authorised. Checking only the tenant would let a linguist with
-- access to one file write into a sibling file in the same tenant.
SELECT t.* FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE t.id = $1 AND s.file_id = $2 AND p.tenant_id = $3 AND p.deleted_at IS NULL;

-- name: GetTranslationBySegmentAndLang :one
SELECT * FROM translations WHERE segment_id = $1 AND target_language = $2;

-- name: GetTranslationConflictDetails :one
-- Tenant-scoped lookup for the 409 conflict-response payload. Returns the
-- current state of the translation plus the editor's display name. The
-- frontend renders the "their version vs my edit" side-by-side banner from
-- this payload — informed consent, no silent overwrite, no dismiss trap.
-- Tenant scope is required even though callers already pre-checked via
-- GetTranslationTenant — every user-data query carries its own scope
-- (CONTRIBUTING.md, "Tenant isolation").
SELECT t.id, t.active_text, t.active_rich_text, t.updated_at,
       t.edited_by, COALESCE(u.name, '') AS edited_by_name
FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
LEFT JOIN users u ON u.id = t.edited_by
WHERE t.id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL;

-- name: ListTranslationsByFile :many
-- Returns all translations for a file+language. Not paginated by design: loaded
-- in lockstep with segments for the CATEditor. The UNIQUE(segment_id, target_language)
-- constraint provides a composite index for this query pattern.
SELECT t.* FROM translations t
JOIN segments s ON s.id = t.segment_id
WHERE s.file_id = $1 AND t.target_language = $2
ORDER BY s.position;

-- name: ListTranslationsByFileTenant :many
-- Tenant-scoped: returns translations only if file belongs to caller's tenant.
SELECT t.* FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE s.file_id = $1 AND t.target_language = $2 AND p.tenant_id = $3 AND p.deleted_at IS NULL
ORDER BY s.position;

-- name: UpdateTranslationMT :one
-- The MT output is written to active_text; provenance (engine, kind) flows through
-- translation_history (kind='mt', mt_engine_id) + the `origin` column
-- updated by EmitAutomationHistory.
-- 'pre_translated' is the derived state `status='needs_review' AND origin IS
-- NOT NULL AND edited_by IS NULL AND predict_outcome IS NULL`. This write sets needs_review;
-- the `origin` stamp from EmitAutomationHistory completes the derivation.
--
-- SQL-level race guard. The caller filters rows via
-- ListUntranslatedByFile (`active_text IS NULL`), but a linguist confirming a
-- segment in another tab between the fetch and the write would otherwise be
-- clobbered. The `active_text IS NULL AND edited_by IS NULL` clause makes
-- this write a no-op once a human (or any prior phase) has written content,
-- and `locked = false` once someone locks the row mid-run; the :one return
-- value is then pgx.ErrNoRows and the caller (saveMTChunk) treats it as "row
-- no longer eligible, skip". The same holds for a row a user other than
-- @actor_id (the user who ran the phase; NULL = any user) has open.
UPDATE translations
SET active_text = $2, active_rich_text = $3,
    status = 'needs_review', updated_at = now()
WHERE id = $1
  AND active_text IS NULL
  AND edited_by IS NULL
  AND locked = false
  AND NOT translation_held_by_other(translations.id, sqlc.narg('actor_id')::bigint)
RETURNING *;

-- name: UpdateTranslationPredict :one
-- The APE rewrite is written to active_text (and preserved in
-- translation_history kind='ape'); predict_outcome carries the verdict.
--
-- SQL-level race guard. ListPreTranslatedByFile filters to
-- `status='needs_review' AND origin IS NOT NULL AND edited_by IS NULL AND
-- predict_outcome IS NULL`. Mirror that here so a linguist confirming a
-- pre-translated segment mid-predict-batch isn't clobbered. Returns
-- pgx.ErrNoRows when the row is no longer eligible; PredictFile logs +
-- continues to the next row.
-- active_rich_text travels with active_text: the editor and the export both
-- prefer the rich column, so an APE postedit that updated only the plain one
-- was invisible in the editor and absent from the delivered file. COALESCE
-- keeps a NULL from blanking rich text on the outcomes that leave the target
-- untouched; the caller passes the row's current value there and the decoded
-- postedit on the outcomes that rewrite it.
-- @review_all mirrors ListPreTranslatedByFile: people's text is fair game,
-- a locked row never is.
UPDATE translations
SET predict_outcome = @predict_outcome,
    active_text = @active_text,
    active_rich_text = COALESCE(sqlc.narg('active_rich_text')::text, active_rich_text),
    status = @status, qe_note = @qe_note, updated_at = now()
WHERE id = @id
  AND status = 'needs_review'
  AND (edited_by IS NULL OR (@review_all::bool AND NOT locked))
  AND predict_outcome IS NULL
RETURNING *;

-- name: UpdateTranslationHuman :one
-- Optimistic locking: if @expected_updated_at is non-null, the row only
-- updates when the current updated_at matches. A mismatch returns zero rows,
-- which sqlc surfaces as pgx.ErrNoRows — the caller maps that to 409 Conflict.
-- Passing NULL preserves the legacy behavior (no version check) for callers
-- that don't track updated_at (background workers, tests).
--
-- The translation_history `human` event is the audit; the sticky `origin`
-- column is left untouched (a human action does not reset automation
-- lineage).
--
-- confirmed_via: NULL means a person confirmed ('user'); the assistant's
-- confirm passes 'assistant', which keeps the row out of TM write-back.
--
-- A row another user holds (a live translation_holds row) is not written:
-- zero rows, like a version mismatch. The writer is @actor_id, else
-- @edited_by: the TM exact-match confirm records no editor but passes the
-- holds of the user who ran it. IS DISTINCT FROM, so a write with neither is
-- blocked by any live hold.
UPDATE translations
SET step_edited = step_edited OR active_text IS DISTINCT FROM @human_text,
    qe_note     = CASE WHEN active_text IS DISTINCT FROM @human_text THEN NULL ELSE qe_note END,
    active_text = @human_text, active_rich_text = @human_rich_text,
    status = 'approved',
    confirmed_via = COALESCE(sqlc.narg('confirmed_via')::text, 'user'),
    editing_time_ms = editing_time_ms + @delta_editing_time_ms,
    edit_distance = @edit_distance,
    edited_by = @edited_by,
    updated_at = now()
WHERE id = @id
  AND (sqlc.narg('expected_updated_at')::timestamptz IS NULL
       OR updated_at = sqlc.narg('expected_updated_at')::timestamptz)
  AND NOT translation_held_by_other(translations.id, COALESCE(sqlc.narg('actor_id')::bigint, @edited_by))
RETURNING *;

-- name: SaveTranslationDraft :one
-- Auto-save draft: persists the user's typing without promoting to 'approved'.
-- Always sets status='needs_review' regardless of the prior status — a draft
-- save means "the user typed and we're persisting the edit," which by definition
-- is no longer in any prior state. Drops 'approved' / 'auto_approved' back to
-- 'needs_review' so the row no longer counts as confirmed and the completion
-- dialog warns the PM.
--
-- Optimistic locking: same shape as UpdateTranslationHuman —
-- pass @expected_updated_at to enforce version check, NULL to skip it.
--
-- A draft writes no TEXT row to
-- translation_history — only confirms do, and that is what keeps the
-- edit-distance baseline honest. The handler does emit one text-less
-- `unconfirmed` row when this save takes an approval away.
--
-- Hold guard as in UpdateTranslationHuman: a row another user holds matches
-- nothing, whatever the lock or version.
UPDATE translations
SET step_edited = step_edited OR active_text IS DISTINCT FROM @human_text,
    qe_note     = CASE WHEN active_text IS DISTINCT FROM @human_text THEN NULL ELSE qe_note END,
    active_text = @human_text, active_rich_text = @human_rich_text,
    status = 'needs_review',
    -- A draft is never locked: a locked segment is always approved. Only
    -- find and replace with "Include locked" (owner/admin) drafts one.
    locked = false,
    editing_time_ms = editing_time_ms + @delta_editing_time_ms,
    edited_by = @edited_by,
    updated_at = now()
WHERE id = @id
  AND (sqlc.narg('expected_updated_at')::timestamptz IS NULL
       OR updated_at = sqlc.narg('expected_updated_at')::timestamptz)
  AND NOT translation_held_by_other(translations.id, @edited_by)
RETURNING *;

-- name: LockTranslation :exec
-- Internal: test fixtures only. Sets the flag and nothing else; product code
-- locks through segmentlock.Lock (LockApproveTranslations), which also
-- approves the segment.
UPDATE translations SET locked = TRUE, updated_at = now() WHERE id = $1;

-- name: UnlockTranslation :exec
-- Internal: bypasses tenant isolation. Callers MUST verify ownership first
-- (GetTranslationInFileTenant in HandleUnlock). Unlocking keeps the status.
UPDATE translations SET locked = FALSE, updated_at = now() WHERE id = $1;

-- name: ListJobTranslationsByIDs :many
-- The rows of @ids that belong to the job's file + target language, with what
-- a bulk action checks before writing. The file comes from a tenant-scoped job
-- lookup, so the file filter is the tenant scope. IDs outside the job are
-- simply not returned. held_by_other: a user other than @actor_id holds the
-- row now (any holder when the actor is NULL). machine_approved: the text is
-- the one an AI verdict approved and no person has touched it (an earlier
-- step's auto-approval that the step advance took back).
SELECT t.id, t.locked, t.status, t.active_text, t.active_rich_text, t.updated_at,
       s.source_text, s.source_rich_text,
       translation_held_by_other(t.id, sqlc.narg('actor_id')::bigint) AS held_by_other,
       COALESCE(t.edited_by IS NULL AND t.predict_outcome IN ('approved', 'ape_approved'), false)::bool AS machine_approved
FROM translations t
JOIN segments s ON s.id = t.segment_id
WHERE s.file_id = @file_id
  AND t.target_language = @target_language
  AND t.id = ANY(@ids::bigint[]);

-- name: ReapproveMachineTranslations :many
-- Internal: the ids come from ListJobTranslationsByIDs (tenant-scoped job).
-- Puts machine-approved rows back to auto_approved, each only while it is
-- still the unconfirmed, unlocked row the caller read; the ids put back are
-- returned.
UPDATE translations t
SET status = 'auto_approved', updated_at = now()
FROM (SELECT unnest(@ids::bigint[]) AS id, unnest(@expected_updated_at::timestamptz[]) AS expected_updated_at) i
WHERE t.id = i.id
  AND t.updated_at = i.expected_updated_at
  AND t.status = 'needs_review'
  AND NOT t.locked
RETURNING t.id;

-- name: UnlockJobTranslations :many
-- Unlocks the rows of @ids in the job's file + target language (tenant scope
-- as in ListJobTranslationsByIDs). Rows already unlocked are left alone and
-- not returned. The status stays.
UPDATE translations
SET locked = false, updated_at = now()
WHERE segment_id IN (SELECT id FROM segments WHERE file_id = @file_id)
  AND target_language = @target_language
  AND id = ANY(@ids::bigint[])
  AND locked
RETURNING id;

-- name: ListJobUnconfirmedTranslationIDs :many
-- The job's segments that have a translation and are not approved or
-- auto-approved, in file order: what "Approve all" in the Complete step
-- dialog confirms. Locked rows are always approved, so none come back. The
-- caller resolved @file_id tenant-scoped.
SELECT t.id
FROM translations t
JOIN segments s ON s.id = t.segment_id
WHERE s.file_id = @file_id
  AND t.target_language = @target_language
  AND t.status = 'needs_review'
  AND coalesce(t.active_text, '') <> ''
  AND NOT t.locked
ORDER BY s.position;

-- name: LockApproveTranslations :many
-- Locks the rows of @ids in the file + target language and approves them:
-- a locked segment is always Approved or Auto-approved. The caller resolved
-- @file_id tenant-scoped. Empty and already-locked rows are left alone and
-- not returned.
-- Auto-approved keeps its status and confirmed_via, and a machine-approved row
-- (as in ListJobTranslationsByIDs) goes back to auto-approved: a lock never
-- turns machine-approved text into an approval for TM. A person's earlier
-- confirm keeps confirmed_via = 'user'; every other row takes @confirmed_via.
-- prior_* are the values before the write, so the caller can tell which rows
-- this lock approved. With @skip_held, a row a user other than @actor_id holds
-- is left alone too.
UPDATE translations t
SET locked = true,
    status = (CASE WHEN p.status = 'auto_approved' OR p.machine_approved THEN 'auto_approved' ELSE 'approved' END)::translation_status,
    confirmed_via = CASE
        WHEN p.status = 'auto_approved' OR p.machine_approved THEN p.confirmed_via
        WHEN p.status = 'approved' AND p.confirmed_via = 'user' THEN 'user'
        ELSE @confirmed_via::text
    END,
    updated_at = now()
FROM (
    SELECT t2.id, t2.status, t2.confirmed_via,
           COALESCE(t2.edited_by IS NULL AND t2.predict_outcome IN ('approved', 'ape_approved'), false) AS machine_approved
    FROM translations t2
    JOIN segments s ON s.id = t2.segment_id
    WHERE s.file_id = @file_id
      AND t2.target_language = @target_language
      AND t2.id = ANY(@ids::bigint[])
      AND NOT t2.locked
      AND coalesce(t2.active_text, '') <> ''
      AND (NOT @skip_held::bool OR NOT translation_held_by_other(t2.id, sqlc.narg('actor_id')::bigint))
    FOR UPDATE OF t2
) p
WHERE t.id = p.id
RETURNING t.id, t.status, t.confirmed_via,
          p.status AS prior_status, p.confirmed_via AS prior_confirmed_via;

-- name: InsertTranslationHistory :one
-- Append-only audit row capturing one text-changing event on a translation.
-- Use sqlc.narg for every sparse field so callers don't have to
-- plumb pointer wrappers for the columns they don't carry. `details` is
-- jsonb stored as []byte at the Go layer; pass nil to default '{}'.
--
-- Pairs with UpdateTranslationActiveSource for automation events (tm / mt /
-- ape / file_import) — they should run in the same transaction so the
-- denormalized lineage on `translations` matches the latest history row.
-- Human and propagation events insert here but do NOT touch active_source
-- (sticky-through-human-edit rule).
INSERT INTO translation_history (
    translation_id, kind,
    text, rich_text,
    created_by, tm_id, tm_entry_id, mt_engine_id, ai_engine_id,
    tm_match_percent, edit_distance, editing_time_ms, job_step_assignment_id,
    details
) VALUES (
    @translation_id, @kind,
    sqlc.narg('text')::text,
    sqlc.narg('rich_text')::text,
    sqlc.narg('created_by')::bigint,
    sqlc.narg('tm_id')::bigint,
    sqlc.narg('tm_entry_id')::bigint,
    sqlc.narg('mt_engine_id')::bigint,
    sqlc.narg('ai_engine_id')::bigint,
    sqlc.narg('tm_match_percent')::smallint,
    sqlc.narg('edit_distance')::int,
    sqlc.narg('editing_time_ms')::bigint,
    sqlc.narg('job_step_assignment_id')::bigint,
    COALESCE(sqlc.narg('details')::jsonb, '{}'::jsonb)
) RETURNING *;

-- name: InsertClearedHistoryBatch :exec
-- One text-less `cleared` event per id, the same row a single-segment clear
-- writes. Internal: the caller resolved @translation_ids tenant-scoped.
INSERT INTO translation_history (translation_id, kind, created_by, details)
SELECT unnest(@translation_ids::bigint[]), 'cleared', @created_by::bigint, '{}'::jsonb;

-- name: ListTranslationHistory :many
-- Internal: translation_history has no tenant_id (it hangs off translations),
-- so the caller MUST verify the parent with GetTranslationTenant first — see
-- HandleHistory.
--
-- Per-segment audit for the CAT editor's History section, newest first. The id
-- tiebreak is load-bearing: events written in one transaction share a
-- timestamp. LEFT joins throughout — every provenance FK is ON DELETE SET
-- NULL, so an INNER join would drop whole events.
SELECT th.id, th.kind, th.text, th.rich_text, th.created_at,
       th.tm_match_percent, th.edit_distance, th.details,
       th.created_by, u.name AS created_by_name, u.email AS created_by_email,
       e.name AS mt_engine_name,
       tc.name AS tm_name,
       th.job_step_assignment_id,
       ws.name AS step_name
FROM translation_history th
LEFT JOIN users u                  ON u.id   = th.created_by
LEFT JOIN mt_engines e             ON e.id   = th.mt_engine_id AND e.deleted_at IS NULL
LEFT JOIN tm_collections tc        ON tc.id  = th.tm_id
LEFT JOIN job_step_assignments jsa ON jsa.id = th.job_step_assignment_id
LEFT JOIN workflow_steps ws        ON ws.id  = jsa.workflow_step_id
WHERE th.translation_id = @translation_id
ORDER BY th.created_at DESC, th.id DESC
LIMIT 100;

-- name: GetLatestHistoryTextForTranslation :one
-- Internal: caller must have tenant-verified the translation.
--
-- Baseline for a confirm's edit distance: the text this segment held before the
-- current edit. `translations.active_text` cannot serve — a draft autosave
-- overwrites it and emits no history, so by confirm time it already equals the
-- text being confirmed and every genuine post-edit measures 0.
--
-- Newest first, skipping `cleared` (text IS NULL). For a first human edit this
-- is the machine output, which is what post-edit effort means; for a reviewer's
-- edit it is the translator's version.
SELECT text FROM translation_history
WHERE translation_id = @translation_id AND text IS NOT NULL
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: ListLatestHistoryTexts :many
-- Internal: the caller resolved @ids tenant-scoped (a job's rows).
-- GetLatestHistoryTextForTranslation for many rows: each row's newest history
-- text, skipping `cleared`. A row with no history text is not returned.
SELECT DISTINCT ON (translation_id) translation_id, text::text AS text
FROM translation_history
WHERE translation_id = ANY(@ids::bigint[]) AND text IS NOT NULL
ORDER BY translation_id, created_at DESC, id DESC;

-- name: ConfirmTranslationsHuman :many
-- Internal: the caller resolved @ids from the tenant-scoped job.
-- UpdateTranslationHuman for many rows of a bulk confirm (no editing time):
-- each row is written only while its updated_at is still the one read and no
-- other user holds it; the ids written are returned. has_rich false stores no
-- rich text.
UPDATE translations t
SET step_edited      = t.step_edited OR t.active_text IS DISTINCT FROM i.text,
    qe_note          = CASE WHEN t.active_text IS DISTINCT FROM i.text THEN NULL ELSE t.qe_note END,
    active_text      = i.text,
    active_rich_text = CASE WHEN i.has_rich THEN i.rich_text END,
    status           = 'approved',
    confirmed_via    = 'user',
    edit_distance    = i.edit_distance,
    edited_by        = @edited_by::bigint,
    updated_at       = now()
FROM (
    SELECT unnest(@ids::bigint[])                AS id,
           unnest(@texts::text[])                AS text,
           unnest(@rich_texts::text[])           AS rich_text,
           unnest(@has_rich::bool[])             AS has_rich,
           unnest(@edit_distances::int[])        AS edit_distance,
           unnest(@expected_updated_at::timestamptz[]) AS expected_updated_at
) i
WHERE t.id = i.id
  AND t.updated_at = i.expected_updated_at
  AND NOT translation_held_by_other(t.id, @edited_by::bigint)
RETURNING t.id;

-- name: InsertHumanHistoryBatch :exec
-- Internal: the caller resolved @ids tenant-scoped. The history rows of a bulk
-- confirm, as EmitHumanHistory writes them one by one: `human` with the text,
-- its rich text and edit distance, or `approved` with none of them when the
-- confirm left the text as it was. No editing time is recorded (0).
INSERT INTO translation_history (translation_id, kind, text, rich_text, created_by,
                                 edit_distance, editing_time_ms, job_step_assignment_id, details)
SELECT i.id, i.kind::translation_history_kind,
       CASE WHEN i.kind = 'human' THEN i.text END,
       CASE WHEN i.kind = 'human' AND i.has_rich THEN i.rich_text END,
       @created_by::bigint,
       CASE WHEN i.kind = 'human' THEN i.edit_distance END,
       0, sqlc.narg('job_step_assignment_id')::bigint, '{}'::jsonb
FROM (
    SELECT unnest(@ids::bigint[])         AS id,
           unnest(@kinds::text[])         AS kind,
           unnest(@texts::text[])         AS text,
           unnest(@rich_texts::text[])    AS rich_text,
           unnest(@has_rich::bool[])      AS has_rich,
           unnest(@edit_distances::int[]) AS edit_distance
) i;

-- name: UpdateTranslationActiveSource :exec
-- Updates the denormalized lineage columns on translations after an
-- automation event (tm / mt / ape / file_import). Bumps updated_at so
-- optimistic-lock callers see a fresh timestamp.
--
-- This is called ONLY from automation paths; human
-- and propagation events MUST NOT call this query (they leave origin /
-- tm_match_percent untouched).
UPDATE translations
SET origin           = @origin,
    tm_match_percent = sqlc.narg('tm_match_percent')::smallint,
    updated_at       = now()
WHERE id = @id;

-- name: ClearTranslation :one
-- Caller MUST check locked=false first — this query trusts the caller.
-- A row a user other than @actor_id holds is not cleared: zero rows.
UPDATE translations
SET status = 'needs_review',
    active_text = NULL,
    active_rich_text = NULL,
    origin = NULL,
    tm_match_percent = NULL,
    in_flight_phase = NULL,
    predict_outcome = NULL,
    qe_note = NULL,
    editing_time_ms = 0,
    edit_distance = 0,
    edited_by = NULL,
    step_edited = false,
    updated_at = now()
WHERE id = @id
  AND NOT translation_held_by_other(translations.id, sqlc.narg('actor_id')::bigint)
RETURNING *;

-- name: ClearTranslationsByJob :many
-- @scope_all=false → only non-confirmed. Locked rows ALWAYS skipped. Already-
-- empty rows (active_text IS NULL) are skipped too — clearing them is a no-op,
-- and excluding them makes the returned row count match the exact "clearable"
-- count shown in the dialog (GetJobTranslationCounts.clearable_*).
-- Returns the cleared ids so the caller can drop their qa_results in the same
-- transaction: an approved or locked row keeps its text and its findings.
-- @ids narrows the clear to a selection of the job's rows; NULL = whole job.
-- A row a user other than @actor_id holds is skipped; CountHeldClearableByJob
-- counts those.
UPDATE translations
SET status = 'needs_review',
    active_text = NULL,
    active_rich_text = NULL,
    origin = NULL,
    tm_match_percent = NULL,
    in_flight_phase = NULL,
    predict_outcome = NULL,
    qe_note = NULL,
    editing_time_ms = 0,
    edit_distance = 0,
    edited_by = NULL,
    step_edited = false,
    updated_at = now()
WHERE segment_id IN (SELECT id FROM segments WHERE file_id = @file_id)
  AND target_language = @target_language
  AND locked = false
  AND active_text IS NOT NULL
  AND (@scope_all::bool OR status NOT IN ('approved', 'auto_approved'))
  AND (sqlc.narg('ids')::bigint[] IS NULL OR id = ANY(sqlc.narg('ids')::bigint[]))
  AND NOT translation_held_by_other(translations.id, sqlc.narg('actor_id')::bigint)
RETURNING id;

-- name: CountHeldClearableByJob :one
-- The rows ClearTranslationsByJob (whole job) left because a user other than
-- @actor_id holds them. Run after the clear, in its transaction: a held row
-- still matches the clear's predicate, mirrored here. Driven by the holds
-- table, which holds a few rows per job.
SELECT count(*)::bigint
FROM translation_holds h
JOIN translations t ON t.id = h.translation_id
WHERE h.job_id = @job_id AND h.tenant_id = @tenant_id
  AND h.expires_at > now()
  AND h.user_id IS DISTINCT FROM sqlc.narg('actor_id')::bigint
  AND t.locked = false
  AND t.active_text IS NOT NULL
  AND (@scope_all::bool OR t.status NOT IN ('approved', 'auto_approved'));

-- name: ClearTranslationsByProject :many
-- @scope_all=false → only non-confirmed. Locked + already-empty rows skipped
-- (see ClearTranslationsByJob for the empty-row rationale and for why the
-- cleared ids come back), and so are rows a user other than @actor_id holds
-- (CountHeldClearableByProject counts those).
UPDATE translations
SET status = 'needs_review',
    active_text = NULL,
    active_rich_text = NULL,
    origin = NULL,
    tm_match_percent = NULL,
    in_flight_phase = NULL,
    predict_outcome = NULL,
    qe_note = NULL,
    editing_time_ms = 0,
    edit_distance = 0,
    edited_by = NULL,
    step_edited = false,
    updated_at = now()
WHERE segment_id IN (
    SELECT s.id FROM segments s
    JOIN files f ON f.id = s.file_id
    WHERE f.project_id = @project_id
)
AND locked = false
AND active_text IS NOT NULL
AND (@scope_all::bool OR status NOT IN ('approved', 'auto_approved'))
  AND NOT translation_held_by_other(translations.id, sqlc.narg('actor_id')::bigint)
RETURNING id;

-- name: CountHeldClearableByProject :one
-- CountHeldClearableByJob for ClearTranslationsByProject: run after the clear,
-- in its transaction.
SELECT count(*)::bigint
FROM translation_holds h
JOIN translations t ON t.id = h.translation_id
JOIN jobs j ON j.id = h.job_id
JOIN files f ON f.id = j.file_id
WHERE f.project_id = @project_id AND h.tenant_id = @tenant_id
  AND h.expires_at > now()
  AND h.user_id IS DISTINCT FROM sqlc.narg('actor_id')::bigint
  AND t.locked = false
  AND t.active_text IS NOT NULL
  AND (@scope_all::bool OR t.status NOT IN ('approved', 'auto_approved'));

-- name: UpdateTranslationTMFuzzy :one
-- TM match text is written to active_text; the `origin` + `tm_match_percent` + history row are stamped by
-- EmitAutomationHistory in the same write path. Handles both exact and fuzzy
-- TM fills.
--
-- SQL-level race guard. The caller filters rows via
-- ListTMTargetableByFile (`status='needs_review' AND edited_by IS NULL AND
-- predict_outcome IS NULL`), but a linguist confirming a segment in another
-- tab between the fetch and the write would otherwise be clobbered. The
-- mirrored WHERE clause makes this write a no-op once a row has been
-- approved, human-edited, predict-finalized, locked or held by a user other
-- than @actor_id (as in UpdateTranslationMT); the :one return value is
-- then pgx.ErrNoRows and the caller (applyExactMatch / applyFuzzyMatch)
-- treats it as "row no longer eligible, skip — including the paired
-- UpdateTranslationHuman auto-approve call in the exact-match path".
-- active_rich_text carries the source segment's inline tags
-- projected onto the TM target. NULL for tag-free segments — COALESCE keeps
-- any existing value rather than blanking it, so a tag-free match can never
-- erase rich text another path already wrote.
UPDATE translations
SET active_text = $2,
    active_rich_text = COALESCE(sqlc.narg('active_rich_text')::text, active_rich_text),
    status = 'needs_review',
    updated_at = now()
WHERE id = $1
  AND status = 'needs_review'
  AND edited_by IS NULL
  AND predict_outcome IS NULL
  AND locked = false
  AND NOT translation_held_by_other(translations.id, sqlc.narg('actor_id')::bigint)
RETURNING *;

-- name: PropagateTranslation :many
-- Auto-propagation: copy a confirmed translation onto every other segment in
-- the same file + target language whose source matches. Locked rows are left
-- alone: a lock means "don't touch". The check sits on the outer WHERE so a
-- row locked while the statement waits is re-checked and skipped.
--
-- The plain and rich-text forms move together, or the siblings of a tagged
-- segment lose their inline tags. RETURNING the full rows lets the frontend
-- patch each sibling in its cache, so a sibling activated right after starts
-- from the fresh updated_at instead of a stale one that 409s on save.
--
-- A sibling a user other than @actor_id holds is skipped, like a locked one.
-- The siblings are locked in id order, the order a counts recount locks them.
UPDATE translations
SET step_edited      = step_edited OR active_text IS DISTINCT FROM @human_text,
    qe_note          = CASE WHEN active_text IS DISTINCT FROM @human_text THEN NULL ELSE qe_note END,
    active_text      = @human_text,
    active_rich_text = @human_rich_text,
    status           = 'approved',
    updated_at       = now()
WHERE locked = false
  AND NOT translation_held_by_other(translations.id, sqlc.narg('actor_id')::bigint)
  AND id IN (
    SELECT t.id FROM translations t
    JOIN segments s ON s.id = t.segment_id
    WHERE s.file_id = @file_id
      AND s.source_text = @source_text
      AND t.target_language = @target_language
      AND t.id != @exclude_id
      AND NOT t.locked
    ORDER BY t.id
    FOR UPDATE OF t
)
RETURNING *;

-- name: CountPropagationTargets :one
-- The unlocked same-source siblings PropagateTranslation sees: `targets` it
-- would write, and `held` it skips because a user other than @actor_id holds
-- them.
SELECT count(*) FILTER (WHERE NOT translation_held_by_other(t.id, sqlc.narg('actor_id')::bigint))::int AS targets,
       count(*) FILTER (WHERE translation_held_by_other(t.id, sqlc.narg('actor_id')::bigint))::int     AS held
FROM translations t
JOIN segments s ON s.id = t.segment_id
WHERE s.file_id = @file_id
  AND s.source_text = @source_text
  AND t.target_language = @target_language
  AND t.id != @exclude_id
  AND t.locked = false;

-- name: ListUntranslatedByFile :many
-- 'untranslated' is the derived state `active_text IS NULL`: an untranslated
-- row sits at the needs_review default with no text written. Locked rows are
-- never pre-translated. @ids narrows it to a selection; NULL = every row.
-- held_by_other: a user other than @actor_id (the user who ran the phase;
-- NULL = any user) has the row open; the phase skips and counts it.
SELECT t.id, t.segment_id, t.target_language, s.source_text, s.source_rich_text,
       translation_held_by_other(t.id, sqlc.narg('actor_id')::bigint) AS held_by_other
FROM translations t
JOIN segments s ON s.id = t.segment_id
WHERE s.file_id = @file_id AND t.target_language = @target_language
  AND t.active_text IS NULL AND t.locked = false
  AND (sqlc.narg('ids')::bigint[] IS NULL OR t.id = ANY(sqlc.narg('ids')::bigint[]))
ORDER BY s.position;

-- name: ListSegmentSourcesByFile :many
-- Every segment's source text for a file, in document order. The TM
-- phase builds a per-segment neighbor-context map from this (neighbors by
-- array-adjacency, so gaps in `position` don't matter) to detect 101%
-- (in-context exact) matches. Source-only — no translation join.
SELECT id, "position", source_text
FROM segments
WHERE file_id = $1
ORDER BY "position";

-- name: GetSegmentNeighborSources :one
-- The source text of the segments immediately before/after the given
-- segment in document order (nearest lower/higher position) within the same
-- file; NULL at the document boundaries. Equivalent to array-adjacency over
-- position, matching the lookup side (ListSegmentSourcesByFile). Used at TM
-- write-back time to compute the 101% context-match hashes stored on the
-- entry (last-write-wins context).
-- Boundaries COALESCE to '' (non-null) so a document-start/end segment hashes
-- the empty string on both write and lookup sides; a genuinely-missing segment
-- surfaces as a query error the caller maps to "no context" (stores NULL).
SELECT
  COALESCE((SELECT prev.source_text FROM segments prev
     WHERE prev.file_id = s.file_id AND prev."position" < s."position"
     ORDER BY prev."position" DESC LIMIT 1), '')::text AS prev_source,
  COALESCE((SELECT nxt.source_text FROM segments nxt
     WHERE nxt.file_id = s.file_id AND nxt."position" > s."position"
     ORDER BY nxt."position" ASC LIMIT 1), '')::text AS next_source
FROM segments s
WHERE s.id = @segment_id;

-- name: ListTMTargetableByFile :many
-- Segments eligible for the Apply-TM phase (TM overrides unconfirmed MT). Returns
-- unconfirmed rows that no human has touched and that predict has not yet
-- finalized: edited or confirmed segments are protected.
--
-- The eligible rows are `status='needs_review' AND predict_outcome IS NULL`
-- (predict has not produced an outcome) AND `edited_by IS NULL` (no human
-- edit):
--   - auto_approved / approved — confirmed; excluded by status.
--   - predict-rejected needs_review — predict_outcome IS NOT NULL; excluded.
--   - human-edited needs_review — edited_by IS NOT NULL; excluded.
--   - locked — never pre-translated; excluded.
-- @ids narrows it to a selection; NULL = every row. held_by_other as in
-- ListUntranslatedByFile.
SELECT t.id, t.segment_id, t.target_language, s.source_text, s.source_rich_text,
       translation_held_by_other(t.id, sqlc.narg('actor_id')::bigint) AS held_by_other
FROM translations t
JOIN segments s ON s.id = t.segment_id
WHERE s.file_id = @file_id
  AND t.target_language = @target_language
  AND t.status = 'needs_review'
  AND t.predict_outcome IS NULL
  AND t.edited_by IS NULL
  AND t.locked = false
  AND (sqlc.narg('ids')::bigint[] IS NULL OR t.id = ANY(sqlc.narg('ids')::bigint[]))
ORDER BY s.position;

-- name: ListPreTranslatedByFile :many
-- Rows of one target language the Predict phase should score: MT/TM-filled,
-- with text, not yet predicted, not human-edited. Returns active_text.
-- 'pre_translated' is the derived state
-- `status='needs_review' AND origin IS NOT NULL AND edited_by IS NULL AND
-- predict_outcome IS NULL` — automation filled the text, predict hasn't run,
-- no human touched it.
-- source_rich_text + active_rich_text ride along so QE/APE can show the model
-- the inline tags, and so the outcomes that leave the target alone can write
-- the row's existing rich text back unchanged.
-- position is the segment's place in the file. It is what the batch envelope
-- uses to identify a segment to the model, so the number
-- the model reads is the one the editor shows.
-- @review_all (a step round that re-runs its AI after a rollback) takes every
-- unconfirmed, unlocked segment with text, people's text included.
SELECT t.id, t.segment_id, t.target_language, s.source_text, s.source_rich_text,
       t.active_text, t.active_rich_text, s.position, t.origin
FROM translations t
JOIN segments s ON s.id = t.segment_id
WHERE s.file_id = @file_id
  AND t.target_language = @target_language
  AND t.active_text IS NOT NULL
  AND t.status = 'needs_review'
  AND t.predict_outcome IS NULL
  AND (
    (t.origin IS NOT NULL AND t.edited_by IS NULL)
    OR (@review_all::bool AND NOT t.locked AND coalesce(t.active_text, '') <> '')
  )
ORDER BY s.position;

-- name: ListApprovedForExport :many
SELECT t.id, t.segment_id, t.target_language, s.source_text, s.segment_key,
       t.active_text, t.active_rich_text, t.status, s.source_rich_text
FROM translations t
JOIN segments s ON s.id = t.segment_id
WHERE s.file_id = $1 AND t.target_language = $2
ORDER BY s.position;

-- name: ListAllForBilingualExport :many
-- Bilingual XLIFF export: EVERY segment of the file (LEFT JOIN, so
-- untranslated ones appear too) with its source rich text, the translation
-- for the requested language, its lock and status, and its pre-translation
-- lineage (origin + TM score).
-- For a TM pre-translation, match_* is the TM unit that was matched, taken
-- from the latest `tm` history event. It is NULL when that unit or its
-- collection is gone, or the collection belongs to another tenant.
-- The caller checks file ownership (GetFileTenant) first.
SELECT s.id AS segment_id, s.segment_key, s.source_text, s.source_rich_text, s.position,
       t.active_text, t.active_rich_text,
       t.status, t.locked, t.origin, t.tm_match_percent,
       mtu.source_text AS match_source_text, mtu.target_text AS match_target_text
FROM segments s
LEFT JOIN translations t ON t.segment_id = s.id AND t.target_language = @target_language
LEFT JOIN LATERAL (
    SELECT h.tm_entry_id
    FROM translation_history h
    WHERE t.origin = 'tm'
      AND h.translation_id = t.id
      AND h.kind = 'tm'
    ORDER BY h.created_at DESC, h.id DESC
    LIMIT 1
) m ON true
LEFT JOIN translation_memory mtu ON mtu.id = m.tm_entry_id
    AND EXISTS (SELECT 1 FROM tm_collections c
                WHERE c.id = mtu.tm_id AND c.tenant_id = @tenant_id AND c.deleted_at IS NULL)
WHERE s.file_id = @file_id
ORDER BY s.position;

-- name: ListApprovedForTM :many
-- All human-approved translations for a file + language, for TM write-back.
-- Includes source_rich_text + active_rich_text so the write path can preserve
-- inline tag chips on the TM side.
-- The status='approved' filter guarantees a human-confirmed target, so
-- `active_text IS NOT NULL` is the only text check needed.
-- Assistant-confirmed rows (confirmed_via = 'assistant') are excluded: they
-- never reach TM.
SELECT t.id, t.segment_id, t.target_language, s.source_text, s.source_rich_text,
       t.active_text, t.active_rich_text
FROM translations t
JOIN segments s ON s.id = t.segment_id
WHERE s.file_id = $1 AND t.target_language = $2
  AND t.status = 'approved' AND t.active_text IS NOT NULL
  AND t.confirmed_via = 'user'
ORDER BY s.position;

-- name: ListApprovedForTMByIDs :many
-- The listed translations of the tenant that may go to the TM, with what the
-- write needs: human-approved (status='approved', confirmed by a user, never
-- auto_approved or assistant-confirmed) and with text. Others are left out.
SELECT t.id, t.segment_id, t.target_language, s.file_id, s.source_text, s.source_rich_text,
       t.active_text, t.active_rich_text,
       p.id AS project_id, p.source_language AS project_source_language, p.settings_profile_id
FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE t.id = ANY(@ids::bigint[]) AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
  AND t.status = 'approved' AND t.active_text IS NOT NULL
  AND t.confirmed_via = 'user'
ORDER BY s.file_id, t.target_language, s.position;

-- name: GetJobAnalysis :many
-- Bands compute from the new `origin` column (sticky
-- automation lineage) + `tm_match_percent` (integer 0..101). The 101 slot
-- is reserved for ICE (in-context exact) and currently rolls
-- into the '100' band — a distinct ICE band ships with the ICE feature.
SELECT
    COALESCE(t.origin::text, 'unknown') AS origin,
    COALESCE(t.predict_outcome::text, 'none') AS predict_outcome,
    CASE
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 100 THEN '100'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 95  THEN '95-99'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 85  THEN '85-94'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 75  THEN '75-84'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 50  THEN '50-74'
        WHEN t.origin = 'tm' AND t.tm_match_percent IS NOT NULL AND t.tm_match_percent < 50 THEN 'below-50'
        ELSE 'no-match'
    END AS match_band,
    COUNT(*)::int AS segment_count,
    COALESCE(SUM(s.word_count), 0)::int AS word_count,
    COALESCE(SUM(length(s.source_text)), 0)::int AS char_count
FROM translations t
JOIN segments s ON s.id = t.segment_id
WHERE s.file_id = $1 AND t.target_language = $2
GROUP BY t.origin, t.predict_outcome,
    CASE
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 100 THEN '100'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 95  THEN '95-99'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 85  THEN '85-94'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 75  THEN '75-84'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 50  THEN '50-74'
        WHEN t.origin = 'tm' AND t.tm_match_percent IS NOT NULL AND t.tm_match_percent < 50 THEN 'below-50'
        ELSE 'no-match'
    END;

-- name: GetProjectAnalysis :many
-- Same band logic as GetJobAnalysis, project-scoped.
SELECT
    t.target_language,
    COALESCE(t.origin::text, 'unknown') AS origin,
    COALESCE(t.predict_outcome::text, 'none') AS predict_outcome,
    CASE
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 100 THEN '100'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 95  THEN '95-99'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 85  THEN '85-94'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 75  THEN '75-84'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 50  THEN '50-74'
        WHEN t.origin = 'tm' AND t.tm_match_percent IS NOT NULL AND t.tm_match_percent < 50 THEN 'below-50'
        ELSE 'no-match'
    END AS match_band,
    COUNT(*)::int AS segment_count,
    COALESCE(SUM(s.word_count), 0)::int AS word_count,
    COALESCE(SUM(length(s.source_text)), 0)::int AS char_count
FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
WHERE f.project_id = $1
GROUP BY t.target_language, t.origin, t.predict_outcome,
    CASE
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 100 THEN '100'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 95  THEN '95-99'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 85  THEN '85-94'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 75  THEN '75-84'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 50  THEN '50-74'
        WHEN t.origin = 'tm' AND t.tm_match_percent IS NOT NULL AND t.tm_match_percent < 50 THEN 'below-50'
        ELSE 'no-match'
    END;

-- name: GetProjectAnalysisByFile :many
-- Same band logic, per-file breakdown for the V2 surface.
SELECT
    s.file_id,
    f.name AS file_name,
    t.target_language,
    COALESCE(t.origin::text, 'unknown') AS origin,
    COALESCE(t.predict_outcome::text, 'none') AS predict_outcome,
    CASE
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 100 THEN '100'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 95  THEN '95-99'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 85  THEN '85-94'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 75  THEN '75-84'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 50  THEN '50-74'
        WHEN t.origin = 'tm' AND t.tm_match_percent IS NOT NULL AND t.tm_match_percent < 50 THEN 'below-50'
        ELSE 'no-match'
    END AS match_band,
    COUNT(*)::int AS segment_count,
    COALESCE(SUM(s.word_count), 0)::int AS word_count,
    COALESCE(SUM(length(s.source_text)), 0)::int AS char_count
FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
WHERE f.project_id = $1
GROUP BY s.file_id, f.name, t.target_language, t.origin, t.predict_outcome,
    CASE
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 100 THEN '100'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 95  THEN '95-99'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 85  THEN '85-94'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 75  THEN '75-84'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 50  THEN '50-74'
        WHEN t.origin = 'tm' AND t.tm_match_percent IS NOT NULL AND t.tm_match_percent < 50 THEN 'below-50'
        ELSE 'no-match'
    END;

-- name: GetProjectRepetitions :many
WITH numbered AS (
    SELECT
        s.file_id,
        t.target_language,
        s.source_text,
        s.word_count AS words,
        length(s.source_text) AS chars,
        ROW_NUMBER() OVER (
            PARTITION BY t.target_language, s.source_text
            ORDER BY s.file_id, s.position
        ) AS rn
    FROM translations t
    JOIN segments s ON s.id = t.segment_id
    JOIN files f ON f.id = s.file_id
    WHERE f.project_id = $1
)
SELECT
    file_id,
    target_language,
    COUNT(*)::int AS segment_count,
    COALESCE(SUM(words), 0)::int AS word_count,
    COALESCE(SUM(chars), 0)::int AS char_count
FROM numbered
WHERE rn > 1
GROUP BY file_id, target_language;

-- name: GetTranslationFileID :one
-- Vendor access gate: the file that owns a translation (via its segment). Used
-- to scope the translationID-keyed comment + QA routes to a vendor's assigned files.
SELECT s.file_id
FROM translations t
JOIN segments s ON s.id = t.segment_id
WHERE t.id = @translation_id;

-- name: InsertAIUsageEvent :exec
-- One provider call's token cost. ai_engine_name is copied
-- rather than joined: a system can be renamed or re-modelled, and past spend
-- must keep saying what it actually was.
INSERT INTO ai_usage_events (
    tenant_id, ai_engine_id, ai_engine_name, purpose, provider, model,
    project_id, job_id, input_tokens, output_tokens
) VALUES (
    @tenant_id, sqlc.narg('ai_engine_id')::bigint, @ai_engine_name, @purpose, @provider, @model,
    sqlc.narg('project_id')::bigint, sqlc.narg('job_id')::bigint, @input_tokens, @output_tokens
);

-- name: ListJobSegmentsPage :many
-- Source + target for one job, filtered and cursor-paginated.
--
-- Serves the MCP segment tool and the customer API's segment list; the CAT
-- editor loads segments and translations separately. Tenant scoping is
-- structural via files→projects.
--
-- Empty-string filters mean "no filter"; enums are compared as ::text so an
-- unknown value simply matches nothing instead of erroring on a bad enum cast.
--
-- The cursor is a segment position and the comparison is INCLUSIVE (>=), i.e.
-- "start at this position". That's load-bearing: segment positions are 0-based,
-- and an omitted cursor arrives as 0, so an exclusive `>` would silently skip
-- the very first segment of every file. Callers advance by passing
-- last_position + 1, which the backend returns as next_cursor.
SELECT
    t.id             AS translation_id,
    s.id             AS segment_id,
    s.position       AS position,
    s.source_text    AS source_text,
    t.active_text    AS target_text,
    t.status         AS status,
    t.origin         AS origin,
    t.tm_match_percent AS tm_match_percent,
    t.updated_at     AS updated_at
FROM segments s
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
JOIN translations t ON t.segment_id = s.id AND t.target_language = sqlc.arg(target_language)::text
WHERE s.file_id = sqlc.arg(file_id)::bigint
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND (sqlc.arg(status)::text = '' OR t.status::text = sqlc.arg(status)::text)
  AND (sqlc.arg(origin)::text = '' OR t.origin::text = sqlc.arg(origin)::text)
  AND (sqlc.arg(query)::text = ''
       OR s.source_text ILIKE '%' || sqlc.arg(query)::text || '%'
       OR coalesce(t.active_text, '') ILIKE '%' || sqlc.arg(query)::text || '%')
  AND s.position >= sqlc.arg(cursor)::int
ORDER BY s.position
LIMIT sqlc.arg(row_limit)::int;

-- name: CountJobSegmentsPage :one
-- Total matching the same filters as ListJobSegmentsPage (cursor excluded), so
-- a caller can tell it is looking at 50 of 12,431 and should narrow.
SELECT count(*)
FROM segments s
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
JOIN translations t ON t.segment_id = s.id AND t.target_language = sqlc.arg(target_language)::text
WHERE s.file_id = sqlc.arg(file_id)::bigint
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND (sqlc.arg(status)::text = '' OR t.status::text = sqlc.arg(status)::text)
  AND (sqlc.arg(origin)::text = '' OR t.origin::text = sqlc.arg(origin)::text)
  AND (sqlc.arg(query)::text = ''
       OR s.source_text ILIKE '%' || sqlc.arg(query)::text || '%'
       OR coalesce(t.active_text, '') ILIKE '%' || sqlc.arg(query)::text || '%');

-- name: ListTranslationStepStatuses :many
-- Every closed step round of one translation, oldest first: how each step
-- ended for the segment. Tenant-scoped on the row itself.
SELECT tss.job_step_assignment_id, tss.round, tss.ended_at, tss.end_reason,
       tss.status, tss.confirmed_via, tss.predict_outcome, tss.human_edited,
       ws.name AS step_name, ws.step_order, ws.step_type
FROM translation_step_statuses tss
JOIN job_step_assignments jsa ON jsa.id = tss.job_step_assignment_id
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
WHERE tss.translation_id = @translation_id
  AND tss.tenant_id = @tenant_id
  AND tss.ended_at IS NOT NULL
ORDER BY tss.ended_at, tss.id;

-- name: ListJobPreviousStepStatuses :many
-- For each translation of the job, the most recently closed step round: the
-- step before the one in progress, or the reviewer's round after a reject.
SELECT DISTINCT ON (tss.translation_id)
       tss.translation_id, tss.job_step_assignment_id, tss.round, tss.ended_at, tss.end_reason,
       tss.status, tss.confirmed_via, tss.predict_outcome, tss.human_edited,
       ws.name AS step_name, ws.step_order, ws.step_type
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
JOIN job_step_assignments jsa ON jsa.job_id = j.id
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
JOIN translation_step_statuses tss ON tss.job_step_assignment_id = jsa.id
WHERE j.id = @job_id
  AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
  AND tss.ended_at IS NOT NULL
ORDER BY tss.translation_id, tss.ended_at DESC, tss.id DESC;

-- name: ListPreviousStepStatusesByTranslations :many
-- ListJobPreviousStepStatuses for a page of translations.
SELECT DISTINCT ON (tss.translation_id)
       tss.translation_id, tss.job_step_assignment_id, tss.round, tss.ended_at, tss.end_reason,
       tss.status, tss.confirmed_via, tss.predict_outcome, tss.human_edited,
       ws.name AS step_name, ws.step_order, ws.step_type
FROM translation_step_statuses tss
JOIN job_step_assignments jsa ON jsa.id = tss.job_step_assignment_id
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
WHERE tss.translation_id = ANY(@translation_ids::bigint[])
  AND tss.tenant_id = @tenant_id
  AND tss.ended_at IS NOT NULL
ORDER BY tss.translation_id, tss.ended_at DESC, tss.id DESC;

-- name: ListJobPredictedBeforeStep :many
-- Translations whose AI outcome predates the job's current step round. The
-- editor only treats an AI rejection as unfinished work in the round that
-- produced it.
SELECT tss.translation_id
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
JOIN job_step_assignments jsa ON jsa.id = j.current_step_id
JOIN translation_step_statuses tss
  ON tss.job_step_assignment_id = jsa.id AND tss.round = jsa.round
WHERE j.id = @job_id
  AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
  AND tss.start_predict_outcome IS NOT NULL;
