package main

import (
	"testing"
	"time"
)

func TestGatewayInstanceSessionRuntimeConfigFromEnv(t *testing.T) {
	t.Setenv("SESSION_GATEWAY_GRPC_ADDR", " session-gateway.example:9090 ")
	t.Setenv("SESSION_GATEWAY_GRPC_TIMEOUT", "3s")
	cfg := gatewayInstanceSessionRuntimeConfigFromEnv()
	if cfg.Addr != "session-gateway.example:9090" || cfg.CallTimeout != 3*time.Second {
		t.Fatalf("config = %+v", cfg)
	}

	t.Setenv("SESSION_GATEWAY_GRPC_TIMEOUT", "")
	if got := gatewayInstanceSessionRuntimeConfigFromEnv().CallTimeout; got != 5*time.Second {
		t.Fatalf("default timeout = %s, want 5s", got)
	}
}

func TestGatewayInstanceSessionRuntimeAllowsMissingAddressForHandlerFailClosed(t *testing.T) {
	issuer, closeRuntime, err := newGatewayInstanceSessionIssuer(gatewayInstanceSessionRuntimeConfig{})
	if err != nil || issuer != nil || closeRuntime != nil {
		t.Fatalf("issuer=%T close=%v err=%v, want nils", issuer, closeRuntime != nil, err)
	}
}

func TestGatewayInstanceSessionRuntimeRejectsInvalidAddress(t *testing.T) {
	issuer, closeRuntime, err := newGatewayInstanceSessionIssuer(gatewayInstanceSessionRuntimeConfig{Addr: "://", CallTimeout: time.Second})
	if err == nil || issuer != nil || closeRuntime != nil {
		t.Fatalf("issuer=%T close=%v err=%v, want invalid-address error", issuer, closeRuntime != nil, err)
	}
}
