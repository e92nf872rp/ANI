-- B5 (#22): KBConfig.ocr_enabled persistence. Default false (v1.yaml default).
ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS ocr_enabled BOOLEAN NOT NULL DEFAULT FALSE;
