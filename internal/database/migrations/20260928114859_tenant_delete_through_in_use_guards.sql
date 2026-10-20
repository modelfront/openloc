-- +goose Up
-- +goose StatementBegin

-- Three in-use guards are checked at commit (DEFERRABLE INITIALLY DEFERRED).
-- A direct delete of a prompt, connection or TM profile still in use still
-- fails: its transaction commits with the statement. Checked at once (RESTRICT
-- or NO ACTION), a guard fires in the middle of a tenant cascade (DeleteTenant),
-- before the cascade reaches the rows pointing at the thing; at commit both
-- sides are gone.
ALTER TABLE ai_engine_rows DROP CONSTRAINT ai_engine_rows_system_prompt_id_fkey,
    ADD CONSTRAINT ai_engine_rows_system_prompt_id_fkey
        FOREIGN KEY (system_prompt_id) REFERENCES ai_prompts(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ai_vendor_tokens DROP CONSTRAINT ai_vendor_tokens_connection_id_fkey,
    ADD CONSTRAINT ai_vendor_tokens_connection_id_fkey
        FOREIGN KEY (connection_id) REFERENCES ai_connections(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE settings_profiles DROP CONSTRAINT settings_profiles_tm_profile_id_fkey,
    ADD CONSTRAINT settings_profiles_tm_profile_id_fkey
        FOREIGN KEY (tm_profile_id) REFERENCES tm_profiles(id) DEFERRABLE INITIALLY DEFERRED;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE ai_engine_rows DROP CONSTRAINT ai_engine_rows_system_prompt_id_fkey,
    ADD CONSTRAINT ai_engine_rows_system_prompt_id_fkey
        FOREIGN KEY (system_prompt_id) REFERENCES ai_prompts(id) ON DELETE RESTRICT;
ALTER TABLE ai_vendor_tokens DROP CONSTRAINT ai_vendor_tokens_connection_id_fkey,
    ADD CONSTRAINT ai_vendor_tokens_connection_id_fkey
        FOREIGN KEY (connection_id) REFERENCES ai_connections(id) ON DELETE RESTRICT;
ALTER TABLE settings_profiles DROP CONSTRAINT settings_profiles_tm_profile_id_fkey,
    ADD CONSTRAINT settings_profiles_tm_profile_id_fkey
        FOREIGN KEY (tm_profile_id) REFERENCES tm_profiles(id) ON DELETE RESTRICT;

-- +goose StatementEnd
