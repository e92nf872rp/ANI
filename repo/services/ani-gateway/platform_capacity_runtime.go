package main

import (
	"fmt"
	"os"
	"strings"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

// newGatewayPlatformCapacityService 按 env 装配平台容量态势服务。
// PLATFORM_CAPACITY_PROVIDER 取值：
//   - "" / "local" / "not_configured" → 返回 nil，router 回退 local 确定性降级；
//   - "kubernetes_rest" → 返回 real adapter（组合 GPUInventory +
//     KubernetesRESTClient + TenantService），任一依赖缺失时按降级语义
//     仍返回 real adapter（adapter 内部 real_provider=false + reason）；
//   - 其他值 → 返回 ErrUnsupported。
// 复用 GPU_INVENTORY_PROVIDER 的 K8s 连接配置与已装配的 gpuInventory /
// kubernetesRESTClient / tenantService，不重复建连。
func newGatewayPlatformCapacityService(cfg gatewayGPUInventoryRuntimeConfig, gpuInventory ports.GPUInventory, k8sClient *runtimeadapter.KubernetesRESTClient, tenantService ports.TenantService) (ports.PlatformCapacityService, error) {
	switch mode := strings.TrimSpace(os.Getenv("PLATFORM_CAPACITY_PROVIDER")); mode {
	case "", "local", "not_configured":
		return nil, nil
	case "kubernetes_rest":
		// 依赖缺失时不阻塞启动：real adapter 内部按单源降级语义返回
		// 200 + real_provider=false + reason。
		return runtimeadapter.NewKubernetesPlatformCapacityService(gpuInventory, k8sClient, tenantService), nil
	default:
		return nil, fmt.Errorf("%w: unsupported PLATFORM_CAPACITY_PROVIDER %q", ports.ErrPlatformCapacityUnsupported, mode)
	}
}
