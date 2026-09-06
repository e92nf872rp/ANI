package main

import (
	"testing"

	"github.com/kubercloud/ani/services/ani-gateway/internal/targetiam"
)

func TestGatewayRedisConfigFromEnvParsesSentinel(t *testing.T) {
	t.Setenv("GATEWAY_REDIS_MODE", "sentinel")
	t.Setenv("GATEWAY_REDIS_ADDRS", "redis-sentinel-a:26379, redis-sentinel-b:26379")
	t.Setenv("GATEWAY_REDIS_MASTER_NAME", "ani-redis")
	t.Setenv("GATEWAY_REDIS_USERNAME", "ani")
	t.Setenv("GATEWAY_REDIS_PASSWORD", "secret")
	t.Setenv("GATEWAY_REDIS_DB", "2")

	cfg := gatewayRedisConfigFromEnv()
	if cfg.Mode != "sentinel" || cfg.MasterName != "ani-redis" {
		t.Fatalf("redis mode/master = %q/%q, want sentinel/ani-redis", cfg.Mode, cfg.MasterName)
	}
	if len(cfg.Addrs) != 2 || cfg.Addrs[0] != "redis-sentinel-a:26379" || cfg.Addrs[1] != "redis-sentinel-b:26379" {
		t.Fatalf("redis addrs = %#v, want parsed sentinel addrs", cfg.Addrs)
	}
	if cfg.Username != "ani" || cfg.Password != "secret" || cfg.DB != 2 {
		t.Fatalf("redis auth/db = %q/%q/%d, want ani/secret/2", cfg.Username, cfg.Password, cfg.DB)
	}
}

func TestTargetIAMRuntimeConfigFromEnv(t *testing.T) {
	t.Setenv("IAM_TARGET_MODE", " dp2_05 ")
	t.Setenv("IAM_TARGET_GRPC_ADDR", " 127.0.0.1:8443 ")
	t.Setenv("IAM_TARGET_TLS_SERVER_NAME", " iam.dp2.test ")
	t.Setenv("IAM_TARGET_TLS_CA_FILE", " /run/secrets/iam-ca.crt ")
	t.Setenv("IAM_TARGET_TLS_CERT_FILE", " /run/secrets/gateway.crt ")
	t.Setenv("IAM_TARGET_TLS_KEY_FILE", " /run/secrets/gateway.key ")

	config := targetIAMRuntimeConfigFromEnv()
	if config.Mode != "dp2_05" || config.Address != "127.0.0.1:8443" {
		t.Fatalf("Mode/Address = %q/%q", config.Mode, config.Address)
	}
	wantTLS := targetiam.MutualTLSConfig{
		ServerName:      "iam.dp2.test",
		CAFile:          "/run/secrets/iam-ca.crt",
		CertificateFile: "/run/secrets/gateway.crt",
		PrivateKeyFile:  "/run/secrets/gateway.key",
	}
	if config.MutualTLS != wantTLS {
		t.Fatalf("MutualTLS = %#v, want %#v", config.MutualTLS, wantTLS)
	}
}

func TestNewTargetIAMClientAllowsDisabledAddress(t *testing.T) {
	client, closeClient, err := newTargetIAMClient(targetIAMRuntimeConfig{Mode: "disabled"})
	if err != nil || client != nil || closeClient != nil {
		t.Fatalf("newTargetIAMClient(disabled): clientNil=%v closeNil=%v error=%v", client == nil, closeClient == nil, err)
	}
}

func TestNewTargetIAMClientRejectsImplicitOrIncompleteTargetMode(t *testing.T) {
	tests := []struct {
		name   string
		config targetIAMRuntimeConfig
	}{
		{name: "missing mode"},
		{name: "unknown mode", config: targetIAMRuntimeConfig{Mode: "auto"}},
		{name: "target mode without address", config: targetIAMRuntimeConfig{Mode: "dp2_05"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, closeClient, err := newTargetIAMClient(test.config)
			if err == nil || client != nil || closeClient != nil {
				t.Fatalf("newTargetIAMClient(%s): clientNil=%v closeNil=%v error=%v", test.name, client == nil, closeClient == nil, err)
			}
		})
	}
}

func TestNewTargetIAMClientFailsClosedWithoutMutualTLS(t *testing.T) {
	client, closeClient, err := newTargetIAMClient(targetIAMRuntimeConfig{Mode: "dp2_05", Address: "127.0.0.1:8443"})
	if err == nil || client != nil || closeClient != nil {
		t.Fatalf("newTargetIAMClient(incomplete TLS): clientNil=%v closeNil=%v error=%v", client == nil, closeClient == nil, err)
	}
}
