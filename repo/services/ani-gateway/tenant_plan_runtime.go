package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/pkg/ports"
)

// newGatewayTenantPlanService 连接控制面元数据库并构造 TenantPlanService（/admin/plans*）。
//
// 与仓库其它 PG runtime 一致，从标准 DATABASE_URL 读取控制面数据库地址；
// 未配置时返回 ErrNotConfigured，由调用方决定启动策略。
func newGatewayTenantPlanService(ctx context.Context) (ports.TenantPlanService, func(), error) {
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return nil, func() {}, fmt.Errorf("%w: DATABASE_URL is required for tenant admin endpoints", ports.ErrNotConfigured)
	}
	store, closeStore, err := bootstrap.ConnectMetadataStore(ctx, dsn)
	if err != nil {
		return nil, func() {}, err
	}
	if err := store.Ping(ctx); err != nil {
		closeStore()
		return nil, func() {}, fmt.Errorf("%w: tenant plan store database unreachable: %w", ports.ErrUnavailable, err)
	}
	return runtimeadapter.NewPostgresTenantPlan(store), closeStore, nil
}
