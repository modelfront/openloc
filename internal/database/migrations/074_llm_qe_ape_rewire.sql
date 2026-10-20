-- +goose Up
-- LLM QE+APE rewire — merged branch migration.
--
-- LLM-backed QE+APE. The AI step's LLM run writes a free-text note
-- per segment — currently the quality score of whatever text landed in the
-- segment ({postedit_}score from the {approved, score, postedit,
-- postedit_approved, postedit_score, comment} contract); later it can hold
-- anything else we ask the LLM for. NULL = never scored by an LLM row
-- (ModelFront /predict writes no note).
ALTER TABLE translations ADD COLUMN qe_note text;

-- Comments: the LLM contract's `comment` field lands as a real
-- segment comment. NULL user_id = AI/system author — the same convention as
-- translation_history.created_by (migration 037).
ALTER TABLE segment_comments ALTER COLUMN user_id DROP NOT NULL;

-- An AI engine is used EITHER as an MT engine (settings MT
-- assignment) OR as a QE+APE workflow-step engine — never both. The role is
-- explicit so the UI can filter pickers and the backend can validate
-- prompt-mode compatibility (MT engines take mode-'mt' prompts only; QE+APE
-- engines take status-producing prompts only).
ALTER TABLE ai_engines ADD COLUMN purpose text NOT NULL DEFAULT 'qe_ape'
    CONSTRAINT ai_engines_purpose_check CHECK (purpose IN ('mt', 'qe_ape'));

-- Backfill: engines already assigned as MT are MT engines.
UPDATE ai_engines e SET purpose = 'mt'
WHERE EXISTS (
    SELECT 1 FROM settings_mt_assignments a WHERE a.ai_engine_id = e.id
);

-- Built-in default system prompts. Every tenant gets one read-only,
-- undeletable prompt per mode family (QE, QE+APE, MT), pre-filled with a
-- stock template so engines are configurable without writing a prompt first.
-- Rows are provisioned lazily by aiprompt.EnsureDefaultPrompts (Go carries
-- the template text); this column just marks them.
ALTER TABLE ai_prompts ADD COLUMN is_default boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE ai_prompts DROP COLUMN is_default;
ALTER TABLE ai_engines DROP COLUMN purpose;
DELETE FROM segment_comments WHERE user_id IS NULL;
ALTER TABLE segment_comments ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE translations DROP COLUMN qe_note;
