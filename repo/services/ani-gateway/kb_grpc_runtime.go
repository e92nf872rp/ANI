package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/kubercloud/ani/services/ani-gateway/internal/router"
)

// gatewayKBServiceRuntimeConfig configures the kb-service gRPC client.
//
// KB_SERVICE_GRPC_ADDR: the kb-service gRPC endpoint (e.g. "kb-service:9090").
// When empty the gateway boots without a KB client and the KB handlers
// return 503 UNAVAILABLE, preserving the existing local-dev behaviour where
// kb-service is not deployed.
//
// KB_SERVICE_GRPC_CALL_TIMEOUT: per-RPC timeout (Go duration). Defaults to 5s
// so a slow/hung kb-service cannot indefinitely hold gateway goroutines; a
// deadline-exceeded surfaces as HTTP 504 DEADLINE_EXCEEDED (SPEC §5.1).
type gatewayKBServiceRuntimeConfig struct {
	Addr        string
	CallTimeout time.Duration
}

func gatewayKBServiceRuntimeConfigFromEnv() gatewayKBServiceRuntimeConfig {
	cfg := gatewayKBServiceRuntimeConfig{
		Addr:        strings.TrimSpace(os.Getenv("KB_SERVICE_GRPC_ADDR")),
		CallTimeout: gatewayDurationFromEnv("KB_SERVICE_GRPC_CALL_TIMEOUT"),
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = 5 * time.Second
	}
	return cfg
}

// newGatewayKBServiceClient dials kb-service and returns the gRPC client plus
// a close function. When Addr is empty (kb-service not configured) it returns
// (nil, nil, nil) so the gateway boots and the KB handlers return 503.
func newGatewayKBServiceClient(ctx context.Context, cfg gatewayKBServiceRuntimeConfig) (router.KBGRPCClient, func(), error) {
	if cfg.Addr == "" {
		return nil, nil, nil
	}
	conn, client, err := router.DialKBGRPC(ctx, cfg.Addr, cfg.CallTimeout)
	if err != nil {
		return nil, nil, err
	}
	return client, func() { _ = conn.Close() }, nil
}

// ── SSE streaming query runtime (US-017) ─────────────────────────────────────
//
// The SSE handler (kb_sse.go) depends on two backends:
//   - rag-engine for retrieval (RAG_ENGINE_URL, REST)
//   - vLLM for token streaming (VLLM_API_BASE + VLLM_API_KEY + VLLM_MODEL)
//
// When either backend is not configured the SSE handler degrades gracefully:
//   - RAG_ENGINE_URL empty → sources=[] + done (no retrieval)
//   - VLLM_API_BASE/VLLM_MODEL empty → no token events (sources + done only)
//
// This preserves the local-dev behaviour where rag-engine/vLLM are not
// deployed: the SSE endpoint stays functional with an empty stream.

// gatewaySSERuntimeConfig configures the SSE streaming query dependencies.
type gatewaySSERuntimeConfig struct {
	RagEngineURL      string
	VLLMBaseURL       string
	VLLMAPIKey        string
	VLLMModel         string
	RagEngineTimeout  time.Duration
}

func gatewaySSERuntimeConfigFromEnv() gatewaySSERuntimeConfig {
	cfg := gatewaySSERuntimeConfig{
		RagEngineURL:     strings.TrimSpace(os.Getenv("RAG_ENGINE_URL")),
		RagEngineTimeout: gatewayDurationFromEnv("RAG_ENGINE_TIMEOUT"),
		VLLMBaseURL:      strings.TrimSpace(os.Getenv("VLLM_API_BASE")),
		VLLMAPIKey:       strings.TrimSpace(os.Getenv("VLLM_API_KEY")),
		VLLMModel:        strings.TrimSpace(os.Getenv("VLLM_MODEL")),
	}
	if cfg.RagEngineTimeout <= 0 {
		cfg.RagEngineTimeout = 30 * time.Second
	}
	return cfg
}

// newGatewaySSEConfig builds the kbSSEConfig from environment. When backends
// are not configured the returned config has nil clients so the SSE handler
// degrades to an empty stream.
func newGatewaySSEConfig(cfg gatewaySSERuntimeConfig) router.KbSSEConfig {
	var ragClient router.RagEngineClient
	if cfg.RagEngineURL != "" {
		ragClient = router.NewRagEngineHTTPClient(cfg.RagEngineURL, cfg.RagEngineTimeout)
	}
	var vllmStreamer router.VLLMStreamer
	if cfg.VLLMBaseURL != "" {
		vllmStreamer = router.NewVLLMHTTPStreamer(cfg.VLLMBaseURL, cfg.VLLMAPIKey)
	}
	return router.KbSSEConfig{
		RagClient:   ragClient,
		VLLMStreamer: vllmStreamer,
		VLLMModel:    cfg.VLLMModel,
	}
}
