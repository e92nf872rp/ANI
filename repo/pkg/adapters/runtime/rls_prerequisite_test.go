//go:build integration

// Package runtime 存放依赖真实基础设施（PG/RLS、K8s 等）的 adapter 集成测试。
// 本文件对应 issue-000 Phase 0 前置验证：确认 RLS 双 policy（platform_bypass + self）
// 在真实 PG 实例上成立，是 #3/#4/#5 adapter 管理方法依赖 WithPlatformTx 的前提。
package runtime

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kubercloud/ani/pkg/adapters/postgres"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
)

// 集成测试连接真实 PostgreSQL（李宇 migration 已落地），验证 RLS 双 policy 前提：
//   - WithPlatformTx（不设 app.current_tenant_id）能看到 resource_quota 所有行（platform_bypass 放行）
//   - WithTenantTx（设 app.current_tenant_id）只能看到本租户行（self policy 生效）
//   - WithTenantTx 试图 INSERT 别的 tenant_id 行会被 RLS 拒绝（self policy WITH CHECK）
//
// 运行命令（PG 已部署在 10.10.1.66:30945）：
//
//	ANI_TEST_PG_DSN="postgres://ani_app_user:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable" \
//	  go test ./pkg/adapters/runtime/ -v -run RLS -tags integration
//
// 集成测试为手动验证项，不作为硬性门禁（对应 issue-000 Phase 0 前置验证）。
// 若测试失败，说明 RLS 双 policy 前提不成立，#3/#4/#5 的 WithPlatformTx 管理方法作废，需改用别的事务模型。

// testDSN 读取环境变量，默认连本地 PG。
func testDSN() string {
	if dsn := os.Getenv("ANI_TEST_PG_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://ani_app_user:ani_dev_password@127.0.0.1:5432/ani?sslmode=disable"
}

// rlsTestEnv 封装集成测试用的连接池、MetadataStore 和两个测试租户。
type rlsTestEnv struct {
	pool    *pgxpool.Pool
	store   ports.MetadataStore
	t       *testing.T
	tenantA uuid.UUID // 租户 A（本租户视角）
	tenantB uuid.UUID // 租户 B（跨租户视角）
}

// newRLSTestEnv 建立连接、创建 MetadataStore、插入两个测试租户和各自的 resource_quota 行。
// 使用 WithPlatformTx bypass RLS 插入 resource_quota 种子数据。
func newRLSTestEnv(t *testing.T) *rlsTestEnv {
	t.Helper()
	dsn := testDSN()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("连接 PG 失败 %s: %v（确认 PG 已启动并可通过 ANI_TEST_PG_DSN 访问）", dsn, err)
	}
	env := &rlsTestEnv{
		pool:  pool,
		store: postgres.NewMetadataStore(pool),
		t:     t,
	}
	t.Cleanup(env.cleanup)

	// 确保 resource_quota_meta 有测试所需维度（FK 前置；李宇 migration 已 seed 则 ON CONFLICT 跳过）。
	// resource_quota_meta 无 RLS，可直接 INSERT。
	// gpu_count 用于测试 1/2 的 SELECT；cpu_core 用于测试 3 的跨租户 INSERT，必须显式 seed，
	// 否则测试 3 会因 FK 约束失败而非 RLS 拒绝，产生假阳性。
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO resource_quota_meta (resource_type, display_name, unit, is_discrete, default_quota, enabled)
		VALUES
			('gpu_count', 'GPU 数量', '个', true, 0, true),
			('cpu_core', 'CPU 核心', '个', true, 0, true)
		ON CONFLICT (resource_type) DO NOTHING
	`); err != nil {
		t.Fatalf("seed resource_quota_meta 失败: %v（确认李宇 migration 已创建 resource_quota_meta 表）", err)
	}

	// 插入两个测试租户（tenants 表无 RLS，可直接 INSERT）。
	// name 带 UUID 前缀避免并发测试或残留数据冲突。
	env.tenantA = uuid.New()
	env.tenantB = uuid.New()
	for _, tid := range []uuid.UUID{env.tenantA, env.tenantB} {
		short := tid.String()[:8]
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO tenants (id, name, display_name, status)
			VALUES ($1, $2, $3, 'active')
			ON CONFLICT (id) DO NOTHING
		`, tid, fmt.Sprintf("rls-test-%s", short), fmt.Sprintf("RLS测试-%s", short)); err != nil {
			t.Fatalf("插入测试租户 %s 失败: %v", short, err)
		}
	}

	// 用 WithPlatformTx 给两个租户各插入一行 resource_quota（bypass RLS）。
	// 这同时也是 platform_bypass policy 的首次真实验证：若 bypass 不成立，这里会失败。
	if err := env.store.WithPlatformTx(context.Background(), func(ctx context.Context, tx ports.MetadataTx) error {
		for _, tid := range []uuid.UUID{env.tenantA, env.tenantB} {
			if _, err := tx.Exec(ctx, `
				INSERT INTO resource_quota (tenant_id, resource_type, total, reserved, used)
				VALUES ($1, 'gpu_count', 10, 0, 0)
				ON CONFLICT (tenant_id, resource_type) DO NOTHING
			`, tid); err != nil {
				return fmt.Errorf("insert resource_quota for tenant %s: %w", tid, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("WithPlatformTx 插入 resource_quota 失败: %v（platform_bypass policy 可能未创建）", err)
	}

	return env
}

// cleanup 删除测试租户（CASCADE 级联删除 resource_quota/resource_reservations）并关闭连接池。
func (e *rlsTestEnv) cleanup() {
	if e.pool != nil {
		for _, tid := range []uuid.UUID{e.tenantA, e.tenantB} {
			_, _ = e.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tid)
		}
		e.pool.Close()
	}
}

// TestRLSPlatformBypass 测试 1：WithPlatformTx（不设 app.current_tenant_id）
// → SELECT resource_quota 能看到所有行（至少两个测试租户的行）。
// 验证 platform_bypass policy（current_setting IS NULL 时放行）成立。
func TestRLSPlatformBypass(t *testing.T) {
	env := newRLSTestEnv(t)
	var count int
	err := env.store.WithPlatformTx(context.Background(), func(ctx context.Context, tx ports.MetadataTx) error {
		// 严格确认本事务未设 app.current_tenant_id（platform_bypass 前提）。
		// current_setting(..., true) 对未设置的 GUC 返回 NULL；若返回空字符串 '' 则双 policy 都不放行。
		var setting *string
		if err := tx.QueryRow(ctx, `SELECT current_setting('app.current_tenant_id', true)`).Scan(&setting); err != nil {
			return fmt.Errorf("查询 current_setting 失败: %w", err)
		}
		if setting != nil {
			return fmt.Errorf("platform tx 中 app.current_tenant_id 应为 NULL，实际=%q（空字符串会导致双 policy 都不放行）", *setting)
		}
		// SELECT 应能看到所有行（至少两个测试租户的行）。
		row := tx.QueryRow(ctx, `SELECT COUNT(*) FROM resource_quota WHERE resource_type='gpu_count'`)
		return row.Scan(&count)
	})
	if err != nil {
		t.Fatalf("WithPlatformTx 查询失败: %v", err)
	}
	if count < 2 {
		t.Fatalf("platform_bypass 前提不成立：WithPlatformTx 只看到 %d 行（期望 >= 2，说明 RLS 拦截了平台操作）", count)
	}
	t.Logf("WithPlatformTx 看到 %d 行 resource_quota（gpu_count），platform_bypass 前提成立", count)
}

// TestRLSTenantSelf 测试 2：WithTenantTx（设 app.current_tenant_id）
// → SELECT resource_quota 只看到本租户行。
// 验证 self policy（tenant_id = current_setting 时放行本租户行、拦截他租户行）成立。
func TestRLSTenantSelf(t *testing.T) {
	env := newRLSTestEnv(t)
	ctx := types.WithTenant(context.Background(), &types.TenantContext{
		TenantID: env.tenantA,
		UserID:   uuid.New(),
		Roles:    []string{"user"},
	})
	var count int
	err := env.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, `SELECT COUNT(*) FROM resource_quota WHERE resource_type='gpu_count'`)
		return row.Scan(&count)
	})
	if err != nil {
		t.Fatalf("WithTenantTx 查询失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("self policy 行为异常：租户 A WithTenantTx 看到 %d 行（期望 1，只应看到自己；若为 2 说明 RLS 未拦截跨租户行）", count)
	}
	t.Logf("租户 A WithTenantTx 看到 %d 行 resource_quota（gpu_count），self policy 生效", count)
}

// TestRLSTenantInsertRejected 测试 3：WithTenantTx 试图 INSERT 别的 tenant_id 行
// → RLS self policy 的 WITH CHECK 拒绝。
// 验证 self policy FOR ALL → WITH CHECK 能阻止租户越权写入他租户行。
func TestRLSTenantInsertRejected(t *testing.T) {
	env := newRLSTestEnv(t)
	ctx := types.WithTenant(context.Background(), &types.TenantContext{
		TenantID: env.tenantA,
		UserID:   uuid.New(),
		Roles:    []string{"user"},
	})
	err := env.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 租户 A 试图插入租户 B 的 resource_quota 行，RLS self policy（FOR ALL → WITH CHECK）应拒绝。
		_, err := tx.Exec(ctx, `
			INSERT INTO resource_quota (tenant_id, resource_type, total, reserved, used)
			VALUES ($1, 'cpu_core', 10, 0, 0)
		`, env.tenantB)
		return err
	})
	if err == nil {
		t.Fatalf("RLS 未拒绝跨租户 INSERT：租户 A 成功插入了租户 B 的 resource_quota 行（self policy WITH CHECK 失效）")
	}
	t.Logf("RLS 拒绝跨租户 INSERT，错误: %v（self policy WITH CHECK 生效）", err)
}
