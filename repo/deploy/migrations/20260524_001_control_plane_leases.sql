CREATE TABLE IF NOT EXISTS control_plane_leases (
    lease_name TEXT PRIMARY KEY,
    holder_id TEXT NOT NULL,
    lease_until TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_control_plane_leases_until
    ON control_plane_leases (lease_until);
