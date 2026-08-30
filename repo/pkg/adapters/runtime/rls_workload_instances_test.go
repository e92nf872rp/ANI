//go:build integration

// Package runtime 存放依赖真实基础设施（PG/RLS）的 adapter 集成测试。
//
// 本文件验证 workload_instances 表的 RLS 双策略（platform_bypass + self）
// 在真实 PG 实例上成立，是 specInUse 跨租户检查的前提。
//
// 修复前：workload_instances 仅有 RESTRICTIVE tenant_isolation 策略，无 PERMISSIVE
// 策略，PostgreSQL RLS 拒绝所有行，导致 specInUse COUNT(*) 假阴性。
// 修复后（20260825000100_workload_instances_rls_fix.sql）：PERMISSIVE 双策略。
//
// 运行命令（PG 已部署在 10.10.1.66:30945）：
//
//	ANI_TEST_ADMIN_DSN="postgres://ani:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable" \
//	  go test ./pkg/adapters/runtime/ -v -run TestRLSWorkloadInstances -tags integration
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

// adminWItestDSN 读取管理员 DSN（superuser，用于创建临时角色和插入测试数据）。
func adminWItestDSN() string {
	if dsn := os.Getenv("ANI_TEST_ADMIN_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://ani:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable"
}

// TestRLSWorkloadInstancesPlatformBypass 验证修复后 WithPlatformTx 能跨租户看到所有行。
// 用 superuser 创建临时非 superuser 角色 + 插入测试数据，再用临时角色验证 RLS。
func TestRLSWorkloadInstancesPlatformBypass(t *testing.T) {
	adminDSN := adminWItestDSN()
	adminPool, err := pgxpool.New(context.Background(), adminDSN)
	if err != nil {
		t.Fatalf("连接 admin PG 失败 %s: %v", adminDSN, err)
	}
	defer adminPool.Close()

	ctx := context.Background()

	// --- 查询 workload_instances 上的所有 RLS 策略 ---
	policyRows, _ := adminPool.Query(ctx, `
		SELECT polname, polpermissive, pg_get_expr(polqual, polrelid)
		FROM pg_policy WHERE polrelid = 'workload_instances'::regclass
	`)
	t.Log("=== workload_instances RLS 策略 ===")
	hasPermissive := false
	for policyRows.Next() {
		var name, expr string
		var permissive bool
		if err := policyRows.Scan(&name, &permissive, &expr); err != nil {
			t.Fatalf("scan policy 失败: %v", err)
		}
		polType := "RESTRICTIVE"
		if permissive {
			polType = "PERMISSIVE"
			hasPermissive = true
		}
		t.Logf("  %s %s: %s", polType, name, expr)
	}
	policyRows.Close()
	if !hasPermissive {
		t.Fatal("无 PERMISSIVE 策略 — 迁移 20260825000100 可能未执行，PostgreSQL RLS 会拒绝所有行")
	}

	// --- 创建临时非 superuser 角色（模拟 ani_app_user，受 RLS 约束） ---
	testRoleName := fmt.Sprintf("rls_wi_test_%s", uuid.New().String()[:8])
	testRolePassword := "rls-wi-test-pwd"

	if _, err := adminPool.Exec(ctx,
		fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s' IN ROLE ani_app", testRoleName, testRolePassword)); err != nil {
		t.Fatalf("创建临时角色失败: %v", err)
	}
	if _, err := adminPool.Exec(ctx,
		fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE ON workload_instances TO %s", testRoleName)); err != nil {
		t.Fatalf("GRANT workload_instances 失败: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(),
			fmt.Sprintf("DROP ROLE IF EXISTS %s", testRoleName))
	})

	// --- 构建非 superuser DSN 并连接 ---
	appDSN := fmt.Sprintf("postgres://%s:%s@10.10.1.66:30945/ani?sslmode=disable",
		testRoleName, testRolePassword)
	appPool, err := pgxpool.New(context.Background(), appDSN)
	if err != nil {
		t.Fatalf("连接 app PG 失败 %s: %v", appDSN, err)
	}
	defer appPool.Close()

	store := postgres.NewMetadataStore(appPool)

	// --- 用 superuser 连接插入测试数据 ---
	tenantID := uuid.New()
	instanceID := fmt.Sprintf("rls-test-%s", uuid.New().String()[:8])
	starterPlanID := "00000000-0000-0000-0000-000000000001"

	if _, err := adminPool.Exec(ctx, `
		INSERT INTO tenants (id, name, display_name, status, plan_id)
		VALUES ($1, $2, $3, 'active', $4) ON CONFLICT (id) DO NOTHING
	`, tenantID, "rls-wi-test-"+instanceID, "RLS WI测试", starterPlanID); err != nil {
		t.Fatalf("插入测试租户失败: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	})

	gpuStatusJSON := `{"SpecID": "test-spec-001"}`
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO workload_instances (tenant_id, instance_id, name, workload_kind, state, gpu_status, created_at, updated_at)
		VALUES ($1, $2, $2, 'gpu_container', 'running', $3::jsonb, NOW(), NOW())
		ON CONFLICT (tenant_id, instance_id) DO UPDATE SET state='running', gpu_status=$3::jsonb, updated_at=NOW()
	`, tenantID, instanceID, gpuStatusJSON); err != nil {
		t.Fatalf("插入测试实例失败: %v", err)
	}

	// --- sanity check: superuser 能看到行 ---
	var adminCount int64
	if err := adminPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM workload_instances WHERE instance_id = $1`, instanceID).Scan(&adminCount); err != nil {
		t.Fatalf("admin 查询失败: %v", err)
	}
	if adminCount != 1 {
		t.Fatalf("admin 应看到 1 行，实际 %d", adminCount)
	}
	t.Logf("admin (superuser) 看到 %d 行（instance_id 过滤）", adminCount)

	// --- 测试 1: WithPlatformTx（非 superuser，不设 tenant_id）应看到 >= 1 行 ---
	t.Log("=== 测试 1: WithPlatformTx（非 superuser，不设 tenant_id）===")
	var platformCount int64
	err = store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		var setting *string
		if err := tx.QueryRow(ctx, `SELECT current_setting('app.current_tenant_id', true)`).Scan(&setting); err != nil {
			return err
		}
		if setting != nil {
			return fmt.Errorf("app.current_tenant_id 应为 NULL，实际=%q", *setting)
		}
		return tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM workload_instances
			WHERE state <> 'deleted'
			  AND (gpu_status->>'SpecID' = $1 OR compute_summary->>'SpecID' = $1)
		`, "test-spec-001").Scan(&platformCount)
	})
	if err != nil {
		t.Fatalf("WithPlatformTx 查询失败: %v", err)
	}
	t.Logf("WithPlatformTx 结果: %d 行", platformCount)
	if platformCount == 0 {
		t.Errorf("WithPlatformTx 返回 0 行 — platform_bypass 策略未生效，specInUse 会假阴性")
	}

	// --- 测试 2: WithTenantTx（非 superuser，设正确 tenant_id）应看到 1 行 ---
	t.Log("=== 测试 2: WithTenantTx（非 superuser，设正确 tenant_id）===")
	tenantCtx := types.WithTenant(ctx, &types.TenantContext{
		TenantID: tenantID,
		UserID:   uuid.New(),
		Roles:    []string{"user"},
	})
	var tenantCount int64
	err = store.WithTenantTx(tenantCtx, func(ctx context.Context, tx ports.MetadataTx) error {
		return tx.QueryRow(ctx, `SELECT COUNT(*) FROM workload_instances WHERE tenant_id = $1::uuid`, tenantID.String()).Scan(&tenantCount)
	})
	if err != nil {
		t.Fatalf("WithTenantTx 查询失败: %v", err)
	}
	t.Logf("WithTenantTx 结果: %d 行（设 tenant_id=%s）", tenantCount, tenantID)
	if tenantCount == 0 {
		t.Errorf("WithTenantTx 返回 0 行 — self 策略未生效，租户查询会返回空")
	}
}

// TestRLSWorkloadInstancesTenantIsolation 验证 WithTenantTx 设 tenant A 时
// 看不到 tenant B 的行（跨租户隔离）。
func TestRLSWorkloadInstancesTenantIsolation(t *testing.T) {
	adminDSN := adminWItestDSN()
	adminPool, err := pgxpool.New(context.Background(), adminDSN)
	if err != nil {
		t.Fatalf("连接 admin PG 失败: %v", err)
	}
	defer adminPool.Close()

	ctx := context.Background()

	// 创建临时非 superuser 角色
	testRoleName := fmt.Sprintf("rls_wi_iso_%s", uuid.New().String()[:8])
	testRolePassword := "rls-wi-iso-pwd"
	if _, err := adminPool.Exec(ctx,
		fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s' IN ROLE ani_app", testRoleName, testRolePassword)); err != nil {
		t.Fatalf("创建临时角色失败: %v", err)
	}
	if _, err := adminPool.Exec(ctx,
		fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE ON workload_instances TO %s", testRoleName)); err != nil {
		t.Fatalf("GRANT 失败: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(),
			fmt.Sprintf("DROP ROLE IF EXISTS %s", testRoleName))
	})

	appDSN := fmt.Sprintf("postgres://%s:%s@10.10.1.66:30945/ani?sslmode=disable",
		testRoleName, testRolePassword)
	appPool, err := pgxpool.New(context.Background(), appDSN)
	if err != nil {
		t.Fatalf("连接 app PG 失败: %v", err)
	}
	defer appPool.Close()
	store := postgres.NewMetadataStore(appPool)

	// 创建两个租户，各插入一行实例
	tenantA := uuid.New()
	tenantB := uuid.New()
	starterPlanID := "00000000-0000-0000-0000-000000000001"
	for _, tid := range []uuid.UUID{tenantA, tenantB} {
		if _, err := adminPool.Exec(ctx, `
			INSERT INTO tenants (id, name, display_name, status, plan_id)
			VALUES ($1, $2, $3, 'active', $4) ON CONFLICT (id) DO NOTHING
		`, tid, "rls-iso-"+tid.String()[:8], "隔离测试", starterPlanID); err != nil {
			t.Fatalf("插入租户失败: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), `DELETE FROM tenants WHERE id IN ($1, $2)`, tenantA, tenantB)
	})

	instA := fmt.Sprintf("rls-iso-a-%s", uuid.New().String()[:8])
	instB := fmt.Sprintf("rls-iso-b-%s", uuid.New().String()[:8])
	for _, item := range []struct {
		tid uuid.UUID
		iid string
	}{{tenantA, instA}, {tenantB, instB}} {
		if _, err := adminPool.Exec(ctx, `
			INSERT INTO workload_instances (tenant_id, instance_id, name, workload_kind, state, created_at, updated_at)
			VALUES ($1, $2, $2, 'container', 'running', NOW(), NOW())
			ON CONFLICT (tenant_id, instance_id) DO UPDATE SET state='running', updated_at=NOW()
		`, item.tid, item.iid); err != nil {
			t.Fatalf("插入实例失败: %v", err)
		}
	}

	// 租户 A 查询应只看到自己的行
	tenantACtx := types.WithTenant(ctx, &types.TenantContext{
		TenantID: tenantA,
		UserID:   uuid.New(),
		Roles:    []string{"user"},
	})
	var countA int64
	err = store.WithTenantTx(tenantACtx, func(ctx context.Context, tx ports.MetadataTx) error {
		return tx.QueryRow(ctx, `SELECT COUNT(*) FROM workload_instances WHERE instance_id IN ($1, $2)`, instA, instB).Scan(&countA)
	})
	if err != nil {
		t.Fatalf("租户 A 查询失败: %v", err)
	}
	t.Logf("租户 A 看到 %d 行（期望 1，只应看到自己的）", countA)
	if countA != 1 {
		t.Errorf("租户隔离失效: 租户 A 看到 %d 行（期望 1）", countA)
	}
}
