package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// dataPlaneAuditEntry records a single data-plane operation for audit (SPEC
// §3.3-6 / §3.2 independent audit for role=service).
type dataPlaneAuditEntry struct {
	ID              string
	Role            string
	ServiceIdentity string
	TenantID        uuid.UUID
	SQL             string
	StatementHash   string
	TableName       string
	DurationMs      int64
	StatementAt     time.Time
	CreatedAt       time.Time
}

// dataPlaneAuditWriter is satisfied by both *pgxpool.Pool (independent audit)
// and pgx.Tx (in-transaction audit).
type dataPlaneAuditWriter interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// hashDataPlaneSQL returns a short SHA-256 digest of the SQL text for audit
// records (SPEC §3.3-6). The full SQL is also stored separately for forensics;
// the hash provides a stable correlation key for grouping repeated queries.
func hashDataPlaneSQL(sql string) string {
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:16])
}

// persistDataPlaneAudit writes the audit entry to the data_plane_audit table
// through the given writer (a transaction or the pool).
func persistDataPlaneAudit(ctx context.Context, w dataPlaneAuditWriter, entry dataPlaneAuditEntry) error {
	_, err := w.Exec(ctx, `
		INSERT INTO data_plane_audit (
			id, role, service_identity, tenant_id, sql_text, statement_hash, table_name, duration_ms, statement_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, entry.ID, entry.Role, nullStr(entry.ServiceIdentity), nullUUID(entry.TenantID), entry.SQL, nullStr(entry.StatementHash), nullStr(entry.TableName), entry.DurationMs, entry.StatementAt, entry.CreatedAt)
	if err != nil {
		return fmt.Errorf("persist data plane audit: %w", err)
	}
	return nil
}

func nullUUID(v uuid.UUID) any {
	if v == uuid.Nil {
		return nil
	}
	return v
}

func nullStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}
