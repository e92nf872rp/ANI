-- Migration: 20260827000100_storage_objects_bucket_id_nullable.sql
-- Description: Revert storage_objects.bucket_id NOT NULL drift to the authoritative nullable column
-- Depends on: 20260803000100_storage_control_plane_state.sql
-- Notes:
--   - deploy/migrations is the authoritative schema source. 20260803000100 adds
--     storage_objects.bucket_id as a nullable additive column (optional FK target).
--   - Some live databases acquired a NOT NULL constraint on bucket_id outside the
--     migrations (manual ALTER drift). Control-plane object upserts never write
--     bucket_id, so the drift constraint caused SQLSTATE 23502 on object complete.
--   - Idempotent: DROP NOT NULL is a no-op on databases where the column is
--     already nullable, so this is safe to re-apply on clean and drifted databases.


ALTER TABLE storage_objects ALTER COLUMN bucket_id DROP NOT NULL;

