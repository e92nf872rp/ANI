package ports

import (
	"context"

	"github.com/google/uuid"
)

// DataPlaneRole controls the tenant-context applied to a data-plane query.
// The value mirrors the OpenAPI `role` enum (SPEC §3.1):
//   - tenant:  RLS enforced via SET LOCAL app.current_tenant_id (derived from
//     X-Tenant-Id by the handler/middleware, never trusted from the client body).
//   - service: cross-tenant execution for platform-managed service identities
//     (e.g. the outbox dispatcher, formerly the BYPASSRLS `ani_outbox_publisher`
//     semantics). Requires independent audit (SPEC §3.2).
type DataPlaneRole string

const (
	DataPlaneRoleTenant  DataPlaneRole = "tenant"
	DataPlaneRoleService DataPlaneRole = "service"
)

// DataPlaneQueryRequest is a single parameterized data-plane read/write call.
// A call maps to exactly one PostgreSQL transaction (BEGIN/COMMIT/ROLLBACK).
// The `sql` string may contain one or more statements; when multiple statements
// are present they must be executed together in the same transaction. Because
// the PostgreSQL extended query protocol only binds parameters to a single
// statement, parameter binding (`Params`) requires a single-statement `sql`.
type DataPlaneQueryRequest struct {
	// SQL is the parameterized statement(s) to run inside one transaction.
	SQL string
	// Params binds $1..$n. Must be nil/empty when `SQL` contains multiple
	// statements (simple protocol), otherwise pgx rejects the call.
	Params []any
	// Role selects the tenant-context to apply.
	Role DataPlaneRole
	// TenantID is derived by the handler from X-Tenant-Id. It is only applied to
	// RLS when Role == DataPlaneRoleTenant; ignored for DataPlaneRoleService.
	TenantID uuid.UUID
	// ServiceIdentity is the authenticated caller identity (e.g. "kb-service",
	// "platform") derived by the handler from middleware scope. It is recorded
	// in the audit row so every data-plane call is attributable to a specific
	// service identity (SPEC §3.3-6).
	ServiceIdentity string
}

// DataPlaneQueryResult holds the outcome of a DataPlaneQueryRequest.
// Columns is the ordered list of column names of the last result set, so
// callers preserving column order are not subject to map iteration order.
// Rows is the last result set produced by the call; RowCount sums affected
// rows across the transaction; LastResult mirrors the committed/rolled-back
// state of the transaction (true iff the transaction committed successfully).
type DataPlaneQueryResult struct {
	Columns    []string
	Rows       []map[string]any
	RowCount   int64
	LastResult bool
}

// DataPlaneCreateTableRequest is a managed CreateTable request.
// Only managed DDL (create/alter) supplied by the migration orchestration is
// accepted; it must be validated upstream (white-list check) before reaching
// the adapter. Destructive statements are rejected at the handler boundary.
type DataPlaneCreateTableRequest struct {
	Name       string
	Definition string
	// ServiceIdentity is the authenticated caller identity recorded in the
	// audit row (SPEC §3.3-6).
	ServiceIdentity string
}

// SQLDataPlane is the Core-owned generic data-plane capability abstraction
// (SPEC design-kb-persistence-to-core-datapipe §3.2). It lets Services
// business code read/write managed tables through Core instead of directly
// touching PostgreSQL, preserving CLAUDE.md §3's cross-layer boundary while
// keeping schema ownership and RLS under Core control.
type SQLDataPlane interface {
	// QueryTx executes parameterized SQL inside a single transaction, applying
	// RLS when Role == tenant and auditing every call.
	QueryTx(ctx context.Context, req DataPlaneQueryRequest) (DataPlaneQueryResult, error)
	// CreateTable executes managed DDL for a managed services table and records
	// an audit entry (migration orchestration).
	CreateTable(ctx context.Context, req DataPlaneCreateTableRequest) error
}
