package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

// fakeDataPlane is an in-memory ports.SQLDataPlane for handler tests. It
// records the last call so tests can assert tenant-context propagation and
// parameter binding, and returns canned results/errors per-test.
type fakeDataPlane struct {
	lastQuery   ports.DataPlaneQueryRequest
	lastCreate  ports.DataPlaneCreateTableRequest
	queryResult ports.DataPlaneQueryResult
	queryErr    error
	createErr   error
}

func (f *fakeDataPlane) QueryTx(_ context.Context, req ports.DataPlaneQueryRequest) (ports.DataPlaneQueryResult, error) {
	f.lastQuery = req
	if f.queryErr != nil {
		return ports.DataPlaneQueryResult{}, f.queryErr
	}
	if f.queryResult.Rows == nil {
		f.queryResult.Rows = []map[string]any{}
	}
	return f.queryResult, nil
}

func (f *fakeDataPlane) CreateTable(_ context.Context, req ports.DataPlaneCreateTableRequest) error {
	f.lastCreate = req
	return f.createErr
}

// newDataPlaneEngine builds a Hertz engine with the data-plane routes and a
// middleware that seeds the request context with the given scope/tenant id,
// mimicking the Auth middleware for service identities.
func newDataPlaneEngine(t *testing.T, scope, tenantID string, plane ports.SQLDataPlane) *server.Hertz {
	t.Helper()
	return newDataPlaneEngineWithStore(t, scope, tenantID, plane, nil)
}

func newDataPlaneEngineWithStore(t *testing.T, scope, tenantID string, plane ports.SQLDataPlane, store ports.CacheStore) *server.Hertz {
	t.Helper()
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("request_id", "req-test")
		if scope != "" {
			c.Set("scope", scope)
		}
		if tenantID != "" {
			c.Set("tenant_id", tenantID)
		}
		c.Next(ctx)
	})
	registerDataPlaneResources(h.Group("/api/v1"), plane, store)
	return h
}

func postDataPlane(t *testing.T, h *server.Hertz, path, body string) (int, map[string]any) {
	t.Helper()
	resp := ut.PerformRequest(h.Engine, http.MethodPost, path,
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	var parsed map[string]any
	if err := json.Unmarshal(resp.Body(), &parsed); err != nil {
		t.Fatalf("decode response body %q: %v", resp.Body(), err)
	}
	return resp.StatusCode(), parsed
}

// ── /data/query security hardening ───────────────────────────────────────────

func TestDataQueryRejectsTenantEndUserScope(t *testing.T) {
	h := newDataPlaneEngine(t, "tenant", "", &fakeDataPlane{})
	status, body := postDataPlane(t, h, "/api/v1/data/query", `{"sql":"SELECT 1","params":[]}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%v", status, body)
	}
	if code, _ := body["code"].(string); code != "FORBIDDEN" {
		t.Fatalf("code = %q, want FORBIDDEN", code)
	}
}

func TestDataQueryRejectsMissingScope(t *testing.T) {
	h := newDataPlaneEngine(t, "", "", &fakeDataPlane{})
	status, _ := postDataPlane(t, h, "/api/v1/data/query", `{"sql":"SELECT 1","params":[]}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (no scope = not a service identity)", status)
	}
}

func TestDataQueryRejectsServiceRoleFromNonPlatformScope(t *testing.T) {
	// scope=service is allowed at the gate, but role=service requires
	// platform scope; a plain service-identity caller cannot escalate.
	h := newDataPlaneEngine(t, "service", "", &fakeDataPlane{})
	status, body := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"SELECT 1 FROM outbox_events","params":[],"role":"service"}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%v", status, body)
	}
}

func TestDataQueryRejectsInvalidRole(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, body := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"SELECT 1","params":[],"role":"admin"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%v", status, body)
	}
}

func TestDataQueryRejectsEmptySQL(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, _ := postDataPlane(t, h, "/api/v1/data/query", `{"sql":"  ","params":[]}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

func TestDataQueryRejectsOverlongSQL(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	long := strings.Repeat("a", dataPlaneMaxSQLLength+1)
	body := `{"sql":"` + long + `","params":[]}`
	status, _ := postDataPlane(t, h, "/api/v1/data/query", body)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (sql too long)", status)
	}
}

func TestDataQueryRejectsTooManyParams(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	params := make([]string, dataPlaneMaxParams+1)
	for i := range params {
		params[i] = "1"
	}
	body := `{"sql":"SELECT 1","params":[` + strings.Join(params, ",") + `]}`
	status, _ := postDataPlane(t, h, "/api/v1/data/query", body)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (too many params)", status)
	}
}

func TestDataQueryRejectsDestructiveDrop(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, body := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"DROP TABLE knowledge_bases","params":[]}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%v", status, body)
	}
	if code, _ := body["code"].(string); code != "UNSUPPORTED_QUERY" {
		t.Fatalf("code = %q, want UNSUPPORTED_QUERY", code)
	}
}

func TestDataQueryRejectsDestructiveTruncate(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, _ := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"TRUNCATE TABLE knowledge_bases","params":[]}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", status)
	}
}

func TestDataQueryRejectsDestructiveAlterSystem(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, _ := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"ALTER SYSTEM SET fsync=off","params":[]}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", status)
	}
}

func TestDataQueryRejectsCopyToProgram(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, _ := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"COPY outbox_events TO PROGRAM 'cat'","params":[]}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", status)
	}
}

func TestDataQueryRejectsPgReadFile(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, _ := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"SELECT pg_read_file('passwd')","params":[]}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", status)
	}
}

func TestDataQueryRejectsAlterTableDropColumn(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, _ := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"ALTER TABLE knowledge_bases DROP COLUMN tenant_id","params":[]}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (ALTER TABLE DROP COLUMN)", status)
	}
}

func TestDataQueryRejectsCreateExtension(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, _ := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"CREATE EXTENSION IF NOT EXISTS plpython3u","params":[]}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (CREATE EXTENSION)", status)
	}
}

func TestDataQueryRejectsDoBlock(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, _ := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"DO $$ BEGIN PERFORM pg_sleep(100); END $$","params":[]}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (DO $$)", status)
	}
}

func TestDataQueryRejectsGrant(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, _ := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"GRANT ALL ON knowledge_bases TO public","params":[]}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (GRANT)", status)
	}
}

func TestDataQueryRejectsRevoke(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, _ := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"REVOKE ALL ON knowledge_bases FROM ani_app","params":[]}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (REVOKE)", status)
	}
}

func TestDataQueryRejectsUnregisteredTable(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, body := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"SELECT * FROM users","params":[]}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%v", status, body)
	}
	if code, _ := body["code"].(string); code != "FORBIDDEN" {
		t.Fatalf("code = %q, want FORBIDDEN", code)
	}
}

func TestDataQueryAllowsRegisteredTable(t *testing.T) {
	plane := &fakeDataPlane{queryResult: ports.DataPlaneQueryResult{RowCount: 1, LastResult: true}}
	h := newDataPlaneEngine(t, "platform", uuid.NewString(), plane)
	status, body := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"SELECT * FROM knowledge_bases WHERE id = $1","params":["abc"]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", status, body)
	}
	if plane.lastQuery.SQL != "SELECT * FROM knowledge_bases WHERE id = $1" {
		t.Fatalf("lastQuery.SQL = %q", plane.lastQuery.SQL)
	}
	if len(plane.lastQuery.Params) != 1 || plane.lastQuery.Params[0] != "abc" {
		t.Fatalf("lastQuery.Params = %#v", plane.lastQuery.Params)
	}
	if plane.lastQuery.Role != ports.DataPlaneRoleTenant {
		t.Fatalf("lastQuery.Role = %q, want tenant", plane.lastQuery.Role)
	}
}

func TestDataQueryPlatformRoleDefaultsToTenantAndUsesMiddlewareTenant(t *testing.T) {
	tenantID := uuid.NewString()
	plane := &fakeDataPlane{}
	h := newDataPlaneEngine(t, "platform", tenantID, plane)
	status, _ := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"SELECT * FROM kb_documents","params":[]}`)
	if status != http.StatusOK {
		t.Fatalf("status = 200 expected, got from body")
	}
	// Tenant id must come from middleware (X-Tenant-Id via auth), not the body.
	if plane.lastQuery.TenantID.String() != tenantID {
		t.Fatalf("lastQuery.TenantID = %q, want %q (from middleware)", plane.lastQuery.TenantID, tenantID)
	}
	if plane.lastQuery.Role != ports.DataPlaneRoleTenant {
		t.Fatalf("lastQuery.Role = %q, want tenant default", plane.lastQuery.Role)
	}
}

func TestDataQueryRoleTenantRequiresTenantContext(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, body := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"SELECT * FROM knowledge_bases","params":[],"role":"tenant"}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (no tenant context)", status)
	}
	if !strings.Contains(body["message"].(string), "tenant") {
		t.Fatalf("message = %v, want tenant-related", body["message"])
	}
}

func TestDataQueryRoleServiceRequiresPlatformScope(t *testing.T) {
	plane := &fakeDataPlane{}
	h := newDataPlaneEngine(t, "platform", "", plane)
	status, _ := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"SELECT * FROM outbox_events","params":[],"role":"service"}`)
	if status != http.StatusOK {
		t.Fatalf("status = 200 expected for role=service with platform scope")
	}
	if plane.lastQuery.Role != ports.DataPlaneRoleService {
		t.Fatalf("lastQuery.Role = %q, want service", plane.lastQuery.Role)
	}
	// role=service must not set a tenant id (cross-tenant BYPASSRLS semantics).
	if plane.lastQuery.TenantID != uuid.Nil {
		t.Fatalf("lastQuery.TenantID = %q, want Nil for role=service", plane.lastQuery.TenantID)
	}
}

func TestDataQueryMapsAdapterErrorsToHTTP(t *testing.T) {
	plane := &fakeDataPlane{queryErr: ports.ErrConflict}
	h := newDataPlaneEngine(t, "platform", uuid.NewString(), plane)
	status, body := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"SELECT * FROM knowledge_bases","params":[]}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%v", status, body)
	}
}

func TestDataQueryMapsInvalidErrorTo400(t *testing.T) {
	plane := &fakeDataPlane{queryErr: ports.ErrInvalid}
	h := newDataPlaneEngine(t, "platform", uuid.NewString(), plane)
	status, _ := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"SELECT * FROM knowledge_bases","params":[]}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

func TestDataQueryMapsPayloadTooLargeTo400(t *testing.T) {
	plane := &fakeDataPlane{queryErr: ports.ErrPayloadTooLarge}
	h := newDataPlaneEngine(t, "platform", uuid.NewString(), plane)
	status, _ := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"SELECT * FROM knowledge_bases","params":[]}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (payload too large)", status)
	}
}

func TestDataQueryRejectsStatementCountOverflow(t *testing.T) {
	// 17 statements > dataPlaneMaxStatements (16).
	stmts := strings.Repeat("SELECT 1;", dataPlaneMaxStatements+1)
	body := `{"sql":"` + stmts + `","params":[]}`
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, _ := postDataPlane(t, h, "/api/v1/data/query", body)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (too many statements)", status)
	}
}

func TestDataQueryRejectsInvalidRequestBody(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, _ := postDataPlane(t, h, "/api/v1/data/query", `{not json}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

func TestDataQueryRejectsNilPlane(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", nil)
	status, body := postDataPlane(t, h, "/api/v1/data/query", `{"sql":"SELECT 1","params":[]}`)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%v", status, body)
	}
}

// ── /data/tables security hardening ───────────────────────────────────────────

func TestDataCreateTableRejectsNonPlatformScope(t *testing.T) {
	h := newDataPlaneEngine(t, "service", "", &fakeDataPlane{})
	status, body := postDataPlane(t, h, "/api/v1/data/tables",
		`{"name":"knowledge_bases","definition":"CREATE TABLE knowledge_bases (id uuid)"}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%v", status, body)
	}
}

func TestDataCreateTableRejectsUnregisteredTable(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, body := postDataPlane(t, h, "/api/v1/data/tables",
		`{"name":"evil_table","definition":"CREATE TABLE evil_table (id uuid)"}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%v", status, body)
	}
}

func TestDataCreateTableRejectsDestructiveDDL(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, _ := postDataPlane(t, h, "/api/v1/data/tables",
		`{"name":"knowledge_bases","definition":"DROP TABLE knowledge_bases"}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", status)
	}
}

func TestDataCreateTableRejectsMissingFields(t *testing.T) {
	h := newDataPlaneEngine(t, "platform", "", &fakeDataPlane{})
	status, _ := postDataPlane(t, h, "/api/v1/data/tables", `{"name":"knowledge_bases"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

func TestDataCreateTableSucceedsForRegisteredTable(t *testing.T) {
	plane := &fakeDataPlane{}
	h := newDataPlaneEngine(t, "platform", "", plane)
	status, body := postDataPlane(t, h, "/api/v1/data/tables",
		`{"name":"knowledge_bases","definition":"CREATE TABLE knowledge_bases (id uuid PRIMARY KEY)"}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%v", status, body)
	}
	if plane.lastCreate.Name != "knowledge_bases" {
		t.Fatalf("lastCreate.Name = %q", plane.lastCreate.Name)
	}
	if name, _ := body["name"].(string); name != "knowledge_bases" {
		t.Fatalf("body name = %q, want knowledge_bases", name)
	}
	if statusText, _ := body["status"].(string); statusText != "applied" {
		t.Fatalf("body status = %q, want applied", statusText)
	}
}

func TestDataCreateTableMapsAdapterError(t *testing.T) {
	plane := &fakeDataPlane{createErr: ports.ErrInvalid}
	h := newDataPlaneEngine(t, "platform", "", plane)
	status, _ := postDataPlane(t, h, "/api/v1/data/tables",
		`{"name":"knowledge_bases","definition":"CREATE TABLE knowledge_bases (id uuid)"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

// ── pure helpers ──────────────────────────────────────────────────────────────

func TestParseDataPlaneRole(t *testing.T) {
	tests := []struct {
		in      string
		want    ports.DataPlaneRole
		wantErr bool
	}{
		{"", ports.DataPlaneRoleTenant, false},
		{"tenant", ports.DataPlaneRoleTenant, false},
		{"TENANT", ports.DataPlaneRoleTenant, false},
		{"service", ports.DataPlaneRoleService, false},
		{"Service", ports.DataPlaneRoleService, false},
		{"admin", "", true},
	}
	for _, tt := range tests {
		got, err := parseDataPlaneRole(tt.in)
		if (err != nil) != tt.wantErr {
			t.Fatalf("parseDataPlaneRole(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
		}
		if got != tt.want {
			t.Fatalf("parseDataPlaneRole(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDataPlaneScopeAllowed(t *testing.T) {
	if !dataPlaneScopeAllowed("platform") {
		t.Fatal("platform scope should be allowed")
	}
	if !dataPlaneScopeAllowed("service") {
		t.Fatal("service scope should be allowed")
	}
	if dataPlaneScopeAllowed("tenant") {
		t.Fatal("tenant scope should be rejected (end-user)")
	}
	if dataPlaneScopeAllowed("") {
		t.Fatal("empty scope should be rejected")
	}
}

func TestValidateDataPlaneTablesAllowsRegistered(t *testing.T) {
	for table := range dataPlaneAllowedTables {
		if err := validateDataPlaneTables("SELECT * FROM " + table); err != nil {
			t.Fatalf("validateDataPlaneTables(%s) err = %v, want nil", table, err)
		}
	}
}

func TestValidateDataPlaneTablesRejectsUnknown(t *testing.T) {
	if err := validateDataPlaneTables("SELECT * FROM pg_authid"); err == nil {
		t.Fatal("validateDataPlaneTables should reject pg_authid")
	}
}

func TestValidateDataPlaneTablesAllowsNoTableReference(t *testing.T) {
	if err := validateDataPlaneTables("SELECT 1"); err != nil {
		t.Fatalf("validateDataPlaneTables(SELECT 1) err = %v, want nil", err)
	}
}

func TestCountStatements(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"SELECT 1", 1},
		{"SELECT 1;", 1},
		{"SELECT 1; SELECT 2", 2},
		{"SELECT 1; SELECT 2;", 2},
		{"", 1},
	}
	for _, tt := range tests {
		if got := countStatements(tt.in); got != tt.want {
			t.Fatalf("countStatements(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestDataPlaneTenantIDFromContext(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "not-a-uuid")
		_, err := dataPlaneTenantIDFromContext(c)
		if err == nil {
			t.Fatal("expected error for invalid uuid")
		}
		c.Next(ctx)
	})
	_ = h
}

// miniCacheStore is a minimal ports.CacheStore for rate-limit tests.
type miniCacheStore struct {
	counts map[string]int64
}

func newMiniCacheStore() *miniCacheStore {
	return &miniCacheStore{counts: make(map[string]int64)}
}

func (s *miniCacheStore) Get(_ context.Context, _ string) ([]byte, error) { return nil, nil }
func (s *miniCacheStore) Set(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return nil
}
func (s *miniCacheStore) SetNX(_ context.Context, _ string, _ []byte, _ time.Duration) (bool, error) {
	return true, nil
}
func (s *miniCacheStore) Delete(_ context.Context, _ string) error { return nil }
func (s *miniCacheStore) Increment(_ context.Context, key string, _ time.Duration) (int64, error) {
	s.counts[key]++
	return s.counts[key], nil
}
func (s *miniCacheStore) Exists(_ context.Context, _ string) (bool, error) { return false, nil }

func TestDataQueryRateLimitedByServiceIdentity(t *testing.T) {
	store := newMiniCacheStore()
	plane := &fakeDataPlane{}
	h := newDataPlaneEngineWithStore(t, "platform", uuid.NewString(), plane, store)
	// The first request should pass; subsequent requests past the limit
	// should be rejected with 429.
	body := `{"sql":"SELECT * FROM knowledge_bases","params":[]}`
	for i := 0; i < dataPlaneRateLimitRequests; i++ {
		status, _ := postDataPlane(t, h, "/api/v1/data/query", body)
		if status != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 (within rate limit)", i+1, status)
		}
	}
	// Next request exceeds the limit.
	status, respBody := postDataPlane(t, h, "/api/v1/data/query", body)
	if status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (rate limit exceeded); body=%v", status, respBody)
	}
	if code, _ := respBody["code"].(string); code != "RATE_LIMITED" {
		t.Fatalf("code = %q, want RATE_LIMITED", code)
	}
}

func TestDataQueryRateLimitFailsOpenWhenStoreErrors(t *testing.T) {
	plane := &fakeDataPlane{}
	h := newDataPlaneEngineWithStore(t, "platform", uuid.NewString(), plane, nil)
	status, _ := postDataPlane(t, h, "/api/v1/data/query",
		`{"sql":"SELECT * FROM knowledge_bases","params":[]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (nil store = no rate limiting)", status)
	}
}
