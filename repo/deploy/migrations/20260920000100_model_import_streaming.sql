-- Remote model imports are stored as individual immutable files.  The
-- import task remains the durable owner; this table only records the upload
-- checkpoint for each source path so a worker restart does not require a
-- complete repository re-download.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'model_import_tasks_tenant_id_unique'
          AND conrelid = 'model_import_tasks'::regclass
    ) THEN
        ALTER TABLE model_import_tasks
            ADD CONSTRAINT model_import_tasks_tenant_id_unique
            UNIQUE (tenant_id, id);
    END IF;
END $$;

CREATE TABLE model_import_files (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    import_task_id      UUID NOT NULL,
    file_path           TEXT NOT NULL,
    object_key          TEXT NOT NULL,
    size_bytes          BIGINT NOT NULL CHECK (size_bytes >= 0),
    uploaded_bytes      BIGINT NOT NULL DEFAULT 0 CHECK (uploaded_bytes >= 0 AND uploaded_bytes <= size_bytes),
    sha256              TEXT,
    status              TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'uploading', 'completed', 'failed')),
    upload_id           TEXT,
    completed_parts     JSONB NOT NULL DEFAULT '[]'::jsonb,
    attempt_count       INT NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    error_message       TEXT,
    started_at          TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT model_import_files_task_fk
        FOREIGN KEY (tenant_id, import_task_id)
        REFERENCES model_import_tasks (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT model_import_files_path_unique
        UNIQUE (tenant_id, import_task_id, file_path),
    CONSTRAINT model_import_files_object_key_unique
        UNIQUE (tenant_id, object_key)
);

CREATE INDEX idx_model_import_files_task
    ON model_import_files (tenant_id, import_task_id, status, file_path);

ALTER TABLE model_import_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE model_import_files FORCE ROW LEVEL SECURITY;
CREATE POLICY model_import_files_tenant_isolation
    ON model_import_files
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
CREATE POLICY model_import_files_tenant_access
    ON model_import_files
    AS PERMISSIVE
    FOR ALL
    USING (TRUE)
    WITH CHECK (TRUE);

GRANT SELECT, INSERT, UPDATE ON model_import_files TO ani_app;
