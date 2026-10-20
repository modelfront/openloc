-- TM Collection CRUD

-- name: CreateTMCollection :one
INSERT INTO tm_collections (name, tenant_id, source_language, target_languages, customer_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: ListTMCollections :many
-- created_by_name is the creator's display name, resolved here so a list row
-- arrives ready to render (same idea as customer_name below). Precedence:
--   1. users.name
--   2. users.email  — when the name is blank (the column defaults to an empty
--                     string and signup/SSO paths don't always set one)
--   3. blank        — when there is no user at all: created_by is NULL (row
--                     predates attribution) or the user was deleted
SELECT tc.*,
  (SELECT count(*) FROM translation_memory tm WHERE tm.tm_id = tc.id) AS entry_count,
  c.name AS customer_name,
  COALESCE(NULLIF(u.name, ''), u.email, '')::text AS created_by_name
FROM tm_collections tc
LEFT JOIN customers c ON c.id = tc.customer_id AND c.deleted_at IS NULL
LEFT JOIN users u ON u.id = tc.created_by
WHERE tc.tenant_id = $1 AND tc.deleted_at IS NULL ORDER BY tc.created_at DESC;

-- name: GetTMCollection :one
SELECT * FROM tm_collections WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: RenameTMCollection :one
UPDATE tm_collections SET name = $2, updated_at = now() WHERE id = $1 AND tenant_id = $3 AND deleted_at IS NULL RETURNING *;

-- name: SoftDeleteTMCollection :execrows
-- The entries stay with the tombstoned TM. Callers refuse first while any
-- settings_tm_assignments row still points at it (CountTMReferences).
UPDATE tm_collections SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- TM Entry queries

-- name: InsertTM :one
-- Plain insert — no deduplication. Used by TMX import (every TU stored as-is).
-- source_rich_text / target_rich_text are nullable: pass empty string to
-- store NULL (the parser-on-plain-text fallback covers older entries).
INSERT INTO translation_memory (tm_id, source_language, target_language, source_text, target_text,
  source_hash, target_hash, created_by, updated_by, tu_id,
  source_rich_text, target_rich_text)
VALUES ($1, $2, $3, $4, $5, md5($4), md5($5), $6, $6, $7,
  NULLIF(@source_rich_text::text, ''), NULLIF(@target_rich_text::text, ''))
RETURNING *;

-- name: TMExactMatchExists :one
-- Check if an exact match (same source + same target) exists in a TM.
-- DEPRECATED for pipeline write-back: use UpsertTMPipelineEntry instead to
-- avoid the check-then-insert race (Section 5.2). Kept for other callers
-- that need a boolean existence check.
SELECT count(*) FROM translation_memory
WHERE tm_id = @tm_id AND source_language = @source_language AND target_language = @target_language
  AND source_hash = md5(@source_text::text) AND target_hash = md5(@target_text::text);

-- name: TMUpdateExactMatch :exec
-- DEPRECATED: superseded by UpsertTMPipelineEntry. Kept for callers that
-- want to bump metadata without caring about inserting.
-- Update metadata on all exact matches (same source + same target). Used by TM write-back.
UPDATE translation_memory SET updated_at = now(), updated_by = @updated_by, usage_count = usage_count + 1
WHERE tm_id = @tm_id AND source_language = @source_language AND target_language = @target_language
  AND source_hash = md5(@source_text::text) AND target_hash = md5(@target_text::text);

-- name: AcquireTMSourceLock :exec
-- Serialize concurrent SaveSegmentToTM
-- calls for the same (tm_id, source_lang, target_lang, source_text) tuple
-- so prev_entry_id resolution in UpsertTMPipelineEntry sees a stable view.
--
-- Without this lock, two parallel saves with different targets both snapshot
-- an empty `prev` CTE before either inserts; the loser's UPSERT overwrites
-- the winner's target but the loser's outer LEFT JOIN against its own empty
-- prev returns NULL, so the caller skips the history insert. Net effect:
-- the winner's target is silently lost from BOTH the live row AND the audit
-- log — real data loss, not just "misleading sequence."
--
-- pg_advisory_xact_lock holds the lock until the surrounding transaction
-- commits or rolls back. Concurrent calls for the same source queue;
-- different sources don't interfere. Released automatically — no manual
-- unlock needed.
SELECT pg_advisory_xact_lock(hashtextextended(
  format('%s|%s|%s|%s', @tm_id::bigint, @source_language::text, @target_language::text, @source_text::text), 0
));

-- name: UpsertTMPipelineEntry :one
-- Atomic upsert for pipeline-written TM entries.
-- Dedupes on source only (created_by='human'), so re-confirming a segment
-- with a new target overwrites the existing row's target_text + target_hash
-- instead of stacking duplicates. The conflict predicate must match the
-- partial unique index from migration 023 (source_hash without target_hash).
--
-- Returns the previous row's columns (prev_*) alongside the new ones via
-- LEFT JOIN. prev_entry_id is NULL when this was an INSERT (no row existed),
-- non-NULL when DO UPDATE fired. The caller (SaveSegmentToTM) uses prev_*
-- to decide whether to write a translation_memory_history snapshot in the
-- same transaction — only when prev_target_text differs from the new target
-- so re-confirms with no semantic change don't spam history.
--
-- TMX imports use InsertTM and have created_by ∈ {Trados, memoQ, ...} which
-- sit outside the partial predicate; multi-target preservation for
-- the import path is unaffected.
WITH prev AS (
  SELECT id, target_text, source_rich_text, target_rich_text
    FROM translation_memory tm
   WHERE tm.tm_id = @tm_id
     AND tm.source_language = @source_language
     AND tm.target_language = @target_language
     AND tm.source_hash = md5(@source_text::text)
     AND tm.created_by = 'human'
),
upserted AS (
  INSERT INTO translation_memory (
    tm_id, source_language, target_language,
    source_text, target_text,
    source_hash, target_hash,
    created_by, updated_by, tu_id,
    source_rich_text, target_rich_text,
    prev_source_hash, next_source_hash
  )
  VALUES (
    @tm_id, @source_language, @target_language,
    @source_text, @target_text,
    md5(@source_text::text), md5(@target_text::text),
    'human', @updated_by, '',
    NULLIF(@source_rich_text::text, ''), NULLIF(@target_rich_text::text, ''),
    NULLIF(@prev_source_hash::text, ''), NULLIF(@next_source_hash::text, '')
  )
  ON CONFLICT (tm_id, source_language, target_language, source_hash)
    WHERE created_by = 'human'
  DO UPDATE SET
    target_text       = EXCLUDED.target_text,
    target_hash       = EXCLUDED.target_hash,
    updated_at        = now(),
    updated_by        = @updated_by,
    usage_count       = translation_memory.usage_count + 1,
    source_rich_text  = COALESCE(NULLIF(EXCLUDED.source_rich_text, ''), translation_memory.source_rich_text),
    target_rich_text  = COALESCE(NULLIF(EXCLUDED.target_rich_text, ''), translation_memory.target_rich_text),
    -- Last-write-wins context. A confirm that resolved neighbor
    -- context overwrites; one that couldn't (NULL/'') keeps the prior context.
    prev_source_hash  = COALESCE(NULLIF(EXCLUDED.prev_source_hash, ''), translation_memory.prev_source_hash),
    next_source_hash  = COALESCE(NULLIF(EXCLUDED.next_source_hash, ''), translation_memory.next_source_hash)
  RETURNING id, target_text, source_rich_text, target_rich_text, usage_count
)
SELECT
  u.id,
  u.target_text,
  u.source_rich_text,
  u.target_rich_text,
  u.usage_count,
  p.id               AS prev_entry_id,
  p.target_text      AS prev_target_text,
  p.source_rich_text AS prev_source_rich_text,
  p.target_rich_text AS prev_target_rich_text
FROM upserted u
LEFT JOIN prev p ON p.id = u.id;

-- name: ListSegmentNeighborSourcesByIDs :many
-- Internal: not tenant-scoped; callers pass segment ids taken from
-- tenant-scoped rows (ListApprovedForTMByIDs, or ListApprovedForTM after
-- GetFileTenant).
-- GetSegmentNeighborSources for many segments in one statement: each listed
-- segment's previous/next source text in its file ('' at a boundary).
SELECT s.id,
  COALESCE((SELECT prev.source_text FROM segments prev
     WHERE prev.file_id = s.file_id AND prev."position" < s."position"
     ORDER BY prev."position" DESC LIMIT 1), '')::text AS prev_source,
  COALESCE((SELECT nxt.source_text FROM segments nxt
     WHERE nxt.file_id = s.file_id AND nxt."position" > s."position"
     ORDER BY nxt."position" ASC LIMIT 1), '')::text AS next_source
FROM segments s
WHERE s.id = ANY(@segment_ids::bigint[]);

-- name: AcquireTMSourceLocks :exec
-- AcquireTMSourceLock for many sources of ONE TM in one statement, taken in
-- lock-key order so two writers sharing sources cannot deadlock. Held until
-- the transaction ends.
SELECT pg_advisory_xact_lock(l.k)
FROM (
  SELECT DISTINCT hashtextextended(
    format('%s|%s|%s|%s', @tm_id::bigint, @source_language::text, @target_language::text, src.t), 0) AS k
  FROM unnest(@source_texts::text[]) AS src(t)
) l
ORDER BY l.k;

-- name: UpsertTMPipelineEntries :one
-- UpsertTMPipelineEntry for many entries of ONE TM in one statement, plus
-- the history snapshot of every entry whose target it changes (what
-- InsertTMHistory does per entry). The source texts must be distinct: ON
-- CONFLICT cannot touch one row twice. The arrays are parallel. Returns the
-- entries written.
WITH input AS (
  SELECT unnest(@source_texts::text[]) AS source_text,
         unnest(@target_texts::text[]) AS target_text,
         unnest(@source_rich_texts::text[]) AS source_rich_text,
         unnest(@target_rich_texts::text[]) AS target_rich_text,
         unnest(@prev_source_hashes::text[]) AS prev_source_hash,
         unnest(@next_source_hashes::text[]) AS next_source_hash
),
prev AS (
  SELECT tm.id, tm.source_hash, tm.target_text, tm.source_rich_text, tm.target_rich_text
    FROM translation_memory tm
   WHERE tm.tm_id = @tm_id
     AND tm.source_language = @source_language
     AND tm.target_language = @target_language
     AND tm.created_by = 'human'
     AND tm.source_hash IN (SELECT md5(i.source_text) FROM input i)
),
upserted AS (
  INSERT INTO translation_memory (
    tm_id, source_language, target_language,
    source_text, target_text,
    source_hash, target_hash,
    created_by, updated_by, tu_id,
    source_rich_text, target_rich_text,
    prev_source_hash, next_source_hash
  )
  SELECT @tm_id, @source_language, @target_language,
         i.source_text, i.target_text,
         md5(i.source_text), md5(i.target_text),
         'human', @updated_by, '',
         NULLIF(i.source_rich_text, ''), NULLIF(i.target_rich_text, ''),
         NULLIF(i.prev_source_hash, ''), NULLIF(i.next_source_hash, '')
  FROM input i
  ON CONFLICT (tm_id, source_language, target_language, source_hash)
    WHERE created_by = 'human'
  DO UPDATE SET
    target_text       = EXCLUDED.target_text,
    target_hash       = EXCLUDED.target_hash,
    updated_at        = now(),
    updated_by        = @updated_by,
    usage_count       = translation_memory.usage_count + 1,
    source_rich_text  = COALESCE(NULLIF(EXCLUDED.source_rich_text, ''), translation_memory.source_rich_text),
    target_rich_text  = COALESCE(NULLIF(EXCLUDED.target_rich_text, ''), translation_memory.target_rich_text),
    prev_source_hash  = COALESCE(NULLIF(EXCLUDED.prev_source_hash, ''), translation_memory.prev_source_hash),
    next_source_hash  = COALESCE(NULLIF(EXCLUDED.next_source_hash, ''), translation_memory.next_source_hash)
  RETURNING id
),
history AS (
  INSERT INTO translation_memory_history (
    entry_id, source_text, target_text, source_rich_text, target_rich_text,
    changed_by_user_id, change_reason
  )
  SELECT p.id, i.source_text, p.target_text, p.source_rich_text, p.target_rich_text,
         sqlc.narg('changed_by_user_id')::bigint, @change_reason::text
  FROM prev p
  JOIN input i ON md5(i.source_text) = p.source_hash
  WHERE p.target_text IS DISTINCT FROM i.target_text
  RETURNING entry_id
)
SELECT count(*)::int AS written FROM upserted;

-- name: LookupTM :one
-- Sublanguage-aware on BOTH sides: es-mx matches es-mx entries AND es entries,
-- and an es entry is reachable from an es-mx project. Exact locale wins via the
-- ORDER BY; callers charge translationmemory.SublanguagePenalty when they land
-- on a variant, so a variant never reads as a true exact match.
--
-- Plain equality on SPLIT_PART rather than `col = $n OR SPLIT_PART(...)`: the
-- OR form can't use a btree index, and idx_tm_exact_match_base exists purely to
-- keep this lookup off a sequential scan (see migration 017 — unindexed exact
-- lookups took 6+ minutes on 12K segments). The OR was also redundant, since an
-- exact match implies a base match.
SELECT * FROM translation_memory
WHERE tm_id = @tm_id
  AND SPLIT_PART(source_language, '-', 1) = SPLIT_PART(@source_language::text, '-', 1)
  AND source_text = @source_text
  AND SPLIT_PART(target_language, '-', 1) = SPLIT_PART(@target_language::text, '-', 1)
ORDER BY (target_language = @target_language) DESC, (source_language = @source_language) DESC
LIMIT 1;

-- name: LookupTMBatch :many
-- Batch exact lookup: finds TM matches for multiple source texts in one query.
-- Returns one row per matched source text (the best-locale match per text).
-- Used by the pipeline to replace 12K individual LookupTM queries with ~24 batch queries.
-- Returns the entry's stored neighbor-context hashes so the pipeline
-- can promote a byte-exact hit to 101% when both neighbors also match.
--
-- Sublanguage-aware on both sides — see LookupTM for the SPLIT_PART rationale.
-- Target locale is the stronger preference: it's the text actually delivered to
-- the reader, whereas a source-locale difference only affects what matched.
-- Also returns source_language/target_language so the caller can tell an exact
-- hit from a penalized variant.
SELECT DISTINCT ON (source_text)
  id, tm_id, source_language, target_language, source_text, target_text,
  source_rich_text, target_rich_text, created_at,
  prev_source_hash, next_source_hash
FROM translation_memory
WHERE tm_id = @tm_id
  AND SPLIT_PART(source_language, '-', 1) = SPLIT_PART(@source_language::text, '-', 1)
  AND source_text = ANY(@source_texts::text[])
  AND SPLIT_PART(target_language, '-', 1) = SPLIT_PART(@target_language::text, '-', 1)
ORDER BY source_text, (target_language = @target_language) DESC, (source_language = @source_language) DESC, id;

-- name: LookupTMAll :many
-- Sublanguage-aware version of LookupTM. Returns every matching row, exact
-- locale first, then sublanguage variants.
SELECT * FROM translation_memory
WHERE tm_id = @tm_id
  AND SPLIT_PART(source_language, '-', 1) = SPLIT_PART(@source_language::text, '-', 1)
  AND source_text = @source_text
  AND SPLIT_PART(target_language, '-', 1) = SPLIT_PART(@target_language::text, '-', 1)
ORDER BY (target_language = @target_language) DESC, (source_language = @source_language) DESC, id;

-- name: FuzzyLookupTM :many
-- Stage 1 of the find/score split: @threshold MUST stay low for
-- recall — the application Scorer rescores and applies the display threshold
-- after. Bumping @threshold drops short-sentence fuzzy candidates (Bug 2).
--
-- Sublanguage-aware on both sides (see LookupTM). Callers rescore with
-- translationmemory.PenalizeSublanguage before comparing against the display
-- threshold, so a variant candidate has to clear a slightly higher bar.
-- prev/next_source_hash let an exact hit score 101% in context.
--
-- `%` is what lets idx_tm_source_trgm serve the search; it compares against
-- pg_trgm.similarity_threshold, so run it through
-- translationmemory.FuzzyLookup, which sets that to @threshold first. The
-- similarity() test keeps the result exact whatever the setting.
SELECT id, tm_id, source_language, target_language, source_text, target_text,
       source_rich_text, target_rich_text, created_at, prev_source_hash, next_source_hash,
       similarity(source_text, @query::text) AS score
FROM translation_memory
WHERE tm_id = @tm_id
  AND SPLIT_PART(source_language, '-', 1) = SPLIT_PART(@source_language::text, '-', 1)
  AND SPLIT_PART(target_language, '-', 1) = SPLIT_PART(@target_language::text, '-', 1)
  AND source_text % @query::text
  AND similarity(source_text, @query::text) >= @threshold::real
ORDER BY score DESC
LIMIT @entry_limit::int;

-- name: SetTrgmSimilarityThreshold :exec
-- Sets the threshold of pg_trgm's `%` operator until the transaction ends.
SELECT set_config('pg_trgm.similarity_threshold', (@threshold::real)::text, true);

-- name: ListTMEntries :many
SELECT * FROM translation_memory
WHERE tm_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: ListTMLanguagePairs :many
SELECT DISTINCT source_language, target_language
FROM translation_memory WHERE tm_id = $1
ORDER BY source_language, target_language;

-- name: ListTMEntriesFiltered :many
SELECT * FROM translation_memory
WHERE tm_id = @tm_id
  AND (@source_language::text = '' OR source_language = @source_language)
  AND (@target_language::text = '' OR target_language = @target_language)
ORDER BY created_at DESC
LIMIT @entry_limit;

-- name: ListTMEntriesCursor :many
SELECT * FROM translation_memory
WHERE tm_id = @tm_id
  AND (@source_language::text = '' OR source_language = @source_language)
  AND (@target_language::text = '' OR target_language = @target_language)
  AND id > @cursor
ORDER BY id
LIMIT @entry_limit;

-- name: CountTMEntries :one
SELECT count(*) FROM translation_memory
WHERE tm_id = @tm_id
  AND (@source_language::text = '' OR source_language = @source_language)
  AND (@target_language::text = '' OR target_language = @target_language);

-- name: CountTMEntriesSearch :one
-- @pattern is a Postgres regex built by the service (entry_search.go); plain
-- text arrives escaped, so every search mode is one regex test per field.
SELECT count(*) FROM translation_memory
WHERE tm_id = @tm_id
  AND (@source_language::text = '' OR source_language = @source_language)
  AND (@target_language::text = '' OR target_language = @target_language)
  AND ((@in_source::bool AND CASE WHEN @case_sensitive::bool THEN source_text ~ @pattern::text ELSE source_text ~* @pattern::text END)
    OR (@in_target::bool AND CASE WHEN @case_sensitive::bool THEN target_text ~ @pattern::text ELSE target_text ~* @pattern::text END));

-- name: SearchTMEntries :many
SELECT id, tm_id, source_language, target_language, source_text, target_text,
       source_rich_text, target_rich_text, created_at,
       GREATEST(similarity(source_text, @query::text), similarity(target_text, @query::text)) AS score
FROM translation_memory
WHERE tm_id = @tm_id
  AND (@source_language::text = '' OR source_language = @source_language)
  AND (@target_language::text = '' OR target_language = @target_language)
  AND (source_text ILIKE '%' || @query || '%' OR target_text ILIKE '%' || @query || '%')
ORDER BY score DESC
LIMIT @entry_limit;

-- name: SearchTMEntriesCursor :many
-- Same filter as CountTMEntriesSearch; @query only feeds the score.
SELECT id, tm_id, source_language, target_language, source_text, target_text,
       source_rich_text, target_rich_text, created_at, updated_at, updated_by,
       GREATEST(similarity(source_text, @query::text), similarity(target_text, @query::text)) AS score
FROM translation_memory
WHERE tm_id = @tm_id
  AND (@source_language::text = '' OR source_language = @source_language)
  AND (@target_language::text = '' OR target_language = @target_language)
  AND ((@in_source::bool AND CASE WHEN @case_sensitive::bool THEN source_text ~ @pattern::text ELSE source_text ~* @pattern::text END)
    OR (@in_target::bool AND CASE WHEN @case_sensitive::bool THEN target_text ~ @pattern::text ELSE target_text ~* @pattern::text END))
  AND id > @cursor
ORDER BY id
LIMIT @entry_limit;

-- name: DeleteTMEntry :execrows
-- TM-scoped delete. Tenant scoping is required: `WHERE id = $1` alone
-- would let any admin/owner DELETE entries from any other tenant by
-- guessing IDs (cross-tenant data destruction).
--
-- Adding `tm_id = $2` is sufficient for both invariants because the parent
-- route's RequireTMOwnership middleware already validates the URL's tmID
-- belongs to the caller's tenant. So:
--   - cross-tenant attack: tenant-2's URL passes, but their tmID won't
--     match a tenant-1 entry's tm_id → 0 rows affected → 404.
--   - cross-TM (same tenant): wrong URL tmID → entry's tm_id doesn't match
--     → 0 rows affected → 404.
-- :execrows so the handler can distinguish "deleted" (1) from "not in this
-- TM / not found" (0) and return 204 vs 404 accordingly.
DELETE FROM translation_memory WHERE id = $1 AND tm_id = $2;

-- name: GetTMEntryTenant :one
-- Tenant-scoped fetch of a single TM entry. Used by the inline-edit handler
-- to (a) verify ownership before any mutation and (b) re-check existence
-- when sqlc returns ErrNoRows from UpdateTMEntry — that distinguishes
-- "stale optimistic-lock baseline → 409" from "entry was deleted → 404",
-- the same disambiguation translations use.
SELECT tm.*
FROM translation_memory tm
JOIN tm_collections tc ON tc.id = tm.tm_id
WHERE tm.id = $1 AND tc.tenant_id = $2 AND tc.deleted_at IS NULL;

-- name: GetTMEntryScoped :one
-- TM-scoped + tenant-scoped fetch. Used instead of
-- GetTMEntryTenant on every code path that has the URL's tmID in context.
-- Closes the cross-TM URL-tampering hygiene gap the original PATCH handler
-- comment flagged as "cheap to close" but didn't:
--   1. tm.id = $1            — entry exists
--   2. tm.tm_id = $2         — entry actually belongs to the URL's TM
--   3. tc.tenant_id = $3     — TM belongs to caller's tenant (defense-in-depth
--                              alongside RequireTMOwnership)
SELECT tm.*
FROM translation_memory tm
JOIN tm_collections tc ON tc.id = tm.tm_id
WHERE tm.id = $1 AND tm.tm_id = $2 AND tc.tenant_id = $3 AND tc.deleted_at IS NULL;

-- name: GetTMEntryConflictDetails :one
-- Tenant-scoped + TM-scoped lookup for the 409 conflict-response payload.
-- Returns the current state of the entry plus the editor's resolved user
-- record (id + display name) so the frontend can render the side-by-side
-- conflict banner with the right same-user-vs-different-user heading.
--
-- `updated_by` is a TEXT label (the handler stamps it to
-- auth.UserLabel = "Name <email>", or a bare email for legacy rows). The LEFT
-- JOIN resolves it back to a user record so the banner gets a numeric id for
-- safe identity comparison. We extract the email from the "<...>" form when
-- present and fall back to the whole label otherwise — this keeps working for
-- legacy bare-email rows and yields NULL for non-user labels ('human',
-- "tmx-import" from bulk imports).
--
-- Tenant scope is required even though callers already pre-checked via
-- GetTMEntryScoped — every user-data query carries its own scope
-- (CONTRIBUTING.md, "Tenant isolation").
SELECT tm.id, tm.source_text, tm.target_text, tm.source_rich_text, tm.target_rich_text,
       tm.updated_at, tm.updated_by,
       u.id AS updated_by_user_id, COALESCE(u.name, '') AS updated_by_name
FROM translation_memory tm
JOIN tm_collections tc ON tc.id = tm.tm_id
LEFT JOIN users u ON u.email = CASE
        -- UserLabel appends the email as the LAST "<...>" group, so anchor the
        -- extraction to end-of-string — a display name that itself contains
        -- angle brackets ("Ops <Team> <a@b>") must not shadow the email.
        WHEN tm.updated_by LIKE '%<%>' THEN substring(tm.updated_by from '<([^>]*)>$')
        ELSE tm.updated_by END
    AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.tenant_id = tc.tenant_id)
WHERE tm.id = $1 AND tm.tm_id = $2 AND tc.tenant_id = $3 AND tc.deleted_at IS NULL;

-- name: UpdateTMEntry :one
-- Inline edit from the TM browser. Updates plain text + rich text + recomputes
-- both hashes (the partial-unique-index on `created_by = 'human'` from
-- migration 014 uses these hashes — keeping them consistent prevents
-- post-edit duplicate detection from misfiring).
--
-- Optimistic locking: if @expected_updated_at is non-null, the row only
-- updates when the current updated_at matches. A mismatch returns zero rows,
-- which sqlc surfaces as pgx.ErrNoRows — the handler maps that to 409
-- Conflict (mirroring the UpdateTranslationHuman pattern). Passing
-- NULL preserves the legacy "last writer wins" semantics for callers that
-- don't track version (background workers, scripted fix-ups, tests).
--
-- `updated_by` is the editing user's display label (email or name); the
-- handler resolves it from the authenticated context. NULLIF on rich_text
-- mirrors the InsertTM contract — empty string means "no rich text"
-- (column should be NULL), non-empty means "store this rich form."
UPDATE translation_memory
SET source_text       = @source_text,
    target_text       = @target_text,
    source_hash       = md5(@source_text::text),
    target_hash       = md5(@target_text::text),
    source_rich_text  = NULLIF(@source_rich_text::text, ''),
    target_rich_text  = NULLIF(@target_rich_text::text, ''),
    updated_at        = now(),
    updated_by        = @updated_by
WHERE id = @id
  AND tm_id = @tm_id
  AND (sqlc.narg('expected_updated_at')::timestamptz IS NULL
       OR updated_at = sqlc.narg('expected_updated_at')::timestamptz)
RETURNING *;

-- name: InsertTMHistory :exec
-- Append a snapshot of the entry's fields BEFORE a change. Called in the
-- same transaction as UpdateTMEntry so the history row + the update either
-- both land or neither does. See migration 022 for the rationale.
INSERT INTO translation_memory_history (
  entry_id, source_text, target_text, source_rich_text, target_rich_text,
  changed_by_user_id, change_reason
)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListTMEntryHistory :many
-- Returns the change history for a TM entry, newest first. Drives the
-- future history-viewer UI. Tenant scoping is enforced by the caller via
-- the parent translation_memory → tm_collections join (the entry's
-- existence in the caller's tenant must be verified before this is invoked).
SELECT h.*, u.email AS changed_by_email
FROM translation_memory_history h
LEFT JOIN users u ON u.id = h.changed_by_user_id
WHERE h.entry_id = $1
ORDER BY h.created_at DESC;

-- name: FindTMEntriesForFR :many
-- One page of the entries of ONE TM whose source (@in_source) or target
-- (@in_target) contains @pattern, a LIKE pattern ('%term%', wildcards
-- escaped), in id order after @after_id. The route has already checked the TM
-- belongs to the caller's tenant. Empty language filters match any language.
-- LIKE and ILIKE are what idx_tm_source_trgm and idx_tm_target_trgm can serve.
SELECT * FROM translation_memory
WHERE tm_id = @tm_id
  AND (@source_language::text = '' OR source_language = @source_language)
  AND (@target_language::text = '' OR target_language = @target_language)
  AND id > @after_id::bigint
  AND (
    (@in_source::bool AND CASE WHEN @case_sensitive::bool THEN source_text LIKE @pattern::text
                               ELSE source_text ILIKE @pattern::text END)
    OR (@in_target::bool AND CASE WHEN @case_sensitive::bool THEN target_text LIKE @pattern::text
                                  ELSE target_text ILIKE @pattern::text END)
  )
ORDER BY id
LIMIT @page_limit::int;

-- name: CountTMFRMatches :one
-- Totals of FindTMEntriesForFR over the whole TM: matching entries and
-- occurrences of @find in the searched fields (case-folded with lower() when
-- not @case_sensitive).
WITH m AS (
  SELECT
    CASE WHEN @case_sensitive::bool THEN source_text ELSE lower(source_text) END AS src,
    CASE WHEN @case_sensitive::bool THEN target_text ELSE lower(target_text) END AS tgt,
    CASE WHEN @case_sensitive::bool THEN @find::text ELSE lower(@find::text) END AS f
  FROM translation_memory
  WHERE tm_id = @tm_id
    AND (@source_language::text = '' OR source_language = @source_language)
    AND (@target_language::text = '' OR target_language = @target_language)
    AND (
      (@in_source::bool AND CASE WHEN @case_sensitive::bool THEN source_text LIKE @pattern::text
                                 ELSE source_text ILIKE @pattern::text END)
      OR (@in_target::bool AND CASE WHEN @case_sensitive::bool THEN target_text LIKE @pattern::text
                                    ELSE target_text ILIKE @pattern::text END)
    )
)
SELECT count(*)::bigint AS entries,
       coalesce(sum(
         CASE WHEN @in_source::bool THEN (length(src) - length(replace(src, f, ''))) / length(f) ELSE 0 END
         + CASE WHEN @in_target::bool THEN (length(tgt) - length(replace(tgt, f, ''))) / length(f) ELSE 0 END
       ), 0)::bigint AS replacements
FROM m;

-- name: InsertTMHistoryFromEntries :exec
-- Snapshots the listed entries of ONE TM to history as they are now, in one
-- statement. Runs in the transaction that then updates them.
INSERT INTO translation_memory_history (
  entry_id, source_text, target_text, source_rich_text, target_rich_text,
  changed_by_user_id, change_reason
)
SELECT id, source_text, target_text, source_rich_text, target_rich_text,
       sqlc.narg('changed_by_user_id')::bigint, @change_reason::text
FROM translation_memory
WHERE tm_id = @tm_id AND id = ANY(@ids::bigint[]);

-- name: BulkUpdateTMEntryTexts :execrows
-- UpdateTMEntry for many entries of ONE TM in one statement, without the
-- optimistic lock. The arrays are parallel; an empty rich text stores NULL.
UPDATE translation_memory tm
SET source_text      = u.source_text,
    target_text      = u.target_text,
    source_hash      = md5(u.source_text),
    target_hash      = md5(u.target_text),
    source_rich_text = NULLIF(u.source_rich_text, ''),
    target_rich_text = NULLIF(u.target_rich_text, ''),
    updated_at       = now(),
    updated_by       = @updated_by
FROM (
  SELECT unnest(@ids::bigint[]) AS id,
         unnest(@source_texts::text[]) AS source_text,
         unnest(@target_texts::text[]) AS target_text,
         unnest(@source_rich_texts::text[]) AS source_rich_text,
         unnest(@target_rich_texts::text[]) AS target_rich_text
) u
WHERE tm.id = u.id AND tm.tm_id = @tm_id;

-- TMX Import/Export queries

-- name: ListAllTMEntries :many
SELECT * FROM translation_memory WHERE tm_id = $1
ORDER BY source_language, target_language, source_text;

-- name: ListTMEntriesBatch :many
SELECT * FROM translation_memory
WHERE tm_id = @tm_id AND id > @cursor
ORDER BY id
LIMIT @batch_limit;

-- name: CreateTMImport :one
INSERT INTO tm_imports (tm_id, tenant_id, file_name) VALUES ($1, $2, $3) RETURNING *;

-- name: GetTMImport :one
SELECT * FROM tm_imports WHERE id = $1;

-- name: UpdateTMImportProgress :exec
UPDATE tm_imports SET status = $2, total_units = $3, imported = $4, skipped = $5, updated_at = now() WHERE id = $1;

-- name: UpdateTMImportError :exec
UPDATE tm_imports SET status = 'failed', error_message = $2, updated_at = now() WHERE id = $1;

-- name: FailStaleTMImports :execrows
-- Internal: the scheduled sweep, across tenants. An import updates its row
-- after every batch, so one idle since stale_before lost its process (a
-- restart or a crash) and is marked failed.
UPDATE tm_imports SET status = 'failed', error_message = @error_message, updated_at = now()
WHERE status IN ('pending', 'processing') AND updated_at < @stale_before;

-- name: ListTMImports :many
SELECT * FROM tm_imports WHERE tm_id = $1 ORDER BY created_at DESC LIMIT 10;

-- name: CountTMReferences :one
-- Count how many settings TM assignments reference this TM collection.
-- Used to warn before deletion.
SELECT count(*)::int AS count
FROM settings_tm_assignments
WHERE tm_id = $1
  AND settings_profile_live(settings_tm_assignments.settings_profile_id);

