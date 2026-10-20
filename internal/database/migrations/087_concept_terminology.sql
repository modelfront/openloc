-- +goose Up

-- Concept-centric, multilingual terminology (supersedes the flat term model).
-- A term base now groups CONCEPTS; each concept holds TERMS in any
-- number of languages, and per language may hold several terms (synonyms), each
-- with its own status. Direction (source→target) is NOT stored here — it comes
-- from the project + settings_terminology_assignments at lookup time.
--
-- term_bases.source_language/target_language are TRANSITIONAL. This migration
-- is the only thing that genuinely needs them: a legacy terminology_entries row
-- records no languages, so these columns are the sole record of which language
-- each side belonged to. After migration, bases are multilingual and the UI must
-- NOT treat this pair as authoritative — lookup/QA already take the pair from the
-- project + assignment, and the bilingual browser/CSV take an explicit pair.
-- They linger only as a server-side fallback default until every caller passes a
-- pair explicitly; a dedicated later migration should DROP both columns once
-- nothing references them. Do not build new behavior on them.

-- deleted_at: soft delete. NULL = live; non-NULL = deleted (hidden everywhere,
-- kept for audit/restore). All reads filter deleted_at IS NULL. updated_by on the
-- soft-delete records who removed it.
CREATE TABLE term_concept (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    term_base_id bigint NOT NULL REFERENCES term_bases(id) ON DELETE CASCADE,
    definition text NOT NULL DEFAULT '',
    domain text NOT NULL DEFAULT '',
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    updated_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE INDEX idx_term_concept_base ON term_concept (term_base_id) WHERE deleted_at IS NULL;

CREATE TABLE term (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    concept_id bigint NOT NULL REFERENCES term_concept(id) ON DELETE CASCADE,
    language text NOT NULL,
    text text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'approved',
    part_of_speech text NOT NULL DEFAULT '',
    notes text NOT NULL DEFAULT '',
    mt_translation text NOT NULL DEFAULT '',
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    updated_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
-- keyset projection (target-lang rows by id) + per-concept language fan-out
CREATE INDEX idx_term_concept_lang_id ON term (concept_id, language, id) WHERE deleted_at IS NULL;
-- token lookup: lower(text) = ANY(...) scoped by language, live rows only
-- (replaces idx_terminology_entries_source_term_lower)
CREATE INDEX idx_term_lang_lower_text ON term (language, lower(text)) WHERE deleted_at IS NULL;
-- fuzzy/substring search over any term text — consolidates the DUPLICATED
-- source_term trigram indexes (001 idx_terminology_entries_trgm + 006
-- idx_terminology_entries_source_term_trgm), and now covers targets too
CREATE INDEX idx_term_text_trgm ON term USING gin (text gin_trgm_ops);

-- Bilingual projection of the concept model for the term-browser CRUD/search
-- (the legacy flat-entry API). One row per target-language term of a base's
-- default view pair, paired with its concept's representative (lowest-id)
-- source-language term. "entry id" = the target-language term.id.
CREATE VIEW terminology_entry_view AS
SELECT tt.id,
       c.term_base_id,
       COALESCE((SELECT s.text FROM term s
                 WHERE s.concept_id = c.id AND s.language = b.source_language
                   AND s.deleted_at IS NULL
                 ORDER BY s.id LIMIT 1), '')::text AS source_term,
       tt.text AS target_term,
       tt.status,
       tt.notes,
       c.domain,
       tt.created_by,
       tt.created_at,
       tt.updated_at,
       tt.mt_translation
FROM term tt
JOIN term_concept c ON c.id = tt.concept_id
JOIN term_bases b ON b.id = c.term_base_id
WHERE tt.language = b.target_language
  AND tt.deleted_at IS NULL
  AND c.deleted_at IS NULL;

-- Data migration: each flat entry → 1 concept + 1 source-lang term + 1
-- target-lang term (bijective 1:1, so the Phase-1 projection reproduces exactly
-- today's behavior). Set-based via a temporary correlation column (no per-row
-- loop; scales to large tenants). Empty-text terms are allowed, so a seed's
-- forbidden entry with an empty target survives.
ALTER TABLE term_concept ADD COLUMN legacy_entry_id bigint;

INSERT INTO term_concept (term_base_id, domain, created_by, created_at, updated_at, legacy_entry_id)
SELECT te.term_base_id, te.domain, te.created_by, te.created_at, te.updated_at, te.id
FROM terminology_entries te;

-- source-language term (status forced 'approved' — the source side is the term
-- being defined, not a candidate translation)
INSERT INTO term (concept_id, language, text, status, notes, mt_translation, created_by, created_at, updated_at)
SELECT tc.id, tb.source_language, te.source_term, 'approved', '', '',
       te.created_by, te.created_at, te.updated_at
FROM term_concept tc
JOIN terminology_entries te ON te.id = tc.legacy_entry_id
JOIN term_bases tb ON tb.id = te.term_base_id;

-- target-language term (carries the entry's status/notes/mt_translation)
INSERT INTO term (concept_id, language, text, status, notes, mt_translation, created_by, created_at, updated_at)
SELECT tc.id, tb.target_language, te.target_term, te.status, te.notes, te.mt_translation,
       te.created_by, te.created_at, te.updated_at
FROM term_concept tc
JOIN terminology_entries te ON te.id = tc.legacy_entry_id
JOIN term_bases tb ON tb.id = te.term_base_id;

ALTER TABLE term_concept DROP COLUMN legacy_entry_id;

-- terminology_entries is replaced by the projection over term_concept+term.
-- Its indexes drop with it.
DROP TABLE terminology_entries;

-- +goose Down

-- Best-effort reverse projection into the flat model. LOSSY: multilingual terms,
-- synonyms, part_of_speech, definition, and the new id space cannot round-trip,
-- so migrations_test.go skips the down+re-up cycle for version 85. This Down is
-- still kept valid for real rollbacks.
CREATE TABLE terminology_entries (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    term_base_id bigint NOT NULL REFERENCES term_bases(id) ON DELETE CASCADE,
    source_term text NOT NULL,
    target_term text NOT NULL,
    status text NOT NULL DEFAULT 'approved',
    notes text NOT NULL DEFAULT '',
    domain text NOT NULL DEFAULT '',
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    mt_translation text NOT NULL DEFAULT ''
);
CREATE INDEX idx_terminology_entries_base ON terminology_entries (term_base_id);
CREATE INDEX idx_terminology_entries_source_term_lower ON terminology_entries (lower(source_term));
CREATE INDEX idx_terminology_entries_source_term_trgm ON terminology_entries USING gin (source_term gin_trgm_ops);

-- cross-join each concept's source-lang terms × target-lang terms (default pair)
INSERT INTO terminology_entries (term_base_id, source_term, target_term, status, notes, domain, created_by, created_at, updated_at, mt_translation)
SELECT c.term_base_id, s.text, t.text, t.status, t.notes, c.domain,
       t.created_by, t.created_at, t.updated_at, t.mt_translation
FROM term_concept c
JOIN term_bases tb ON tb.id = c.term_base_id
JOIN term s ON s.concept_id = c.id AND s.language = tb.source_language AND s.deleted_at IS NULL
JOIN term t ON t.concept_id = c.id AND t.language = tb.target_language AND t.deleted_at IS NULL
WHERE c.deleted_at IS NULL;

DROP VIEW IF EXISTS terminology_entry_view;
DROP TABLE term;
DROP TABLE term_concept;
