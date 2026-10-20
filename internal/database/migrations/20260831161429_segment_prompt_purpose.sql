-- +goose Up
-- Which purpose a segment prompt is written for, so the MT engines page and
-- the AI page can each list only their own. AI systems already carry `purpose`
-- and system prompts derive an equivalent from their output flags (flagsToMode:
-- out_text alone ⇒ MT); segment prompts had no such field, so they were the one
-- prompt kind that could not be split.
--
-- Same name and same two values as ai_systems.purpose deliberately: this
-- answers the same question about the same pair of surfaces. (Not `mode` —
-- that word already means the four-way OutputMode, qe/qe_ape/ape/mt, which is
-- a finer split than this.)
--
-- NOT NULL with a default rather than nullable: a prompt with no purpose lists
-- on neither page, which is a state no screen can render sensibly.
--
-- Distinct from `default_for`, which marks the ONE built-in a new AI system of
-- that purpose starts with. Every row has a `purpose`; at most one row per
-- purpose has a `default_for`.

ALTER TABLE segment_prompts ADD COLUMN purpose text NOT NULL DEFAULT 'qe_ape'
    CHECK (purpose IN ('mt', 'qe_ape'));

-- Built-ins already know their purpose — carry it across verbatim.
UPDATE segment_prompts SET purpose = default_for WHERE default_for IS NOT NULL;

-- Custom rows keep the 'qe_ape' default from the ADD COLUMN above. The shipped
-- template they were authored against is QE+APE-shaped (it carries a
-- "translation" to judge plus tm_match/note/comments), so QE+APE is where an
-- existing prompt was in fact being used.

CREATE INDEX idx_segment_prompts_tenant_purpose ON segment_prompts (tenant_id, purpose);

-- +goose Down
DROP INDEX idx_segment_prompts_tenant_purpose;
ALTER TABLE segment_prompts DROP COLUMN purpose;
