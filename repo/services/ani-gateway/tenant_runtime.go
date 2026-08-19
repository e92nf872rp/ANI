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

// newGatewayTenantStore 连接控制面元数据库并构造 TenantService（/admin/tenants 最小读写）。
//
// 与仓库其它 PG runtime 一致，从标准 DATABASE_URL 读取控制面数据库地址；
// 未配置时返回 ErrNotConfigured，由调用方决定启动策略（tenant 管理端点必须要有实现）。
func newGatewayTenantStore(ctx context.Context) (ports.TenantService, func(), error) {
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
		return nil, func() {}, fmt.Errorf("%w: tenant store database unreachable: %w", ports.ErrUnavailable, err)
	}
	return runtimeadapter.NewPostgresTenant(store), closeStore, nil
}
