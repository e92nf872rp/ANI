package router

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"

	"google.golang.org/grpc/metadata"

	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	kbv1 "github.com/kubercloud/ani/pkg/generated/pb/kb/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

// setupSSETestServer builds a gateway with the SSE handler wired to the
// given fake backends so tests can assert the token→sources→done sequence.
func setupSSETestServer(sseCfg KbSSEConfig) *server.Hertz {
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
	registerKnowledgeBasesWithClient(svc, nil, sseCfg)
	return h
}

// TestSSE_NoBackendsDegradesToSourcesAndDone asserts the handler degrades
// gracefully when no backends are configured: it emits sources=[] + done
// without token events, so the endpoint stays functional (SPEC §5.4).
func TestSSE_NoBackendsDegradesToSourcesAndDone(t *testing.T) {
	h := setupSSETestServer(KbSSEConfig{})
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/query/stream?question=hi", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()

	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	body := string(resp.Body())
	if !strings.Contains(body, "event: sources") {
		t.Fatalf("body missing sources event: %q", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("body missing done event: %q", body)
	}
	if strings.Contains(body, "event: token") {
		t.Fatalf("body should not contain token events: %q", body)
	}
}

// TestSSE_QuestionTooLongReturns400 asserts the 2000-char limit (SPEC §4.3).
func TestSSE_QuestionTooLongReturns400(t *testing.T) {
	h := setupSSETestServer(KbSSEConfig{})
	longQ := strings.Repeat("a", 2001)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/query/stream?question="+longQ, nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode())
	}
}

// ── New path (issue-038/039) test doubles ──────────────────────────────────

// fakeKBRetrieveClient implements KBGRPCClient for the new SSE path tests.
// Only Retrieve is functional; all other methods panic to catch misuse.
type fakeKBRetrieveClient struct {
	stream  kbv1.KBService_RetrieveClient
	err     error
	called  bool
	lastReq *kbv1.RetrieveRequest
}

func (f *fakeKBRetrieveClient) Retrieve(_ context.Context, tenantID string, kbID string, req *kbv1.RetrieveRequest) (kbv1.KBService_RetrieveClient, error) {
	f.called = true
	// Mirror real kbGRPCClient.Retrieve: set tenant_id and kb_id on the request.
	req.TenantId = tenantID
	req.KbId = kbID
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return f.stream, nil
}

// Unused methods — return zero values to satisfy the interface.
func (f *fakeKBRetrieveClient) CreateKB(context.Context, string, string, *kbv1.CreateKBRequest) (*kbv1.KnowledgeBase, error) {
	return nil, nil
}
func (f *fakeKBRetrieveClient) GetKB(context.Context, string, string) (*kbv1.KnowledgeBase, error) {
	return nil, nil
}
func (f *fakeKBRetrieveClient) ListKBs(context.Context, string, int32, string) (*kbv1.ListKBsResponse, error) {
	return nil, nil
}
func (f *fakeKBRetrieveClient) DeleteKB(context.Context, string, string) (*emptypb.Empty, error) {
	return nil, nil
}
func (f *fakeKBRetrieveClient) GetDocumentUploadURL(context.Context, string, string, string, *kbv1.GetDocumentUploadURLRequest) (*kbv1.GetDocumentUploadURLResponse, error) {
	return nil, nil
}
func (f *fakeKBRetrieveClient) NotifyDocumentUploaded(context.Context, string, string, string, string) (*commonv1.AsyncTaskRef, error) {
	return nil, nil
}
func (f *fakeKBRetrieveClient) GetDocument(context.Context, string, string, string) (*kbv1.KBDocument, error) {
	return nil, nil
}
func (f *fakeKBRetrieveClient) ListDocuments(context.Context, string, string, string, int32, string) (*kbv1.ListDocumentsResponse, error) {
	return nil, nil
}
func (f *fakeKBRetrieveClient) DeleteDocument(context.Context, string, string, string) (*emptypb.Empty, error) {
	return nil, nil
}
func (f *fakeKBRetrieveClient) Query(context.Context, string, string, string, *kbv1.QueryRequest) (*kbv1.QueryResponse, error) {
	return nil, nil
}
func (f *fakeKBRetrieveClient) ListKBCitations(context.Context, string, string, int32, string) (*kbv1.ListKBCitationsResponse, error) {
	return nil, nil
}
func (f *fakeKBRetrieveClient) ListKBSessions(context.Context, string, string, int32, string) (*kbv1.ListKBSessionsResponse, error) {
	return nil, nil
}
func (f *fakeKBRetrieveClient) UpdateKBPermissions(context.Context, string, string, string, *kbv1.UpdateKBPermissionsRequest) (*kbv1.KnowledgeBase, error) {
	return nil, nil
}

// fakeRetrieveStream implements kbv1.KBService_RetrieveClient for tests.
// It replays a canned list of RetrieveEvent messages, then returns io.EOF.
type fakeRetrieveStream struct {
	events []*kbv1.RetrieveEvent
	idx    int
}

func (s *fakeRetrieveStream) Recv() (*kbv1.RetrieveEvent, error) {
	if s.idx >= len(s.events) {
		return nil, io.EOF
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, nil
}

// grpc.ClientStream methods — no-ops for test purposes.
func (s *fakeRetrieveStream) Header() (metadata.MD, error) { return nil, nil }
func (s *fakeRetrieveStream) Trailer() metadata.MD         { return nil }
func (s *fakeRetrieveStream) CloseSend() error             { return nil }
func (s *fakeRetrieveStream) Context() context.Context     { return context.Background() }
func (s *fakeRetrieveStream) SendMsg(interface{}) error    { return nil }
func (s *fakeRetrieveStream) RecvMsg(interface{}) error {
	if s.idx >= len(s.events) {
		return io.EOF
	}
	return nil
}

// Ensure fakeRetrieveStream satisfies the interface.
var _ kbv1.KBService_RetrieveClient = (*fakeRetrieveStream)(nil)

// TestSSE_NewPath_TokenSourcesDone asserts the new path (kb-service Retrieve
// gRPC stream) produces the same token*→sources→done event sequence as the
// legacy path (Plan §10.3, issue-038 AC 7).
func TestSSE_NewPath_TokenSourcesDone(t *testing.T) {
	stream := &fakeRetrieveStream{
		events: []*kbv1.RetrieveEvent{
			{Event: &kbv1.RetrieveEvent_Token{Token: &kbv1.RetrieveTokenEvent{Content: "Hel"}}},
			{Event: &kbv1.RetrieveEvent_Token{Token: &kbv1.RetrieveTokenEvent{Content: "lo"}}},
			{Event: &kbv1.RetrieveEvent_Sources{Sources: &kbv1.RetrieveSourcesEvent{
				Sources: []*kbv1.SourceChunk{
					{DocId: "doc-1", FileName: "a.pdf", Page: 1, Content: "ctx", Score: 0.9},
				},
			}}},
			{Event: &kbv1.RetrieveEvent_Done{Done: &kbv1.RetrieveDoneEvent{
				InputTokens: 10, OutputTokens: 20, SessionId: "sess-1",
			}}},
		},
	}
	kbClient := &fakeKBRetrieveClient{stream: stream}
	h := setupSSETestServer(KbSSEConfig{
		KBClient: kbClient,
	})
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/query/stream?question=hi", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()

	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	if ct := string(resp.Header.ContentType()); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
	body := string(resp.Body())

	// Event sequence: token* → sources → done (SPEC §4.3).
	tokenIdx := strings.Index(body, "event: token")
	sourcesIdx := strings.Index(body, "event: sources")
	doneIdx := strings.Index(body, "event: done")
	if tokenIdx < 0 {
		t.Fatalf("body missing token event: %q", body)
	}
	if sourcesIdx < 0 {
		t.Fatalf("body missing sources event: %q", body)
	}
	if doneIdx < 0 {
		t.Fatalf("body missing done event: %q", body)
	}
	if tokenIdx >= sourcesIdx || sourcesIdx >= doneIdx {
		t.Fatalf("event order wrong: token=%d sources=%d done=%d", tokenIdx, sourcesIdx, doneIdx)
	}
	// Two token events expected.
	if got := strings.Count(body, "event: token"); got != 2 {
		t.Fatalf("token events = %d, want 2", got)
	}
	// Sources event contains the doc_id.
	if !strings.Contains(body, "doc-1") {
		t.Fatalf("sources event missing doc-1: %q", body)
	}
	// kb-service Retrieve was called.
	if !kbClient.called {
		t.Fatal("kb-service Retrieve was not called")
	}
	if kbClient.lastReq.GetQuestion() != "hi" || kbClient.lastReq.GetKbId() != "kb-1" {
		t.Fatalf("retrieve req = %+v, want question=hi kb=kb-1", kbClient.lastReq)
	}
}

// TestSSE_NewPath_NoClientDegrades asserts the new path degrades to an empty
// stream (sources=[] + done) when KBClient is nil (SPEC §5.4, issue-038 AC 8).
func TestSSE_NewPath_NoClientDegrades(t *testing.T) {
	h := setupSSETestServer(KbSSEConfig{
		// KBClient is nil → degrade to empty stream.
	})
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/query/stream?question=hi", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()

	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	body := string(resp.Body())
	if !strings.Contains(body, "event: sources") {
		t.Fatalf("body missing sources event: %q", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("body missing done event: %q", body)
	}
	if strings.Contains(body, "event: token") {
		t.Fatalf("body should not contain token events: %q", body)
	}
}

// TestSSE_NewPath_QuestionTooLongReturns400 asserts the 2000-char limit
// works on the new path (SPEC §4.3).
func TestSSE_NewPath_QuestionTooLongReturns400(t *testing.T) {
	h := setupSSETestServer(KbSSEConfig{
		KBClient: &fakeKBRetrieveClient{},
	})
	longQ := strings.Repeat("a", 2001)
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/query/stream?question="+longQ, nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode())
	}
}
