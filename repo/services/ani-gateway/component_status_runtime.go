package main

import (
	"fmt"
	"os"
	"strings"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

// newGatewayComponentStatusService 按 env 装配平台组件状态服务。
// COMPONENT_STATUS_PROVIDER 取值：
//   - "" / "local" / "not_configured" → 返回 nil，router 回退 local 确定性降级；
//   - "kubernetes_rest" → 返回 real adapter（组合 KubernetesRESTClient +
//     PlatformServiceHealthReader），依赖缺失时按降级语义仍返回 real adapter
//     （adapter 内部按单源降级，real_provider=false + reason）；
//   - 其他值 → 返回 ErrComponentStatusUnsupported。
//
// 复用已装配的 kubernetesRESTClient 与 platformServiceHealthReader，不重复建连。
func newGatewayComponentStatusService(k8sClient *runtimeadapter.KubernetesRESTClient, health ports.PlatformServiceHealthReader) (ports.ComponentStatusService, error) {
	switch mode := strings.TrimSpace(os.Getenv("COMPONENT_STATUS_PROVIDER")); mode {
	case "", "local", "not_configured":
		return nil, nil
	case "kubernetes_rest":
		// 依赖缺失时不阻塞启动：real adapter 内部按单源降级语义返回
		// 200 + real_provider=false + reason。
		return runtimeadapter.NewKubernetesComponentStatusService(k8sClient, health, 0), nil
	default:
		return nil, fmt.Errorf("%w: unsupported COMPONENT_STATUS_PROVIDER %q", ports.ErrComponentStatusUnsupported, mode)
	}
}
