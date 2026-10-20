-- ============================================================
-- Settings Profile CRUD
-- ============================================================

-- name: CreateSettingsProfile :one
-- Penalties, auto_complete and inline_html are deliberately absent: they take
-- the column defaults (1% / 1% / on / on). Adding them as params would make every existing
-- struct-literal caller pass a silent zero value — 0 means "locales are
-- equivalent" (new projects would start auto-confirming sublanguage variants)
-- and false would switch auto-completion or HTML strings off. Callers that need non-defaults
-- (create-from-template) set them via UpdateSettingsProfile.
INSERT INTO settings_profiles (tenant_id, fuzzy_threshold, mt_on_upload, tm_on_upload, qa_profile_id)
VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: GetSettingsProfile :one
SELECT * FROM settings_profiles WHERE id = $1;

-- name: UpdateSettingsProfile :one
-- The penalty columns are nil-keeps-stored (sqlc.narg + COALESCE), not
-- full-replace like the fields above: a zero-valued struct literal must not
-- reset them — with 0 meaning "locales are equivalent", that would quietly
-- re-enable auto-confirm on sublanguage variants.
-- auto_complete is nil-keeps-stored for the same reason:
-- its default is TRUE, and `false` is a bool's zero value, so a struct literal
-- that omits it would silently switch auto-completion off.
-- tm_lookup_order is nil-keeps-stored so the same callers keep the stored mode.
-- inline_html is nil-keeps-stored like auto_complete: its default is TRUE.
UPDATE settings_profiles SET fuzzy_threshold = $2, mt_on_upload = $3, tm_on_upload = $4, qa_profile_id = $5,
  sublanguage_penalty_pct = COALESCE(sqlc.narg('sublanguage_penalty_pct')::int, sublanguage_penalty_pct),
  near_exact_penalty_pct  = COALESCE(sqlc.narg('near_exact_penalty_pct')::int, near_exact_penalty_pct),
  auto_complete           = COALESCE(sqlc.narg('auto_complete')::bool, auto_complete),
  tm_lookup_order         = COALESCE(sqlc.narg('tm_lookup_order')::text, tm_lookup_order),
  inline_html             = COALESCE(sqlc.narg('inline_html')::bool, inline_html),
  updated_at = now()
WHERE id = $1 RETURNING *;

-- name: DeleteSettingsProfile :exec
DELETE FROM settings_profiles WHERE id = $1;

-- name: GetTMScopeSettingsProfile :one
-- The settings_profiles row whose TM rows + match scoring apply to this one:
-- the linked TM profile's own row when tm_profile_id is set, else the row
-- itself. Read fuzzy_threshold and the penalty columns from the result; the
-- other columns belong to the caller's own row and must not be read from here.
SELECT * FROM settings_profiles WHERE id = tm_scope_profile_id($1);

-- name: SetSettingsProfileTMProfile :one
-- Links (or, with NULL, unlinks) a settings row to a TM profile. Separate from
-- UpdateSettingsProfile on purpose: a nullable FK cannot be nil-keeps-stored,
-- and that query's struct-literal callers would all have to learn the field.
UPDATE settings_profiles SET tm_profile_id = sqlc.narg('tm_profile_id')::bigint, updated_at = now()
WHERE id = $1 RETURNING *;

-- ============================================================
-- Settings TM Assignments
-- ============================================================

-- name: ListSettingsTMAssignments :many
SELECT * FROM settings_tm_assignments WHERE settings_profile_id = $1
ORDER BY priority, id;

-- name: DeleteSettingsTMAssignments :exec
DELETE FROM settings_tm_assignments WHERE settings_profile_id = $1;

-- name: CreateSettingsTMAssignment :one
INSERT INTO settings_tm_assignments (settings_profile_id, tm_id, mode, priority, penalty_pct)
VALUES ($1, $2, $3, $4, @penalty_pct) RETURNING *;

-- Resolve TMs for a settings profile + language pair (for pipeline).
-- Language coverage comes from the TM's own declared source +
-- target languages (tm_collections), not from the assignment. Base-language
-- fallback: "fr" matches "fr-fr", "fr-ca", etc. and vice versa, for both the
-- scalar source and each element of the target_languages[] array.
-- A TM with NULL declared languages is treated as a wildcard (matches any pair).
-- Both lookups (and SettingsConcordanceSearch) resolve through
-- tm_scope_profile_id(): a settings row linked to a TM profile reads the
-- profile's TM rows, an unlinked one reads its own. Callers pass the project's
-- own settings_profile_id and never see the difference.
-- name: LookupSettingsTMs :many
SELECT sta.tm_id, sta.mode, sta.priority, sta.penalty_pct, tc.name AS tm_name
FROM settings_tm_assignments sta
JOIN tm_collections tc ON tc.id = sta.tm_id AND tc.deleted_at IS NULL
WHERE sta.settings_profile_id = tm_scope_profile_id(@settings_profile_id)
  AND (tc.source_language IS NULL
       OR tc.source_language = @source_language::text
       OR SPLIT_PART(tc.source_language, '-', 1) = SPLIT_PART(@source_language::text, '-', 1))
  -- Base-language fallback against the target array WITHOUT unnest (sqlc can't
  -- resolve unnest column aliases): comma-wrap the joined targets and LIKE-match
  -- the requested language's base code against an exact element (",xx,") or a
  -- locale of it (",xx-..."). Covers fr↔fr-fr in both directions.
  AND (tc.target_languages IS NULL
       OR ',' || array_to_string(tc.target_languages, ',') || ',' LIKE '%,' || SPLIT_PART(@target_language::text, '-', 1) || ',%'
       OR ',' || array_to_string(tc.target_languages, ',') || ',' LIKE '%,' || SPLIT_PART(@target_language::text, '-', 1) || '-%')
  AND sta.mode IN ('read', 'read_write')
ORDER BY sta.priority ASC, sta.id ASC;

-- Resolve write TMs for a settings profile + language pair (for feedback/TM population)
-- name: LookupSettingsWriteTMs :many
SELECT sta.tm_id
FROM settings_tm_assignments sta
JOIN tm_collections tc ON tc.id = sta.tm_id AND tc.deleted_at IS NULL
WHERE sta.settings_profile_id = tm_scope_profile_id(@settings_profile_id)
  AND (tc.source_language IS NULL
       OR tc.source_language = @source_language::text
       OR SPLIT_PART(tc.source_language, '-', 1) = SPLIT_PART(@source_language::text, '-', 1))
  AND (tc.target_languages IS NULL
       OR ',' || array_to_string(tc.target_languages, ',') || ',' LIKE '%,' || SPLIT_PART(@target_language::text, '-', 1) || ',%'
       OR ',' || array_to_string(tc.target_languages, ',') || ',' LIKE '%,' || SPLIT_PART(@target_language::text, '-', 1) || '-%')
  AND sta.mode IN ('write', 'read_write')
ORDER BY sta.priority ASC, sta.id ASC;

-- ============================================================
-- Settings Terminology Assignments
-- ============================================================

-- name: ListSettingsTermAssignments :many
-- A deleted term base is soft-deleted and keeps its assignment rows; they are
-- left out here, so settings reads and profile copies never see them.
SELECT * FROM settings_terminology_assignments sta
WHERE sta.settings_profile_id = $1
  AND EXISTS (SELECT 1 FROM term_bases tb WHERE tb.id = sta.term_base_id AND tb.deleted_at IS NULL)
ORDER BY sta.source_language, sta.target_language, sta.priority;

-- name: DeleteSettingsTermAssignments :exec
DELETE FROM settings_terminology_assignments WHERE settings_profile_id = $1;

-- name: CreateSettingsTermAssignment :one
INSERT INTO settings_terminology_assignments (settings_profile_id, term_base_id, source_language, target_language, mode, priority)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- ============================================================
-- Settings Workflow Steps
-- ============================================================

-- name: ListSettingsWorkflowSteps :many
SELECT * FROM workflow_steps WHERE settings_profile_id = $1 ORDER BY step_order;

-- name: CreateSettingsWorkflowStep :one
-- step_type defaults from the router when the caller leaves it empty (no
-- router → human, router → hybrid). Library-backed writers always pass it.
INSERT INTO workflow_steps (settings_profile_id, name, step_order, role_required, default_assignee, deadline, ai_router_id, step_type, step_definition_id)
VALUES (@settings_profile_id, @name, @step_order, @role_required, @default_assignee, @deadline, @ai_router_id,
        COALESCE(NULLIF(@step_type::text, ''), CASE WHEN @ai_router_id::bigint IS NULL THEN 'human' ELSE 'hybrid' END),
        @step_definition_id)
RETURNING *;

-- name: DeleteSettingsWorkflowSteps :exec
DELETE FROM workflow_steps WHERE settings_profile_id = $1;

-- name: UpdateSettingsWorkflowStep :exec
-- Update a workflow step IN PLACE (keeps its id). Used by the diff-based
-- workflow editor so that steps kept across an edit retain their id and thus
-- their job_step_assignments (FK ON DELETE CASCADE would otherwise wipe every
-- in-flight assignment). Scoped by settings_profile_id as a safety guard.
UPDATE workflow_steps
SET name = $3, step_order = $4, role_required = $5, default_assignee = $6,
    deadline = $7, ai_router_id = $8, step_type = $9, step_definition_id = $10
WHERE id = $1 AND settings_profile_id = $2;

-- name: DeleteSettingsWorkflowStep :exec
-- Delete a single workflow step by id (scoped to its profile). Used to prune
-- trailing steps when a workflow edit shrinks the step count. Removing a step
-- necessarily drops its job_step_assignments (the step no longer exists).
DELETE FROM workflow_steps WHERE id = $1 AND settings_profile_id = $2;

-- ============================================================
-- Per-target-language workflow step settings (workflow_step_assignments)
--   deadline(step, language) = this table's deadline ?? workflow_steps.deadline
--                              ?? projects.deadline ?? none (never for an ai step)
-- ============================================================

-- name: ListWorkflowStepAssignmentsForProfile :many
-- Internal: keyed by settings_profile_id with no tenant filter. Every caller
-- already holds a profile id from a tenant-verified project or template.
-- Every per-language row of every step of one profile, in one round trip —
-- upload and the settings read both need the whole map, never a single step's.
-- Rows carrying no deadline are skipped: once this table holds more than one
-- field, a row can exist for another reason and must not shadow the step's
-- deadline with a NULL.
SELECT wsa.workflow_step_id, wsa.target_language, wsa.deadline
FROM workflow_step_assignments wsa
JOIN workflow_steps ws ON ws.id = wsa.workflow_step_id
WHERE ws.settings_profile_id = $1 AND wsa.deadline IS NOT NULL
ORDER BY wsa.workflow_step_id, wsa.target_language;

-- name: GetProjectForProfile :one
-- Internal: keyed by settings_profile_id with no tenant filter. Every caller
-- already holds a profile id from a tenant-verified project or template.
-- The scheduling context a profile's steps resolve against: the target
-- languages its per-language rows may name, and the deadline its assignments
-- fall back to. A template profile belongs to no project and returns no row,
-- which callers read as "no language list to check against, and no fallback".
SELECT p.target_languages, p.deadline FROM projects p WHERE p.settings_profile_id = $1;

-- name: DeleteWorkflowStepAssignments :exec
-- Internal: bare workflow_step_id. The ids come from ListSettingsWorkflowSteps
-- / CreateSettingsWorkflowStep for a tenant-verified profile, never from a client.
-- Wholesale replace: a step's per-language rows are rewritten as a set, never
-- patched row by row, so a language the edit dropped cannot survive.
DELETE FROM workflow_step_assignments WHERE workflow_step_id = $1;

-- name: CreateWorkflowStepAssignment :exec
-- Internal: bare workflow_step_id, same provenance as the delete above.
INSERT INTO workflow_step_assignments (workflow_step_id, target_language, deadline)
VALUES ($1, $2, $3);

-- name: RefreshJobStepAssignmentDeadlines :exec
-- Push a step's blueprint deadline onto the jobs already sitting on it,
-- resolved per the job's target language. The twin of
-- ReassignInFlightStepAssignments, and scoped the same way: structurally
-- through job -> file -> project rather than trusting the step id, so an edit
-- can never reach another tenant's assignments.
--
-- Completed assignments are history and keep what they had. ReplaceWorkflowSteps
-- runs this only for a step whose deadline inputs changed; when it does, a
-- deadline a PM set on one job by hand is overwritten — same as a changed
-- default assignee overwrites one, so the two edits behave alike.
--
-- The project's own deadline is the last resort, so a step nobody scheduled
-- still gives its tasks the date the whole project is due. An ai step is
-- excluded outright: it completes itself, and a DB constraint keeps its
-- blueprint row deadline-free, so the fallback must not sneak one in.
--
-- The override is a scalar subquery, not a join: a step carrying rows for
-- other languages must still fall back to its own deadline for this job's
-- language, which a join on workflow_step_assignments would filter away
-- instead. A row that carries no deadline needs no guard here — COALESCE
-- treats the NULL it returns exactly like the missing row.
--
-- The resolve runs once in the inner select; the outer update writes only the
-- rows whose stored deadline differs from it.
UPDATE job_step_assignments jsa
SET deadline = resolved.deadline
FROM (
    SELECT a.id,
           CASE WHEN ws.step_type IN ('ai', 'pretranslate') THEN NULL ELSE COALESCE(
               (SELECT wsa.deadline
                  FROM workflow_step_assignments wsa
                 WHERE wsa.workflow_step_id = ws.id
                   AND wsa.target_language = j.target_language),
               ws.deadline,
               p.deadline) END AS deadline
      FROM job_step_assignments a
      JOIN jobs j ON j.id = a.job_id
      JOIN files f ON f.id = j.file_id
      JOIN projects p ON p.id = f.project_id
      JOIN workflow_steps ws ON ws.id = a.workflow_step_id
     WHERE a.workflow_step_id = @workflow_step_id
       AND a.status IN ('pending', 'in_progress')
       AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
) resolved
WHERE jsa.id = resolved.id
  AND jsa.deadline IS DISTINCT FROM resolved.deadline;

-- ============================================================
-- CAT Editor: Term Lookup
-- Looks up matching terms from all term bases assigned to a settings profile.
-- ============================================================

-- name: LookupSettingsTerms :many
-- ILIKE substring lookup used by QA; the CAT sidebar, MCP and segment
-- prompts use LookupSettingsTermsByTokens.
-- Projects the concept model: each matched source-language term joins to every
-- target-language term of the SAME concept, so callers can group by concept_id.
-- id = the target-language term.id.
SELECT tgt.id, c.id AS concept_id,
       src.text AS source_term, tgt.text AS target_term,
       tgt.status, src.status AS source_status, tgt.notes, c.domain, tgt.mt_translation,
       tb.id AS term_base_id, tb.name AS term_base_name,
       sta.priority
FROM term src
JOIN term_concept c ON c.id = src.concept_id AND c.deleted_at IS NULL AND src.deleted_at IS NULL
JOIN term tgt ON tgt.concept_id = c.id
  AND tgt.deleted_at IS NULL
JOIN term_bases tb ON tb.id = c.term_base_id AND tb.deleted_at IS NULL
-- LATERAL semi-join: a base assigned via MULTIPLE rows that all match this job
-- (e.g. en→fr AND en→fr-fr, both base-subtag-matching an fr job — common after
-- assignment fan-out) must contribute each term ONCE, not once per assignment.
-- Picking the single best (lowest-priority) matching assignment prevents
-- duplicate term.id rows, which the CAT sidebar's keyed list cannot handle.
JOIN LATERAL (
  SELECT a.priority
  FROM settings_terminology_assignments a
  WHERE a.term_base_id = tb.id
    AND a.settings_profile_id = @settings_profile_id
    AND (a.source_language = @source_language
         OR SPLIT_PART(a.source_language, '-', 1) = SPLIT_PART(@source_language, '-', 1))
    AND (a.target_language = @target_language
         OR SPLIT_PART(a.target_language, '-', 1) = SPLIT_PART(@target_language, '-', 1))
  ORDER BY a.priority ASC
  LIMIT 1
) sta ON true
WHERE (src.language = @source_language
       OR SPLIT_PART(src.language, '-', 1) = SPLIT_PART(@source_language, '-', 1))
  AND (tgt.language = @target_language
       OR SPLIT_PART(tgt.language, '-', 1) = SPLIT_PART(@target_language, '-', 1))
  AND @source_text ILIKE '%' || src.text || '%'
-- The (src.id, tgt.id) tail makes the order total: one row IS one (source term,
-- target term) pair, so no two rows can tie on every key. tgt.id alone is not
-- enough — two source synonyms of the same concept and the same character
-- length join to the same target term and would tie. QA composes the
-- terminology finding's message in row order, and that message is the finding's
-- identity for the per-finding ignore.
ORDER BY
  (CASE WHEN @project_domain::text <> '' AND c.domain ILIKE '%' || @project_domain || '%' THEN 0 ELSE 1 END) ASC,
  sta.priority ASC, length(src.text) DESC, src.id ASC, tgt.id ASC;

-- name: LookupSettingsTermsByTokens :many
-- Fast token-based lookup: source text is pre-tokenized into word n-grams on the server,
-- then matched via indexed lower(text) = ANY(@tokens) on source-language terms.
-- Each matched source term joins to every target-language term of the same concept
-- (group by concept_id downstream). id = the target-language term.id.
SELECT tgt.id, c.id AS concept_id,
       src.text AS source_term, tgt.text AS target_term,
       tgt.status, src.status AS source_status, tgt.notes, c.domain, tgt.mt_translation,
       tb.id AS term_base_id, tb.name AS term_base_name,
       sta.priority
FROM term src
JOIN term_concept c ON c.id = src.concept_id AND c.deleted_at IS NULL AND src.deleted_at IS NULL
JOIN term tgt ON tgt.concept_id = c.id
  AND tgt.deleted_at IS NULL
JOIN term_bases tb ON tb.id = c.term_base_id AND tb.deleted_at IS NULL
-- LATERAL semi-join: dedupe terms when a base has multiple matching assignment
-- rows (see LookupSettingsTerms above for the full rationale).
JOIN LATERAL (
  SELECT a.priority
  FROM settings_terminology_assignments a
  WHERE a.term_base_id = tb.id
    AND a.settings_profile_id = @settings_profile_id
    AND (a.source_language = @source_language
         OR SPLIT_PART(a.source_language, '-', 1) = SPLIT_PART(@source_language, '-', 1))
    AND (a.target_language = @target_language
         OR SPLIT_PART(a.target_language, '-', 1) = SPLIT_PART(@target_language, '-', 1))
  ORDER BY a.priority ASC
  LIMIT 1
) sta ON true
WHERE (src.language = @source_language
       OR SPLIT_PART(src.language, '-', 1) = SPLIT_PART(@source_language, '-', 1))
  AND (tgt.language = @target_language
       OR SPLIT_PART(tgt.language, '-', 1) = SPLIT_PART(@target_language, '-', 1))
  AND lower(src.text) = ANY(@tokens::text[])
-- (src.id, tgt.id) makes the order total, as in LookupSettingsTerms above.
ORDER BY
  (CASE WHEN @project_domain::text <> '' AND c.domain ILIKE '%' || @project_domain || '%' THEN 0 ELSE 1 END) ASC,
  sta.priority ASC, length(src.text) DESC, src.id ASC, tgt.id ASC;

-- name: ListSettingsTermsForQA :many
-- LookupSettingsTerms without the source-text filter, in the same order: a QA
-- job run loads the glossary once and applies the ILIKE match per segment in
-- Go (qa.termIndex). Keep the two queries' joins and ORDER BY identical.
SELECT tgt.id, c.id AS concept_id,
       src.text AS source_term, tgt.text AS target_term,
       tgt.status, src.status AS source_status, c.domain
FROM term src
JOIN term_concept c ON c.id = src.concept_id AND c.deleted_at IS NULL AND src.deleted_at IS NULL
JOIN term tgt ON tgt.concept_id = c.id
  AND tgt.deleted_at IS NULL
JOIN term_bases tb ON tb.id = c.term_base_id AND tb.deleted_at IS NULL
JOIN LATERAL (
  SELECT a.priority
  FROM settings_terminology_assignments a
  WHERE a.term_base_id = tb.id
    AND a.settings_profile_id = @settings_profile_id
    AND (a.source_language = @source_language
         OR SPLIT_PART(a.source_language, '-', 1) = SPLIT_PART(@source_language, '-', 1))
    AND (a.target_language = @target_language
         OR SPLIT_PART(a.target_language, '-', 1) = SPLIT_PART(@target_language, '-', 1))
  ORDER BY a.priority ASC
  LIMIT 1
) sta ON true
WHERE (src.language = @source_language
       OR SPLIT_PART(src.language, '-', 1) = SPLIT_PART(@source_language, '-', 1))
  AND (tgt.language = @target_language
       OR SPLIT_PART(tgt.language, '-', 1) = SPLIT_PART(@target_language, '-', 1))
ORDER BY
  (CASE WHEN @project_domain::text <> '' AND c.domain ILIKE '%' || @project_domain || '%' THEN 0 ELSE 1 END) ASC,
  sta.priority ASC, length(src.text) DESC, src.id ASC, tgt.id ASC;


-- ============================================================
-- Concordance Search
-- Substring match across TMs assigned to a settings profile for a language pair.
-- ============================================================

-- name: SettingsConcordanceSearch :many
-- source_rich_text / target_rich_text are returned alongside plain text so
-- the Concordance modal can render inline tags as atomic chips.
-- Frontend prefers rich_text when non-null, falls back to plain otherwise.
SELECT tm.id, tm.source_language, tm.target_language, tm.source_text, tm.target_text,
       tm.source_rich_text, tm.target_rich_text,
       tc.name AS tm_name
FROM translation_memory tm
JOIN tm_collections tc ON tc.id = tm.tm_id AND tc.deleted_at IS NULL
JOIN settings_tm_assignments sta ON sta.tm_id = tm.tm_id
WHERE sta.settings_profile_id = tm_scope_profile_id(@settings_profile_id)
  -- TM assignments carry no languages; filter concordance hits
  -- by the TM entry's own language pair (base-language fallback), scoped to the
  -- TMs assigned to this profile.
  AND (tm.source_language = @source_language
       OR SPLIT_PART(tm.source_language, '-', 1) = SPLIT_PART(@source_language, '-', 1))
  AND (tm.target_language = @target_language
       OR SPLIT_PART(tm.target_language, '-', 1) = SPLIT_PART(@target_language, '-', 1))
  AND (tm.source_text ILIKE '%' || @query || '%' OR tm.target_text ILIKE '%' || @query || '%')
ORDER BY CASE WHEN tm.source_text ILIKE @query THEN 0 ELSE 1 END, tm.source_text
LIMIT @entry_limit;

-- name: SettingsTermConcordanceSearch :many
-- Concordance search across term bases assigned to a settings profile.
-- Projects the concept model (source term × target term of the same concept).
SELECT tgt.id, src.text AS source_term, tgt.text AS target_term, tgt.status, tgt.notes,
       tb.name AS term_base_name
FROM term src
JOIN term_concept c ON c.id = src.concept_id AND c.deleted_at IS NULL AND src.deleted_at IS NULL
JOIN term tgt ON tgt.concept_id = c.id
  AND tgt.deleted_at IS NULL
JOIN term_bases tb ON tb.id = c.term_base_id AND tb.deleted_at IS NULL
-- EXISTS (semi-join): a base with multiple matching assignment rows must not
-- multiply concordance hits (see LookupSettingsTerms). No priority needed here.
WHERE EXISTS (
  SELECT 1 FROM settings_terminology_assignments sta
  WHERE sta.term_base_id = tb.id
    AND sta.settings_profile_id = @settings_profile_id
    AND (sta.source_language = @source_language
         OR SPLIT_PART(sta.source_language, '-', 1) = SPLIT_PART(@source_language, '-', 1))
    AND (sta.target_language = @target_language
         OR SPLIT_PART(sta.target_language, '-', 1) = SPLIT_PART(@target_language, '-', 1))
)
  AND (src.language = @source_language
       OR SPLIT_PART(src.language, '-', 1) = SPLIT_PART(@source_language, '-', 1))
  AND (tgt.language = @target_language
       OR SPLIT_PART(tgt.language, '-', 1) = SPLIT_PART(@target_language, '-', 1))
  AND (src.text ILIKE '%' || @query || '%' OR tgt.text ILIKE '%' || @query || '%')
ORDER BY CASE WHEN src.text ILIKE @query THEN 0 ELSE 1 END, src.text
LIMIT @entry_limit;

-- name: SetSettingsProfileWorkflow :exec
-- Stamps which library workflow a profile's steps were copied from
-- (provenance). NULL clears it.
UPDATE settings_profiles SET workflow_id = $2 WHERE id = $1;

-- name: SetSettingsProfileMtOnUpload :exec
-- Pre-translation profiles always fill in MT on upload; the step-replace
-- path switches it on with the workflow.
UPDATE settings_profiles SET mt_on_upload = $2, updated_at = now()
WHERE id = $1;
