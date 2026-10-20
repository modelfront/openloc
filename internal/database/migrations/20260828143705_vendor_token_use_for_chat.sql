-- +goose Up
-- Which key the built-in chat should use, when a vendor has more than one.
--
-- Chat resolved its key as "the oldest token for this vendor", which is not a
-- choice anyone made — it is just whichever key happened to be entered first.
-- A tenant holding a cheap key and an expensive one had no way to say which
-- one the chat spends.
--
-- Deliberately NO unique index on (vendor_id) WHERE use_for_chat. The handler
-- sets the flag with a single `SET use_for_chat = (id = $1) WHERE vendor_id`,
-- which yields exactly one winner by construction; a partial unique index would
-- add nothing and could fail transiently mid-statement, since PostgreSQL checks
-- uniqueness per row as it updates rather than at statement end.
ALTER TABLE ai_vendor_tokens
    ADD COLUMN use_for_chat boolean NOT NULL DEFAULT false;

-- Backfill the token chat WAS already using, so nothing changes on deploy and
-- the UI can state the current answer instead of leaving it implicit.
--
-- That was `ORDER BY created_at, id LIMIT 1` per vendor — the oldest key. Only
-- chat-capable vendors are marked: the flag is read solely by
-- GetChatVendorToken, and marking a ModelFront or DeepL token would put an
-- indicator on a row whose vendor has no chat surface to explain it.
UPDATE ai_vendor_tokens
SET use_for_chat = true
WHERE id IN (
    SELECT DISTINCT ON (t.vendor_id) t.id
    FROM ai_vendor_tokens t
    JOIN ai_vendors v ON v.id = t.vendor_id
    WHERE v.provider IN ('claude', 'openai')
    ORDER BY t.vendor_id, t.created_at, t.id
);

-- +goose Down
ALTER TABLE ai_vendor_tokens DROP COLUMN use_for_chat;
