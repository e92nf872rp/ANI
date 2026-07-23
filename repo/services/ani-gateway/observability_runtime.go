package main

import (
	"fmt"
	"os"
	"strings"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

// gatewayObservabilityRuntimeConfig 装配可观测性 PromQL 代理服务的运行时配置。
// 复用 INSTANCE_OBSERVABILITY_PROVIDER 与 INSTANCE_OBSERVABILITY_PROMETHEUS_URL
// （与实例观测快照同一组 env），避免运维同时维护两套可观测性 env。
// 当 INSTANCE_OBSERVABILITY_PROVIDER=prometheus_kubernetes 时，
// 快照指标（GetMetrics）与时序图（Query）链路同时启用真实 Prometheus。
type gatewayObservabilityRuntimeConfig struct {
	Provider       string
	PrometheusURL  string
	InstanceLookup runtimeadapter.InstanceLookup
}

func gatewayObservabilityRuntimeConfigFromEnv(instanceLookup runtimeadapter.InstanceLookup) gatewayObservabilityRuntimeConfig {
	return gatewayObservabilityRuntimeConfig{
		Provider:       os.Getenv("INSTANCE_OBSERVABILITY_PROVIDER"),
		PrometheusURL:  os.Getenv("INSTANCE_OBSERVABILITY_PROMETHEUS_URL"),
		InstanceLookup: instanceLookup,
	}
}

// newGatewayObservabilityService 按 env 装配可观测性 PromQL 代理服务。
// Provider 为 "" / "local" / "not_configured" → 返回 nil，router 回退 local 空结果闭环。
// Provider 为 "prometheus_kubernetes" → 返回真实 PrometheusObservabilityService。
// 其他值 → 返回 ErrUnsupported。
func newGatewayObservabilityService(cfg gatewayObservabilityRuntimeConfig) (ports.ObservabilityService, error) {
	switch provider := strings.TrimSpace(cfg.Provider); provider {
	case "", "local", "not_configured":
		return nil, nil
	case "prometheus_kubernetes":
		// InstanceLookup 允许为 nil：Gateway 启动时 demo instance store 尚未创建，
		// 先构造 service 占位，router 注册 demo instances 后通过 SetInstanceLookup
		// 注入真实 lookup；注入前 QueryRange 返回空结果（见 adapter nil 保护）。
		service, err := runtimeadapter.NewPrometheusObservabilityService(runtimeadapter.PrometheusObservabilityServiceConfig{
			PrometheusURL:  cfg.PrometheusURL,
			InstanceLookup: cfg.InstanceLookup,
		})
		if err != nil {
			return nil, err
		}
		return service, nil
	default:
		return nil, fmt.Errorf("%w: unsupported INSTANCE_OBSERVABILITY_PROVIDER %q", ports.ErrUnsupported, provider)
	}
}
