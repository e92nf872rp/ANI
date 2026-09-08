package main

import (
	"fmt"
	"os"
	"strings"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

// newGatewayComponentDiagnosticsService 按 env 装配平台组件诊断服务（指标 + 日志）。
// COMPONENT_DIAGNOSTICS_PROVIDER 取值：
//   - "" / "local" / "not_configured" → 返回 nil 双 nil，router 回退 local 确定性降级；
//   - "prometheus_loki" → 返回 real adapter（Prometheus 指标 + Loki 日志）；
//     Loki 未配置（INSTANCE_OBSERVABILITY_LOG_STORE != loki）时日志端口返回
//     ErrNotConfigured（503），指标不受影响；
//   - 其他值 → 返回 ErrPlatformComponentDiagnosticsUnsupported。
//
// 复用 INSTANCE_OBSERVABILITY_PROMETHEUS_URL 与 INSTANCE_OBSERVABILITY_LOKI_URL
// （默认 http://ani-loki.ani-s07-observability:3100），不重复建连。
func newGatewayComponentDiagnosticsService() (ports.PlatformComponentMetricsReader, ports.PlatformComponentLogReader, error) {
	switch mode := strings.TrimSpace(os.Getenv("COMPONENT_DIAGNOSTICS_PROVIDER")); mode {
	case "", "local", "not_configured":
		return nil, nil, nil
	case "prometheus_loki":
		var lokiStore *runtimeadapter.LokiLogStore
		if strings.TrimSpace(os.Getenv("INSTANCE_OBSERVABILITY_LOG_STORE")) == "loki" {
			lokiURL := strings.TrimSpace(os.Getenv("INSTANCE_OBSERVABILITY_LOKI_URL"))
			if lokiURL == "" {
				lokiURL = "http://ani-loki.ani-s07-observability:3100"
			}
			store, err := runtimeadapter.NewLokiLogStore(runtimeadapter.LokiLogStoreConfig{BaseURL: lokiURL})
			if err != nil {
				return nil, nil, fmt.Errorf("build component diagnostics loki store: %w", err)
			}
			lokiStore = store
		}
		service := runtimeadapter.NewKubernetesPlatformComponentDiagnosticsService(
			os.Getenv("INSTANCE_OBSERVABILITY_PROMETHEUS_URL"), lokiStore, nil)
		return service, service, nil
	default:
		return nil, nil, fmt.Errorf("%w: unsupported COMPONENT_DIAGNOSTICS_PROVIDER %q", ports.ErrPlatformComponentDiagnosticsUnsupported, mode)
	}
}
