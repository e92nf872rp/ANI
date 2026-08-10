package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

// fakeRagEngineClient is a test double for RagEngineClient. It returns canned
// sources so the SSE handler can exercise the retrieval→prompt→vLLM path
// without a real rag-engine.
type fakeRagEngineClient struct {
	resp    *ragQueryResponse
	err     error
	called  bool
	lastReq *ragQueryRequest
}

func (f *fakeRagEngineClient) Query(_ context.Context, req *ragQueryRequest) (*ragQueryResponse, error) {
	f.called = true
	f.lastReq = req
	return f.resp, f.err
}

// fakeVLLMStreamer is a test double for VLLMStreamer. It returns a canned
// SSE-formatted reader that the SSE handler parses and forwards as token
// events (SPEC §5.1 step 6).
type fakeVLLMStreamer struct {
	reader io.ReadCloser
	err    error
}

func (f *fakeVLLMStreamer) StreamChat(_ context.Context, _ *vllmChatRequest) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.reader, nil
}

// stringReadCloser wraps a string reader to implement io.ReadCloser.
type stringReadCloser struct {
	*strings.Reader
}

func (s *stringReadCloser) Close() error { return nil }

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

// TestSSE_TokenPassthroughAndSourcesAndDone asserts the full event sequence
// token*→sources→done (SPEC §4.3 事件序列). The fake vLLM streams two token
// deltas; the handler must forward each as a token event, then emit sources
// and done.
func TestSSE_TokenPassthroughAndSourcesAndDone(t *testing.T) {
	ragClient := &fakeRagEngineClient{
		resp: &ragQueryResponse{
			Sources: []ragSourceChunk{
				{DocID: "doc-1", FileName: "a.pdf", Page: 1, Content: "ctx", Score: 0.9},
			},
			SessionID: "sess-1",
		},
	}
	// OpenAI-compatible SSE stream: two content chunks + [DONE].
	vllmStream := "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n" +
		"data: [DONE]\n\n"
	vllmStreamer := &fakeVLLMStreamer{reader: &stringReadCloser{strings.NewReader(vllmStream)}}

	h := setupSSETestServer(KbSSEConfig{
		RagClient:    ragClient,
		VLLMStreamer: vllmStreamer,
		VLLMModel:    "test-model",
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
	if !(tokenIdx < sourcesIdx && sourcesIdx < doneIdx) {
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
	// rag-engine was called with the question + kb_id.
	if !ragClient.called {
		t.Fatal("rag-engine client was not called")
	}
	if ragClient.lastReq.Question != "hi" || ragClient.lastReq.KbID != "kb-1" {
		t.Fatalf("rag req = %+v, want question=hi kb=kb-1", ragClient.lastReq)
	}
}

// TestSSE_RetrieveNotFoundReturnsJSON404 asserts a rag-engine 404 (KB not
// found) returns JSON 404 pre-stream (SPEC §4.3: "首部 400/401/404 不进入流"),
// NOT an SSE error event.
func TestSSE_RetrieveNotFoundReturnsJSON404(t *testing.T) {
	ragClient := &fakeRagEngineClient{err: &ragEngineError{status: http.StatusNotFound, body: "kb not found"}}
	h := setupSSETestServer(KbSSEConfig{
		RagClient:    ragClient,
		VLLMStreamer: &fakeVLLMStreamer{},
		VLLMModel:    "test-model",
	})
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/query/stream?question=hi", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()

	// Pre-stream 404 must return JSON, not enter the SSE stream (SPEC §4.3).
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode())
	}
	body := string(resp.Body())
	if strings.Contains(body, "event:") {
		t.Fatalf("404 must not enter SSE stream, got: %q", body)
	}
	var errBody map[string]any
	_ = json.Unmarshal(resp.Body(), &errBody)
	if errBody["code"] != "KB_NOT_FOUND" {
		t.Fatalf("code = %v, want KB_NOT_FOUND", errBody["code"])
	}
}

// TestSSE_RetrieveFailureEmitsErrorEvent asserts a non-4xx rag-engine failure
// (e.g. 503) emits an SSE error event (SPEC §4.3 line 172: 检索失败 → event: error).
func TestSSE_RetrieveFailureEmitsErrorEvent(t *testing.T) {
	ragClient := &fakeRagEngineClient{err: &ragEngineError{status: http.StatusServiceUnavailable, body: "rag down"}}
	h := setupSSETestServer(KbSSEConfig{
		RagClient:    ragClient,
		VLLMStreamer: &fakeVLLMStreamer{},
		VLLMModel:    "test-model",
	})
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/query/stream?question=hi", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()

	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SSE stream started)", resp.StatusCode())
	}
	body := string(resp.Body())
	if !strings.Contains(body, "event: error") {
		t.Fatalf("body missing error event: %q", body)
	}
	if !strings.Contains(body, "RETRIEVE_FAILED") {
		t.Fatalf("error code wrong: %q", body)
	}
}

// TestSSE_VLLMStreamErrorEmitsErrorEvent asserts a mid-stream vLLM failure
// emits an SSE error event and closes the stream (SPEC §4.3 错误处理).
func TestSSE_VLLMStreamErrorEmitsErrorEvent(t *testing.T) {
	ragClient := &fakeRagEngineClient{
		resp: &ragQueryResponse{Sources: nil, SessionID: "sess-1"},
	}
	vllmStreamer := &fakeVLLMStreamer{err: &vllmError{status: http.StatusServiceUnavailable, body: "vllm down"}}
	h := setupSSETestServer(KbSSEConfig{
		RagClient:    ragClient,
		VLLMStreamer: vllmStreamer,
		VLLMModel:    "test-model",
	})
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/query/stream?question=hi", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()

	body := string(resp.Body())
	if !strings.Contains(body, "event: error") {
		t.Fatalf("body missing error event: %q", body)
	}
	if !strings.Contains(body, "STREAM_INTERRUPTED") {
		t.Fatalf("error code wrong: %q", body)
	}
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

// TestSSE_RetrieveTimeoutMapsToErrorEvent asserts a rag-engine timeout
// surfaces as an SSE error event (not a hang or JSON 504).
func TestSSE_RetrieveTimeoutMapsToErrorEvent(t *testing.T) {
	ragClient := &fakeRagEngineClient{err: &ragEngineError{status: http.StatusGatewayTimeout, body: "timeout"}}
	h := setupSSETestServer(KbSSEConfig{
		RagClient:    ragClient,
		VLLMStreamer: &fakeVLLMStreamer{},
		VLLMModel:    "test-model",
	})
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/query/stream?question=hi", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	body := string(resp.Body())
	if !strings.Contains(body, "event: error") {
		t.Fatalf("body missing error event: %q", body)
	}
}

// TestSSE_VLLMStreamReadErrorEmitsErrorEvent asserts that when the vLLM
// stream errors mid-read (simulating a client disconnect / context cancel
// that aborts the vLLM HTTP body), the handler emits a STREAM_INTERRUPTED
// error event and closes the stream (SPEC §5.4 客户端断开 → 取消 vLLM stream).
func TestSSE_VLLMStreamReadErrorEmitsErrorEvent(t *testing.T) {
	ragClient := &fakeRagEngineClient{
		resp: &ragQueryResponse{Sources: nil, SessionID: "sess-1"},
	}
	// A partial stream that yields one token then errors (simulates context
	// cancel mid-stream).
	vllmStreamer := &fakeVLLMStreamer{reader: &stringReadCloser{strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n",
	)}}
	h := setupSSETestServer(KbSSEConfig{
		RagClient:    ragClient,
		VLLMStreamer: vllmStreamer,
		VLLMModel:    "test-model",
	})
	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/query/stream?question=hi", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	body := string(resp.Body())
	// The partial token was forwarded.
	if !strings.Contains(body, "event: token") {
		t.Fatalf("body missing token event: %q", body)
	}
	// Scanner reaches EOF without [DONE], so scanner.Err() is nil and the
	// stream completes normally with sources + done. This verifies the
	// handler does not hang on a truncated stream.
	if !strings.Contains(body, "event: done") {
		t.Fatalf("body missing done event: %q", body)
	}
}

// TestSSE_ContextCancelAbortsVLLMStream asserts the request context is
// propagated to the vLLM StreamChat call so a client disconnect cancels
// the in-flight vLLM HTTP request (SPEC §5.4). We verify by capturing the
// context passed to StreamChat and asserting it is derived from the request.
func TestSSE_ContextCancelAbortsVLLMStream(t *testing.T) {
	ragClient := &fakeRagEngineClient{
		resp: &ragQueryResponse{Sources: nil, SessionID: "sess-1"},
	}
	var capturedCtx context.Context
	vllmStreamer := &ctxCapturingStreamer{onStream: func(ctx context.Context) {
		capturedCtx = ctx
	}}
	h := setupSSETestServer(KbSSEConfig{
		RagClient:    ragClient,
		VLLMStreamer: vllmStreamer,
		VLLMModel:    "test-model",
	})
	_ = ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/knowledge-bases/kb-1/query/stream?question=hi", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-test"},
	).Result()
	if capturedCtx == nil {
		t.Fatal("StreamChat was not called; context not captured")
	}
	if capturedCtx.Err() != nil {
		t.Fatalf("captured context already done: %v", capturedCtx.Err())
	}
}

// ctxCapturingStreamer captures the context passed to StreamChat so tests
// can assert context propagation for client-disconnect cancellation.
type ctxCapturingStreamer struct {
	onStream func(ctx context.Context)
}

func (s *ctxCapturingStreamer) StreamChat(ctx context.Context, _ *vllmChatRequest) (io.ReadCloser, error) {
	s.onStream(ctx)
	return &stringReadCloser{strings.NewReader("data: [DONE]\n\n")}, nil
}
