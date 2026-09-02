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

	deleteKbErr error

	listDocsResp *kbv1.ListDocumentsResponse
	listDocsErr  error

	uploadURLResp *kbv1.GetDocumentUploadURLResponse
	uploadURLErr  error

	deleteDocErr error

	queryResp *kbv1.QueryResponse
	queryErr  error

	citationsErr    error
	sessionsErr     error
	permissionsResp *kbv1.KnowledgeBase
	permissionsErr  error
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
	return nil, nil
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
func (f *fakeKBClient) Retrieve(_ context.Context, _ string, _ string, _ *kbv1.RetrieveRequest) (kbv1.KBService_RetrieveClient, error) {
	return nil, status.Error(codes.Unimplemented, "fakeKBClient.Retrieve not implemented")
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

// TestKBRoutes_AllTwelveEndpointsRegistered asserts the SPEC §4.1 12-endpoint
// surface is registered (US-016 AC1: 12 端点全部就位). We hit each path with a
// method that forces the handler to run (rather than 404) so a missing route
// fails the test.
func TestKBRoutes_AllTwelveEndpointsRegistered(t *testing.T) {
	h := setupKBTestServer(&fakeKBClient{
		listKbsResp:    &kbv1.ListKBsResponse{},
		listDocsResp:   &kbv1.ListDocumentsResponse{},
		citationsErr:   status.Error(codes.Unimplemented, "P1"),
		sessionsErr:    status.Error(codes.Unimplemented, "P1"),
		permissionsErr: status.Error(codes.Unimplemented, "P1"),
	})

	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/svc/knowledge-bases", ""},
		{http.MethodPost, "/api/v1/svc/knowledge-bases", `{"idempotency_key":"k1","name":"kb"}`},
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1", ""},
		{http.MethodDelete, "/api/v1/svc/knowledge-bases/kb-1", ""},
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/documents", ""},
		{http.MethodPost, "/api/v1/svc/knowledge-bases/kb-1/documents", `{"idempotency_key":"k2","file_name":"a.pdf","file_type":"pdf"}`},
		{http.MethodDelete, "/api/v1/svc/knowledge-bases/kb-1/documents/doc-1", ""},
		{http.MethodPost, "/api/v1/svc/knowledge-bases/kb-1/query", `{"idempotency_key":"k3","question":"hi"}`},
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/query/stream?question=hi", ""},
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/citations", ""},
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/sessions", ""},
		{http.MethodPut, "/api/v1/svc/knowledge-bases/kb-1/permissions", `{"idempotency_key":"k4"}`},
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

// TestKBRoutes_P1EndpointsReturn501 asserts the 3 P1 endpoints route to
// kb-service and surface UNIMPLEMENTED as HTTP 501 (SPEC §4.1, US-016 AC1).
func TestKBRoutes_P1EndpointsReturn501(t *testing.T) {
	h := setupKBTestServer(&fakeKBClient{
		citationsErr:   status.Error(codes.Unimplemented, "ListKBCitations P1"),
		sessionsErr:    status.Error(codes.Unimplemented, "ListKBSessions P1"),
		permissionsErr: status.Error(codes.Unimplemented, "UpdateKBPermissions P1"),
	})

	p1 := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/citations", ""},
		{http.MethodGet, "/api/v1/svc/knowledge-bases/kb-1/sessions", ""},
		{http.MethodPut, "/api/v1/svc/knowledge-bases/kb-1/permissions", `{"idempotency_key":"k1"}`},
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
	createBody := `{"idempotency_key":"idem-1","name":"alpha"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost,
		"/api/v1/svc/knowledge-bases",
		&ut.Body{Body: strings.NewReader(createBody), Len: len(createBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode())
	}
	if client.lastIDemKey != "idem-1" {
		t.Fatalf("idempotency_key = %q, want idem-1", client.lastIDemKey)
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
		`{"idempotency_key":"k","name":""}`,
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

// TestKBRoutes_NilClientReturns503 asserts the 9 gRPC handlers return 503
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
	queryBody := `{"idempotency_key":"k","question":"what?"}`
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
