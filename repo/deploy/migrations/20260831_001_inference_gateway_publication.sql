-- C41 durable, fenced publication state for tenant-scoped inference gateway routing.
BEGIN;

ALTER TABLE inference_services
    ADD COLUMN IF NOT EXISTS publication_desired TEXT NOT NULL DEFAULT 'unpublished'
        CHECK (publication_desired IN ('published', 'unpublished')),
    ADD COLUMN IF NOT EXISTS publication_generation BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS publication_observed_generation BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS publication_phase TEXT NOT NULL DEFAULT 'unpublished'
        CHECK (publication_phase IN ('pending', 'publishing', 'published', 'unpublishing', 'unpublished', 'failed')),
    ADD COLUMN IF NOT EXISTS publication_lease_owner TEXT,
    ADD COLUMN IF NOT EXISTS publication_lease_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS publication_lease_token UUID,
    ADD COLUMN IF NOT EXISTS publication_last_error TEXT,
    ADD COLUMN IF NOT EXISTS publication_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

UPDATE inference_services
SET publication_desired = 'unpublished',
    publication_generation = 0,
    publication_observed_generation = 0,
    publication_phase = 'unpublished',
    invocation_url = NULL,
    publication_updated_at = NOW()
WHERE publication_generation = 0;

CREATE INDEX IF NOT EXISTS idx_inference_services_publication_claim
    ON inference_services(publication_updated_at, id)
    WHERE deleted_at IS NULL
      AND (publication_generation <> publication_observed_generation
           OR publication_phase IN ('pending', 'publishing', 'unpublishing', 'failed'));

COMMIT;
