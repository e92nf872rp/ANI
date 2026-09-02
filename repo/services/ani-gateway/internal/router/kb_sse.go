package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"

	kbv1 "github.com/kubercloud/ani/pkg/generated/pb/kb/v1"
)

// kb_sse.go implements the SSE streaming query endpoint held by the gateway
// (SPEC §4.3 / US-017). The handler orchestrates:
//
//  1. rag-engine retrieval (synchronous HTTP) to obtain SourceChunk list
//     (SPEC §5.1 step 3)
//  2. prompt construction from sources + question (SPEC §5.1 step 4)
//  3. vLLM /v1/chat/completions stream=true (SPEC §5.1 step 5)
//  4. token event passthrough (SPEC §5.1 step 6, §4.3 event: token)
//  5. sources event (SPEC §4.3 event: sources, emitted after stream)
//  6. done event (SPEC §4.3 event: done)
//  7. error event on mid-stream failure (SPEC §4.3 event: error)
//
// Pre-stream errors (400/401/404) return JSON without entering the stream
// (SPEC §4.3 错误处理). Mid-stream errors emit an SSE error event and close
// the stream.
//
// Client disconnect detection: the handler uses the request context; when the
// client closes the connection the context is cancelled, which aborts the
// in-flight vLLM HTTP stream (SPEC §5.4).
//
// The handler writes SSE frames via c.Write which, combined with a chunked
// transfer-encoding hijack writer and c.Flush, pushes each frame to the
// client immediately for real-time token streaming.

// KbSSEConfig holds the dependencies injected by the route registrar. When
// vllmStreamer is nil the handler degrades gracefully: it emits an empty
// token stream (sources=[] + done) so the endpoint surface stays functional
// without backend services configured.
//
// Issue #039: the legacy rag-engine REST path has been removed. The SSE
// handler now always calls kbClient.Retrieve (gRPC server-streaming). The
// kb-service Retrieve RPC orchestrates retrieval + GenerateStream
// internally, and the gateway just forwards the gRPC stream events as SSE
// frames.
type KbSSEConfig struct {
	VLLMStreamer VLLMStreamer
	VLLMModel    string // default model name for /v1/chat/completions
	// KBClient is the kb-service gRPC client. When nil the handler degrades
	// to an empty stream (sources=[] + done).
	KBClient KBGRPCClient
}

// sseEvent represents one SSE event frame written to the response stream.
type sseEvent struct {
	event string
	data  any
}

// encodeSSEEvent formats an SSE frame: "event: <type>\ndata: <json>\n\n".
func encodeSSEEvent(ev sseEvent) ([]byte, error) {
	dataBytes, err := json.Marshal(ev.data)
	if err != nil {
		return nil, fmt.Errorf("encode sse %s data: %w", ev.event, err)
	}
	var buf bytes.Buffer
	buf.Grow(len("event: \n\ndata: \n\n") + len(ev.event) + len(dataBytes))
	buf.WriteString("event: ")
	buf.WriteString(ev.event)
	buf.WriteString("\ndata: ")
	buf.Write(dataBytes)
	buf.WriteString("\n\n")
	return buf.Bytes(), nil
}

// writeSSEEvent writes one SSE frame to the response and immediately flushes
// it to the client so tokens arrive in real time (not buffered until handler
// return). Uses Hertz's chunked transfer-encoding hijack writer.
func writeSSEEvent(c *app.RequestContext, ev sseEvent) error {
	frame, err := encodeSSEEvent(ev)
	if err != nil {
		return err
	}
	if _, err := c.Write(frame); err != nil {
		return fmt.Errorf("write sse %s: %w", ev.event, err)
	}
	return nil
}

// streamQueryKnowledgeBaseSSE is the SSE handler registered at
// GET /api/v1/svc/knowledge-bases/{kb_id}/query/stream (SPEC §4.3).
//
// Issue #039: the legacy rag-engine REST retrieval path has been removed
// (rag-engine no longer exposes /api/v1/kb/{id}/query). The handler always
// calls kbClient.Retrieve (gRPC server-streaming) and forwards events as
// SSE frames.
func streamQueryKnowledgeBaseSSE(cfg KbSSEConfig) app.HandlerFunc {
	return streamQuerySSENewPath(cfg)
}

// streamQuerySSENewPath calls kbClient.Retrieve (gRPC server-streaming) and
// forwards RetrieveEvent messages as SSE frames, preserving the
// token*→sources→done event sequence.
//
// When KBClient is nil the handler degrades to an empty stream (sources=[] +
// done) so the endpoint stays functional without kb-service configured,
// matching the legacy degradation behavior (SPEC §5.4).
//
// Pre-stream errors (400 for missing question, 404 for KB not found) return
// JSON without entering the stream (SPEC §4.3: "首部 400/401/404 不进入流").
// Mid-stream errors emit an SSE error event and close the stream.
func streamQuerySSENewPath(cfg KbSSEConfig) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		tenantID := middleware.GetTenantID(c)
		if tenantID == "" {
			tenantID = instanceTenantID(c)
		}

		// ── Validate query params (SPEC §4.3, §5.2) ────────────────────────
		question := string(c.QueryArgs().Peek("question"))
		if len(question) == 0 {
			writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "question is required")
			return
		}
		if len(question) > 2000 {
			writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "question must be at most 2000 characters")
			return
		}
		sessionID := string(c.QueryArgs().Peek("session_id"))
		topK := int32(queryInt(c, "top_k", 5))
		if topK < 1 || topK > 20 {
			topK = 5
		}
		scoreThreshold := queryFloat32(c, "score_threshold", 0)
		inferenceServiceName := string(c.QueryArgs().Peek("inference_service_name"))
		retrievalMode := string(c.QueryArgs().Peek("retrieval_mode"))

		// ── SSE headers (SPEC §4.3) ────────────────────────────────────────
		c.Response.Header.Set("Content-Type", "text/event-stream")
		c.Response.Header.Set("Cache-Control", "no-cache")
		c.Response.Header.Set("Connection", "keep-alive")
		c.Response.Header.Set("X-Accel-Buffering", "no")
		c.Response.SetStatusCode(http.StatusOK)

		// ── Degrade: kb-service not configured → empty stream (SPEC §5.4) ───
		if cfg.KBClient == nil {
			_ = writeSSEEvent(c, sseEvent{event: "sources", data: []map[string]any{}})
			_ = writeSSEEvent(c, sseEvent{event: "done", data: map[string]any{
				"session_id":    sessionID,
				"input_tokens":  0,
				"output_tokens": 0,
			}})
			return
		}

		// ── Call kb-service Retrieve (gRPC server-streaming) ───────────────
		// TenantId and KbId are set by KBClient.Retrieve from the path params.
		// The SSE endpoint is a GET with no idempotency_key in its contract
		// (SPEC §4.3), so the gateway generates a fresh key per request; this
		// satisfies kb-service's required-key validation (prevents duplicate
		// billing on retry) without exposing the field to SSE clients.
		req := &kbv1.RetrieveRequest{
			Question:             question,
			SessionId:            sessionID,
			IdempotencyKey:       "sse-" + uuid.NewString(),
			TopK:                 topK,
			ScoreThreshold:       scoreThreshold,
			InferenceServiceName: inferenceServiceName,
			RetrievalMode:        retrievalMode,
		}
		stream, err := cfg.KBClient.Retrieve(ctx, tenantID, c.Param("kb_id"), req)
		if err != nil {
			// Pre-stream gRPC errors: map to JSON for 4xx, SSE error for others.
			ke := mapGRPCError(err)
			if ke.httpStatus == http.StatusNotFound || ke.httpStatus == http.StatusBadRequest ||
				ke.httpStatus == http.StatusUnauthorized {
				writeInstanceError(c, ke.httpStatus, ke.code, ke.message)
				return
			}
			// Non-4xx → emit SSE error event.
			_ = writeSSEEvent(c, sseEvent{event: "error", data: map[string]string{
				"code":    ke.code,
				"message": ke.message,
			}})
			return
		}

		// ── Forward gRPC stream events as SSE frames ───────────────────────
		// Event sequence from kb-service: token* → sources → done (Plan §10.2).
		// We map each RetrieveEvent oneof to the corresponding SSE event.
		// CloseSend is deferred to release server-side transport resources.
		defer func() { _ = stream.CloseSend() }()
		for {
			ev, recvErr := stream.Recv()
			if recvErr != nil {
				if errors.Is(recvErr, io.EOF) {
					// Stream complete — done event was already forwarded
					// as part of the RetrieveEvent sequence.
					return
				}
				// Mid-stream error: emit SSE error event and close.
				_ = writeSSEEvent(c, sseEvent{event: "error", data: map[string]string{
					"code":    "STREAM_INTERRUPTED",
					"message": recvErr.Error(),
				}})
				return
			}

			switch event := ev.Event.(type) {
			case *kbv1.RetrieveEvent_Token:
				if werr := writeSSEEvent(c, sseEvent{event: "token", data: map[string]string{
					"delta": event.Token.GetContent(),
				}}); werr != nil {
					return
				}
			case *kbv1.RetrieveEvent_Sources:
				srcs := make([]map[string]any, 0, len(event.Sources.GetSources()))
				for _, s := range event.Sources.GetSources() {
					srcs = append(srcs, map[string]any{
						"doc_id":    s.GetDocId(),
						"file_name": s.GetFileName(),
						"page":      s.GetPage(),
						"content":   s.GetContent(),
						"score":     s.GetScore(),
					})
				}
				_ = writeSSEEvent(c, sseEvent{event: "sources", data: srcs})
			case *kbv1.RetrieveEvent_Done:
				doneData := map[string]any{
					"session_id":    event.Done.GetSessionId(),
					"input_tokens":  event.Done.GetInputTokens(),
					"output_tokens": event.Done.GetOutputTokens(),
				}
				_ = writeSSEEvent(c, sseEvent{event: "done", data: doneData})
			case *kbv1.RetrieveEvent_Error:
				_ = writeSSEEvent(c, sseEvent{event: "error", data: map[string]string{
					"code":    event.Error.GetCode(),
					"message": event.Error.GetMessage(),
				}})
				return
			}
		}
	}
}

// queryFloat32 reads a float32 query parameter with a fallback default.
func queryFloat32(c *app.RequestContext, name string, fallback float32) float32 {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 32)
	if err != nil {
		return fallback
	}
	return float32(value)
}
