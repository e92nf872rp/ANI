// Package router contains the Core data-plane Gateway handlers for the
// SQL-over-HTTP surface (SPEC design-kb-persistence-to-core-datapipe §3.2/§3.3).
//
// /data/query  -> SQLDataPlane.QueryTx  (parameterized SQL, single transaction)
// /data/tables -> SQLDataPlane.CreateTable (managed DDL, platform admin only)
//
// Security hardening (SPEC §3.3) enforced here at the handler boundary:
//   - target table allow-list check (knowledge_bases/kb_documents/...)
//   - destructive statement rejection (DROP/TRUNCATE/ALTER SYSTEM/COPY/pg_read_file)
//   - SQL length / statement count / params count limits
//   - service-identity-only access: scope must be "platform" or "service";
//     tenant end-users may never call this surface directly
//   - tenant context is taken from middleware (X-Tenant-Id via auth), never
//     trusted from the request body
//   - full audit (service/tenant/table/statement hash/duration) is recorded by
//     the adapter; the handler only forwards the caller identity
package router

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	apierrors "github.com/kubercloud/ani/services/ani-gateway/internal/pkg/errors"
)

// dataPlaneMaxSQLLength caps the SQL string length to keep queries bounded
// (SPEC §3.3-5: SQL length upper limit). Mirrors the OpenAPI `maxLength: 16384`.
const dataPlaneMaxSQLLength = 16 * 1024

// dataPlaneMaxParams caps the number of bound parameters per request
// (SPEC §3.3-5). Mirrors the OpenAPI `maxItems: 100`.
const dataPlaneMaxParams = 100

// dataPlaneMaxStatements caps the number of semicolon-separated statements
// inside a single /data/query call, preventing a single request from turning
// into an unbounded batch (SPEC §3.3-5).
const dataPlaneMaxStatements = 16

// dataPlaneQueryTimeout bounds the execution time of a single data-plane
// request (SPEC §3.3-5). The context deadline propagates to the adapter and
// the underlying pgx transaction.
const dataPlaneQueryTimeout = 30 * time.Second

// dataPlaneRateLimitRequests / dataPlaneRateLimitWindow bound the per-service-
// identity request rate for the data plane (SPEC §3.3-5). Each distinct
// service identity gets its own counter so one service cannot starve another.
const (
	dataPlaneRateLimitRequests = 200
	dataPlaneRateLimitWindow   = time.Second
)

// dataPlaneAllowedTables is the white-list of Services business tables that
// the data plane may touch (SPEC §3.3-2). Anything else is rejected with 403.
var dataPlaneAllowedTables = map[string]struct{}{
	"knowledge_bases": {},
	"kb_documents":    {},
	"kb_chunks":       {},
	"kb_messages":     {},
	"kb_sessions":     {},
	"async_tasks":     {},
	"outbox_events":   {},
}

// dataPlaneAllowedCreateTableNames restricts /data/tables to the same set so
// the managed-migration endpoint cannot create arbitrary tables.
var dataPlaneAllowedCreateTableNames = dataPlaneAllowedTables

// destructiveStatementRe matches SQL statements that are forbidden by the
// data plane (SPEC §3.3-4): DROP, TRUNCATE, ALTER SYSTEM, ALTER TABLE ...
// DROP COLUMN, COPY ... TO external targets, CREATE EXTENSION (untrusted
// code loading), GRANT/REVOKE (privilege changes), and DO $$ (anonymous code
// blocks that can embed arbitrary PL/pgSQL). The match is case-insensitive
// and anchored to statement starts (;, whitespace, or string start) so we
// do not reject these words when they appear as column names or inside string
// literals of a well-formed parameterized statement.
var destructiveStatementRe = regexp.MustCompile(
	`(?im)(?:^|;)\s*(?:DROP\s+|TRUNCATE\s+|ALTER\s+SYSTEM\s+|ALTER\s+TABLE\s+[^;]*\bDROP\b|COPY\s+[^;]+\s+TO\s+(?:PROGRAM|STDOUT|FILE)|CREATE\s+EXTENSION|GRANT\s+|REVOKE\s+|DO\s+\$\$)`,
)

// destructiveFunctionRe matches PostgreSQL superuser file/program helpers that
// are forbidden by the data plane (SPEC §3.3-4). Unlike destructiveStatementRe
// these are function calls and may appear anywhere inside a SELECT list, so
// they are matched as word-boundary tokens rather than statement-anchored.
var destructiveFunctionRe = regexp.MustCompile(
	`(?im)\b(?:pg_read_file|pg_read_binary_file|pg_ls_dir)\s*\(`,
)

// tableTokenRe extracts candidate SQL identifiers from a statement so the
// handler can verify the statement touches only allow-listed tables. It is
// intentionally conservative: it matches identifiers following FROM/JOIN/INTO/
// UPDATE/TABLE keywords and lets the allow-list check reject anything that
// looks like an unregistered table.
var tableTokenRe = regexp.MustCompile(
	`(?im)\b(?:FROM|JOIN|INTO|UPDATE|TABLE)\s+([A-Za-z_][A-Za-z0-9_]*)`,
)

type dataPlaneAPI struct {
	plane ports.SQLDataPlane
	// store is the shared gateway cache store used for per-service-identity
	// rate limiting (SPEC §3.3-5). When nil, rate limiting is skipped (the
	// handler still enforces SQL/params/timeout limits).
	store ports.CacheStore
}

// DataPlaneQueryRequest mirrors the OpenAPI DataQueryRequest schema.
// The tenant context is NEVER taken from here; it is derived from middleware.
type DataPlaneQueryRequest struct {
	SQL    string `json:"sql"`
	Params []any  `json:"params"`
	Role   string `json:"role"`
}

// DataPlaneQueryResponse mirrors the OpenAPI DataQueryResponse schema.
// Columns carries the ordered column names of the last result set so clients
// can consume column order deterministically (Go map keys are alphabetically
// sorted by encoding/json, not insertion-ordered).
type DataPlaneQueryResponse struct {
	Columns    []string         `json:"columns,omitempty"`
	Rows       []map[string]any `json:"rows"`
	RowCount   int64            `json:"rowcount"`
	LastResult bool             `json:"last_result"`
}

// DataPlaneCreateTableRequest mirrors the OpenAPI DataTableCreateRequest schema.
type DataPlaneCreateTableRequest struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

// DataPlaneCreateTableResponse mirrors the OpenAPI response for /data/tables.
type DataPlaneCreateTableResponse struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func registerDataPlaneResources(v1 *route.RouterGroup, plane ports.SQLDataPlane, store ports.CacheStore) {
	api := &dataPlaneAPI{plane: plane, store: store}
	// /data/query and /data/tables live under /api/v1 and are only callable by
	// service identities (SPEC §3.3-7). Enforcement is done inside the handler
	// (scope/role check) because the gateway middleware chain is tenant-facing.
	v1.POST("/data/query", api.dataQuery)
	v1.POST("/data/tables", api.dataCreateTable)
}

// dataQuery implements POST /api/v1/data/query.
//
// Security checks are performed BEFORE the adapter is called:
//  1. service-identity-only: scope must be "platform" or "service" (never
//     tenant end-user).
//  2. role validation (tenant|service) — role=service additionally requires
//     platform scope.
//  3. SQL / params / statement-count limits (SPEC §3.3-5).
//  4. destructive-statement rejection (SPEC §3.3-4) -> 422.
//  5. target-table white-list (SPEC §3.3-2) -> 403 when missing.
//  6. tenant id is taken from middleware (X-Tenant-Id via auth), never the
//     request body (SPEC §3.2).
//
// The adapter then runs the parameterized SQL inside one transaction with RLS
// applied for role=tenant and records the audit row (SPEC §3.3-6).
func (api *dataPlaneAPI) dataQuery(ctx context.Context, c *app.RequestContext) {
	if api == nil || api.plane == nil {
		writeDataPlaneError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "data plane not configured")
		return
	}

	scope := middleware.GetScope(c)
	if !dataPlaneScopeAllowed(scope) {
		writeDataPlaneError(c, http.StatusForbidden, "FORBIDDEN", "data plane is only available to service identities")
		return
	}

	// Per-service-identity rate limiting (SPEC §3.3-5). Each service identity
	// gets its own counter so one service cannot starve another.
	if !api.checkDataPlaneRateLimit(ctx, scope) {
		writeDataPlaneError(c, http.StatusTooManyRequests, "RATE_LIMITED", "data plane rate limit exceeded for this service identity")
		return
	}

	var req DataPlaneQueryRequest
	if err := c.BindJSON(&req); err != nil {
		writeDataPlaneError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid data query request")
		return
	}

	role, err := parseDataPlaneRole(req.Role)
	if err != nil {
		writeDataPlaneError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	// role=service is cross-tenant and reserved for platform-managed service
	// identities (the outbox dispatcher, etc.). A plain tenant token must not
	// be able to escalate to cross-tenant access.
	if role == ports.DataPlaneRoleService && scope != "platform" {
		writeDataPlaneError(c, http.StatusForbidden, "FORBIDDEN", "service role requires platform service identity")
		return
	}

	sql := strings.TrimSpace(req.SQL)
	if sql == "" {
		writeDataPlaneError(c, http.StatusBadRequest, "BAD_REQUEST", "sql is required")
		return
	}
	if len(sql) > dataPlaneMaxSQLLength {
		writeDataPlaneError(c, http.StatusBadRequest, "BAD_REQUEST", "sql exceeds maximum length")
		return
	}
	if len(req.Params) > dataPlaneMaxParams {
		writeDataPlaneError(c, http.StatusBadRequest, "BAD_REQUEST", "params exceed maximum count")
		return
	}
	if countStatements(sql) > dataPlaneMaxStatements {
		writeDataPlaneError(c, http.StatusBadRequest, "BAD_REQUEST", "statement count exceeds maximum")
		return
	}

	if isDataPlaneDestructive(sql) {
		writeDataPlaneError(c, http.StatusUnprocessableEntity, "UNSUPPORTED_QUERY", "destructive statements are not allowed")
		return
	}

	if err := validateDataPlaneTables(sql); err != nil {
		writeDataPlaneError(c, http.StatusForbidden, "FORBIDDEN", err.Error())
		return
	}

	// Tenant context comes from middleware (X-Tenant-Id via auth), never the
	// request body (SPEC §3.2).
	var tenantID uuid.UUID
	if role == ports.DataPlaneRoleTenant {
		tenantID, err = dataPlaneTenantIDFromContext(c)
		if err != nil {
			writeDataPlaneError(c, http.StatusForbidden, "FORBIDDEN", err.Error())
			return
		}
	}

	queryCtx, cancel := context.WithTimeout(ctx, dataPlaneQueryTimeout)
	defer cancel()

	result, err := api.plane.QueryTx(queryCtx, ports.DataPlaneQueryRequest{
		SQL:             sql,
		Params:          req.Params,
		Role:            role,
		TenantID:        tenantID,
		ServiceIdentity: scope,
	})
	if err != nil {
		writeDataPlaneQueryError(c, err)
		return
	}

	c.JSON(http.StatusOK, DataPlaneQueryResponse{
		Columns:    result.Columns,
		Rows:       result.Rows,
		RowCount:   result.RowCount,
		LastResult: result.LastResult,
	})
}

// dataCreateTable implements POST /api/v1/data/tables.
//
// Only platform admins may submit managed DDL (SPEC §3.3-3). The handler
// rejects destructive statements and requires the target table name to be in
// the managed white-list (SPEC §3.3-2). The adapter executes the DDL and
// records an audit entry (SPEC §3.3-6).
func (api *dataPlaneAPI) dataCreateTable(ctx context.Context, c *app.RequestContext) {
	if api == nil || api.plane == nil {
		writeDataPlaneError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "data plane not configured")
		return
	}

	scope := middleware.GetScope(c)
	if scope != "platform" {
		writeDataPlaneError(c, http.StatusForbidden, "FORBIDDEN", "managed table creation requires platform admin")
		return
	}

	if !api.checkDataPlaneRateLimit(ctx, scope) {
		writeDataPlaneError(c, http.StatusTooManyRequests, "RATE_LIMITED", "data plane rate limit exceeded for this service identity")
		return
	}

	var req DataPlaneCreateTableRequest
	if err := c.BindJSON(&req); err != nil {
		writeDataPlaneError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid data table request")
		return
	}
	name := strings.TrimSpace(req.Name)
	definition := strings.TrimSpace(req.Definition)
	if name == "" || definition == "" {
		writeDataPlaneError(c, http.StatusBadRequest, "BAD_REQUEST", "name and definition are required")
		return
	}
	if _, ok := dataPlaneAllowedCreateTableNames[name]; !ok {
		writeDataPlaneError(c, http.StatusForbidden, "FORBIDDEN", "target table is not registered for managed migration")
		return
	}
	if isDataPlaneDestructive(definition) {
		writeDataPlaneError(c, http.StatusUnprocessableEntity, "UNSUPPORTED_QUERY", "destructive DDL is not allowed")
		return
	}

	queryCtx, cancel := context.WithTimeout(ctx, dataPlaneQueryTimeout)
	defer cancel()

	if err := api.plane.CreateTable(queryCtx, ports.DataPlaneCreateTableRequest{
		Name:            name,
		Definition:      definition,
		ServiceIdentity: scope,
	}); err != nil {
		writeDataPlaneQueryError(c, err)
		return
	}

	c.JSON(http.StatusCreated, DataPlaneCreateTableResponse{Name: name, Status: "applied"})
}

// dataPlaneScopeAllowed returns true when the authenticated caller is a service
// identity (SPEC §3.3-7). Platform admins and platform-managed service tokens
// carry scope=platform; the data plane is never reachable by tenant end-users.
func dataPlaneScopeAllowed(scope string) bool {
	return scope == "platform" || scope == "service"
}

func parseDataPlaneRole(raw string) (ports.DataPlaneRole, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "tenant":
		return ports.DataPlaneRoleTenant, nil
	case "service":
		return ports.DataPlaneRoleService, nil
	default:
		return "", errors.New("role must be tenant or service")
	}
}

// checkDataPlaneRateLimit enforces per-service-identity request limiting for
// the data plane (SPEC §3.3-5). When no store is configured the check is
// skipped (rate limiting is best-effort and must not block the handler when
// the cache is unavailable). The key is scoped to the service identity so
// one service cannot consume another's budget.
func (api *dataPlaneAPI) checkDataPlaneRateLimit(ctx context.Context, serviceIdentity string) bool {
	if api == nil || api.store == nil {
		return true
	}
	count, err := api.store.Increment(ctx, "dp:ratelimit:"+serviceIdentity, dataPlaneRateLimitWindow)
	if err != nil {
		// Store unavailable: fail open (the handler still enforces
		// SQL/params/timeout limits).
		return true
	}
	return count <= dataPlaneRateLimitRequests
}

// dataPlaneTenantIDFromContext reads the tenant id set by the Auth middleware
// (derived from the JWT/X-Tenant-Id, never the client body) and validates it
// is a real UUID. This is the RLS tenant identity for role=tenant.
func dataPlaneTenantIDFromContext(c *app.RequestContext) (uuid.UUID, error) {
	raw := strings.TrimSpace(middleware.GetTenantID(c))
	if raw == "" {
		return uuid.Nil, errors.New("tenant context is required for role=tenant")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errors.New("tenant context is not a valid uuid")
	}
	if id == uuid.Nil {
		return uuid.Nil, errors.New("tenant context is required for role=tenant")
	}
	return id, nil
}

// isDataPlaneDestructive reports whether the given SQL text contains a
// destructive statement (DROP/TRUNCATE/ALTER SYSTEM/COPY TO external) or a
// superuser file/program function call (pg_read_file/...) that the data plane
// must reject (SPEC §3.3-4).
func isDataPlaneDestructive(sql string) bool {
	return destructiveStatementRe.MatchString(sql) || destructiveFunctionRe.MatchString(sql)
}

// validateDataPlaneTables scans the SQL for table identifiers following
// FROM/JOIN/INTO/UPDATE/TABLE keywords and asserts every referenced table is
// in the data-plane white-list (SPEC §3.3-2). It deliberately over-matches:
// any unregistered identifier is rejected so the handler fails closed.
//
// CTE names introduced by `WITH name AS (...)` are extracted first and
// excluded from the allow-list check, so CTE-based folds (issue-030) that
// reference CTE names like `FROM sess` or `FROM effective_session` are not
// rejected as unregistered tables.
func validateDataPlaneTables(sql string) error {
	// Extract CTE names from WITH clauses: `WITH name AS (` or `, name AS (`.
	// These are CTE aliases, not physical tables, so they are exempt from the
	// allow-list check.
	cteNames := make(map[string]struct{})
	cteRe := regexp.MustCompile(`(?im)(?:^|\bWITH\b|,)\s*([A-Za-z_][A-Za-z0-9_]*)\s+AS\s*\(`)
	for _, m := range cteRe.FindAllStringSubmatch(sql, -1) {
		cteNames[strings.ToLower(strings.TrimSpace(m[1]))] = struct{}{}
	}

	matches := tableTokenRe.FindAllStringSubmatch(sql, -1)
	if len(matches) == 0 {
		// No table reference detected (e.g. `SELECT 1`). Allow for health-style
		// probes; business SQL will always reference a registered table.
		return nil
	}
	for _, m := range matches {
		table := strings.ToLower(strings.TrimSpace(m[1]))
		if table == "" {
			continue
		}
		// Skip CTE names — they are query-local aliases, not physical tables.
		if _, isCTE := cteNames[table]; isCTE {
			continue
		}
		if _, ok := dataPlaneAllowedTables[table]; !ok {
			return errors.New("target table is not registered: " + table)
		}
	}
	return nil
}

// countStatements returns the number of ;-separated statements in the SQL.
// Uses strings.Count (runtime byte-level scan) instead of a rune loop for
// speed; the input is already capped at dataPlaneMaxSQLLength.
func countStatements(sql string) int {
	count := strings.Count(sql, ";")
	// A trailing semicolon does not imply an extra statement.
	if count > 0 && strings.HasSuffix(strings.TrimSpace(sql), ";") {
		return count
	}
	return count + 1
}

func writeDataPlaneError(c *app.RequestContext, status int, code, message string) {
	c.JSON(status, apierrors.APIError{
		Code:      code,
		Message:   message,
		RequestID: middleware.GetRequestID(c),
	})
}

// writeDataPlaneQueryError maps ports error taxonomy to stable HTTP semantics
// for the data-plane surface (SPEC §3.1 x-errors).
func writeDataPlaneQueryError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrPayloadTooLarge):
		writeDataPlaneError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	case errors.Is(err, ports.ErrNotFound):
		writeDataPlaneError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeDataPlaneError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrFailedPrecondition):
		writeDataPlaneError(c, http.StatusUnprocessableEntity, "UNSUPPORTED_QUERY", err.Error())
	case errors.Is(err, ports.ErrInvalidCredentials):
		writeDataPlaneError(c, http.StatusForbidden, "FORBIDDEN", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeDataPlaneError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeDataPlaneError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
