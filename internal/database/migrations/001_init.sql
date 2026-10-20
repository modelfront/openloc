-- +goose Up
-- Squashed from 33 migrations (001-033) on 2026-03-24

CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;
CREATE TYPE file_format AS ENUM (
    'json_i18n',
    'po',
    'pot',
    'xliff',
    'markdown',
    'html',
    'pdf',
    'docx',
    'apple_strings',
    'android_xml',
    'txt'
);
CREATE TYPE predict_outcome AS ENUM (
    'approved',
    'ape_approved',
    'ape_needs_review',
    'rejected'
);
CREATE TYPE translation_origin AS ENUM (
    'tm_exact',
    'tm_fuzzy',
    'mt',
    'human',
    'file_import',
    'propagation'
);
CREATE TYPE translation_status AS ENUM (
    'untranslated',
    'mt_pending',
    'pre_translated',
    'predict_pending',
    'auto_approved',
    'needs_review',
    'approved'
);
CREATE TABLE analysis_weights (
    id bigint NOT NULL,
    tenant_id bigint NOT NULL,
    band text NOT NULL,
    weight_pct integer NOT NULL
);
ALTER TABLE analysis_weights ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME analysis_weights_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE cost_rates (
    id bigint NOT NULL,
    tenant_id bigint NOT NULL,
    source_language text NOT NULL,
    target_language text NOT NULL,
    rate_per_word numeric(10,4) DEFAULT 0 NOT NULL,
    currency text DEFAULT 'USD'::text NOT NULL
);
ALTER TABLE cost_rates ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME cost_rates_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE files (
    id bigint NOT NULL,
    project_id bigint NOT NULL,
    name text NOT NULL,
    format file_format NOT NULL,
    storage_path text NOT NULL,
    segment_count integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    original_name text
);
ALTER TABLE files ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME files_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE invites (
    id bigint NOT NULL,
    tenant_id bigint NOT NULL,
    email text NOT NULL,
    role text DEFAULT 'member'::text NOT NULL,
    invited_by bigint NOT NULL,
    token text NOT NULL,
    accepted_at timestamp with time zone,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);
ALTER TABLE invites ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME invites_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE job_step_assignments (
    id bigint NOT NULL,
    job_id bigint NOT NULL,
    workflow_step_id bigint NOT NULL,
    assigned_to bigint,
    status text DEFAULT 'pending'::text NOT NULL,
    started_at timestamp with time zone,
    completed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    rejection_reason text DEFAULT ''::text NOT NULL
);
ALTER TABLE job_step_assignments ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME job_step_assignments_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE jobs (
    id bigint NOT NULL,
    file_id bigint NOT NULL,
    target_language text NOT NULL,
    word_count integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    status text DEFAULT 'created'::text NOT NULL,
    current_step text DEFAULT ''::text NOT NULL,
    error_message text,
    current_step_id bigint,
    deadline timestamp with time zone
);
ALTER TABLE jobs ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME jobs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE languages (
    code text NOT NULL,
    name text NOT NULL
);
CREATE TABLE mt_engines (
    id bigint NOT NULL,
    name text NOT NULL,
    engine_type text NOT NULL,
    api_key text,
    api_url text,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    tenant_id bigint NOT NULL
);
ALTER TABLE mt_engines ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME mt_engines_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE notification_preferences (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    email_assignment boolean DEFAULT true NOT NULL,
    email_rejection boolean DEFAULT true NOT NULL,
    email_completion boolean DEFAULT true NOT NULL,
    email_deadline boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
ALTER TABLE notification_preferences ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME notification_preferences_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE notifications (
    id bigint NOT NULL,
    tenant_id bigint NOT NULL,
    user_id bigint NOT NULL,
    event_type text NOT NULL,
    subject text NOT NULL,
    body_preview text DEFAULT ''::text NOT NULL,
    reference_type text DEFAULT ''::text NOT NULL,
    reference_id bigint DEFAULT 0 NOT NULL,
    read boolean DEFAULT false NOT NULL,
    emailed boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);
ALTER TABLE notifications ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME notifications_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE org_notification_rules (
    id bigint NOT NULL,
    tenant_id bigint NOT NULL,
    notify_assignment boolean DEFAULT true NOT NULL,
    notify_rejection boolean DEFAULT true NOT NULL,
    notify_completion boolean DEFAULT true NOT NULL,
    notify_deadline boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
ALTER TABLE org_notification_rules ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME org_notification_rules_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE projects (
    id bigint NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    source_language text NOT NULL,
    target_languages text[] NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    template_id bigint,
    tenant_id bigint NOT NULL,
    deadline timestamp with time zone
);
ALTER TABLE projects ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME projects_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE qa_profiles (
    id bigint NOT NULL,
    tenant_id bigint NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
ALTER TABLE qa_profiles ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME qa_profiles_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE qa_results (
    id bigint NOT NULL,
    translation_id bigint NOT NULL,
    check_type text NOT NULL,
    severity text NOT NULL,
    message text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);
ALTER TABLE qa_results ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME qa_results_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE qa_rules (
    id bigint NOT NULL,
    profile_id bigint NOT NULL,
    check_type text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    severity text DEFAULT 'error'::text NOT NULL,
    config jsonb DEFAULT '{}'::jsonb NOT NULL
);
ALTER TABLE qa_rules ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME qa_rules_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE segments (
    id bigint NOT NULL,
    file_id bigint NOT NULL,
    source_text text NOT NULL,
    segment_key text,
    "position" integer NOT NULL,
    context text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    word_count integer DEFAULT 0 NOT NULL
);
ALTER TABLE segments ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME segments_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE template_mt_assignments (
    id bigint NOT NULL,
    template_id bigint NOT NULL,
    mt_engine_id bigint NOT NULL,
    source_language text NOT NULL,
    target_language text NOT NULL
);
ALTER TABLE template_mt_assignments ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME template_mt_assignments_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE template_terminology_assignments (
    id bigint NOT NULL,
    template_id bigint NOT NULL,
    term_base_id bigint NOT NULL,
    source_language text NOT NULL,
    target_language text NOT NULL,
    mode text DEFAULT 'read'::text NOT NULL,
    priority integer DEFAULT 0 NOT NULL
);
ALTER TABLE template_terminology_assignments ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME template_terminology_assignments_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE template_tm_assignments (
    id bigint NOT NULL,
    template_id bigint NOT NULL,
    tm_id bigint NOT NULL,
    source_language text NOT NULL,
    target_language text NOT NULL,
    mode text DEFAULT 'read_write'::text NOT NULL,
    priority integer DEFAULT 0 NOT NULL
);
ALTER TABLE template_tm_assignments ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME template_tm_assignments_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE templates (
    id bigint NOT NULL,
    name text NOT NULL,
    pre_translate_on_upload boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    tenant_id bigint NOT NULL,
    fuzzy_threshold real DEFAULT 0.75 NOT NULL,
    qa_profile_id bigint
);
ALTER TABLE templates ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME templates_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE tenants (
    id bigint NOT NULL,
    name text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    domain text,
    slug text NOT NULL
);
ALTER TABLE tenants ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME tenants_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE term_bases (
    id bigint NOT NULL,
    tenant_id bigint NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    source_language text NOT NULL,
    target_language text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
ALTER TABLE term_bases ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME term_bases_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE terminology_entries (
    id bigint NOT NULL,
    term_base_id bigint NOT NULL,
    source_term text NOT NULL,
    target_term text NOT NULL,
    status text DEFAULT 'approved'::text NOT NULL,
    notes text DEFAULT ''::text NOT NULL,
    domain text DEFAULT ''::text NOT NULL,
    created_by bigint,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    mt_translation text DEFAULT ''::text NOT NULL
);
ALTER TABLE terminology_entries ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME terminology_entries_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE tm_collections (
    id bigint NOT NULL,
    name text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    tenant_id bigint NOT NULL
);
CREATE TABLE tm_imports (
    id bigint NOT NULL,
    tm_id bigint NOT NULL,
    tenant_id bigint NOT NULL,
    file_name text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    total_units integer DEFAULT 0 NOT NULL,
    imported integer DEFAULT 0 NOT NULL,
    skipped integer DEFAULT 0 NOT NULL,
    error_message text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
ALTER TABLE tm_imports ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME tm_imports_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
ALTER TABLE tm_collections ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME translation_memories_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE translation_memory (
    id bigint NOT NULL,
    source_language text NOT NULL,
    target_language text NOT NULL,
    source_text text NOT NULL,
    target_text text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    tm_id bigint NOT NULL
);
ALTER TABLE translation_memory ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME translation_memory_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE translations (
    id bigint NOT NULL,
    segment_id bigint NOT NULL,
    target_language text NOT NULL,
    status translation_status DEFAULT 'untranslated'::translation_status NOT NULL,
    mt_text text,
    mt_engine text,
    predict_outcome predict_outcome,
    predict_approved boolean,
    ape_text text,
    ape_approved boolean,
    active_text text,
    human_text text,
    feedback_sent boolean DEFAULT false NOT NULL,
    feedback_sent_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    tm_match_score real,
    origin translation_origin,
    editing_time_ms bigint DEFAULT 0 NOT NULL,
    edit_distance integer DEFAULT 0 NOT NULL,
    edited_by bigint
);
ALTER TABLE translations ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME translations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE users (
    id bigint NOT NULL,
    tenant_id bigint NOT NULL,
    email text NOT NULL,
    password_hash text DEFAULT ''::text,
    name text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    role text DEFAULT 'member'::text NOT NULL
);
ALTER TABLE users ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME users_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
CREATE TABLE workflow_steps (
    id bigint NOT NULL,
    template_id bigint NOT NULL,
    name text NOT NULL,
    step_order integer DEFAULT 0 NOT NULL,
    role_required text DEFAULT 'member'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    default_assignee bigint
);
ALTER TABLE workflow_steps ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME workflow_steps_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);
ALTER TABLE ONLY analysis_weights
    ADD CONSTRAINT analysis_weights_pkey PRIMARY KEY (id);
ALTER TABLE ONLY analysis_weights
    ADD CONSTRAINT analysis_weights_tenant_id_band_key UNIQUE (tenant_id, band);
ALTER TABLE ONLY cost_rates
    ADD CONSTRAINT cost_rates_pkey PRIMARY KEY (id);
ALTER TABLE ONLY cost_rates
    ADD CONSTRAINT cost_rates_tenant_id_source_language_target_language_key UNIQUE (tenant_id, source_language, target_language);
ALTER TABLE ONLY files
    ADD CONSTRAINT files_pkey PRIMARY KEY (id);
ALTER TABLE ONLY invites
    ADD CONSTRAINT invites_pkey PRIMARY KEY (id);
ALTER TABLE ONLY invites
    ADD CONSTRAINT invites_token_key UNIQUE (token);
ALTER TABLE ONLY job_step_assignments
    ADD CONSTRAINT job_step_assignments_job_id_workflow_step_id_key UNIQUE (job_id, workflow_step_id);
ALTER TABLE ONLY job_step_assignments
    ADD CONSTRAINT job_step_assignments_pkey PRIMARY KEY (id);
ALTER TABLE ONLY jobs
    ADD CONSTRAINT jobs_file_id_target_language_key UNIQUE (file_id, target_language);
ALTER TABLE ONLY jobs
    ADD CONSTRAINT jobs_pkey PRIMARY KEY (id);
ALTER TABLE ONLY languages
    ADD CONSTRAINT languages_pkey PRIMARY KEY (code);
ALTER TABLE ONLY mt_engines
    ADD CONSTRAINT mt_engines_pkey PRIMARY KEY (id);
ALTER TABLE ONLY notification_preferences
    ADD CONSTRAINT notification_preferences_pkey PRIMARY KEY (id);
ALTER TABLE ONLY notification_preferences
    ADD CONSTRAINT notification_preferences_user_id_key UNIQUE (user_id);
ALTER TABLE ONLY notifications
    ADD CONSTRAINT notifications_pkey PRIMARY KEY (id);
ALTER TABLE ONLY org_notification_rules
    ADD CONSTRAINT org_notification_rules_pkey PRIMARY KEY (id);
ALTER TABLE ONLY org_notification_rules
    ADD CONSTRAINT org_notification_rules_tenant_id_key UNIQUE (tenant_id);
ALTER TABLE ONLY projects
    ADD CONSTRAINT projects_pkey PRIMARY KEY (id);
ALTER TABLE ONLY qa_profiles
    ADD CONSTRAINT qa_profiles_pkey PRIMARY KEY (id);
ALTER TABLE ONLY qa_results
    ADD CONSTRAINT qa_results_pkey PRIMARY KEY (id);
ALTER TABLE ONLY qa_results
    ADD CONSTRAINT qa_results_translation_id_check_type_key UNIQUE (translation_id, check_type);
ALTER TABLE ONLY qa_rules
    ADD CONSTRAINT qa_rules_pkey PRIMARY KEY (id);
ALTER TABLE ONLY qa_rules
    ADD CONSTRAINT qa_rules_profile_id_check_type_key UNIQUE (profile_id, check_type);
ALTER TABLE ONLY segments
    ADD CONSTRAINT segments_pkey PRIMARY KEY (id);
ALTER TABLE ONLY template_mt_assignments
    ADD CONSTRAINT template_mt_assignments_pkey PRIMARY KEY (id);
ALTER TABLE ONLY template_mt_assignments
    ADD CONSTRAINT template_mt_assignments_template_id_source_language_target__key UNIQUE (template_id, source_language, target_language);
ALTER TABLE ONLY template_terminology_assignments
    ADD CONSTRAINT template_terminology_assignme_template_id_term_base_id_sour_key UNIQUE (template_id, term_base_id, source_language, target_language);
ALTER TABLE ONLY template_terminology_assignments
    ADD CONSTRAINT template_terminology_assignments_pkey PRIMARY KEY (id);
ALTER TABLE ONLY template_tm_assignments
    ADD CONSTRAINT template_tm_assignments_pkey PRIMARY KEY (id);
ALTER TABLE ONLY template_tm_assignments
    ADD CONSTRAINT template_tm_assignments_template_id_tm_id_source_language_t_key UNIQUE (template_id, tm_id, source_language, target_language);
ALTER TABLE ONLY templates
    ADD CONSTRAINT templates_pkey PRIMARY KEY (id);
ALTER TABLE ONLY tenants
    ADD CONSTRAINT tenants_pkey PRIMARY KEY (id);
ALTER TABLE ONLY term_bases
    ADD CONSTRAINT term_bases_pkey PRIMARY KEY (id);
ALTER TABLE ONLY terminology_entries
    ADD CONSTRAINT terminology_entries_pkey PRIMARY KEY (id);
ALTER TABLE ONLY tm_imports
    ADD CONSTRAINT tm_imports_pkey PRIMARY KEY (id);
ALTER TABLE ONLY tm_collections
    ADD CONSTRAINT translation_memories_pkey PRIMARY KEY (id);
ALTER TABLE ONLY translation_memory
    ADD CONSTRAINT translation_memory_pkey PRIMARY KEY (id);
ALTER TABLE ONLY translation_memory
    ADD CONSTRAINT translation_memory_tm_id_src_tgt_text_key UNIQUE (tm_id, source_language, target_language, source_text);
ALTER TABLE ONLY translations
    ADD CONSTRAINT translations_pkey PRIMARY KEY (id);
ALTER TABLE ONLY translations
    ADD CONSTRAINT translations_segment_id_target_language_key UNIQUE (segment_id, target_language);
ALTER TABLE ONLY users
    ADD CONSTRAINT users_email_key UNIQUE (email);
ALTER TABLE ONLY users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);
ALTER TABLE ONLY workflow_steps
    ADD CONSTRAINT workflow_steps_pkey PRIMARY KEY (id);
CREATE INDEX idx_files_project_id ON files USING btree (project_id);
CREATE INDEX idx_invites_email ON invites USING btree (email);
CREATE INDEX idx_invites_tenant ON invites USING btree (tenant_id);
CREATE INDEX idx_job_step_assignments_job ON job_step_assignments USING btree (job_id, status);
CREATE INDEX idx_job_step_assignments_user ON job_step_assignments USING btree (assigned_to, status);
CREATE INDEX idx_jobs_deadline ON jobs USING btree (deadline) WHERE (deadline IS NOT NULL);
CREATE INDEX idx_jobs_file_id ON jobs USING btree (file_id);
CREATE INDEX idx_mt_engines_tenant ON mt_engines USING btree (tenant_id);
CREATE INDEX idx_notifications_user_created ON notifications USING btree (user_id, created_at DESC);
CREATE INDEX idx_notifications_user_unread ON notifications USING btree (user_id, read) WHERE (read = false);
CREATE INDEX idx_projects_tenant ON projects USING btree (tenant_id);
CREATE INDEX idx_qa_profiles_tenant ON qa_profiles USING btree (tenant_id);
CREATE INDEX idx_qa_results_translation ON qa_results USING btree (translation_id);
CREATE INDEX idx_qa_rules_profile ON qa_rules USING btree (profile_id);
CREATE INDEX idx_segments_file_id ON segments USING btree (file_id);
CREATE INDEX idx_segments_file_position ON segments USING btree (file_id, "position");
CREATE INDEX idx_template_mt_lang_lookup ON template_mt_assignments USING btree (template_id, source_language, target_language);
CREATE INDEX idx_template_term_lookup ON template_terminology_assignments USING btree (template_id, source_language, target_language);
CREATE INDEX idx_template_tm_lookup ON template_tm_assignments USING btree (template_id, source_language, target_language);
CREATE INDEX idx_templates_tenant ON templates USING btree (tenant_id);
CREATE UNIQUE INDEX idx_tenants_slug ON tenants USING btree (slug);
CREATE INDEX idx_term_bases_tenant ON term_bases USING btree (tenant_id);
CREATE INDEX idx_terminology_entries_base ON terminology_entries USING btree (term_base_id);
CREATE INDEX idx_terminology_entries_trgm ON terminology_entries USING gin (source_term gin_trgm_ops);
CREATE INDEX idx_tm_collections_tenant ON tm_collections USING btree (tenant_id);
CREATE INDEX idx_tm_entries_pagination ON translation_memory USING btree (tm_id, id);
CREATE INDEX idx_tm_imports_tm_id ON tm_imports USING btree (tm_id);
CREATE INDEX idx_tm_lookup ON translation_memory USING btree (tm_id, source_language, target_language);
CREATE INDEX idx_tm_source_trgm ON translation_memory USING gin (source_text gin_trgm_ops);
CREATE INDEX idx_translations_edited_by ON translations USING btree (edited_by) WHERE (edited_by IS NOT NULL);
CREATE INDEX idx_translations_segment_id ON translations USING btree (segment_id);
CREATE INDEX idx_translations_status ON translations USING btree (status);
CREATE INDEX idx_users_tenant ON users USING btree (tenant_id);
CREATE INDEX idx_workflow_steps_template ON workflow_steps USING btree (template_id, step_order);
ALTER TABLE ONLY analysis_weights
    ADD CONSTRAINT analysis_weights_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
ALTER TABLE ONLY cost_rates
    ADD CONSTRAINT cost_rates_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
ALTER TABLE ONLY files
    ADD CONSTRAINT files_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE;
ALTER TABLE ONLY invites
    ADD CONSTRAINT invites_invited_by_fkey FOREIGN KEY (invited_by) REFERENCES users(id);
ALTER TABLE ONLY invites
    ADD CONSTRAINT invites_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);
ALTER TABLE ONLY job_step_assignments
    ADD CONSTRAINT job_step_assignments_assigned_to_fkey FOREIGN KEY (assigned_to) REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE ONLY job_step_assignments
    ADD CONSTRAINT job_step_assignments_job_id_fkey FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE;
ALTER TABLE ONLY job_step_assignments
    ADD CONSTRAINT job_step_assignments_workflow_step_id_fkey FOREIGN KEY (workflow_step_id) REFERENCES workflow_steps(id) ON DELETE CASCADE;
ALTER TABLE ONLY jobs
    ADD CONSTRAINT jobs_current_step_id_fkey FOREIGN KEY (current_step_id) REFERENCES job_step_assignments(id) ON DELETE SET NULL;
ALTER TABLE ONLY jobs
    ADD CONSTRAINT jobs_file_id_fkey FOREIGN KEY (file_id) REFERENCES files(id) ON DELETE CASCADE;
ALTER TABLE ONLY mt_engines
    ADD CONSTRAINT mt_engines_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);
ALTER TABLE ONLY notification_preferences
    ADD CONSTRAINT notification_preferences_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE ONLY notifications
    ADD CONSTRAINT notifications_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
ALTER TABLE ONLY notifications
    ADD CONSTRAINT notifications_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE ONLY org_notification_rules
    ADD CONSTRAINT org_notification_rules_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
ALTER TABLE ONLY projects
    ADD CONSTRAINT projects_template_id_fkey FOREIGN KEY (template_id) REFERENCES templates(id) ON DELETE SET NULL;
ALTER TABLE ONLY projects
    ADD CONSTRAINT projects_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);
ALTER TABLE ONLY qa_profiles
    ADD CONSTRAINT qa_profiles_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
ALTER TABLE ONLY qa_results
    ADD CONSTRAINT qa_results_translation_id_fkey FOREIGN KEY (translation_id) REFERENCES translations(id) ON DELETE CASCADE;
ALTER TABLE ONLY qa_rules
    ADD CONSTRAINT qa_rules_profile_id_fkey FOREIGN KEY (profile_id) REFERENCES qa_profiles(id) ON DELETE CASCADE;
ALTER TABLE ONLY segments
    ADD CONSTRAINT segments_file_id_fkey FOREIGN KEY (file_id) REFERENCES files(id) ON DELETE CASCADE;
ALTER TABLE ONLY template_mt_assignments
    ADD CONSTRAINT template_mt_assignments_mt_engine_id_fkey FOREIGN KEY (mt_engine_id) REFERENCES mt_engines(id) ON DELETE CASCADE;
ALTER TABLE ONLY template_mt_assignments
    ADD CONSTRAINT template_mt_assignments_template_id_fkey FOREIGN KEY (template_id) REFERENCES templates(id) ON DELETE CASCADE;
ALTER TABLE ONLY template_terminology_assignments
    ADD CONSTRAINT template_terminology_assignments_template_id_fkey FOREIGN KEY (template_id) REFERENCES templates(id) ON DELETE CASCADE;
ALTER TABLE ONLY template_terminology_assignments
    ADD CONSTRAINT template_terminology_assignments_term_base_id_fkey FOREIGN KEY (term_base_id) REFERENCES term_bases(id) ON DELETE CASCADE;
ALTER TABLE ONLY template_tm_assignments
    ADD CONSTRAINT template_tm_assignments_template_id_fkey FOREIGN KEY (template_id) REFERENCES templates(id) ON DELETE CASCADE;
ALTER TABLE ONLY template_tm_assignments
    ADD CONSTRAINT template_tm_assignments_tm_id_fkey FOREIGN KEY (tm_id) REFERENCES tm_collections(id) ON DELETE CASCADE;
ALTER TABLE ONLY templates
    ADD CONSTRAINT templates_qa_profile_id_fkey FOREIGN KEY (qa_profile_id) REFERENCES qa_profiles(id) ON DELETE SET NULL;
ALTER TABLE ONLY templates
    ADD CONSTRAINT templates_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);
ALTER TABLE ONLY term_bases
    ADD CONSTRAINT term_bases_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
ALTER TABLE ONLY terminology_entries
    ADD CONSTRAINT terminology_entries_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE ONLY terminology_entries
    ADD CONSTRAINT terminology_entries_term_base_id_fkey FOREIGN KEY (term_base_id) REFERENCES term_bases(id) ON DELETE CASCADE;
ALTER TABLE ONLY tm_collections
    ADD CONSTRAINT tm_collections_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);
ALTER TABLE ONLY tm_imports
    ADD CONSTRAINT tm_imports_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);
ALTER TABLE ONLY tm_imports
    ADD CONSTRAINT tm_imports_tm_id_fkey FOREIGN KEY (tm_id) REFERENCES tm_collections(id) ON DELETE CASCADE;
ALTER TABLE ONLY translation_memory
    ADD CONSTRAINT translation_memory_tm_id_fkey FOREIGN KEY (tm_id) REFERENCES tm_collections(id) ON DELETE CASCADE;
ALTER TABLE ONLY translations
    ADD CONSTRAINT translations_edited_by_fkey FOREIGN KEY (edited_by) REFERENCES users(id);
ALTER TABLE ONLY translations
    ADD CONSTRAINT translations_segment_id_fkey FOREIGN KEY (segment_id) REFERENCES segments(id) ON DELETE CASCADE;
ALTER TABLE ONLY users
    ADD CONSTRAINT users_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);
ALTER TABLE ONLY workflow_steps
    ADD CONSTRAINT workflow_steps_default_assignee_fkey FOREIGN KEY (default_assignee) REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE ONLY workflow_steps
    ADD CONSTRAINT workflow_steps_template_id_fkey FOREIGN KEY (template_id) REFERENCES templates(id) ON DELETE CASCADE;

-- +goose Down
-- Full teardown — drops everything
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;
