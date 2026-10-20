-- +goose Up
-- +goose StatementBegin
-- The one definition of "another user has this segment open": a live hold
-- (not expired) whose holder is not actor_id. IS DISTINCT FROM, so a NULL
-- actor (a write with no user) counts every live hold. Writes call it in the
-- statement that writes.
CREATE FUNCTION translation_held_by_other(p_translation_id bigint, p_actor_id bigint) RETURNS boolean
LANGUAGE sql STABLE
AS $$
    SELECT EXISTS (
        SELECT 1 FROM translation_holds h
        WHERE h.translation_id = p_translation_id
          AND h.expires_at > now()
          AND h.user_id IS DISTINCT FROM p_actor_id)
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS translation_held_by_other(bigint, bigint);
-- +goose StatementEnd
