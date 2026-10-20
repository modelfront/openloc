-- +goose Up
-- Which purpose a built-in segment prompt is the default for.
--
-- Seed ships two built-ins with deliberately different shapes: the QE+APE one
-- asks for "Current translation:" (there IS a translation to judge), the MT one
-- doesn't (nothing is translated yet — the renderer's empty-line rule drops
-- that label, but the placeholder shouldn't be there in the first place).
--
-- The AI-system form picked `is_default` alone, which returns whichever built-in
-- was created first — so a new MT system got the QE-shaped template. This column
-- makes the pairing explicit instead of matching on a name string: the seed knows
-- which prompt suits which purpose, so it records it.
--
-- NULL = not a purpose default (every custom prompt, and the value for a
-- built-in that isn't tied to one purpose).

ALTER TABLE segment_prompts ADD COLUMN default_for text
    CHECK (default_for IS NULL OR default_for IN ('mt', 'qe_ape'));

-- Backfill the existing built-ins by the names seed has used for them.
UPDATE segment_prompts SET default_for = 'mt'
    WHERE is_default AND name IN ('Default MT', 'Default MT segment prompt');
UPDATE segment_prompts SET default_for = 'qe_ape'
    WHERE is_default AND default_for IS NULL;

-- At most one default per purpose per tenant — the form picks by purpose and a
-- second row would make that choice arbitrary again.
CREATE UNIQUE INDEX uq_segment_prompt_default_for
    ON segment_prompts (tenant_id, default_for)
    WHERE default_for IS NOT NULL;

-- +goose Down
DROP INDEX uq_segment_prompt_default_for;
ALTER TABLE segment_prompts DROP COLUMN default_for;
