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

// newGatewayTenantAdminService 连接控制面元数据库并构造租户管理员 Core 端口
//（ports.TenantAdminService：用户/角色管理，走 platform bypass RLS）。
//
// 与 quota_runtime / tenant_runtime 一致，从标准 DATABASE_URL 读取控制面数据库地址；
// 未配置时返回 ErrNotConfigured，由调用方决定启动策略。
func newGatewayTenantAdminService(ctx context.Context) (ports.TenantAdminService, func(), error) {
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
		return nil, func() {}, fmt.Errorf("%w: tenant admin store database unreachable: %w", ports.ErrUnavailable, err)
	}
	return runtimeadapter.NewPostgresTenantAdmin(store), closeStore, nil
}
