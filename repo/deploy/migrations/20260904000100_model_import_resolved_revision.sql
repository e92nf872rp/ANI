-- Persist the immutable source snapshot selected by remote model import.
-- This is additive and does not alter the Services v1 contract. Hugging Face
-- branch/tag imports populate the column before the worker lists or downloads
-- files; ModelScope imports leave it NULL until that source has a documented
-- immutable revision resolver.

ALTER TABLE model_import_tasks
    ADD COLUMN IF NOT EXISTS resolved_revision TEXT;

CREATE INDEX IF NOT EXISTS idx_model_import_tasks_resolved_revision
    ON model_import_tasks (tenant_id, id, resolved_revision)
    WHERE resolved_revision IS NOT NULL;
