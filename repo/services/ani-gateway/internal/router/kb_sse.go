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
	"github.com/cloudwego/hertz/pkg/network"
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
// Client disconnect detection: the streaming loop runs inside a Hertz Hijack
// callback with an independent context; when the client closes the connection
// the SSE write fails, the callback returns, and the deferred cancel aborts
// the in-flight upstream gRPC stream (SPEC §5.4).
//
// Frames are written on the hijacked raw connection with an explicit Flush
// after EVERY frame: c.Write alone only appends to Hertz's response buffer,
// which is flushed once when the handler returns — tokens would all arrive in
// a single burst at stream end and the frontend would show no typewriter
// effect. Same pattern as instance_log_stream.go (see its ctx-lifecycle note:
// the handler ctx must NOT be used inside the Hijack callback — it is already
// cancelled/reused there).

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

// writeSSEEvent writes one SSE frame to the hertz response (degrade path
// only — two terminal frames, no incremental streaming). The streaming path
// uses writeConnSSEEvent on the hijacked connection instead, so tokens are
// flushed immediately.
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

// writeConnSSEEvent writes one SSE frame to the hijacked connection and
// flushes it immediately so the client receives tokens in real time.
func writeConnSSEEvent(conn network.Conn, ev sseEvent) error {
	frame, err := encodeSSEEvent(ev)
	if err != nil {
		return err
	}
	if _, err := conn.WriteBinary(frame); err != nil {
		return fmt.Errorf("write sse %s: %w", ev.event, err)
	}
	return conn.Flush()
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
		// Kept for the degrade path below (hertz writes them with the default
		// response); the hijack path writes the same headers manually.
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
		// The key must be a bare UUID — kb-service validates it with
		// uuid.UUID(...) and rejects any prefixed form ("sse-<uuid>" fails).
		//
		// The stream context is NOT the handler ctx: the streaming loop runs
		// inside the Hijack callback below, which executes AFTER the handler
		// returned (netpoll reuses/cancels the handler ctx there — see
		// instance_log_stream.go). An independent ctx + the same 120s budget
		// as Query applies; client disconnect is sensed via SSE write failure,
		// which cancels this ctx and aborts the upstream stream.
		streamCtx, cancel := context.WithTimeout(context.Background(), queryRPCTimeout)
		req := &kbv1.RetrieveRequest{
			Question:             question,
			SessionId:            sessionID,
			IdempotencyKey:       uuid.NewString(),
			TopK:                 topK,
			ScoreThreshold:       scoreThreshold,
			InferenceServiceName: inferenceServiceName,
			RetrievalMode:        retrievalMode,
		}
		// kb.query rows are audit-worthy: attribute the streamed turn to the
		// acting user via x-user-id (mirrors queryKnowledgeBase).
		stream, err := cfg.KBClient.Retrieve(kbWriteCtx(streamCtx, c), tenantID, c.Param("kb_id"), req)
		if err != nil {
			// Pre-stream gRPC errors: map to JSON for 4xx, SSE error for others.
			cancel()
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

		// ── Stream the events as real-time SSE frames (hijack) ─────────────
		// c.Write alone only appends to Hertz's response buffer, which is
		// flushed once when the handler returns — every token would arrive in
		// a single burst at stream end (no typewriter effect on the frontend).
		// HijackWriter suppresses Hertz's default response write so the Hijack
		// callback owns the raw connection; every frame is written and flushed
		// explicitly (same pattern as instance_log_stream.go).
		c.Response.SetStatusCode(http.StatusOK)
		c.Response.HijackWriter(&noopExtWriter{})
		c.Hijack(func(conn network.Conn) {
			defer func() {
				cancel()
				_ = stream.CloseSend()
				_ = conn.Close()
			}()

			// SSE headers immediately: the client must get 200 +
			// text/event-stream right away, before the first token exists.
			// The ": connected" comment frame (SSE-spec comment, ignored by
			// clients) forces node-style proxies to flush the headers.
			if _, err := conn.WriteBinary([]byte(sseHeaders)); err != nil {
				return // client already gone
			}
			if _, err := conn.WriteBinary([]byte(sseConnectedComment)); err != nil {
				return
			}
			if err := conn.Flush(); err != nil {
				return
			}

			// Forward gRPC stream events as SSE frames. Event sequence from
			// kb-service: token* → sources → done (Plan §10.2).
			for {
				ev, recvErr := stream.Recv()
				if recvErr != nil {
					if errors.Is(recvErr, io.EOF) {
						// Stream complete — done event was already forwarded
						// as part of the RetrieveEvent sequence.
						return
					}
					// Mid-stream error: emit SSE error event and close.
					_ = writeConnSSEEvent(conn, sseEvent{event: "error", data: map[string]string{
						"code":    "STREAM_INTERRUPTED",
						"message": recvErr.Error(),
					}})
					return
				}

				switch event := ev.Event.(type) {
				case *kbv1.RetrieveEvent_Token:
					if werr := writeConnSSEEvent(conn, sseEvent{event: "token", data: map[string]string{
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
					_ = writeConnSSEEvent(conn, sseEvent{event: "sources", data: srcs})
				case *kbv1.RetrieveEvent_Done:
					doneData := map[string]any{
						"session_id":    event.Done.GetSessionId(),
						"input_tokens":  event.Done.GetInputTokens(),
						"output_tokens": event.Done.GetOutputTokens(),
					}
					_ = writeConnSSEEvent(conn, sseEvent{event: "done", data: doneData})
				case *kbv1.RetrieveEvent_Error:
					_ = writeConnSSEEvent(conn, sseEvent{event: "error", data: map[string]string{
						"code":    event.Error.GetCode(),
						"message": event.Error.GetMessage(),
					}})
					return
				}
			}
		})
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
