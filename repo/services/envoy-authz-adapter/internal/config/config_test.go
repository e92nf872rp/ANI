package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("AUTH_SERVICE_GRPC_ADDR", "ani-auth-service.ani-system.svc.cluster.local:9101")
	t.Setenv("GRPC_PORT", "")
	t.Setenv("AUTH_TIMEOUT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GRPCPort != 9002 || cfg.AuthTimeout != 2*time.Second {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadRequiresAuthAddress(t *testing.T) {
	t.Setenv("AUTH_SERVICE_GRPC_ADDR", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected missing auth address error")
	}
}

func TestLoadRejectsInvalidPortAndTimeout(t *testing.T) {
	t.Setenv("AUTH_SERVICE_GRPC_ADDR", "auth:9101")

	for _, tc := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "zero port", key: "GRPC_PORT", value: "0"},
		{name: "negative port", key: "GRPC_PORT", value: "-1"},
		{name: "malformed port", key: "GRPC_PORT", value: "9002x"},
		{name: "zero timeout", key: "AUTH_TIMEOUT", value: "0s"},
		{name: "negative timeout", key: "AUTH_TIMEOUT", value: "-1s"},
		{name: "malformed timeout", key: "AUTH_TIMEOUT", value: "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GRPC_PORT", "")
			t.Setenv("AUTH_TIMEOUT", "")
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatalf("Load() accepted %s=%q", tc.key, tc.value)
			}
		})
	}
}
