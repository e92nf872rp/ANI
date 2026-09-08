package gatewaypublish

import (
	"testing"
	"time"
)

func TestLoadConfigNormalizesPublicBaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://redacted")
	t.Setenv("INFERENCE_AI_GATEWAY_PUBLIC_BASE_URL", "https://ai.example.com/")
	cfg, err := LoadConfig()
	if err != nil || cfg.PublicBaseURL.String() != "https://ai.example.com" {
		t.Fatalf("cfg = %+v, err = %v", cfg, err)
	}
}

func TestLoadConfigRetainsNonRootPublicBasePath(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://redacted")
	t.Setenv("INFERENCE_AI_GATEWAY_PUBLIC_BASE_URL", "https://ai.example.com/ai/")
	cfg, err := LoadConfig()
	if err != nil || cfg.PublicBaseURL.String() != "https://ai.example.com/ai" {
		t.Fatalf("cfg = %+v, err = %v", cfg, err)
	}
}

func TestLoadConfigRejectsUnsafePublicBaseURL(t *testing.T) {
	for name, raw := range map[string]string{
		"missing": "", "relative": "/ai", "userinfo": "https://user@ai.example.com",
		"query": "https://ai.example.com?debug=1", "fragment": "https://ai.example.com#debug",
		"http": "http://ai.example.com",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://redacted")
			t.Setenv("INFERENCE_AI_GATEWAY_PUBLIC_BASE_URL", raw)
			t.Setenv("INFERENCE_AI_GATEWAY_ALLOW_HTTP", "")
			if _, err := LoadConfig(); err == nil {
				t.Fatal("LoadConfig() error = nil")
			}
		})
	}
}

func TestLoadConfigAllowsExplicitHTTPAndUsesSafeDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://redacted")
	t.Setenv("INFERENCE_AI_GATEWAY_PUBLIC_BASE_URL", "http://127.0.0.1:8080/base/")
	t.Setenv("INFERENCE_AI_GATEWAY_ALLOW_HTTP", "true")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GatewayNamespace != "ani-aigw" || cfg.GatewayName != "ani-aigw" || cfg.GatewayController != "gateway.envoyproxy.io/gatewayclass-controller" || cfg.ReconcileInterval != time.Second || cfg.RequestTimeout != 2*time.Second || cfg.StatusTimeout != 45*time.Second || cfg.LeaseDuration != 30*time.Second || cfg.HealthPort != 9206 || !cfg.AllowHTTP {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestLoadConfigRejectsUnsupportedOverridesAndMalformedSettings(t *testing.T) {
	for name, set := range map[string]map[string]string{
		"namespace override": {"INFERENCE_AI_GATEWAY_NAMESPACE": "other"},
		"gateway override":   {"INFERENCE_AI_GATEWAY_NAME": "other"},
		"bad duration":       {"INFERENCE_AI_GATEWAY_REQUEST_TIMEOUT_SECONDS": "zero"},
		"zero duration":      {"INFERENCE_AI_GATEWAY_STATUS_TIMEOUT_SECONDS": "0"},
		"large lease":        {"INFERENCE_AI_GATEWAY_LEASE_DURATION_SECONDS": "301"},
		"lease too short":    {"INFERENCE_AI_GATEWAY_REQUEST_TIMEOUT_SECONDS": "20", "INFERENCE_AI_GATEWAY_LEASE_DURATION_SECONDS": "30"},
		"overflow duration":  {"INFERENCE_AI_GATEWAY_REQUEST_TIMEOUT_SECONDS": "9223372036854775807"},
		"bad port":           {"INFERENCE_AI_GATEWAY_HEALTH_PORT": "70000"},
		"bad HTTP flag":      {"INFERENCE_AI_GATEWAY_ALLOW_HTTP": "yes"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://redacted")
			t.Setenv("INFERENCE_AI_GATEWAY_PUBLIC_BASE_URL", "https://ai.example.com")
			for key, value := range set {
				t.Setenv(key, value)
			}
			if _, err := LoadConfig(); err == nil {
				t.Fatal("LoadConfig() error = nil")
			}
		})
	}
}

func TestLoadConfigRejectsNonCanonicalPublicPath(t *testing.T) {
	for _, raw := range []string{"https://ai.example.com//prefix", "https://ai.example.com/a/../prefix", "https://ai.example.com/a\\prefix"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://redacted")
			t.Setenv("INFERENCE_AI_GATEWAY_PUBLIC_BASE_URL", raw)
			if _, err := LoadConfig(); err == nil {
				t.Fatal("LoadConfig() error = nil")
			}
		})
	}
}
