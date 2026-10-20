-- +goose Up
-- An engine can be UNUSABLE without being gone.
--
-- Two causes, one state: it was switched off, or the AI settings key it spends
-- was deleted. Both leave the engine in place, visible, and flagged wherever it
-- appears — including on the routers that route to it.
--
-- This replaces "you may not delete a key an engine uses", which was a dead end:
-- the only way out was to hunt down every engine first, with nothing telling you
-- which ones they were.

-- An AI engine needs its own switch. `ai_engines.enabled` existed once and was
-- dropped by 068 when rows were introduced; the switch belongs on the ENGINE,
-- which is what a router routes to, not on a language row inside it.
ALTER TABLE ai_engines ADD COLUMN enabled boolean NOT NULL DEFAULT true;

-- Deleting a key nulls the reference instead of being refused. The engine
-- survives — its name, its options and its place in every router are the user's
-- work, and throwing them away to remove a credential is the wrong trade.
--
-- The constraint names are looked up rather than assumed: these columns were
-- added by different migrations over time, and a hardcoded name that does not
-- match silently leaves RESTRICT in place.
-- +goose StatementBegin
DO $$
DECLARE
    tbl  text;
    cname text;
BEGIN
    FOREACH tbl IN ARRAY ARRAY['ai_engine_rows', 'mt_engines'] LOOP
        SELECT conname INTO cname
        FROM pg_constraint
        WHERE conrelid = tbl::regclass
          AND contype = 'f'
          AND confrelid = 'ai_vendor_tokens'::regclass;
        IF cname IS NULL THEN
            RAISE EXCEPTION 'no token_id foreign key found on %', tbl;
        END IF;
        EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', tbl, cname);
        EXECUTE format(
            'ALTER TABLE %I ADD CONSTRAINT %I FOREIGN KEY (token_id) '
            'REFERENCES ai_vendor_tokens(id) ON DELETE SET NULL',
            tbl, tbl || '_token_id_fkey');
    END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
    tbl  text;
    cname text;
BEGIN
    FOREACH tbl IN ARRAY ARRAY['ai_engine_rows', 'mt_engines'] LOOP
        SELECT conname INTO cname
        FROM pg_constraint
        WHERE conrelid = tbl::regclass
          AND contype = 'f'
          AND confrelid = 'ai_vendor_tokens'::regclass;
        IF cname IS NOT NULL THEN
            EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', tbl, cname);
        END IF;
        EXECUTE format(
            'ALTER TABLE %I ADD CONSTRAINT %I FOREIGN KEY (token_id) '
            'REFERENCES ai_vendor_tokens(id) ON DELETE RESTRICT',
            tbl, tbl || '_token_id_fkey');
    END LOOP;
END $$;
-- +goose StatementEnd

ALTER TABLE ai_engines DROP COLUMN enabled;
