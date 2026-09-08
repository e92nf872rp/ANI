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

// newGatewayMeteringService 按 METERING_PROVIDER_MODE 装配计量查询 service：
//   - "" / "local"：LocalMeteringService（进程内 token 数据，dev/CI fallback）
//   - "postgres"：PgMeteringService（统一查询 metering_usage_records）
//
// postgres 模式要求 DATABASE_URL 已配置且 Ping 成功，失败即返回错误阻止 Gateway 启动，
// 不得静默降级到 local；未知模式返回 ErrUnsupported 同样阻止启动。
func newGatewayMeteringService(ctx context.Context) (ports.MeteringService, func(), error) {
	mode := strings.TrimSpace(os.Getenv("METERING_PROVIDER_MODE"))
	switch mode {
	case "", "local":
		return runtimeadapter.NewLocalMeteringService(), nil, nil
	case "postgres":
		dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dsn == "" {
			return nil, func() {}, fmt.Errorf("%w: DATABASE_URL is required for postgres metering", ports.ErrNotConfigured)
		}
		store, closeStore, err := bootstrap.ConnectMetadataStore(ctx, dsn)
		if err != nil {
			return nil, func() {}, err
		}
		if err := store.Ping(ctx); err != nil {
			closeStore()
			return nil, func() {}, fmt.Errorf("%w: metering store database unreachable: %w", ports.ErrUnavailable, err)
		}
		return runtimeadapter.NewPgMeteringService(store), closeStore, nil
	default:
		return nil, func() {}, fmt.Errorf("%w: unsupported METERING_PROVIDER_MODE %q", ports.ErrUnsupported, mode)
	}
}
