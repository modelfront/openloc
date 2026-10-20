-- +goose Up
-- The AI settings grid renders one tile per connection, so a vendor holding
-- several empty connections with the same name and endpoint shows up as that
-- many identical tiles. Delete an empty connection when an identical one on the
-- same vendor has keys or is older; that keeps exactly one of each group.
-- Connections with keys, a different name or a different endpoint are left
-- alone. Only ai_vendor_tokens references ai_connections, so an empty row is
-- referenced by nothing.
DELETE FROM ai_connections c
WHERE NOT EXISTS (SELECT 1 FROM ai_vendor_tokens t WHERE t.connection_id = c.id)
  AND EXISTS (
      SELECT 1 FROM ai_connections o
      WHERE o.vendor_id = c.vendor_id
        AND o.name = c.name
        AND o.base_url = c.base_url
        AND o.id <> c.id
        AND (o.id < c.id OR EXISTS (SELECT 1 FROM ai_vendor_tokens t WHERE t.connection_id = o.id))
  );

-- +goose Down
-- The deleted rows held no keys and repeated a surviving row exactly, so there
-- is nothing to restore.
SELECT 1;
