package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/pkg/ports"
)

// defaultDataPlaneMaxRows caps how many result rows QueryTx materializes, so a
// single runaway SELECT cannot exhaust Core DB/worker memory (SPEC §3.3-5).
const defaultDataPlaneMaxRows = 10000

// SQLDataPlane is the PostgreSQL adapter for ports.SQLDataPlane (SPEC §3.2).
//
// It reuses the Core PostgreSQL connection pool (`pkg/bootstrap` DB) and
// implements the generic data-plane capability: each QueryTx runs one or more
// statements inside a single BEGIN/COMMIT/ROLLBACK transaction, applying RLS
// for role=tenant and leaving cross-tenant access for role=service.
type SQLDataPlane struct {
	pool    *pgxpool.Pool
	now     func() time.Time
	maxRows int
	logger  *slog.Logger
}

var _ ports.SQLDataPlane = (*SQLDataPlane)(nil)

// DataPlaneOption configures a SQLDataPlane adapter.
type DataPlaneOption func(*SQLDataPlane)

// WithDataPlaneMaxRows overrides the materialized row limit (SPEC §3.3-5).
func WithDataPlaneMaxRows(n int) DataPlaneOption {
	return func(d *SQLDataPlane) {
		if n > 0 {
			d.maxRows = n
		}
	}
}

// WithDataPlaneClock overrides the clock used for audit timestamps (tests).
func WithDataPlaneClock(now func() time.Time) DataPlaneOption {
	return func(d *SQLDataPlane) {
		if now != nil {
			d.now = now
		}
	}
}

// WithDataPlaneLogger injects a structured logger; the logger should already
// carry request-level context (trace id, etc.) via slog.With.
func WithDataPlaneLogger(l *slog.Logger) DataPlaneOption {
	return func(d *SQLDataPlane) {
		if l != nil {
			d.logger = l
		}
	}
}

// NewSQLDataPlane creates a data-plane adapter backed by the Core PG pool.
func NewSQLDataPlane(pool *pgxpool.Pool, opts ...DataPlaneOption) *SQLDataPlane {
	d := &SQLDataPlane{
		pool:    pool,
		now:     time.Now,
		maxRows: defaultDataPlaneMaxRows,
		logger:  slog.Default(),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// QueryTx runs the parameterized SQL inside a single transaction.
func (s *SQLDataPlane) QueryTx(ctx context.Context, req ports.DataPlaneQueryRequest) (ports.DataPlaneQueryResult, error) {
	start := s.now()

	if err := validateQueryRequest(req); err != nil {
		return ports.DataPlaneQueryResult{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ports.DataPlaneQueryResult{}, fmt.Errorf("data plane begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// RLS context: role=tenant sets app.current_tenant_id scoped to this tx.
	// role=service intentionally does not set it (cross-tenant, BYPASSRLS-style).
	if req.Role == ports.DataPlaneRoleTenant {
		if err := setDataPlaneRLS(ctx, tx, req.TenantID); err != nil {
			return ports.DataPlaneQueryResult{}, err
		}
	}

	result, err := runDataPlaneQuery(ctx, tx, req.SQL, req.Params, s.maxRows)
	if err != nil {
		return ports.DataPlaneQueryResult{}, fmt.Errorf("%w: %v", dataPlaneQueryError(err), err)
	}

	// role=tenant: audit atomically inside the SAME transaction (savepoint-wrapped
	// so an audit write failure never rolls back the business data). This avoids
	// an extra connection round-trip and keeps the audit row RLS-scoped to the
	// tenant. role=service audit stays independent (below) per SPEC §3.2.
	if req.Role == ports.DataPlaneRoleTenant {
		s.auditInTx(ctx, tx, dataPlaneAuditEntry{
			Role:            string(req.Role),
			ServiceIdentity: req.ServiceIdentity,
			TenantID:        req.TenantID,
			SQL:             req.SQL,
			StatementHash:   hashDataPlaneSQL(req.SQL),
			DurationMs:      s.now().Sub(start).Milliseconds(),
			StatementAt:     start,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return ports.DataPlaneQueryResult{}, fmt.Errorf("data plane commit: %w", err)
	}
	result.LastResult = true

	// role=service: independent cross-tenant audit (SPEC §3.2). Written on a
	// separate connection AFTER commit so it is never RLS-scoped and never
	// rolled back with the business transaction.
	if req.Role == ports.DataPlaneRoleService {
		s.audit(ctx, dataPlaneAuditEntry{
			Role:            string(req.Role),
			ServiceIdentity: req.ServiceIdentity,
			TenantID:        req.TenantID,
			SQL:             req.SQL,
			StatementHash:   hashDataPlaneSQL(req.SQL),
			DurationMs:      s.now().Sub(start).Milliseconds(),
			StatementAt:     start,
		})
	}

	return result, nil
}

// CreateTable executes managed DDL and records an audit entry.
func (s *SQLDataPlane) CreateTable(ctx context.Context, req ports.DataPlaneCreateTableRequest) error {
	start := s.now()

	if strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("%w: managed table name is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(req.Definition) == "" {
		return fmt.Errorf("%w: managed DDL definition is required", ports.ErrInvalid)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("data plane create table begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Managed DDL runs as a platform operation (no RLS tenant context).
	if _, err := tx.Exec(ctx, req.Definition); err != nil {
		return fmt.Errorf("%w: managed DDL rejected: %v", ports.ErrInvalid, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("data plane create table commit: %w", err)
	}

	s.audit(ctx, dataPlaneAuditEntry{
		Role:            "platform_admin",
		ServiceIdentity: req.ServiceIdentity,
		SQL:             req.Definition,
		StatementHash:   hashDataPlaneSQL(req.Definition),
		TableName:       req.Name,
		DurationMs:      s.now().Sub(start).Milliseconds(),
		StatementAt:     start,
	})

	return nil
}

// audit records the data-plane operation. Audit persistence is best-effort so
// a failure to write the audit row never fails the business query; this keeps
// the adapter decoupled from migration/timing of the audit table creation.
func (s *SQLDataPlane) audit(ctx context.Context, entry dataPlaneAuditEntry) {
	entry.ID = uuid.NewString()
	entry.CreatedAt = s.now()
	if err := persistDataPlaneAudit(ctx, s.pool, entry); err != nil {
		s.logger.Error("data plane audit write failed", "err", err, "role", entry.Role)
	}
}

// auditInTx writes the audit row inside the given transaction using a savepoint.
// If the audit write fails (e.g. the audit table is not yet migrated), the
// savepoint is rolled back and the business transaction stays viable. Success
// commits the audit atomically with the business data on the same connection.
func (s *SQLDataPlane) auditInTx(ctx context.Context, tx pgx.Tx, entry dataPlaneAuditEntry) {
	entry.ID = uuid.NewString()
	entry.CreatedAt = s.now()
	if _, err := tx.Exec(ctx, "SAVEPOINT data_plane_audit"); err != nil {
		s.logger.Error("data plane audit savepoint failed", "err", err, "role", entry.Role)
		return
	}
	if err := persistDataPlaneAudit(ctx, tx, entry); err != nil {
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT data_plane_audit")
		_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT data_plane_audit")
		s.logger.Error("data plane audit write failed", "err", err, "role", entry.Role)
		return
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT data_plane_audit"); err != nil {
		s.logger.Error("data plane audit release savepoint failed", "err", err, "role", entry.Role)
	}
}

func validateQueryRequest(req ports.DataPlaneQueryRequest) error {
	if strings.TrimSpace(req.SQL) == "" {
		return fmt.Errorf("%w: sql is required", ports.ErrInvalid)
	}
	switch req.Role {
	case ports.DataPlaneRoleTenant:
		if req.TenantID == uuid.Nil {
			return fmt.Errorf("%w: tenant role requires a valid tenant id", ports.ErrInvalid)
		}
	case ports.DataPlaneRoleService:
		// no tenant id required (cross-tenant)
	default:
		return fmt.Errorf("%w: unsupported data plane role %q", ports.ErrInvalid, req.Role)
	}
	return nil
}

// setDataPlaneRLS sets app.current_tenant_id scoped to the current transaction.
// It uses set_config(..., true) so the setting is LOCAL to the transaction and
// cannot leak across pooled connections (same semantics as types.SetDBTenant,
// but the tenant id is passed explicitly rather than read from context).
func setDataPlaneRLS(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return fmt.Errorf("data plane set tenant rls: %w", err)
	}
	return nil
}

// runDataPlaneQuery executes the SQL inside the active transaction and returns
// the last result set plus the total affected rows across the transaction.
//
// Two execution paths are used:
//   - params present -> extended query protocol (single statement, typed rows).
//     pgx rejects multiple commands with arguments, matching the documented
//     port contract that binding applies to a single statement.
//   - no params -> simple query protocol via the underlying PgConn, which runs
//     every statement in the batch inside the SAME open transaction and lets us
//     sum the affected rows of each statement (multi-statement DML folding).
//
// Returns a *row limit* error when the materialized result exceeds maxRows.
func runDataPlaneQuery(ctx context.Context, tx pgx.Tx, sql string, params []any, maxRows int) (ports.DataPlaneQueryResult, error) {
	if maxRows <= 0 {
		maxRows = defaultDataPlaneMaxRows
	}

	if len(params) > 0 {
		// Single-statement, parameterized: extended protocol via Tx.Query.
		rows, err := tx.Query(ctx, sql, params...)
		if err != nil {
			return ports.DataPlaneQueryResult{}, err
		}
		defer rows.Close()
		result, err := collectRows(rows, maxRows)
		if err != nil {
			return ports.DataPlaneQueryResult{}, err
		}
		result.RowCount = rows.CommandTag().RowsAffected()
		return result, nil
	}

	// No params: simple protocol runs the whole multi-statement batch inside
	// this same transaction, so every statement shares the BEGIN/COMMIT/ROLLBACK.
	pgConn := tx.Conn().PgConn()
	typeMap := tx.Conn().TypeMap()

	mrr := pgConn.Exec(ctx, sql)
	defer func() { _ = mrr.Close() }()

	result := ports.DataPlaneQueryResult{Rows: []map[string]any{}}
	var totalRows int64
	for mrr.NextResult() {
		rr := mrr.ResultReader()
		var affected int64
		if len(rr.FieldDescriptions()) > 0 {
			// Capture the last statement that returns rows (typed decoding via
			// the connection type map) as the result set.
			rows := pgx.RowsFromResultReader(typeMap, rr)
			collected, err := collectRows(rows, maxRows)
			if err != nil {
				return ports.DataPlaneQueryResult{}, err
			}
			result.Rows = collected.Rows
			result.Columns = collected.Columns
			affected = rows.CommandTag().RowsAffected()
		} else {
			// DML / DDL statement: finalize to read its command tag.
			tag, err := rr.Close()
			if err != nil {
				return ports.DataPlaneQueryResult{}, err
			}
			affected = tag.RowsAffected()
		}
		totalRows += affected
	}
	if err := mrr.Close(); err != nil {
		return ports.DataPlaneQueryResult{}, err
	}
	result.RowCount = totalRows
	return result, nil
}

// collectRows materializes a pgx.Rows into []map[string]any with values
// normalized to JSON-safe scalar types, enforcing the row limit. It precomputes
// a per-column normalizer from the field descriptions' DataTypeOID so the hot
// per-cell path is a direct function call instead of a type switch.
func collectRows(rows pgx.Rows, maxRows int) (ports.DataPlaneQueryResult, error) {
	fields := rows.FieldDescriptions()
	columnNames := make([]string, len(fields))
	normalizers := make([]func(any) any, len(fields))
	for i, f := range fields {
		columnNames[i] = string(f.Name)
		normalizers[i] = pickColumnNormalizer(f.DataTypeOID)
	}

	result := ports.DataPlaneQueryResult{Columns: columnNames, Rows: []map[string]any{}}
	for rows.Next() {
		if len(result.Rows) >= maxRows {
			rows.Close()
			return ports.DataPlaneQueryResult{}, fmt.Errorf("%w: result set exceeds %d rows", ports.ErrPayloadTooLarge, maxRows)
		}
		values, err := rows.Values()
		if err != nil {
			return ports.DataPlaneQueryResult{}, err
		}
		row := make(map[string]any, len(columnNames))
		for i, name := range columnNames {
			row[name] = normalizers[i](values[i])
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return ports.DataPlaneQueryResult{}, err
	}
	return result, nil
}

// pickColumnNormalizer selects a JSON-safe normalizer for a column based on its
// PostgreSQL DataTypeOID. Built-in OIDs are stable, so specialized fast paths
// avoid the generic type switch in the hot per-cell path; unknown OIDs (e.g.
// custom domain/enum types) fall back to the generic normalizer.
func pickColumnNormalizer(oid uint32) func(any) any {
	switch oid {
	case 16: // bool
		return normalizeBool
	case 17: // bytea
		return normalizeBytes
	case 18, 19, 25, 1042, 1043: // char, name, text, bpchar, varchar
		return normalizeString
	case 20, 21, 23: // int8, int2, int4
		return normalizeInt
	case 700, 701: // float4, float8
		return normalizeFloat
	case 1082, 1083, 1114, 1184, 1266: // date, time, timestamp, timestamptz, timetz
		return normalizeTime
	case 2950: // uuid
		return normalizeUUID
	case 1700: // numeric
		return normalizeJSONValue // pgtype.Numeric -> float64/string
	default:
		return normalizeJSONValue
	}
}

func normalizeBool(v any) any   { return v }
func normalizeString(v any) any { return v }
func normalizeInt(v any) any    { return v }
func normalizeFloat(v any) any  { return v }

func normalizeBytes(v any) any {
	if b, ok := v.([]byte); ok {
		return base64.StdEncoding.EncodeToString(b)
	}
	return normalizeJSONValue(v)
}

func normalizeTime(v any) any {
	if t, ok := v.(time.Time); ok {
		return t.Format(time.RFC3339Nano)
	}
	return normalizeJSONValue(v)
}

func normalizeUUID(v any) any {
	switch u := v.(type) {
	case uuid.UUID:
		return u.String()
	case [16]byte:
		return uuid.UUID(u).String()
	case nil:
		return nil
	default:
		return normalizeJSONValue(v)
	}
}

// normalizeJSONValue converts pgx-decoded values into JSON-serializable scalar
// values so the handler can return stable JSON without leaking wire types
// ([]byte -> base64, time.Time -> RFC3339, UUID -> string, numeric -> string).
func normalizeJSONValue(v any) any {
	switch val := v.(type) {
	case nil, bool, string, int, int64, int32, float64, float32:
		return val
	case []byte:
		// Preserve binary/bytea fidelity losslessly.
		return base64.StdEncoding.EncodeToString(val)
	case time.Time:
		return val.Format(time.RFC3339Nano)
	case uuid.UUID:
		return val.String()
	case [16]byte:
		// pgtype.UUID decodes to [16]byte; render as canonical UUID string.
		return uuid.UUID(val).String()
	case pgtype.Numeric:
		if f, err := val.Float64Value(); err == nil && f.Valid {
			return f.Float64
		}
		if s, err := val.Value(); err == nil && s != nil {
			return s
		}
		return nil
	case pgtype.UUID:
		if val.Valid {
			return uuid.UUID(val.Bytes).String()
		}
		return nil
	default:
		return val
	}
}

// dataPlaneQueryError remaps pgx errors to the ports error taxonomy so the
// handler can return stable HTTP semantics.
func dataPlaneQueryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return ports.ErrConflict
		case "42501": // insufficient_privilege
			return ports.ErrInvalidCredentials // forbidden, mapped by handler
		case "23503": // foreign_key_violation
			return ports.ErrFailedPrecondition
		}
	}
	return ports.ErrInvalid
}
