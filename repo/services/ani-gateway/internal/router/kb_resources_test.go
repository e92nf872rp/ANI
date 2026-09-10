package router

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	kbv1 "github.com/kubercloud/ani/pkg/generated/pb/kb/v1"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

// fakeKBClient is a test double for KBGRPCClient. It records calls and returns
// canned responses/errors so tests can assert the handler→gRPC wiring without
// a real kb-service.
type fakeKBClient struct {
	lastTenantID string
	lastKbID     string
	lastDocID    string
	lastIDemKey  string

	listKbsResp *kbv1.ListKBsResponse
	listKbsErr  error

	createKbResp *kbv1.KnowledgeBase
	createKbErr  error

	getKbResp *kbv1.KnowledgeBase
	getKbErr  error

	updateKbResp   *kbv1.KnowledgeBase
	updateKbErr    error
	lastUpdateName string
	lastUpdateDesc string

	deleteKbErr error

	listDocsResp *kbv1.ListDocumentsResponse
	listDocsErr  error

	uploadURLResp *kbv1.GetDocumentUploadURLResponse
	uploadURLErr  error

	deleteDocErr error

	getDocResp *kbv1.KBDocument
	getDocErr  error

	queryResp *kbv1.QueryResponse
	queryErr  error

	citationsErr    error
	sessionsErr     error
	permissionsResp *kbv1.KnowledgeBase
	permissionsErr  error
	getPermsResp    *kbv1.KBPermissions
	getPermsErr     error

	lastSessionID string
	lastChunkType string
	lastLimit     int32
	lastCursor    string

	listChunksResp   *kbv1.ListDocumentChunksResponse
	listChunksErr    error
	sessionMsgsResp  *kbv1.GetSessionMessagesResponse
	sessionMsgsErr   error
	deleteSessionErr error

	reparseResp *commonv1.AsyncTaskRef
	reparseErr  error
}

func (f *fakeKBClient) CreateKB(_ context.Context, tenantID, idem string, req *kbv1.CreateKBRequest) (*kbv1.KnowledgeBase, error) {
	f.lastTenantID = tenantID
	f.lastIDemKey = idem
	return f.createKbResp, f.createKbErr
}
func (f *fakeKBClient) GetKB(_ context.Context, tenantID, kbID string) (*kbv1.KnowledgeBase, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	return f.getKbResp, f.getKbErr
}
func (f *fakeKBClient) UpdateKB(_ context.Context, tenantID, kbID, idem, name, description string) (*kbv1.KnowledgeBase, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	f.lastIDemKey = idem
	f.lastUpdateName = name
	f.lastUpdateDesc = description
	return f.updateKbResp, f.updateKbErr
}
func (f *fakeKBClient) ListKBs(_ context.Context, tenantID string, _ int32, _ string) (*kbv1.ListKBsResponse, error) {
	f.lastTenantID = tenantID
	return f.listKbsResp, f.listKbsErr
}
func (f *fakeKBClient) DeleteKB(_ context.Context, tenantID, kbID string) (*emptypb.Empty, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	return &emptypb.Empty{}, f.deleteKbErr
}
func (f *fakeKBClient) GetDocumentUploadURL(_ context.Context, tenantID, kbID, idem string, _ *kbv1.GetDocumentUploadURLRequest) (*kbv1.GetDocumentUploadURLResponse, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	f.lastIDemKey = idem
	return f.uploadURLResp, f.uploadURLErr
}
func (f *fakeKBClient) NotifyDocumentUploaded(context.Context, string, string, string, string) (*commonv1.AsyncTaskRef, error) {
	return nil, status.Error(codes.Unimplemented, "not used in P0 routes")
}
func (f *fakeKBClient) GetDocument(_ context.Context, tenantID, kbID, docID string) (*kbv1.KBDocument, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	f.lastDocID = docID
	return f.getDocResp, f.getDocErr
}
func (f *fakeKBClient) ListDocuments(_ context.Context, tenantID, kbID, _ string, _ int32, _ string) (*kbv1.ListDocumentsResponse, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	return f.listDocsResp, f.listDocsErr
}
func (f *fakeKBClient) DeleteDocument(_ context.Context, tenantID, kbID, docID string) (*emptypb.Empty, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	f.lastDocID = docID
	return &emptypb.Empty{}, f.deleteDocErr
}
func (f *fakeKBClient) Query(_ context.Context, tenantID, kbID, idem string, _ *kbv1.QueryRequest) (*kbv1.QueryResponse, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	f.lastIDemKey = idem
	return f.queryResp, f.queryErr
}
func (f *fakeKBClient) ListKBCitations(_ context.Context, tenantID, kbID string, _ int32, _ string) (*kbv1.ListKBCitationsResponse, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	return nil, f.citationsErr
}
func (f *fakeKBClient) ListKBSessions(_ context.Context, tenantID, kbID string, _ int32, _ string) (*kbv1.ListKBSessionsResponse, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	return nil, f.sessionsErr
}
func (f *fakeKBClient) UpdateKBPermissions(_ context.Context, tenantID, kbID, idem string, _ *kbv1.UpdateKBPermissionsRequest) (*kbv1.KnowledgeBase, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	f.lastIDemKey = idem
	return f.permissionsResp, f.permissionsErr
}
func (f *fakeKBClient) GetKBPermissions(_ context.Context, tenantID, kbID string) (*kbv1.KBPermissions, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	return f.getPermsResp, f.getPermsErr
}
func (f *fakeKBClient) ListDocumentChunks(_ context.Context, tenantID, kbID, docID, chunkType string, limit int32, cursor string) (*kbv1.ListDocumentChunksResponse, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	f.lastDocID = docID
	f.lastChunkType = chunkType
	f.lastLimit = limit
	f.lastCursor = cursor
	return f.listChunksResp, f.listChunksErr
}
func (f *fakeKBClient) GetSessionMessages(_ context.Context, tenantID, kbID, sessionID string, limit int32, cursor string) (*kbv1.GetSessionMessagesResponse, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	f.lastSessionID = sessionID
	f.lastLimit = limit
	f.lastCursor = cursor
	return f.sessionMsgsResp, f.sessionMsgsErr
}
func (f *fakeKBClient) DeleteSession(_ context.Context, tenantID, kbID, sessionID string) (*emptypb.Empty, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	f.lastSessionID = sessionID
	if f.deleteSessionErr != nil {
		return nil, f.deleteSessionErr
	}
	return &emptypb.Empty{}, nil
}
func (f *fakeKBClient) Retrieve(_ context.Context, _ string, _ string, _ *kbv1.RetrieveRequest) (kbv1.KBService_RetrieveClient, error) {
	return nil, status.Error(codes.Unimplemented, "fakeKBClient.Retrieve not implemented")
}
func (f *fakeKBClient) ReparseDocument(_ context.Context, tenantID, kbID, docID, idemKey string) (*commonv1.AsyncTaskRef, error) {
	f.lastTenantID = tenantID
	f.lastKbID = kbID
	f.lastDocID = docID
	f.lastIDemKey = idemKey
	return f.reparseResp, f.reparseErr
}

// setupKBTestServer builds a gateway with the KB routes registered under
// /api/v1/svc using the injected client. A RequestID middleware + dev tenant
// header mirror the production middleware chain the handlers depend on.
func setupKBTestServer(client KBGRPCClient) *server.Hertz {
	h := server.Default()
	h.Use(middleware.RequestID())
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		tenantID := string(c.GetHeader("X-Dev-Tenant-ID"))
		if tenantID == "" {
			tenantID = "tenant-test"
		}
		c.Set("tenant_id", tenantID)
		c.Next(ctx)
	})
	svc := h.Group("/api/v1/svc")
	registerKnowledgeBasesWithClient(svc, client, KbSSEConfig{})
	return h
}

// TestKBRoutes_AllEndpointsRegistered asserts the KB endpoint surface is
// registered (SPEC §4.1 + KB-API-B1 #5/#10). We hit each path with a method
// that forces the handler to run (rather than 404) so a missing route fails
// the test.
func TestKBRoutes_AllEndpointsRegistered(t *testing.T) {
	h := setupKBTestServer(&fakeKBClient{
		listKbsResp:     &kbv1.ListKBsResponse{},
		listDocsResp:    &kbv1.ListDocumentsResponse{},
		getDocResp:      &kbv1.KBDocument{},
		listChunksResp:  &kbv1.ListDocumentChunksResponse{},
		sessionMsgsResp: &kbv1.GetSessionMessagesResponse{},
		reparseResp:     &commonv1.AsyncTaskRef{},
		citationsErr:    status.Error(codes.Unimplemented, "P1"),
		sessionsErr:     status.Error(codes.Unimplemented, "P1"),
		permissionsErr:  status.Error(codes.Unimplemented, "P1"),
	})

	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/svc/knowledge-bases", ""},
		{http.MethodPost, "/api/v1/svc/knowledge-bases", `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440001","name":"kb"}`},
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1", ""},
		{http.MethodPut, "/api/v1/svc/knowledge-bases/kb-1", `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440005","name":"renamed"}`},
		{http.MethodDelete, "/api/v1/svc/knowledge-bases/kb-1", ""},
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/documents", ""},
		{http.MethodPost, "/api/v1/svc/knowledge-bases/kb-1/documents", `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440002","file_name":"a.pdf","file_type":"pdf"}`},
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/documents/doc-1", ""},
		{http.MethodDelete, "/api/v1/svc/knowledge-bases/kb-1/documents/doc-1", ""},
		{http.MethodPost, "/api/v1/svc/knowledge-bases/kb-1/query", `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440003","question":"hi"}`},
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/query/stream?question=hi", ""},
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/citations", ""},
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/sessions", ""},
		{http.MethodPut, "/api/v1/svc/knowledge-bases/kb-1/permissions", `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440004"}`},
		// B4 route (SPEC §4.3 #19, kb-p1-plan §2.6): permissions read pair.
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/permissions", ""},
		// B2 routes (SPEC §4.3 #11/#17/#18).
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/documents/doc-1/chunks", ""},
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/sessions/sess-1/messages", ""},
		{http.MethodDelete, "/api/v1/svc/knowledge-bases/kb-1/sessions/sess-1", ""},
		// B3 route (SPEC §4.3 #12, issue-048).
		{http.MethodPost, "/api/v1/svc/knowledge-bases/kb-1/documents/doc-1/reparse", `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440006"}`},
	}

	for _, r := range routes {
		var bodyArg *ut.Body
		if r.body != "" {
			bodyArg = &ut.Body{Body: strings.NewReader(r.body), Len: len(r.body)}
		}
		resp := ut.PerformRequest(h.Engine, r.method, r.path, bodyArg,
			ut.Header{Key: "Content-Type", Value: "application/json"},
			ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
		).Result()
		// 404 means the route is not registered; any other status means the
		// handler ran. We assert != 404 to keep the test focused on routing.
		if resp.StatusCode() == http.StatusNotFound {
			t.Fatalf("%s %s returned 404 — route not registered", r.method, r.path)
		}
	}
}

// TestKBRoutes_P1EndpointsReturn501 asserts the 2 remaining P1 endpoints
// route to kb-service and surface UNIMPLEMENTED as HTTP 501 (SPEC §4.1,
// US-016 AC1). PUT permissions moved to the B4 wired group with B4.
func TestKBRoutes_P1EndpointsReturn501(t *testing.T) {
	h := setupKBTestServer(&fakeKBClient{
		citationsErr: status.Error(codes.Unimplemented, "ListKBCitations P1"),
		sessionsErr:  status.Error(codes.Unimplemented, "ListKBSessions P1"),
	})

	p1 := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/citations", ""},
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/sessions", ""},
	}
	for _, r := range p1 {
		var bodyArg *ut.Body
		if r.body != "" {
			bodyArg = &ut.Body{Body: strings.NewReader(r.body), Len: len(r.body)}
		}
		resp := ut.PerformRequest(h.Engine, r.method, r.path, bodyArg,
			ut.Header{Key: "Content-Type", Value: "application/json"},
			ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
		).Result()
		if resp.StatusCode() != http.StatusNotImplemented {
			t.Fatalf("%s %s status = %d, want 501", r.method, r.path, resp.StatusCode())
		}
		var body map[string]any
		_ = json.Unmarshal(resp.Body(), &body)
		if body["code"] != "NOT_IMPLEMENTED" {
			t.Fatalf("%s %s code = %v, want NOT_IMPLEMENTED", r.method, r.path, body["code"])
		}
	}
}

// TestKBRoutes_P1WithoutClientReturn501 asserts the P1 handlers return 501
// even when no gRPC client is configured, so the route surface is consistent
// regardless of deployment topology.
func TestKBRoutes_P1WithoutClientReturn501(t *testing.T) {
	h := setupKBTestServer(nil)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/citations", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusNotImplemented {
		t.Fatalf("citations status = %d, want 501", resp.StatusCode())
	}
}

// TestKBRoutes_GetPermissions_Passthrough verifies the B4 GET permissions
// handler (SPEC §4.3 #19, kb-p1-plan §2.6): the gRPC KBPermissions response is
// mapped to the REST KBPermissions shape, the Auth-middleware tenant id is
// injected into the gRPC call, and the zero updated_at surfaces as omitted
// (no 1970-01-01T00:00:00Z leak).
func TestKBRoutes_GetPermissions_Passthrough(t *testing.T) {
	client := &fakeKBClient{
		getPermsResp: &kbv1.KBPermissions{
			KbId:           "kb-1",
			PublicRead:     true,
			AllowedUserIds: []string{"user-1", "user-2"},
			UpdatedAt:      timestamppb.Now(),
		},
	}
	h := setupKBTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/permissions", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	if client.lastTenantID != "tenant-test" {
		t.Fatalf("gRPC tenant id = %q, want tenant-test", client.lastTenantID)
	}
	if client.lastKbID != "kb-1" {
		t.Fatalf("gRPC kb id = %q, want kb-1", client.lastKbID)
	}
	var body struct {
		KbID           string   `json:"kb_id"`
		PublicRead     bool     `json:"public_read"`
		AllowedUserIDs []string `json:"allowed_user_ids"`
		UpdatedAt      string   `json:"updated_at"`
	}
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.KbID != "kb-1" || !body.PublicRead {
		t.Fatalf("body = %+v, want kb-1/public_read=true", body)
	}
	if len(body.AllowedUserIDs) != 2 || body.AllowedUserIDs[0] != "user-1" {
		t.Fatalf("allowed_user_ids = %v, want [user-1 user-2]", body.AllowedUserIDs)
	}
	if body.UpdatedAt == "" {
		t.Fatalf("updated_at = %q, want a non-empty RFC3339 value", body.UpdatedAt)
	}
}

// TestKBRoutes_GetPermissions_ZeroUpdatedAtOmitted asserts a zero/nil proto
// updated_at serializes as an omitted field (kb-p1-plan §2.6: protoTimestampToRFC3339
// maps epoch-zero to ""), never 1970-01-01T00:00:00Z.
func TestKBRoutes_GetPermissions_ZeroUpdatedAtOmitted(t *testing.T) {
	client := &fakeKBClient{
		getPermsResp: &kbv1.KBPermissions{
			KbId:           "kb-1",
			PublicRead:     false,
			AllowedUserIds: []string{},
		},
	}
	h := setupKBTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/permissions", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, ok := body["updated_at"]; ok {
		t.Fatalf("updated_at = %v, want omitted (zero timestamp)", body["updated_at"])
	}
	if ids, _ := body["allowed_user_ids"].([]any); len(ids) != 0 {
		t.Fatalf("allowed_user_ids = %v, want empty array", ids)
	}
}

// TestKBRoutes_GetPermissions_NotFoundMappedTo404 asserts a kb-service
// NOT_FOUND surfaces as HTTP 404 (a missing KB is the only 404 case; a
// missing permissions row never 404s — defaults come from kb-service).
func TestKBRoutes_GetPermissions_NotFoundMappedTo404(t *testing.T) {
	client := &fakeKBClient{
		getPermsErr: status.Error(codes.NotFound, "knowledge base not found"),
	}
	h := setupKBTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-404/permissions", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode())
	}
	var body map[string]any
	_ = json.Unmarshal(resp.Body(), &body)
	if body["code"] != "NOT_FOUND" {
		t.Fatalf("code = %v, want NOT_FOUND", body["code"])
	}
}

// TestKBRoutes_GetPermissions_NilClientReturns503 asserts the GET permissions
// handler returns 503 UNAVAILABLE when kb-service is not configured (B4
// nil-client guard, mirroring the other B2/B3 handlers).
func TestKBRoutes_GetPermissions_NilClientReturns503(t *testing.T) {
	h := setupKBTestServer(nil)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/permissions", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode())
	}
}

// TestKBRoutes_UpdatePermissions_NilClientReturns503 asserts the PUT
// permissions handler returns 503 UNAVAILABLE when kb-service is not
// configured — aligned with the B4 wired group (the RPC is implemented
// server-side, so NOT_IMPLEMENTED would misreport the actual state).
func TestKBRoutes_UpdatePermissions_NilClientReturns503(t *testing.T) {
	h := setupKBTestServer(nil)
	body := `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440001"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPut,
		"/api/v1/svc/knowledge-bases/kb-1/permissions",
		&ut.Body{Body: strings.NewReader(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode())
	}
	var b map[string]any
	_ = json.Unmarshal(resp.Body(), &b)
	if b["code"] != "UNAVAILABLE" {
		t.Fatalf("code = %v, want UNAVAILABLE", b["code"])
	}
}

// TestKBRoutes_GrpcPassthroughList verifies the list handler maps the gRPC
// response (KnowledgeBase + CursorPageMeta) to the OpenAPI list shape and
// injects the Auth-middleware tenant id into the gRPC request.
func TestKBRoutes_GrpcPassthroughList(t *testing.T) {
	client := &fakeKBClient{
		listKbsResp: &kbv1.ListKBsResponse{
			Kbs: []*kbv1.KnowledgeBase{
				{
					TenantId: "tenant-test", Id: "kb-1", Name: "alpha",
					Status: "active", DocCount: 3,
					CreatedAt: timestamppb.Now(),
				},
			},
			Meta: &commonv1.CursorPageMeta{Total: 1, NextCursor: "cursor-x"},
		},
	}
	h := setupKBTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases?limit=5&cursor=abc", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	if client.lastTenantID != "tenant-test" {
		t.Fatalf("gRPC tenant id = %q, want tenant-test", client.lastTenantID)
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body = %v", err)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["id"] != "kb-1" || first["status"] != "active" {
		t.Fatalf("first item = %+v, want kb-1/active", first)
	}
	if body["next_cursor"] != "cursor-x" {
		t.Fatalf("next_cursor = %v, want cursor-x", body["next_cursor"])
	}
}

// TestKBRoutes_GrpcPassthroughCreate verifies create maps idempotency_key +
// name from JSON to gRPC and returns 201 on success.
func TestKBRoutes_GrpcPassthroughCreate(t *testing.T) {
	client := &fakeKBClient{
		createKbResp: &kbv1.KnowledgeBase{Id: "kb-new", Name: "alpha", Status: "active"},
	}
	h := setupKBTestServer(client)
	createBody := `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440001","name":"alpha"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost,
		"/api/v1/svc/knowledge-bases",
		&ut.Body{Body: strings.NewReader(createBody), Len: len(createBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode())
	}
	if client.lastIDemKey != "550e8400-e29b-41d4-a716-446655440001" {
		t.Fatalf("idempotency_key = %q, want 550e8400-e29b-41d4-a716-446655440001", client.lastIDemKey)
	}
	var body map[string]any
	_ = json.Unmarshal(resp.Body(), &body)
	if body["id"] != "kb-new" {
		t.Fatalf("id = %v, want kb-new", body["id"])
	}
}

// TestKBRoutes_CreateValidatesRequiredFields asserts missing name /
// idempotency_key return 400 without calling kb-service.
func TestKBRoutes_CreateValidatesRequiredFields(t *testing.T) {
	h := setupKBTestServer(&fakeKBClient{})
	cases := []string{
		`{"idempotency_key":"550e8400-e29b-41d4-a716-446655440001","name":""}`,
		`{"idempotency_key":"","name":"alpha"}`,
	}
	for _, body := range cases {
		resp := ut.PerformRequest(h.Engine, http.MethodPost,
			"/api/v1/svc/knowledge-bases",
			&ut.Body{Body: strings.NewReader(body), Len: len(body)},
			ut.Header{Key: "Content-Type", Value: "application/json"},
			ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
		).Result()
		if resp.StatusCode() != http.StatusBadRequest {
			t.Fatalf("status = %d for body %q, want 400", resp.StatusCode(), body)
		}
	}
}

// TestKBRoutes_GrpcErrorMapping verifies gRPC status codes map to the right
// HTTP status + error code (SPEC §5.1 step 4).
func TestKBRoutes_GrpcErrorMapping(t *testing.T) {
	cases := []struct {
		name       string
		grpcErr    error
		wantStatus int
		wantCode   string
	}{
		{"not_found", status.Error(codes.NotFound, "kb missing"), http.StatusNotFound, "NOT_FOUND"},
		{"invalid_argument", status.Error(codes.InvalidArgument, "bad input"), http.StatusBadRequest, "BAD_REQUEST"},
		{"failed_precondition", status.Error(codes.FailedPrecondition, "kb rebuilding"), http.StatusConflict, "CONFLICT"},
		{"unavailable", status.Error(codes.Unavailable, "kb-service down"), http.StatusServiceUnavailable, "UNAVAILABLE"},
		{"permission_denied", status.Error(codes.PermissionDenied, "no access"), http.StatusForbidden, "FORBIDDEN"},
		{"deadline_exceeded", status.Error(codes.DeadlineExceeded, "kb-service timeout"), http.StatusGatewayTimeout, "DEADLINE_EXCEEDED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeKBClient{getKbErr: tc.grpcErr}
			h := setupKBTestServer(client)
			resp := ut.PerformRequest(h.Engine, http.MethodGet,
				"/api/v1/svc/knowledge-bases/kb-1", nil,
				ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
			).Result()
			if resp.StatusCode() != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode(), tc.wantStatus)
			}
			var body map[string]any
			_ = json.Unmarshal(resp.Body(), &body)
			if body["code"] != tc.wantCode {
				t.Fatalf("code = %v, want %s", body["code"], tc.wantCode)
			}
		})
	}
}

// TestKBRoutes_NilClientReturns503 asserts the 11 gRPC handlers return 503
// UNAVAILABLE when kb-service is not configured, so the gateway stays up and
// surfaces the missing dependency instead of panicking.
func TestKBRoutes_NilClientReturns503(t *testing.T) {
	h := setupKBTestServer(nil)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode())
	}
	var body map[string]any
	_ = json.Unmarshal(resp.Body(), &body)
	if body["code"] != "UNAVAILABLE" {
		t.Fatalf("code = %v, want UNAVAILABLE", body["code"])
	}
}

// TestKBRoutes_SSEStreamWritesHeadersAndDoneEvent asserts the SSE route is
// held by the gateway (US-017 AC1), writes the SSE content type, and emits a
// well-formed terminal done event so clients do not hang.
func TestKBRoutes_SSEStreamWritesHeadersAndDoneEvent(t *testing.T) {
	h := setupKBTestServer(nil)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/query/stream?question=hello", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	if ct := string(resp.Header.ContentType()); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
	body := string(resp.Body())
	if !strings.Contains(body, "event: done") {
		t.Fatalf("body = %q, want 'event: done' event", body)
	}
}

// TestKBRoutes_SSEStreamRejectsMissingQuestion asserts the SSE handler
// validates the required question query param per SPEC §4.3.
func TestKBRoutes_SSEStreamRejectsMissingQuestion(t *testing.T) {
	h := setupKBTestServer(nil)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/query/stream", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode())
	}
	var body map[string]any
	_ = json.Unmarshal(resp.Body(), &body)
	if body["code"] != "BAD_REQUEST" {
		t.Fatalf("code = %v, want BAD_REQUEST", body["code"])
	}
}

// TestKBRoutes_QueryMapsSources asserts the JSON query response includes the
// sources array from the gRPC QueryResponse.
func TestKBRoutes_QueryMapsSources(t *testing.T) {
	client := &fakeKBClient{
		queryResp: &kbv1.QueryResponse{
			Answer:      "42",
			SessionId:   "sess-1",
			InputTokens: 10, OutputTokens: 20,
			Sources: []*kbv1.SourceChunk{
				{DocId: "doc-1", FileName: "a.pdf", Page: 1, Content: "ctx", Score: 0.9},
			},
		},
	}
	h := setupKBTestServer(client)
	queryBody := `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440003","question":"what?"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost,
		"/api/v1/svc/knowledge-bases/kb-1/query",
		&ut.Body{Body: strings.NewReader(queryBody), Len: len(queryBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body = %v", err)
	}
	if body["answer"] != "42" || body["session_id"] != "sess-1" {
		t.Fatalf("body = %+v, want answer=42 session=sess-1", body)
	}
	sources, _ := body["sources"].([]any)
	if len(sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(sources))
	}
}

// TestKBGrpcErrorMapping_NilError asserts mapGRPCError(nil) returns nil so
// handlers do not synthesize a spurious error on success.
func TestKBGrpcErrorMapping_NilError(t *testing.T) {
	if got := mapGRPCError(nil); got != nil {
		t.Fatalf("mapGRPCError(nil) = %+v, want nil", got)
	}
}

// TestKBGrpcErrorMapping_UnknownCode asserts non-gRPC errors (transport /
// network failures that don't carry a gRPC status) map to 503 UNAVAILABLE
// rather than masking as 400 BAD_REQUEST.
func TestKBGrpcErrorMapping_UnknownCode(t *testing.T) {
	ke := mapGRPCError(context.DeadlineExceeded)
	if ke.httpStatus != http.StatusServiceUnavailable || ke.code != "UNAVAILABLE" {
		t.Fatalf("got %+v, want 503/UNAVAILABLE", ke)
	}
}

// TestKBRoutes_UpdateKnowledgeBase_Success verifies update maps
// idempotency_key/name/description from JSON to gRPC, injects the Auth
// tenant id, and returns the updated KB as JSON (SPEC §4.3 #5).
func TestKBRoutes_UpdateKnowledgeBase_Success(t *testing.T) {
	client := &fakeKBClient{
		updateKbResp: &kbv1.KnowledgeBase{Id: "kb-1", Name: "renamed", Status: "active"},
	}
	h := setupKBTestServer(client)
	updateBody := `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440005","name":"renamed","description":"new desc"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPut,
		"/api/v1/svc/knowledge-bases/kb-1",
		&ut.Body{Body: strings.NewReader(updateBody), Len: len(updateBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	if client.lastTenantID != "tenant-test" || client.lastKbID != "kb-1" {
		t.Fatalf("gRPC tenant/kb = %q/%q, want tenant-test/kb-1", client.lastTenantID, client.lastKbID)
	}
	if client.lastIDemKey != "550e8400-e29b-41d4-a716-446655440005" || client.lastUpdateName != "renamed" || client.lastUpdateDesc != "new desc" {
		t.Fatalf("gRPC idem/name/desc = %q/%q/%q, want 550e8400-e29b-41d4-a716-446655440005/renamed/new desc",
			client.lastIDemKey, client.lastUpdateName, client.lastUpdateDesc)
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body = %v", err)
	}
	if body["id"] != "kb-1" || body["name"] != "renamed" {
		t.Fatalf("body = %+v, want id=kb-1 name=renamed", body)
	}
}

// TestKBRoutes_UpdateKnowledgeBase_MissingIdempotencyKey asserts a missing
// idempotency_key returns 400 without calling kb-service (SPEC §4.3 #5).
func TestKBRoutes_UpdateKnowledgeBase_MissingIdempotencyKey(t *testing.T) {
	client := &fakeKBClient{}
	h := setupKBTestServer(client)
	updateBody := `{"idempotency_key":"","name":"renamed"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPut,
		"/api/v1/svc/knowledge-bases/kb-1",
		&ut.Body{Body: strings.NewReader(updateBody), Len: len(updateBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode())
	}
	var body map[string]any
	_ = json.Unmarshal(resp.Body(), &body)
	if body["code"] != "BAD_REQUEST" {
		t.Fatalf("code = %v, want BAD_REQUEST", body["code"])
	}
	if client.lastTenantID != "" {
		t.Fatalf("kb-service was called (tenant = %q) despite 400 validation failure", client.lastTenantID)
	}
}

// TestKBRoutes_UpdateKnowledgeBase_NameConflictMaps409 asserts
// ALREADY_EXISTS from kb-service maps to 409 (SPEC §6.1).
func TestKBRoutes_UpdateKnowledgeBase_NameConflictMaps409(t *testing.T) {
	client := &fakeKBClient{
		updateKbErr: status.Error(codes.AlreadyExists, "name already taken"),
	}
	h := setupKBTestServer(client)
	updateBody := `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440005","name":"dup"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPut,
		"/api/v1/svc/knowledge-bases/kb-1",
		&ut.Body{Body: strings.NewReader(updateBody), Len: len(updateBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode())
	}
	var body map[string]any
	_ = json.Unmarshal(resp.Body(), &body)
	if body["code"] != "ALREADY_EXISTS" {
		t.Fatalf("code = %v, want ALREADY_EXISTS", body["code"])
	}
}

// TestKBRoutes_UpdateKnowledgeBase_NotFound asserts NOT_FOUND from
// kb-service maps to 404 (SPEC §6.1).
func TestKBRoutes_UpdateKnowledgeBase_NotFound(t *testing.T) {
	client := &fakeKBClient{
		updateKbErr: status.Error(codes.NotFound, "kb missing"),
	}
	h := setupKBTestServer(client)
	updateBody := `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440005","name":"renamed"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPut,
		"/api/v1/svc/knowledge-bases/kb-404",
		&ut.Body{Body: strings.NewReader(updateBody), Len: len(updateBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode())
	}
	var body map[string]any
	_ = json.Unmarshal(resp.Body(), &body)
	if body["code"] != "NOT_FOUND" {
		t.Fatalf("code = %v, want NOT_FOUND", body["code"])
	}
}

// TestKBRoutes_UpdateAndGetDocument_NilClientReturns503 asserts the two B1
// handlers return 503 UNAVAILABLE when kb-service is not configured.
func TestKBRoutes_UpdateAndGetDocument_NilClientReturns503(t *testing.T) {
	h := setupKBTestServer(nil)
	updateBody := `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440005","name":"renamed"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPut,
		"/api/v1/svc/knowledge-bases/kb-1",
		&ut.Body{Body: strings.NewReader(updateBody), Len: len(updateBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("update status = %d, want 503", resp.StatusCode())
	}
	var body map[string]any
	_ = json.Unmarshal(resp.Body(), &body)
	if body["code"] != "UNAVAILABLE" {
		t.Fatalf("update code = %v, want UNAVAILABLE", body["code"])
	}
	resp = ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/documents/doc-1", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("get document status = %d, want 503", resp.StatusCode())
	}
	_ = json.Unmarshal(resp.Body(), &body)
	if body["code"] != "UNAVAILABLE" {
		t.Fatalf("get document code = %v, want UNAVAILABLE", body["code"])
	}
}

// TestKBRoutes_GetKnowledgeBaseDocument_Success verifies the document
// handler injects the Auth tenant id and kb/doc path params into the gRPC
// call and returns the KBDocument JSON shape (SPEC §4.3 #10).
func TestKBRoutes_GetKnowledgeBaseDocument_Success(t *testing.T) {
	client := &fakeKBClient{
		getDocResp: &kbv1.KBDocument{
			TenantId: "tenant-test", KbId: "kb-1", Id: "doc-1",
			FileName: "a.pdf", FileType: "pdf", FileSizeBytes: 1024,
			ParseStatus: "parsed", ChunkCount: 7,
		},
	}
	h := setupKBTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/documents/doc-1", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	if client.lastTenantID != "tenant-test" || client.lastKbID != "kb-1" || client.lastDocID != "doc-1" {
		t.Fatalf("gRPC tenant/kb/doc = %q/%q/%q, want tenant-test/kb-1/doc-1",
			client.lastTenantID, client.lastKbID, client.lastDocID)
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body = %v", err)
	}
	if body["id"] != "doc-1" || body["parse_status"] != "parsed" {
		t.Fatalf("body = %+v, want id=doc-1 parse_status=parsed", body)
	}
	if body["chunk_count"] != float64(7) {
		t.Fatalf("chunk_count = %v, want 7", body["chunk_count"])
	}
}

// TestKBRoutes_GetKnowledgeBaseDocument_NotFound asserts NOT_FOUND from
// kb-service maps to 404 (SPEC §6.1).
func TestKBRoutes_GetKnowledgeBaseDocument_NotFound(t *testing.T) {
	client := &fakeKBClient{
		getDocErr: status.Error(codes.NotFound, "doc missing"),
	}
	h := setupKBTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/documents/doc-404", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode())
	}
	var body map[string]any
	_ = json.Unmarshal(resp.Body(), &body)
	if body["code"] != "NOT_FOUND" {
		t.Fatalf("code = %v, want NOT_FOUND", body["code"])
	}
}

// ── B2 handler tests (SPEC §4.3 #11/#17/#18, issue-046) ─────────────────────

// TestKBRoutes_ListDocumentChunks_Success verifies the chunks handler maps
// the path params, query limit/cursor/chunk_type and the Auth tenant id into
// the gRPC request, and serializes custom_metadata as a JSON object (not a
// JSONB string) per SPEC §3.2.
func TestKBRoutes_ListDocumentChunks_Success(t *testing.T) {
	client := &fakeKBClient{
		listChunksResp: &kbv1.ListDocumentChunksResponse{
			Items: []*kbv1.KBChunk{
				{
					Id: "chunk-1", DocId: "doc-1", KbId: "kb-1",
					ChunkType: "child", Content: "chunk text", FileName: "a.pdf",
					PageNumber: 3, TokenCount: 42,
					CustomMetadata: `{"source":"wiki","tag":"alpha"}`,
					CreatedAt:      timestamppb.Now(),
				},
			},
			NextCursor: "chunk-cursor-1",
		},
	}
	h := setupKBTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/documents/doc-1/chunks?limit=7&cursor=cur&chunk_type=child", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	if client.lastTenantID != "tenant-test" || client.lastKbID != "kb-1" ||
		client.lastDocID != "doc-1" || client.lastChunkType != "child" {
		t.Fatalf("gRPC tenant/kb/doc/chunk_type = %q/%q/%q/%q, want tenant-test/kb-1/doc-1/child",
			client.lastTenantID, client.lastKbID, client.lastDocID, client.lastChunkType)
	}
	if client.lastLimit != 7 || client.lastCursor != "cur" {
		t.Fatalf("gRPC limit/cursor = %d/%q, want 7/cur", client.lastLimit, client.lastCursor)
	}
	var body struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body = %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(body.Items))
	}
	item := body.Items[0]
	if item["id"] != "chunk-1" || item["chunk_type"] != "child" || item["file_name"] != "a.pdf" {
		t.Fatalf("item = %+v, want id=chunk-1 chunk_type=child file_name=a.pdf", item)
	}
	// Serialization focus: custom_metadata must be a JSON object, never a
	// JSONB string (SPEC §3.2).
	meta, ok := item["custom_metadata"].(map[string]any)
	if !ok {
		t.Fatalf("custom_metadata = %#v, want JSON object", item["custom_metadata"])
	}
	if meta["source"] != "wiki" || meta["tag"] != "alpha" {
		t.Fatalf("custom_metadata = %+v, want source=wiki tag=alpha", meta)
	}
	if body.NextCursor != "chunk-cursor-1" {
		t.Fatalf("next_cursor = %q, want chunk-cursor-1", body.NextCursor)
	}
}

// TestKBRoutes_ListDocumentChunks_EmptyCustomMetadata asserts chunks with no
// custom_metadata (empty JSONB string) omit the field instead of surfacing a
// quoted string or invalid JSON.
func TestKBRoutes_ListDocumentChunks_EmptyCustomMetadata(t *testing.T) {
	client := &fakeKBClient{
		listChunksResp: &kbv1.ListDocumentChunksResponse{
			Items: []*kbv1.KBChunk{
				{Id: "chunk-2", DocId: "doc-1", KbId: "kb-1", ChunkType: "child", Content: "x", FileName: "a.pdf", CustomMetadata: ""},
			},
		},
	}
	h := setupKBTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/documents/doc-1/chunks", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	var body struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body = %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(body.Items))
	}
	if raw, present := body.Items[0]["custom_metadata"]; present {
		t.Fatalf("custom_metadata = %s, want omitted for empty JSONB string", raw)
	}
}

// TestKBRoutes_ListDocumentChunks_InvalidCustomMetadata asserts a chunk whose
// custom_metadata is not valid JSON fails with 500 INTERNAL instead of being
// silently dropped (SPEC §7.2 — invalid JSONB cannot occur with DB-constrained
// writes, so it signals corruption and must not pass through).
func TestKBRoutes_ListDocumentChunks_InvalidCustomMetadata(t *testing.T) {
	client := &fakeKBClient{
		listChunksResp: &kbv1.ListDocumentChunksResponse{
			Items: []*kbv1.KBChunk{
				{Id: "chunk-3", DocId: "doc-1", KbId: "kb-1", ChunkType: "child", Content: "x", FileName: "a.pdf",
					CustomMetadata: `{"source": `},
			},
		},
	}
	h := setupKBTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/documents/doc-1/chunks", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode())
	}
	var body map[string]any
	_ = json.Unmarshal(resp.Body(), &body)
	if body["code"] != "INTERNAL" {
		t.Fatalf("code = %v, want INTERNAL", body["code"])
	}
	// The raw JSONB string must not leak to the client (SPEC §7.2).
	if strings.Contains(string(resp.Body()), `"source"`) {
		t.Fatalf("response leaked raw JSONB payload: %s", resp.Body())
	}
}

// TestKBRoutes_ListDocumentChunks_DefaultLimitAndNotFound asserts the chunks
// handler applies the default limit of 50 and maps NOT_FOUND → 404 (SPEC
// §4.3 #11, §6.1).
func TestKBRoutes_ListDocumentChunks_DefaultLimitAndNotFound(t *testing.T) {
	client := &fakeKBClient{
		listChunksResp: &kbv1.ListDocumentChunksResponse{},
	}
	h := setupKBTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/documents/doc-1/chunks", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	if client.lastLimit != 50 {
		t.Fatalf("default limit = %d, want 50", client.lastLimit)
	}

	notFoundClient := &fakeKBClient{
		listChunksErr: status.Error(codes.NotFound, "doc missing"),
	}
	h = setupKBTestServer(notFoundClient)
	resp = ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/documents/doc-404/chunks", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode())
	}
	var errBody map[string]any
	_ = json.Unmarshal(resp.Body(), &errBody)
	if errBody["code"] != "NOT_FOUND" {
		t.Fatalf("code = %v, want NOT_FOUND", errBody["code"])
	}
}

// TestKBRoutes_ListDocumentChunks_LimitOutOfRange asserts limit > 100 returns
// 400 without calling kb-service (SPEC §4.3 #11 limit 1–100).
func TestKBRoutes_ListDocumentChunks_LimitOutOfRange(t *testing.T) {
	client := &fakeKBClient{}
	h := setupKBTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/documents/doc-1/chunks?limit=101", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode())
	}
	var body map[string]any
	_ = json.Unmarshal(resp.Body(), &body)
	if body["code"] != "BAD_REQUEST" {
		t.Fatalf("code = %v, want BAD_REQUEST", body["code"])
	}
	if client.lastTenantID != "" {
		t.Fatalf("kb-service was called (tenant = %q) despite 400 validation failure", client.lastTenantID)
	}
}

// TestKBRoutes_ListSessionMessages_Success verifies the messages handler maps
// the path params, query limit/cursor and the Auth tenant id into the gRPC
// request, and serializes source_chunks as the REST sources array (assistant
// messages) / null (user messages) per SPEC §3.2 + §5.4.
func TestKBRoutes_ListSessionMessages_Success(t *testing.T) {
	client := &fakeKBClient{
		sessionMsgsResp: &kbv1.GetSessionMessagesResponse{
			Items: []*kbv1.KBSessionMessage{
				{
					Id: "msg-1", SessionId: "sess-1", Role: "user", Content: "question",
				},
				{
					Id: "msg-2", SessionId: "sess-1", Role: "assistant", Content: "answer",
					SourceChunks: `[{"doc_id":"doc-1","file_name":"a.pdf","page":1,"content":"ctx","score":0.9}]`,
					InputTokens:  10, OutputTokens: 20, DurationMs: 1500,
				},
			},
			NextCursor: "msg-cursor-1",
		},
	}
	h := setupKBTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/sessions/sess-1/messages?limit=10&cursor=cur", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	if client.lastTenantID != "tenant-test" || client.lastKbID != "kb-1" || client.lastSessionID != "sess-1" {
		t.Fatalf("gRPC tenant/kb/session = %q/%q/%q, want tenant-test/kb-1/sess-1",
			client.lastTenantID, client.lastKbID, client.lastSessionID)
	}
	if client.lastLimit != 10 || client.lastCursor != "cur" {
		t.Fatalf("gRPC limit/cursor = %d/%q, want 10/cur", client.lastLimit, client.lastCursor)
	}
	var body struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode body = %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(body.Items))
	}
	// User message: sources serializes as null (no fabricated empty array).
	if userSources, present := body.Items[0]["sources"]; !present || userSources != nil {
		t.Fatalf("user message sources = %#v, want JSON null", userSources)
	}
	// Assistant message: sources is the unmarshalled array.
	assistantSources, ok := body.Items[1]["sources"].([]any)
	if !ok {
		t.Fatalf("assistant sources = %#v, want array", body.Items[1]["sources"])
	}
	if len(assistantSources) != 1 {
		t.Fatalf("assistant sources = %d, want 1", len(assistantSources))
	}
	firstSource, _ := assistantSources[0].(map[string]any)
	if firstSource["doc_id"] != "doc-1" || firstSource["file_name"] != "a.pdf" {
		t.Fatalf("assistant source = %+v, want doc_id=doc-1 file_name=a.pdf", firstSource)
	}
	if body.NextCursor != "msg-cursor-1" {
		t.Fatalf("next_cursor = %q, want msg-cursor-1", body.NextCursor)
	}
}

// TestKBRoutes_ListSessionMessages_InvalidSources asserts a message whose
// source_chunks is not valid JSON fails with 500 INTERNAL instead of being
// silently nulled (SPEC §7.2 — 序列化异常不应发生，DB 写入侧已约束，静默吞数据
// 会掩盖数据损坏).
func TestKBRoutes_ListSessionMessages_InvalidSources(t *testing.T) {
	client := &fakeKBClient{
		sessionMsgsResp: &kbv1.GetSessionMessagesResponse{
			Items: []*kbv1.KBSessionMessage{
				{Id: "msg-3", SessionId: "sess-1", Role: "assistant", Content: "answer",
					SourceChunks: `[{"doc_id":`},
			},
		},
	}
	h := setupKBTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/sessions/sess-1/messages", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode())
	}
	var body map[string]any
	_ = json.Unmarshal(resp.Body(), &body)
	if body["code"] != "INTERNAL" {
		t.Fatalf("code = %v, want INTERNAL", body["code"])
	}
	// The raw JSONB string must not leak to the client (SPEC §7.2).
	if strings.Contains(string(resp.Body()), `doc_id`) {
		t.Fatalf("response leaked raw JSONB payload: %s", resp.Body())
	}
}

// TestKBRoutes_ListSessionMessages_LimitOutOfRangeAndNotFound asserts limit >
// 100 returns 400 without calling kb-service, and NOT_FOUND maps to 404.
func TestKBRoutes_ListSessionMessages_LimitOutOfRangeAndNotFound(t *testing.T) {
	client := &fakeKBClient{}
	h := setupKBTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/sessions/sess-1/messages?limit=101", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode())
	}
	if client.lastTenantID != "" {
		t.Fatalf("kb-service was called (tenant = %q) despite 400 validation failure", client.lastTenantID)
	}

	notFoundClient := &fakeKBClient{
		sessionMsgsErr: status.Error(codes.NotFound, "session missing"),
	}
	h = setupKBTestServer(notFoundClient)
	resp = ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/sessions/sess-404/messages", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode())
	}
}

// TestKBRoutes_DeleteSession_SuccessAndNotFound verifies the delete handler
// returns 204 on success (idempotent semantics) and maps NOT_FOUND → 404,
// injecting the Auth tenant id and path params into the gRPC call.
func TestKBRoutes_DeleteSession_SuccessAndNotFound(t *testing.T) {
	client := &fakeKBClient{}
	h := setupKBTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodDelete,
		"/api/v1/svc/knowledge-bases/kb-1/sessions/sess-1", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode())
	}
	if client.lastTenantID != "tenant-test" || client.lastKbID != "kb-1" || client.lastSessionID != "sess-1" {
		t.Fatalf("gRPC tenant/kb/session = %q/%q/%q, want tenant-test/kb-1/sess-1",
			client.lastTenantID, client.lastKbID, client.lastSessionID)
	}

	notFoundClient := &fakeKBClient{
		deleteSessionErr: status.Error(codes.NotFound, "kb missing"),
	}
	h = setupKBTestServer(notFoundClient)
	resp = ut.PerformRequest(h.Engine, http.MethodDelete,
		"/api/v1/svc/knowledge-bases/kb-404/sessions/sess-1", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode())
	}
}

// TestKBRoutes_B2Handlers_NilClientReturn503 asserts the 3 B2 handlers
// return 503 UNAVAILABLE when kb-service is not configured (P0 semantics,
// issue-046 AC nil-client 503 守卫).
func TestKBRoutes_B2Handlers_NilClientReturn503(t *testing.T) {
	h := setupKBTestServer(nil)
	requests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/documents/doc-1/chunks"},
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/sessions/sess-1/messages"},
		{http.MethodDelete, "/api/v1/svc/knowledge-bases/kb-1/sessions/sess-1"},
	}
	for _, r := range requests {
		resp := ut.PerformRequest(h.Engine, r.method, r.path, nil,
			ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
		).Result()
		if resp.StatusCode() != http.StatusServiceUnavailable {
			t.Fatalf("%s %s status = %d, want 503", r.method, r.path, resp.StatusCode())
		}
		var body map[string]any
		_ = json.Unmarshal(resp.Body(), &body)
		if body["code"] != "UNAVAILABLE" {
			t.Fatalf("%s %s code = %v, want UNAVAILABLE", r.method, r.path, body["code"])
		}
	}
}

// TestKBRoutes_Citations_EnhancedFields asserts the KBCitation serialization
// includes the B2 enhancement fields message_id/session_id when present and
// omits them when empty (SPEC §4.3 #15, issue-046).
func TestKBRoutes_Citations_EnhancedFields(t *testing.T) {
	// kbCitationToJSON is the unit under test: it is the serialization path
	// shared by listKnowledgeBaseCitations for every KBCitation row.
	withFields := kbCitationToJSON(&kbv1.KBCitation{
		Id: "cit-1", KbId: "kb-1", DocId: "doc-1", FileName: "a.pdf",
		MessageId: "msg-1", SessionId: "sess-1",
	})
	if withFields.MessageID != "msg-1" || withFields.SessionID != "sess-1" {
		t.Fatalf("enhanced citation = %+v, want message_id=msg-1 session_id=sess-1", withFields)
	}
	encoded, err := json.Marshal(withFields)
	if err != nil {
		t.Fatalf("marshal citation = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode citation = %v", err)
	}
	if decoded["message_id"] != "msg-1" || decoded["session_id"] != "sess-1" {
		t.Fatalf("decoded citation = %+v, want message_id=msg-1 session_id=sess-1", decoded)
	}

	empty := kbCitationToJSON(&kbv1.KBCitation{Id: "cit-2", KbId: "kb-1", DocId: "doc-1", FileName: "a.pdf"})
	encoded, err = json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal citation = %v", err)
	}
	if strings.Contains(string(encoded), "message_id") || strings.Contains(string(encoded), "session_id") {
		t.Fatalf("empty citation = %s, want message_id/session_id omitted", encoded)
	}
}

// ── B3 handler tests (SPEC §4.3 #12, issue-048) ─────────────────────────────

// TestKBRoutes_ReparseDocument_Success verifies the reparse handler maps the
// JSON idempotency_key and the path params + Auth tenant id into the gRPC
// call and returns 202 with the AsyncTask JSON shape (SPEC §4.3 #12, §9.2).
func TestKBRoutes_ReparseDocument_Success(t *testing.T) {
	client := &fakeKBClient{
		reparseResp: &commonv1.AsyncTaskRef{
			TaskId: "task-12", TaskType: "kb.reparse", Status: "pending",
		},
	}
	h := setupKBTestServer(client)
	body := `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440006"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost,
		"/api/v1/svc/knowledge-bases/kb-1/documents/doc-1/reparse",
		&ut.Body{Body: strings.NewReader(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode())
	}
	if client.lastTenantID != "tenant-test" || client.lastKbID != "kb-1" ||
		client.lastDocID != "doc-1" || client.lastIDemKey != "550e8400-e29b-41d4-a716-446655440006" {
		t.Fatalf("gRPC tenant/kb/doc/idem = %q/%q/%q/%q, want tenant-test/kb-1/doc-1/550e8400-e29b-41d4-a716-446655440006",
			client.lastTenantID, client.lastKbID, client.lastDocID, client.lastIDemKey)
	}
	var respBody map[string]any
	if err := json.Unmarshal(resp.Body(), &respBody); err != nil {
		t.Fatalf("decode body = %v", err)
	}
	if respBody["task_id"] != "task-12" || respBody["task_type"] != "kb.reparse" || respBody["status"] != "pending" {
		t.Fatalf("body = %+v, want task_id=task-12 task_type=kb.reparse status=pending", respBody)
	}
}

// TestKBRoutes_ReparseDocument_GuardErrors asserts the kb-service guard
// errors map per SPEC §6.1: doc missing → 404 NOT_FOUND, doc=ready or
// KB=rebuilding → 409 CONFLICT (FAILED_PRECONDITION).
func TestKBRoutes_ReparseDocument_GuardErrors(t *testing.T) {
	cases := []struct {
		name       string
		grpcErr    error
		wantStatus int
		wantCode   string
	}{
		{"doc_not_found", status.Error(codes.NotFound, "doc missing"), http.StatusNotFound, "NOT_FOUND"},
		{"doc_ready", status.Error(codes.FailedPrecondition, "doc already ready"), http.StatusConflict, "CONFLICT"},
		{"kb_rebuilding", status.Error(codes.FailedPrecondition, "kb rebuilding"), http.StatusConflict, "CONFLICT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeKBClient{reparseErr: tc.grpcErr}
			h := setupKBTestServer(client)
			body := `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440006"}`
			resp := ut.PerformRequest(h.Engine, http.MethodPost,
				"/api/v1/svc/knowledge-bases/kb-1/documents/doc-1/reparse",
				&ut.Body{Body: strings.NewReader(body), Len: len(body)},
				ut.Header{Key: "Content-Type", Value: "application/json"},
				ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
			).Result()
			if resp.StatusCode() != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode(), tc.wantStatus)
			}
			var respBody map[string]any
			_ = json.Unmarshal(resp.Body(), &respBody)
			if respBody["code"] != tc.wantCode {
				t.Fatalf("code = %v, want %s", respBody["code"], tc.wantCode)
			}
		})
	}
}

// TestKBRoutes_ReparseDocument_MissingIdempotencyKey asserts a missing
// idempotency_key returns 400 without calling kb-service (SPEC §4.3 #12
// required uuid).
func TestKBRoutes_ReparseDocument_MissingIdempotencyKey(t *testing.T) {
	client := &fakeKBClient{}
	h := setupKBTestServer(client)
	body := `{"idempotency_key":""}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost,
		"/api/v1/svc/knowledge-bases/kb-1/documents/doc-1/reparse",
		&ut.Body{Body: strings.NewReader(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode())
	}
	var respBody map[string]any
	_ = json.Unmarshal(resp.Body(), &respBody)
	if respBody["code"] != "BAD_REQUEST" {
		t.Fatalf("code = %v, want BAD_REQUEST", respBody["code"])
	}
	if client.lastTenantID != "" {
		t.Fatalf("kb-service was called (tenant = %q) despite 400 validation failure", client.lastTenantID)
	}
}

// TestKBRoutes_ReparseDocument_InvalidJSON asserts a non-JSON request body is
// rejected with 400 before any validation or kb-service call (issue-048 §4.3
// #12 input guard).
func TestKBRoutes_ReparseDocument_InvalidJSON(t *testing.T) {
	client := &fakeKBClient{}
	h := setupKBTestServer(client)
	body := `not json`
	resp := ut.PerformRequest(h.Engine, http.MethodPost,
		"/api/v1/svc/knowledge-bases/kb-1/documents/doc-1/reparse",
		&ut.Body{Body: strings.NewReader(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode())
	}
	var respBody map[string]any
	_ = json.Unmarshal(resp.Body(), &respBody)
	if respBody["code"] != "BAD_REQUEST" {
		t.Fatalf("code = %v, want BAD_REQUEST", respBody["code"])
	}
	if client.lastTenantID != "" {
		t.Fatalf("kb-service was called (tenant = %q) despite 400 validation failure", client.lastTenantID)
	}
}

// TestKBRoutes_IdempotencyKey_MustBeUUID asserts every idempotent KB entry
// point rejects a non-uuid idempotency_key with 400 without calling
// kb-service (gateway-side format contract, mirrors OpenAPI format: uuid).
func TestKBRoutes_IdempotencyKey_MustBeUUID(t *testing.T) {
	requests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/v1/svc/knowledge-bases", `{"idempotency_key":"not-a-uuid","name":"kb"}`},
		{http.MethodPut, "/api/v1/svc/knowledge-bases/kb-1", `{"idempotency_key":"not-a-uuid","name":"renamed"}`},
		{http.MethodPost, "/api/v1/svc/knowledge-bases/kb-1/documents", `{"idempotency_key":"not-a-uuid","file_name":"a.pdf","file_type":"pdf"}`},
		{http.MethodPost, "/api/v1/svc/knowledge-bases/kb-1/query", `{"idempotency_key":"not-a-uuid","question":"hi"}`},
		{http.MethodPut, "/api/v1/svc/knowledge-bases/kb-1/permissions", `{"idempotency_key":"not-a-uuid"}`},
		{http.MethodPost, "/api/v1/svc/knowledge-bases/kb-1/documents/doc-1/reparse", `{"idempotency_key":"not-a-uuid"}`},
	}
	for _, r := range requests {
		client := &fakeKBClient{}
		h := setupKBTestServer(client)
		resp := ut.PerformRequest(h.Engine, r.method, r.path,
			&ut.Body{Body: strings.NewReader(r.body), Len: len(r.body)},
			ut.Header{Key: "Content-Type", Value: "application/json"},
			ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
		).Result()
		if resp.StatusCode() != http.StatusBadRequest {
			t.Fatalf("%s %s status = %d, want 400 (body %q)", r.method, r.path, resp.StatusCode(), r.body)
		}
		var respBody map[string]any
		_ = json.Unmarshal(resp.Body(), &respBody)
		if respBody["code"] != "BAD_REQUEST" {
			t.Fatalf("%s %s code = %v, want BAD_REQUEST", r.method, r.path, respBody["code"])
		}
		if client.lastTenantID != "" {
			t.Fatalf("%s %s: kb-service was called (tenant = %q) despite 400 validation failure",
				r.method, r.path, client.lastTenantID)
		}
	}
}

// TestKBRoutes_ReparseDocument_NilClientReturns503 asserts the reparse handler
// returns 503 UNAVAILABLE when kb-service is not configured (issue-048 AC
// nil-client 503 守卫).
func TestKBRoutes_ReparseDocument_NilClientReturns503(t *testing.T) {
	h := setupKBTestServer(nil)
	body := `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440006"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost,
		"/api/v1/svc/knowledge-bases/kb-1/documents/doc-1/reparse",
		&ut.Body{Body: strings.NewReader(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode())
	}
	var respBody map[string]any
	_ = json.Unmarshal(resp.Body(), &respBody)
	if respBody["code"] != "UNAVAILABLE" {
		t.Fatalf("code = %v, want UNAVAILABLE", respBody["code"])
	}
}
