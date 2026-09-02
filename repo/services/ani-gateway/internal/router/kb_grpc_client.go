package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	kbv1 "github.com/kubercloud/ani/pkg/generated/pb/kb/v1"
)

// queryRPCTimeout is the per-call deadline for the synchronous Query RPC.
// Query triggers an LLM RAG call downstream (kb-service → rag-engine), which is
// far slower than the generic 5s per-RPC timeout. The RAG path embeds the
// query, hits Milvus + pg_trgm, fuses, and then runs a full LLM generation on
// a remote vLLM (e.g. Qwen3.6-35B-A3B). For long documents + sizable outputs
// this routinely reaches 30-70s and degrades further when the remote vLLM is
// loaded, so a 30s cap caused intermittent DEADLINE_EXCEEDED (HTTP 504) on
// otherwise-successful RAG answers. 120s gives the sync query a safe budget
// while still bounding a hung rag-engine (SPEC §5.1 含超时).
const queryRPCTimeout = 120 * time.Second

// KBGRPCClient wraps the generated KBServiceClient with a typed surface and
// gRPC-status→HTTP-status mapping. The gateway handlers depend on this
// interface (not the concrete grpc.ClientConn) so tests can inject a fake.
//
// All RPC methods inject the tenant_id from the gateway Auth middleware into
// the gRPC request; handlers do not trust the client-supplied tenant_id field
// for cross-tenant isolation (SPEC §7.1 租户注入).
type KBGRPCClient interface {
	CreateKB(ctx context.Context, tenantID string, idempotencyKey string, req *kbv1.CreateKBRequest) (*kbv1.KnowledgeBase, error)
	GetKB(ctx context.Context, tenantID string, kbID string) (*kbv1.KnowledgeBase, error)
	ListKBs(ctx context.Context, tenantID string, limit int32, cursor string) (*kbv1.ListKBsResponse, error)
	DeleteKB(ctx context.Context, tenantID string, kbID string) (*emptypb.Empty, error)
	GetDocumentUploadURL(ctx context.Context, tenantID string, kbID string, idempotencyKey string, req *kbv1.GetDocumentUploadURLRequest) (*kbv1.GetDocumentUploadURLResponse, error)
	NotifyDocumentUploaded(ctx context.Context, tenantID string, kbID string, docID string, storagePath string) (*commonv1.AsyncTaskRef, error)
	GetDocument(ctx context.Context, tenantID string, kbID string, docID string) (*kbv1.KBDocument, error)
	ListDocuments(ctx context.Context, tenantID string, kbID string, parseStatus string, limit int32, cursor string) (*kbv1.ListDocumentsResponse, error)
	DeleteDocument(ctx context.Context, tenantID string, kbID string, docID string) (*emptypb.Empty, error)
	Query(ctx context.Context, tenantID string, kbID string, idempotencyKey string, req *kbv1.QueryRequest) (*kbv1.QueryResponse, error)
	// Retrieve opens a server-streaming Retrieve RPC (Plan §10.2, issue-038).
	// Returns a stream the caller iterates to receive RetrieveEvent messages
	// (token* → sources → done). The caller owns the stream lifecycle and must
	// read until EOF or error. The context controls the per-call deadline.
	Retrieve(ctx context.Context, tenantID string, kbID string, req *kbv1.RetrieveRequest) (kbv1.KBService_RetrieveClient, error)
	ListKBCitations(ctx context.Context, tenantID string, kbID string, limit int32, cursor string) (*kbv1.ListKBCitationsResponse, error)
	ListKBSessions(ctx context.Context, tenantID string, kbID string, limit int32, cursor string) (*kbv1.ListKBSessionsResponse, error)
	UpdateKBPermissions(ctx context.Context, tenantID string, kbID string, idempotencyKey string, req *kbv1.UpdateKBPermissionsRequest) (*kbv1.KnowledgeBase, error)
}

// kbGRPCClient is the production implementation backed by a gRPC ClientConn.
// It mirrors the established auth_client.go pattern: a per-call timeout is
// applied to every RPC (SPEC §5.1 "含超时") so a slow/hung kb-service cannot
// indefinitely hold gateway request goroutines; a deadline-exceeded surfaces
// as HTTP 504 DEADLINE_EXCEEDED via mapGRPCError.
type kbGRPCClient struct {
	client  kbv1.KBServiceClient
	timeout time.Duration
}

// DialKBGRPC creates a gRPC client for kb-service. It uses the non-blocking
// grpc.NewClient (matching the auth_client.go pattern) so gateway startup is
// not delayed when kb-service is unreachable; the connection is established
// lazily and unreachable errors surface as 503 UNAVAILABLE on the first RPC.
// Callers own the returned conn and must Close it on shutdown.
func DialKBGRPC(ctx context.Context, addr string, timeout time.Duration) (*grpc.ClientConn, KBGRPCClient, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, nil, fmt.Errorf("kb-service gRPC address is empty")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial kb-service %s: %w", addr, err)
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return conn, &kbGRPCClient{client: kbv1.NewKBServiceClient(conn), timeout: timeout}, nil
}

func NewKBGRPCClientFromConn(conn *grpc.ClientConn) KBGRPCClient {
	if conn == nil {
		return nil
	}
	return &kbGRPCClient{client: kbv1.NewKBServiceClient(conn), timeout: 5 * time.Second}
}

// callCtx wraps the caller ctx with the client's per-call timeout, matching
// the auth_client.go pattern (callCtx, cancel := context.WithTimeout(...)).
func (c *kbGRPCClient) callCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.timeout)
}

func (c *kbGRPCClient) CreateKB(ctx context.Context, tenantID, idempotencyKey string, req *kbv1.CreateKBRequest) (*kbv1.KnowledgeBase, error) {
	if req == nil {
		req = &kbv1.CreateKBRequest{}
	}
	req.TenantId = tenantID
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.CreateKB(callCtx, req)
}

func (c *kbGRPCClient) GetKB(ctx context.Context, tenantID, kbID string) (*kbv1.KnowledgeBase, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.GetKB(callCtx, &kbv1.GetKBRequest{TenantId: tenantID, KbId: kbID})
}

func (c *kbGRPCClient) ListKBs(ctx context.Context, tenantID string, limit int32, cursor string) (*kbv1.ListKBsResponse, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.ListKBs(callCtx, &kbv1.ListKBsRequest{
		TenantId: tenantID,
		Page:     &commonv1.CursorPageRequest{Limit: limit, Cursor: cursor},
	})
}

func (c *kbGRPCClient) DeleteKB(ctx context.Context, tenantID, kbID string) (*emptypb.Empty, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.DeleteKB(callCtx, &kbv1.DeleteKBRequest{TenantId: tenantID, KbId: kbID})
}

func (c *kbGRPCClient) GetDocumentUploadURL(ctx context.Context, tenantID, kbID, idempotencyKey string, req *kbv1.GetDocumentUploadURLRequest) (*kbv1.GetDocumentUploadURLResponse, error) {
	if req == nil {
		req = &kbv1.GetDocumentUploadURLRequest{}
	}
	req.TenantId = tenantID
	req.KbId = kbID
	req.IdempotencyKey = idempotencyKey
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.GetDocumentUploadURL(callCtx, req)
}

func (c *kbGRPCClient) NotifyDocumentUploaded(ctx context.Context, tenantID, kbID, docID, storagePath string) (*commonv1.AsyncTaskRef, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.NotifyDocumentUploaded(callCtx, &kbv1.NotifyDocumentUploadedRequest{
		TenantId:    tenantID,
		KbId:        kbID,
		DocId:       docID,
		StoragePath: storagePath,
	})
}

func (c *kbGRPCClient) GetDocument(ctx context.Context, tenantID, kbID, docID string) (*kbv1.KBDocument, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.GetDocument(callCtx, &kbv1.GetDocumentRequest{TenantId: tenantID, KbId: kbID, DocId: docID})
}

func (c *kbGRPCClient) ListDocuments(ctx context.Context, tenantID, kbID, parseStatus string, limit int32, cursor string) (*kbv1.ListDocumentsResponse, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.ListDocuments(callCtx, &kbv1.ListDocumentsRequest{
		TenantId:    tenantID,
		KbId:        kbID,
		ParseStatus: parseStatus,
		Page:        &commonv1.CursorPageRequest{Limit: limit, Cursor: cursor},
	})
}

func (c *kbGRPCClient) DeleteDocument(ctx context.Context, tenantID, kbID, docID string) (*emptypb.Empty, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.DeleteDocument(callCtx, &kbv1.DeleteDocumentRequest{TenantId: tenantID, KbId: kbID, DocId: docID})
}

func (c *kbGRPCClient) Query(ctx context.Context, tenantID, kbID, idempotencyKey string, req *kbv1.QueryRequest) (*kbv1.QueryResponse, error) {
	if req == nil {
		req = &kbv1.QueryRequest{}
	}
	req.TenantId = tenantID
	req.KbId = kbID
	req.IdempotencyKey = idempotencyKey
	// Query performs an LLM RAG call downstream (kb-service → rag-engine), so
	// it needs a longer budget than the generic 5s per-RPC timeout (SPEC §5.1).
	// queryRPCTimeout (120s) covers the full retrieve → LLM generate chain.
	queryCtx, cancel := context.WithTimeout(ctx, queryRPCTimeout)
	defer cancel()
	return c.client.Query(queryCtx, req)
}

// Retrieve opens a server-streaming Retrieve RPC to kb-service (Plan §10.2,
// issue-038). The caller iterates the returned stream to receive RetrieveEvent
// messages (token* → sources → done). The per-call timeout uses queryRPCTimeout
// (120s) because Retrieve triggers an LLM generation downstream, matching the
// Query RPC budget. The caller must read the stream to completion (EOF or error)
// and is responsible for cancelling the context on client disconnect.
func (c *kbGRPCClient) Retrieve(ctx context.Context, tenantID, kbID string, req *kbv1.RetrieveRequest) (kbv1.KBService_RetrieveClient, error) {
	if req == nil {
		req = &kbv1.RetrieveRequest{}
	}
	req.TenantId = tenantID
	req.KbId = kbID
	// Retrieve streams tokens from an LLM downstream, so it needs the same
	// long budget as Query (120s). The stream stays open for the duration
	// of generation; the caller's context handles client-disconnect cancel.
	retrieveCtx, cancel := context.WithTimeout(ctx, queryRPCTimeout)
	stream, err := c.client.Retrieve(retrieveCtx, req)
	if err != nil {
		cancel()
		return nil, err
	}
	// Wrap the stream so CloseSend also cancels the timeout context,
	// preventing a leaked timer if the caller abandons the stream early.
	return &retrieveStreamWrapper{stream: stream, cancel: cancel}, nil
}

// retrieveStreamWrapper wraps KBService_RetrieveClient to cancel the per-call
// context when the caller closes the stream (or it reaches EOF). This prevents
// a leaked timeout timer if the caller stops reading before the stream ends.
type retrieveStreamWrapper struct {
	stream kbv1.KBService_RetrieveClient
	cancel context.CancelFunc
}

func (w *retrieveStreamWrapper) Recv() (*kbv1.RetrieveEvent, error) {
	ev, err := w.stream.Recv()
	if err != nil {
		w.cancel()
	}
	return ev, err
}

func (w *retrieveStreamWrapper) Header() (metadata.MD, error) { return w.stream.Header() }
func (w *retrieveStreamWrapper) Trailer() metadata.MD         { return w.stream.Trailer() }
func (w *retrieveStreamWrapper) CloseSend() error {
	err := w.stream.CloseSend()
	w.cancel()
	return err
}

func (w *retrieveStreamWrapper) Context() context.Context { return w.stream.Context() }
func (w *retrieveStreamWrapper) SendMsg(m interface{}) error {
	return w.stream.SendMsg(m)
}

func (w *retrieveStreamWrapper) RecvMsg(m interface{}) error {
	err := w.stream.RecvMsg(m)
	if err != nil {
		w.cancel()
	}
	return err
}

func (c *kbGRPCClient) ListKBCitations(ctx context.Context, tenantID, kbID string, limit int32, cursor string) (*kbv1.ListKBCitationsResponse, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.ListKBCitations(callCtx, &kbv1.ListKBCitationsRequest{
		TenantId: tenantID,
		KbId:     kbID,
		Page:     &commonv1.CursorPageRequest{Limit: limit, Cursor: cursor},
	})
}

func (c *kbGRPCClient) ListKBSessions(ctx context.Context, tenantID, kbID string, limit int32, cursor string) (*kbv1.ListKBSessionsResponse, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.ListKBSessions(callCtx, &kbv1.ListKBSessionsRequest{
		TenantId: tenantID,
		KbId:     kbID,
		Page:     &commonv1.CursorPageRequest{Limit: limit, Cursor: cursor},
	})
}

func (c *kbGRPCClient) UpdateKBPermissions(ctx context.Context, tenantID, kbID, idempotencyKey string, req *kbv1.UpdateKBPermissionsRequest) (*kbv1.KnowledgeBase, error) {
	if req == nil {
		req = &kbv1.UpdateKBPermissionsRequest{}
	}
	req.TenantId = tenantID
	req.KbId = kbID
	req.IdempotencyKey = idempotencyKey
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.UpdateKBPermissions(callCtx, req)
}

// kbError is the structured error produced by mapGRPCError. Handlers convert
// it to the unified Core/Services error shape via writeKBError.
type kbError struct {
	httpStatus int
	code       string
	message    string
}

func (e *kbError) Error() string {
	return fmt.Sprintf("kb-service gRPC error: %s: %s", e.code, e.message)
}

// mapGRPCError converts a gRPC status error to a kbError with the HTTP status
// and error code matching SPEC §5.1 step 4:
//
//	NOT_FOUND=404, INVALID_ARGUMENT=400, UNIMPLEMENTED=501,
//	FAILED_PRECONDITION=409, UNAVAILABLE=503, PERMISSION_DENIED=403,
//	DEADLINE_EXCEEDED=504.
//
// Unknown codes map to 500 so we never mask a server fault as 400.
func mapGRPCError(err error) *kbError {
	if err == nil {
		return nil
	}
	if st, ok := status.FromError(err); ok {
		msg := st.Message()
		if msg == "" {
			msg = st.Code().String()
		}
		switch st.Code() {
		case codes.NotFound:
			return &kbError{httpStatus: http.StatusNotFound, code: "NOT_FOUND", message: msg}
		case codes.InvalidArgument:
			return &kbError{httpStatus: http.StatusBadRequest, code: "BAD_REQUEST", message: msg}
		case codes.Unimplemented:
			return &kbError{httpStatus: http.StatusNotImplemented, code: "NOT_IMPLEMENTED", message: msg}
		case codes.FailedPrecondition:
			return &kbError{httpStatus: http.StatusConflict, code: "CONFLICT", message: msg}
		case codes.PermissionDenied:
			return &kbError{httpStatus: http.StatusForbidden, code: "FORBIDDEN", message: msg}
		case codes.Unauthenticated:
			return &kbError{httpStatus: http.StatusUnauthorized, code: "UNAUTHORIZED", message: msg}
		case codes.Unavailable:
			return &kbError{httpStatus: http.StatusServiceUnavailable, code: "UNAVAILABLE", message: msg}
		case codes.AlreadyExists:
			return &kbError{httpStatus: http.StatusConflict, code: "ALREADY_EXISTS", message: msg}
		case codes.DeadlineExceeded:
			return &kbError{httpStatus: http.StatusGatewayTimeout, code: "DEADLINE_EXCEEDED", message: msg}
		default:
			return &kbError{httpStatus: http.StatusInternalServerError, code: "INTERNAL", message: msg}
		}
	}
	return &kbError{httpStatus: http.StatusServiceUnavailable, code: "UNAVAILABLE", message: err.Error()}
}

// writeKBError serializes a kbError into the unified Core/Services error shape
// used by all gateway handlers: {"code","message","request_id"}.
func writeKBError(c *app.RequestContext, err error) {
	if err == nil {
		return
	}
	var ke *kbError
	if e, ok := err.(*kbError); ok {
		ke = e
	} else {
		ke = mapGRPCError(err)
	}
	writeInstanceError(c, ke.httpStatus, ke.code, ke.message)
}

// ── vLLM streaming client (SSE handler dependency) ───────────────────────────
//
// The SSE handler calls vLLM's OpenAI-compatible /v1/chat/completions endpoint
// with stream=true and reads the SSE response line by line, forwarding each
// token delta to the client as an SSE token event (SPEC §5.1 steps 5-6).
//
// VLLMStreamer abstracts the HTTP call so tests can inject a fake that
// produces canned token chunks.

// VLLMStreamer streams chat completions from vLLM.
type VLLMStreamer interface {
	// StreamChat sends a streaming chat completion request to vLLM and
	// returns a reader of SSE-formatted chunks (OpenAI-compatible
	// "data: {...}\n\n" lines). The caller must close the reader when done.
	// The context controls the request lifecycle; canceling it aborts the
	// in-flight stream (SPEC §5.4 客户端断开 → 取消 vLLM stream).
	StreamChat(ctx context.Context, req *vllmChatRequest) (io.ReadCloser, error)
}

// vllmChatRequest is the OpenAI-compatible chat completion request body.
type vllmChatRequest struct {
	Model    string        `json:"model"`
	Messages []vllmMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	// StreamOptions enables usage reporting in the final stream chunk so
	// the done event can carry accurate input/output token counts.
	StreamOptions *vllmStreamOptions `json:"stream_options,omitempty"`
}

// vllmStreamOptions controls streaming-specific options.
type vllmStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type vllmMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// vllmHTTPStreamer is the production implementation backed by vLLM's
// OpenAI-compatible endpoint. It uses a client with no timeout so the stream
// stays open; the per-request context controls cancellation.
type vllmHTTPStreamer struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewVLLMHTTPStreamer creates a streaming client for vLLM. baseURL is the
// vLLM OpenAI-compatible API root (e.g. "http://vllm:8000" or
// "http://vllm:8000/v1"); the client appends /v1/chat/completions, stripping
// a trailing /v1 if present to avoid a double prefix.
func NewVLLMHTTPStreamer(baseURL, apiKey string) VLLMStreamer {
	base := strings.TrimRight(baseURL, "/")
	// Strip trailing /v1 so "http://host:port/v1" + "/v1/chat/completions"
	// does not produce a double /v1/v1 prefix.
	base = strings.TrimSuffix(base, "/v1")
	return &vllmHTTPStreamer{
		baseURL: base,
		apiKey:  apiKey,
		// The stream stays open for the duration of generation; no overall
		// timeout. The request context handles client-disconnect cancellation.
		// ResponseHeaderTimeout bounds how long we wait for vLLM to send the
		// first response byte (connection + initial headers).
		httpClient: &http.Client{
			Transport: &http.Transport{
				ResponseHeaderTimeout: 30 * time.Second,
				IdleConnTimeout:       90 * time.Second,
				MaxIdleConnsPerHost:   10,
			},
		},
	}
}

func (s *vllmHTTPStreamer) StreamChat(ctx context.Context, req *vllmChatRequest) (io.ReadCloser, error) {
	if s == nil || s.baseURL == "" {
		return nil, fmt.Errorf("vLLM base URL not configured")
	}
	req.Stream = true
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode vLLM request: %w", err)
	}
	url := fmt.Sprintf("%s/v1/chat/completions", s.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build vLLM request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("vLLM request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, &vllmError{status: resp.StatusCode, body: string(raw)}
	}
	return resp.Body, nil
}

type vllmError struct {
	status int
	body   string
}

func (e *vllmError) Error() string {
	return fmt.Sprintf("vLLM returned %d: %s", e.status, e.body)
}
