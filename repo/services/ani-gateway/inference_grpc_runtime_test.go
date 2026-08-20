package main

import (
	"testing"
	"time"
)

func TestGatewayInferenceServiceRuntimeConfigDefaultTimeout(t *testing.T) {
	t.Setenv("INFERENCE_SERVICE_GRPC_CALL_TIMEOUT", "")
	cfg := gatewayInferenceServiceRuntimeConfigFromEnv()
	if cfg.CallTimeout != 30*time.Second {
		t.Fatalf("CallTimeout = %s, want 30s so Core apply can fail on the request path", cfg.CallTimeout)
	}
}
