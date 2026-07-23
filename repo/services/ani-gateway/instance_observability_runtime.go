package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

type gatewayInstanceObservabilityRuntimeConfig struct {
	Provider                          string
	PrometheusURL                     string
	ExecBaseURL                       string
	KubernetesAPIHost                 string
	KubernetesServiceHost             string
	KubernetesServicePort             string
	KubernetesBearerToken             string
	KubernetesServiceAccountTokenFile string
	KubernetesServiceAccountCAFile    string
	KubernetesFieldManager            string
	HTTPClient                        *http.Client
	// LogStoreType 控制 ListLogs 的日志持久化存储实现（PRD FR-7）。
	// 枚举值：loki → 注入 LokiLogStore；elasticsearch → 暂未实现走 fallback；
	// k8s / 空 / not_configured → nil（fallback 到 K8s pod log API）。
	LogStoreType string
	// LokiURL 是 LogStoreType=loki 时 Loki HTTP API 根地址，为空时使用默认地址。
	LokiURL string
}

func gatewayInstanceObservabilityRuntimeConfigFromEnv() gatewayInstanceObservabilityRuntimeConfig {
	return gatewayInstanceObservabilityRuntimeConfig{
		Provider:                          os.Getenv("INSTANCE_OBSERVABILITY_PROVIDER"),
		PrometheusURL:                     os.Getenv("INSTANCE_OBSERVABILITY_PROMETHEUS_URL"),
		ExecBaseURL:                       os.Getenv("INSTANCE_OBSERVABILITY_EXEC_BASE_URL"),
		KubernetesAPIHost:                 os.Getenv("KUBERNETES_API_HOST"),
		KubernetesServiceHost:             os.Getenv("KUBERNETES_SERVICE_HOST"),
		KubernetesServicePort:             os.Getenv("KUBERNETES_SERVICE_PORT"),
		KubernetesBearerToken:             os.Getenv("KUBERNETES_BEARER_TOKEN"),
		KubernetesServiceAccountTokenFile: os.Getenv("KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE"),
		KubernetesServiceAccountCAFile:    os.Getenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE"),
		KubernetesFieldManager:            os.Getenv("KUBERNETES_PROVIDER_FIELD_MANAGER"),
		LogStoreType:                      os.Getenv("INSTANCE_OBSERVABILITY_LOG_STORE"),
		LokiURL:                           os.Getenv("INSTANCE_OBSERVABILITY_LOKI_URL"),
	}
}

func newGatewayInstanceObservability(cfg gatewayInstanceObservabilityRuntimeConfig) (ports.InstanceObservability, bool, error) {
	switch provider := strings.TrimSpace(cfg.Provider); provider {
	case "", "local", "not_configured":
		return nil, false, nil
	case "prometheus_kubernetes":
		observability, err := runtimeadapter.NewPrometheusInstanceObservability(runtimeadapter.PrometheusInstanceObservabilityConfig{
			PrometheusURL:                     cfg.PrometheusURL,
			KubernetesAPIHost:                 cfg.KubernetesAPIHost,
			KubernetesServiceHost:             cfg.KubernetesServiceHost,
			KubernetesServicePort:             cfg.KubernetesServicePort,
			KubernetesBearerToken:             cfg.KubernetesBearerToken,
			KubernetesServiceAccountTokenFile: cfg.KubernetesServiceAccountTokenFile,
			KubernetesServiceAccountCAFile:    cfg.KubernetesServiceAccountCAFile,
			KubernetesFieldManager:            cfg.KubernetesFieldManager,
			ExecBaseURL:                       cfg.ExecBaseURL,
			HTTPClient:                        cfg.HTTPClient,
		})
		if err != nil {
			return nil, false, err
		}
		// 根据环境变量注入 LogStore 实现（PRD FR-7 / US-006）。
		// 未配置或未实现时返回 nil，ListLogs fallback 到 K8s pod log API（零回归）。
		if store, err := buildLogStore(cfg); err != nil {
			return nil, false, err
		} else if store != nil {
			observability.SetLogStore(store)
		}
		return observability, true, nil
	default:
		return nil, false, fmt.Errorf("%w: unsupported INSTANCE_OBSERVABILITY_PROVIDER %q", ports.ErrUnsupported, provider)
	}
}

// buildLogStore 根据 LogStoreType 选择 LogStore 实现（PRD FR-7）。
//
// 返回值语义：
//   - 非 nil：注入对应 LogStore 实现，ListLogs 走持久化存储路径。
//   - nil：不注入，ListLogs fallback 到 K8s pod log API（零回归）。
//
// 错误仅在配置明确非法时返回（例如 Loki URL 构造失败）。
func buildLogStore(cfg gatewayInstanceObservabilityRuntimeConfig) (ports.LogStore, error) {
	switch storeType := strings.TrimSpace(cfg.LogStoreType); storeType {
	case "", "k8s", "not_configured":
		// fallback 到 K8s API，不注入。
		return nil, nil
	case "loki":
		lokiURL := strings.TrimSpace(cfg.LokiURL)
		if lokiURL == "" {
			lokiURL = "http://ani-loki.ani-s07-observability:3100"
		}
		store, err := runtimeadapter.NewLokiLogStore(runtimeadapter.LokiLogStoreConfig{
			BaseURL: lokiURL,
		})
		if err != nil {
			return nil, fmt.Errorf("build loki log store: %w", err)
		}
		return store, nil
	case "elasticsearch":
		// ES adapter 暂未实现（PRD US-005 Non-Goals / OQ-5），走 fallback。
		slog.Warn("INSTANCE_OBSERVABILITY_LOG_STORE=elasticsearch not yet implemented, falling back to K8s pod log API")
		return nil, nil
	default:
		// 未知值走 fallback，避免配置错误阻塞启动。
		slog.Warn("unknown INSTANCE_OBSERVABILITY_LOG_STORE value, falling back to K8s pod log API", "value", storeType)
		return nil, nil
	}
}
