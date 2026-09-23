package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// enableTenantInfraProvision 控制租户基础设施异步初始化；单测可置 false。
var enableTenantInfraProvision = true

// scheduleTenantInfraProvision 异步初始化租户基础设施（Harbor 项目 + K8s Namespace）：
// 经 Core SDK 调 POST /admin/tenants/{id}/provision，端点业务层自幂等。
// 最多 3 次，指数退避 1s/2s/4s；失败不回滚租户，逐次写 tenant.infra_provision_failed 审计。
// 单测可将 enableTenantInfraProvision 置 false 以禁用后台 goroutine。
func scheduleTenantInfraProvision(audit ports.AuditStore, tenants ports.TenantSvcClient, tenantID uuid.UUID) {
	// 步骤 1：单测开关关闭时不启 goroutine
	if !enableTenantInfraProvision {
		return
	}
	// 步骤 2：uuid 为值类型，直接拷贝入参

	go func() {
		// 步骤 3：最多 3 次；失败写 tenant.infra_provision_failed（含 attempt）
		backoff := time.Second
		for attempt := 1; attempt <= 3; attempt++ {
			time.Sleep(backoff)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := tenants.ProvisionTenantInfra(ctx, tenantID)
			cancel()
			if err == nil {
				return
			}
			writeAuditFailure(context.Background(), audit, auditResourceTenant, "tenant.infra_provision_failed", map[string]any{
				"tenant_id": tenantID.String(),
				"attempt":   attempt,
			}, err, &tenantID)
			backoff *= 2
		}
	}()
}
