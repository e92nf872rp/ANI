package router

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
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
// The handler writes SSE frames via c.Write which accumulates into the
// response body buffer. For P0 this is correct: the event sequence
// (token*→sources→done) is well-formed and the response is a valid
// text/event-stream. The per-frame flush delay is a documented risk
// (SPEC §11.2) addressed by a future Hijack-based writer upgrade.

// KbSSEConfig holds the dependencies injected by the route registrar. When
// ragClient or vllmStreamer is nil the handler degrades gracefully: it
// emits an empty token stream (sources=[] + done) so the endpoint surface
// stays functional without backend services configured.
type KbSSEConfig struct {
	RagClient    RagEngineClient
	VLLMStreamer  VLLMStreamer
	VLLMModel     string // default model name for /v1/chat/completions
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

// writeSSEEvent writes one SSE frame to the response body buffer.
// Returns an error if the write fails so the caller can abort the stream.
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
// It validates the question query param (400), reads the tenant id from the
// Auth middleware (SPEC §7.1 租户注入), calls rag-engine for retrieval,
// constructs a RAG prompt, streams tokens from vLLM, and emits the
// token*→sources→done event sequence. Errors before the stream starts return
// JSON; errors during the stream emit an SSE error event and close.
func streamQueryKnowledgeBaseSSE(cfg KbSSEConfig) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		// Tenant id from Auth middleware (SPEC §7.1).
		tenantID := middleware.GetTenantID(c)
		if tenantID == "" {
			tenantID = demoTenantID(c)
		}

		// ── Validate query params (SPEC §4.3, §5.2) ────────────────────────
		question := string(c.QueryArgs().Peek("question"))
		if len(question) == 0 {
			writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "question is required")
			return
		}
		if len(question) > 2000 {
			writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "question must be at most 2000 characters")
			return
		}
		sessionID := string(c.QueryArgs().Peek("session_id"))
		topK := int32(queryInt(c, "top_k", 5))
		if topK < 1 || topK > 20 {
			topK = 5
		}
		// score_threshold: default 0 (disabled) because RRF fusion scores are
		// typically ~0.016, far below the system-wide 0.3 default. A non-zero
		// default would filter out all sources in the hybrid retrieval path.
		scoreThreshold := queryFloat32(c, "score_threshold", 0)
		inferenceServiceName := string(c.QueryArgs().Peek("inference_service_name"))

		// ── SSE headers (SPEC §4.3) ────────────────────────────────────────
		// NOTE: headers are set here per SPEC §5.1 step 2, but in Hertz's
		// buffered-response model the status line + headers are not
		// committed until the first c.Write or handler return. This lets us
		// still return JSON 400/401/404 for retrieval-stage errors (SPEC §4.3:
		// "首部 400/401/404 不进入流") by overwriting the status/content-type
		// via writeDemoError (c.JSON) before any SSE body bytes are written.
		c.Response.Header.Set("Content-Type", "text/event-stream")
	c.Response.Header.Set("Cache-Control", "no-cache")
	c.Response.Header.Set("Connection", "keep-alive")
	// X-Accel-Buffering: no tells nginx (and compatible reverse proxies) not
	// to buffer the SSE response — critical for real-time token streaming.
	c.Response.Header.Set("X-Accel-Buffering", "no")
	c.Response.SetStatusCode(http.StatusOK)

		// ── Step 3: rag-engine retrieval (SPEC §5.1) ───────────────────────
		// When ragClient is nil (rag-engine not configured) degrade to an
		// empty stream so the endpoint stays functional.
		var sources []ragSourceChunk
		var retrieveSessionID string
		if cfg.RagClient != nil {
			ragReq := &ragQueryRequest{
				TenantID:             tenantID,
				KbID:                  c.Param("kb_id"),
				Question:              question,
				SessionID:             sessionID,
				TopK:                  topK,
				ScoreThreshold:        scoreThreshold,
				InferenceServiceName:  inferenceServiceName,
			}
			ragResp, err := cfg.RagClient.Query(ctx, ragReq)
			if err != nil {
				// SPEC §4.3 错误处理: 400/401/404 are pre-stream → return JSON
				// (no SSE body written yet, so writeDemoError can override
				// the status/content-type). Other retrieval failures (503,
				// timeouts, etc.) emit an SSE error event and close the
				// stream (SPEC §4.3 line 172: 检索失败 → event: error).
				if re, ok := err.(*ragEngineError); ok {
					switch re.status {
					case http.StatusNotFound:
						// KB_NOT_FOUND → JSON 404 (pre-stream).
						writeDemoError(c, http.StatusNotFound, "KB_NOT_FOUND", "knowledge base not found")
						return
					case http.StatusBadRequest:
						writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", re.body)
						return
					case http.StatusUnauthorized:
						writeDemoError(c, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
						return
					}
				}
				// Non-4xx retrieval failure → SSE error event (SPEC §4.3).
				_ = writeSSEEvent(c, sseEvent{event: "error", data: map[string]string{
					"code":    "RETRIEVE_FAILED",
					"message": err.Error(),
				}})
				return
			}
			sources = ragResp.Sources
			retrieveSessionID = ragResp.SessionID
		}

		// ── Step 4: construct prompt (SPEC §5.1) ───────────────────────────
		prompt := buildRAGPrompt(question, sources)

		// ── Step 5-6: vLLM streaming + token passthrough (SPEC §5.1) ───────
	var vllmUsage vllmTokenUsage
	if cfg.VLLMStreamer != nil && cfg.VLLMModel != "" {
		vllmReq := &vllmChatRequest{
			Model: cfg.VLLMModel,
			Messages: []vllmMessage{
				{Role: "system", Content: "You are a helpful assistant. Answer the user's question based on the provided context. If the context is insufficient, say so."},
				{Role: "user", Content: prompt},
			},
			StreamOptions: &vllmStreamOptions{IncludeUsage: true},
		}
		usage, streamErr := streamVLLMTokens(ctx, c, cfg.VLLMStreamer, vllmReq)
		if streamErr != nil {
			// Mid-stream error: emit error event and close (SPEC §4.3).
			_ = writeSSEEvent(c, sseEvent{event: "error", data: map[string]string{
				"code":    "STREAM_INTERRUPTED",
				"message": streamErr.Error(),
			}})
			return
		}
		vllmUsage = usage
	}

	// ── Step 7: sources event (SPEC §4.3) ──────────────────────────────
	_ = writeSSEEvent(c, sseEvent{event: "sources", data: sourcesToJSON(sources)})

	// ── Step 8: done event (SPEC §4.3) ─────────────────────────────────
	doneData := map[string]any{
		"session_id":    retrieveSessionID,
		"input_tokens":  vllmUsage.inputTokens,
		"output_tokens": vllmUsage.outputTokens,
	}
	_ = writeSSEEvent(c, sseEvent{event: "done", data: doneData})
	}
}

// vllmTokenUsage carries the token counts from the final vLLM stream chunk.
type vllmTokenUsage struct {
	inputTokens  int
	outputTokens int
}

// streamVLLMTokens reads the vLLM SSE stream and forwards token deltas to
// the client as SSE token events. Returns the usage stats from the final
// chunk and an error if the stream fails mid-way (SPEC §5.1 step 6, §5.4).
func streamVLLMTokens(ctx context.Context, c *app.RequestContext, streamer VLLMStreamer, req *vllmChatRequest) (vllmTokenUsage, error) {
	var usage vllmTokenUsage
	body, err := streamer.StreamChat(ctx, req)
	if err != nil {
		// Map vLLM connection errors to INFERENCE_UNAVAILABLE (SPEC §6.1).
		if ve, ok := err.(*vllmError); ok && ve.status >= 500 {
			return usage, fmt.Errorf("inference unavailable: %s", ve.body)
		}
		return usage, fmt.Errorf("vLLM stream start failed: %w", err)
	}
	defer body.Close()

	scanner := bufio.NewScanner(body)
	// Increase buffer for large SSE lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		// OpenAI-compatible SSE: "data: {json}\n\n". Skip keep-alive comments
		// and the terminal "data: [DONE]" marker.
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		delta, u, err := parseVLLMChunk(payload)
		if err != nil {
			// Skip malformed chunks rather than aborting the stream.
			continue
		}
		if u.inputTokens > 0 || u.outputTokens > 0 {
			usage = u
		}
		if delta == "" {
			continue
		}
		if werr := writeSSEEvent(c, sseEvent{event: "token", data: map[string]string{"delta": delta}}); werr != nil {
			return usage, fmt.Errorf("write token event: %w", werr)
		}
	}
	return usage, scanner.Err()
}

// parseVLLMChunk extracts the content delta and usage stats from an
// OpenAI-compatible streaming chunk. Qwen3 models also emit "reasoning"
// deltas (chain-of-thought tokens) which are NOT forwarded — only content
// deltas become token events (SPEC §4.3 event: token).
func parseVLLMChunk(payload string) (delta string, usage vllmTokenUsage, err error) {
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning"`
			} `json:"delta"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return "", vllmTokenUsage{}, err
	}
	if chunk.Usage != nil {
		usage.inputTokens = chunk.Usage.PromptTokens
		usage.outputTokens = chunk.Usage.CompletionTokens
	}
	if len(chunk.Choices) == 0 {
		return "", usage, nil
	}
	// Only forward content deltas. Qwen3 models emit "reasoning" deltas
	// (chain-of-thought) before content; forwarding those as token events
	// would mix thinking output into the answer. Skip reasoning entirely —
	// streamVLLMTokens already skips empty deltas.
	d := chunk.Choices[0].Delta
	return d.Content, usage, nil
}

// buildRAGPrompt constructs the RAG prompt from the question and retrieved
// sources (SPEC §5.1 step 4). Sources are included as context blocks so the
// LLM can ground its answer; when no sources are found the LLM is still
// called (SPEC §5.4: sources=[] + still call LLM, let LLM refuse).
func buildRAGPrompt(question string, sources []ragSourceChunk) string {
	if len(sources) == 0 {
		return question
	}
	var b strings.Builder
	b.WriteString("Context:\n")
	for i, s := range sources {
		b.WriteString(fmt.Sprintf("[%d] %s (page %d, score %.2f):\n%s\n\n",
			i+1, s.FileName, s.Page, s.Score, s.Content))
	}
	b.WriteString("Question: ")
	b.WriteString(question)
	return b.String()
}

// sourcesToJSON converts ragSourceChunk slice to the SSE sources event data
// shape (SPEC §4.3: SourceChunk[]).
func sourcesToJSON(sources []ragSourceChunk) []map[string]any {
	if len(sources) == 0 {
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(sources))
	for _, s := range sources {
		result = append(result, map[string]any{
			"doc_id":    s.DocID,
			"file_name": s.FileName,
			"page":      s.Page,
			"content":   s.Content,
			"score":     s.Score,
		})
	}
	return result
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
