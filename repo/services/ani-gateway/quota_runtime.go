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

// newGatewayQuotaStore 连接控制面元数据库并构造 QuotaAdminService（管理端点用，走 platform bypass RLS）
// 和 QuotaStoreService（租户自查 /quotas/me 用，走 tenant RLS）。它同时返回底层
// MetadataStore 以供 GPU spec 删除时的跨租户 in-use 检查使用（WithPlatformTx bypass RLS）。
//
// 与仓库其它 PG runtime 一致，从标准 DATABASE_URL 读取控制面数据库地址；
// 未配置时返回 ErrNotConfigured，由调用方决定启动策略（quota 管理端点必须要有实现）。
// 两个 quota 接口由同一个 *PostgresQuota 实例实现（编译期断言见 postgres_quota.go）。
func newGatewayQuotaStore(ctx context.Context) (ports.QuotaAdminService, ports.QuotaStoreService, ports.MetadataStore, func(), error) {
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return nil, nil, nil, func() {}, fmt.Errorf("%w: DATABASE_URL is required for quota admin endpoints", ports.ErrNotConfigured)
	}
	store, closeStore, err := bootstrap.ConnectMetadataStore(ctx, dsn)
	if err != nil {
		return nil, nil, nil, func() {}, err
	}
	if err := store.Ping(ctx); err != nil {
		closeStore()
		return nil, nil, nil, func() {}, fmt.Errorf("%w: quota store database unreachable: %w", ports.ErrUnavailable, err)
	}
	quota := runtimeadapter.NewPostgresQuota(store)
	return quota, quota, store, closeStore, nil
}
