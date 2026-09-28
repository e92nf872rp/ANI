package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

// TenantNamespaceApplier 收窄 K8s apply 依赖，便于 handler 层单测注入 fake。
// KubernetesRESTClient 天然满足该接口。
type TenantNamespaceApplier interface {
	ApplyManifests(ctx context.Context, manifests []ports.WorkloadManifest) ([]string, error)
}

// EnsureTenantNamespace 确保租户命名空间 ani-tenant-<tenantID> 存在。
// 复用 renderPlatformWorkloadNamespace 渲染（标签与 workload 路径完全一致，
// 避免 SSA 字段管理冲突）；SSA 幂等，已存在时无副作用。
// 单独 apply 一个 manifest：compensateAppliedManifests 回滚路径会跳过 Namespace，
// 不会因后续失败删除命名空间。
func EnsureTenantNamespace(ctx context.Context, applier TenantNamespaceApplier, tenantID string) error {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return fmt.Errorf("%w: tenant id is required for tenant namespace ensure", ports.ErrInvalid)
	}
	if applier == nil {
		return fmt.Errorf("%w: tenant namespace applier is not configured", ports.ErrNotConfigured)
	}
	namespace := renderPlatformWorkloadNamespace(tenantID)
	_, err := applier.ApplyManifests(ctx, []ports.WorkloadManifest{namespace})
	return err
}
