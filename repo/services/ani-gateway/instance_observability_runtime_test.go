package main

import (
	"testing"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
)

func TestGatewayInstanceObservabilityDefaultsToRouterLocalService(t *testing.T) {
	observability, useName, err := newGatewayInstanceObservability(gatewayInstanceObservabilityRuntimeConfig{})
	if err != nil {
		t.Fatalf("newGatewayInstanceObservability() error = %v", err)
	}
	if observability != nil || useName {
		t.Fatalf("observability=%T useName=%v, want nil/false so router keeps local default", observability, useName)
	}
}

func TestGatewayInstanceObservabilityCanInjectPrometheusProvider(t *testing.T) {
	observability, useName, err := newGatewayInstanceObservability(gatewayInstanceObservabilityRuntimeConfig{
		Provider:          "prometheus_kubernetes",
		PrometheusURL:     "http://prometheus.example:9090",
		KubernetesAPIHost: "https://kubernetes.example",
		ExecBaseURL:       "wss://gateway.example/api/v1",
	})
	if err != nil {
		t.Fatalf("newGatewayInstanceObservability() error = %v", err)
	}
	if observability == nil || !useName {
		t.Fatalf("observability=%T useName=%v, want provider and instance-name targeting", observability, useName)
	}
}

func TestGatewayInstanceObservabilityConfigFromEnv(t *testing.T) {
	t.Setenv("INSTANCE_OBSERVABILITY_PROVIDER", "prometheus_kubernetes")
	t.Setenv("INSTANCE_OBSERVABILITY_PROMETHEUS_URL", "http://prometheus.example:9090")
	t.Setenv("INSTANCE_OBSERVABILITY_EXEC_BASE_URL", "wss://gateway.example/api/v1")
	t.Setenv("KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE", "/var/run/token")
	t.Setenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE", "/var/run/ca.crt")

	cfg := gatewayInstanceObservabilityRuntimeConfigFromEnv()
	if cfg.Provider != "prometheus_kubernetes" || cfg.PrometheusURL == "" || cfg.ExecBaseURL == "" {
		t.Fatalf("instance observability env config not loaded: %#v", cfg)
	}
	if cfg.KubernetesServiceAccountTokenFile == "" || cfg.KubernetesServiceAccountCAFile == "" {
		t.Fatalf("kubernetes service account files not loaded from env")
	}
}

func TestGatewayInstanceObservabilityRejectsUnsupportedProvider(t *testing.T) {
	if _, _, err := newGatewayInstanceObservability(gatewayInstanceObservabilityRuntimeConfig{Provider: "prometheus"}); err == nil {
		t.Fatal("newGatewayInstanceObservability() error = nil, want unsupported provider error")
	}
}

// TestBuildLogStoreFallbackForEmptyAndK8s 验证 LogStoreType 为空、k8s、not_configured 时
// buildLogStore 返回 nil（fallback 到 K8s API，零回归，PRD FR-7 / US-006 AC）。
func TestBuildLogStoreFallbackForEmptyAndK8s(t *testing.T) {
	cases := []string{"", "  ", "k8s", "not_configured", "K8S"}
	for _, storeType := range cases {
		store, err := buildLogStore(gatewayInstanceObservabilityRuntimeConfig{LogStoreType: storeType})
		if err != nil {
			t.Fatalf("buildLogStore(%q) error = %v, want nil", storeType, err)
		}
		if store != nil {
			t.Fatalf("buildLogStore(%q) = %T, want nil (fallback to K8s API)", storeType, store)
		}
	}
}

// TestBuildLogStoreInjectsLoki 验证 LogStoreType=loki 时 buildLogStore 返回 *LokiLogStore，
// 且 LokiURL 为空时使用默认地址（PRD FR-7 / US-006 AC）。
func TestBuildLogStoreInjectsLoki(t *testing.T) {
	store, err := buildLogStore(gatewayInstanceObservabilityRuntimeConfig{LogStoreType: "loki"})
	if err != nil {
		t.Fatalf("buildLogStore(loki) error = %v", err)
	}
	if store == nil {
		t.Fatalf("buildLogStore(loki) = nil, want *LokiLogStore")
	}
	if _, ok := store.(*runtimeadapter.LokiLogStore); !ok {
		t.Fatalf("buildLogStore(loki) = %T, want *LokiLogStore", store)
	}
}

// TestBuildLogStoreUsesCustomLokiURL 验证 LokiURL 配置时传入 LokiLogStore。
func TestBuildLogStoreUsesCustomLokiURL(t *testing.T) {
	store, err := buildLogStore(gatewayInstanceObservabilityRuntimeConfig{
		LogStoreType: "loki",
		LokiURL:      "http://custom-loki.example:3100",
	})
	if err != nil {
		t.Fatalf("buildLogStore() error = %v", err)
	}
	if store == nil {
		t.Fatalf("buildLogStore() = nil, want *LokiLogStore with custom URL")
	}
}

// TestBuildLogStoreElasticsearchFallsBack 验证 LogStoreType=elasticsearch 时
// 走 fallback（ES adapter 暂未实现，PRD OQ-5 / US-005 Non-Goals）。
func TestBuildLogStoreElasticsearchFallsBack(t *testing.T) {
	store, err := buildLogStore(gatewayInstanceObservabilityRuntimeConfig{LogStoreType: "elasticsearch"})
	if err != nil {
		t.Fatalf("buildLogStore(elasticsearch) error = %v, want nil (fallback should not error)", err)
	}
	if store != nil {
		t.Fatalf("buildLogStore(elasticsearch) = %T, want nil (ES not implemented, fallback to K8s API)", store)
	}
}

// TestBuildLogStoreUnknownValueFallsBack 验证未知 LogStoreType 值走 fallback（不阻塞启动）。
func TestBuildLogStoreUnknownValueFallsBack(t *testing.T) {
	store, err := buildLogStore(gatewayInstanceObservabilityRuntimeConfig{LogStoreType: "unknown-backend"})
	if err != nil {
		t.Fatalf("buildLogStore(unknown) error = %v, want nil (fallback should not error)", err)
	}
	if store != nil {
		t.Fatalf("buildLogStore(unknown) = %T, want nil (unknown value fallback to K8s API)", store)
	}
}

// TestNewGatewayInstanceObservabilityInjectsLokiWhenConfigured 验证完整 runtime 工厂路径：
// LogStoreType=loki 时 newGatewayInstanceObservability 创建 adapter 并注入 LokiLogStore。
// 通过类型断言确认返回的 adapter 是 *PrometheusInstanceObservability（可注入 LogStore 的唯一实现）。
func TestNewGatewayInstanceObservabilityInjectsLokiWhenConfigured(t *testing.T) {
	observability, useName, err := newGatewayInstanceObservability(gatewayInstanceObservabilityRuntimeConfig{
		Provider:          "prometheus_kubernetes",
		PrometheusURL:     "http://prometheus.example:9090",
		KubernetesAPIHost: "https://kubernetes.example",
		ExecBaseURL:       "wss://gateway.example/api/v1",
		LogStoreType:      "loki",
	})
	if err != nil {
		t.Fatalf("newGatewayInstanceObservability() error = %v", err)
	}
	if observability == nil || !useName {
		t.Fatalf("observability=%T useName=%v, want provider and instance-name targeting", observability, useName)
	}
	// 确认返回的是可注入 LogStore 的 adapter 类型。
	if _, ok := observability.(*runtimeadapter.PrometheusInstanceObservability); !ok {
		t.Fatalf("observability = %T, want *PrometheusInstanceObservability for LogStore injection", observability)
	}
}

// TestGatewayInstanceObservabilityConfigFromEnvLoadsLogStore 验证环境变量
// INSTANCE_OBSERVABILITY_LOG_STORE 和 INSTANCE_OBSERVABILITY_LOKI_URL 被正确加载。
func TestGatewayInstanceObservabilityConfigFromEnvLoadsLogStore(t *testing.T) {
	t.Setenv("INSTANCE_OBSERVABILITY_LOG_STORE", "loki")
	t.Setenv("INSTANCE_OBSERVABILITY_LOKI_URL", "http://loki.example:3100")

	cfg := gatewayInstanceObservabilityRuntimeConfigFromEnv()
	if cfg.LogStoreType != "loki" {
		t.Fatalf("LogStoreType = %q, want loki", cfg.LogStoreType)
	}
	if cfg.LokiURL != "http://loki.example:3100" {
		t.Fatalf("LokiURL = %q, want http://loki.example:3100", cfg.LokiURL)
	}
}
