-- +goose Up
-- The MT built-in prompts are named plain "Default".
--
-- Each page now lists one purpose, and the MT engines page
-- shows exactly one built-in of each prompt kind — so "MT" in the name was
-- distinguishing it from siblings that are no longer on screen. The system
-- prompt card still carries a mode badge, so the purpose is on screen either
-- way.
--
-- The AI page keeps "Default QE" / "Default QE+APE" / "Default APE": three
-- built-ins share that list, so those names are still doing work.
--
-- Renaming in SQL rather than only changing the code constants: both upserts
-- key on name, so a constant change alone inserts a second row and leaves the
-- old one behind as a second is_default entry in the same list.

-- ai_prompts has no plain "Default", so this one is a straight rename.
UPDATE ai_prompts SET name = 'Default' WHERE is_default AND name = 'Default MT';

-- segment_prompts already has a "Default" per tenant (the QE+APE built-in), so
-- the tenant-wide unique name has to become unique per purpose first. That is
-- the right grain now regardless: the two lists are separate surfaces, and a
-- name only has to be unambiguous within the one it appears on.
ALTER TABLE segment_prompts DROP CONSTRAINT segment_prompts_tenant_id_name_key;
ALTER TABLE segment_prompts ADD CONSTRAINT segment_prompts_tenant_id_name_purpose_key
    UNIQUE (tenant_id, name, purpose);

UPDATE segment_prompts SET name = 'Default'
    WHERE is_default AND name IN ('Default MT', 'Default MT segment prompt');

-- +goose Down
-- Rename before restoring the tenant-wide constraint, or the two "Default"
-- rows collide on the way back.
UPDATE segment_prompts SET name = 'Default MT' WHERE is_default AND purpose = 'mt' AND name = 'Default';
ALTER TABLE segment_prompts DROP CONSTRAINT segment_prompts_tenant_id_name_purpose_key;
ALTER TABLE segment_prompts ADD CONSTRAINT segment_prompts_tenant_id_name_key UNIQUE (tenant_id, name);
UPDATE ai_prompts SET name = 'Default MT'
    WHERE is_default AND name = 'Default' AND out_text AND NOT out_status AND NOT out_comments;
