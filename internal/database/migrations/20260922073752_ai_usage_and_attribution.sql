-- +goose Up
-- AI engine accounting, in two independent halves.
--
-- 1. WHICH AI ENGINE produced a translation. Deliberately a separate column from
--    mt_engine_id rather than a shared one: DeepL and "GPT-4o via prompt X" are
--    not comparable units, and folding them together would make the existing
--    "MT volume by engine" chart answer a question nobody asked. Two columns
--    means two measures, and a dashboard can show either or both.
ALTER TABLE translation_history
    ADD COLUMN ai_engine_id bigint REFERENCES ai_engines(id) ON DELETE SET NULL;

-- Matches the measure's predicate (ai_engine_id IS NOT NULL, ordered by time).
CREATE INDEX idx_translation_history_ai_engine
    ON translation_history (ai_engine_id, created_at) WHERE ai_engine_id IS NOT NULL;

-- 2. WHAT THE API CALLS COST in tokens. Volume and tokens answer different
--    questions — a thousand words may be one call or thirty — so this is its own
--    table rather than more columns on history. One row per provider call.
CREATE TABLE ai_usage_events (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    -- Nullable so a deleted AI engine does not take its usage history with it:
    -- the tokens were still spent, and a report that shrinks retroactively is
    -- worse than one with an "unknown" row.
    ai_engine_id  bigint REFERENCES ai_engines(id) ON DELETE SET NULL,
    -- Copied at write time, not joined: an AI engine can be renamed or have its
    -- model changed, and past spend must keep saying what it actually was.
    ai_engine_name text  NOT NULL DEFAULT '',
    purpose       text   NOT NULL,
    provider      text   NOT NULL,
    model         text   NOT NULL,
    -- The metrics compiler pins every query with `p.tenant_id`, so a measure
    -- over this table has to reach projects. Calls with no project (chat, MT
    -- preview) are still recorded, and are simply not visible to the dashboards.
    project_id    bigint REFERENCES projects(id) ON DELETE CASCADE,
    job_id        bigint REFERENCES jobs(id) ON DELETE SET NULL,
    input_tokens  bigint NOT NULL DEFAULT 0,
    output_tokens bigint NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_ai_usage_events_tenant_time ON ai_usage_events (tenant_id, created_at);
CREATE INDEX idx_ai_usage_events_project ON ai_usage_events (project_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS ai_usage_events;
DROP INDEX IF EXISTS idx_translation_history_ai_engine;
ALTER TABLE translation_history DROP COLUMN ai_engine_id;
