//go:build integration

// Package runtime 存放依赖真实基础设施（PG/RLS）的 adapter 集成测试。
//
// 本文件验证 async_tasks 表的 RLS 双策略（platform_bypass + self）在真实 PG
// 实例上对非 BYPASSRLS 应用角色成立（TASKCENTER-A2 遗留项收口）。
//
// 背景：TASKCENTER-A1 交付时 dev PG 只有 ani（SUPERUSER + BYPASSRLS）凭据，
// RLS 全被绕过，跨租户拦截无法验证，隔离正确性只能由 LocalAsyncTaskStore
// 租户键隔离测试 + SQL WHERE tenant_id 双层保证。2026-08-31 切换 ani_app_user
// （非 BYPASSRLS）真实验证后确认：dev 库的 async_tasks 已是双 PERMISSIVE 策略，
// 但该修复与表级 GRANT 均未入库（init_schema 的 GRANT ON ALL TABLES 在建表前
// 执行、RESTRICTIVE-only 策略会 fail-closed）——由 20260831_001 迁移补齐。
//
// 覆盖（对应 MetadataAsyncTaskStore 的真实 SQL 形态）：
//   - 策略形态：无 RESTRICTIVE-only tenant_isolation；platform_bypass/self 均为
//     PERMISSIVE（fail-closed 回归防护：全新部署漏跑 20260831_001 会在此失败）
//   - 本租户可见（List/Get 路径前提）
//   - 跨租户拦截：A 上下文读 B 的行 = 0；A 上下文 + B 的 task id = 0
//   - Create 同款 INSERT（self WITH CHECK 放行本租户写入）
//   - Update 同款 SQL（懒同步路径 + 终态写保护守卫）
//   - WithPlatformTx 平台上下文可见全部测试行（platform_bypass）
//
// 运行命令（PG 部署于 dev 库；凭据沿用 integration_test.go 的 DSN 约定）：
//
//	ANI_TEST_TENANT_DSN="postgres://ani_app_user:...@host:port/ani?sslmode=disable" \
//	  go test ./pkg/adapters/runtime/ -v -run TestRLSAsyncTasks -tags integration
//
// 测试使用固定租户/任务 UUID 与固定幂等键：ON CONFLICT DO NOTHING +
// 终态同值写使全部断言可重复执行。应用角色无 async_tasks DELETE 权限
// （产品代码无删除路径），探察行残留可由 idempotency_key 前缀 rls-at- 识别。
package runtime

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kubercloud/ani/pkg/adapters/postgres"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
)

func rlsATtenantDSN() string {
	if dsn := os.Getenv("ANI_TEST_TENANT_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://ani_app_user:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable"
}

// rls-async-tasks 专用固定探察数据（幂等键固定 → 重跑不累积残留）。
var (
	rlsATTenantA = uuid.MustParse("00000000-0aaa-0000-0000-00000000a101")
	rlsATTenantB = uuid.MustParse("00000000-0bbb-0000-0000-00000000b202")
	rlsATTaskA   = uuid.MustParse("00000000-0aaa-0000-0000-00000000a111")
	rlsATTaskB   = uuid.MustParse("00000000-0bbb-0000-0000-00000000b222")
	rlsATTaskC   = uuid.MustParse("00000000-0aaa-0000-0000-00000000a333")
)

func rlsATtenantACtx(ctx context.Context) context.Context {
	return types.WithTenant(ctx, &types.TenantContext{
		TenantID: rlsATTenantA,
		UserID:   uuid.New(),
		Roles:    []string{"user"},
	})
}

func rlsATtenantBCtx(ctx context.Context) context.Context {
	return types.WithTenant(ctx, &types.TenantContext{
		TenantID: rlsATTenantB,
		UserID:   uuid.New(),
		Roles:    []string{"user"},
	})
}

// TestRLSAsyncTasksPolicyShape 断言 async_tasks 的 RLS 策略形态：
// 双 PERMISSIVE（platform_bypass + self），无 RESTRICTIVE-only tenant_isolation。
// 全新部署漏跑 20260831_001 时，RESTRICTIVE-only 会让非 BYPASSRLS 角色查询
// async_tasks 恒返回 0 行（任务中心空表），本测试在此处失败给出明确信号。
func TestRLSAsyncTasksPolicyShape(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, rlsATtenantDSN())
	if err != nil {
		t.Fatalf("ani_app_user 连接失败: %v", err)
	}
	defer pool.Close()

	var rolsuper, rolbypassrls bool
	if err := pool.QueryRow(ctx,
		`SELECT (SELECT rolsuper FROM pg_roles WHERE rolname = current_user),
		        (SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user)`).
		Scan(&rolsuper, &rolbypassrls); err != nil {
		t.Fatalf("查询角色属性失败: %v", err)
	}
	if rolsuper || rolbypassrls {
		t.Fatalf("current_user 为 superuser/bypassrls（rolsuper=%v bypassrls=%v），RLS 验证无意义", rolsuper, rolbypassrls)
	}

	var rlsOn, rlsForced bool
	if err := pool.QueryRow(ctx,
		`SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE oid = 'async_tasks'::regclass`).
		Scan(&rlsOn, &rlsForced); err != nil {
		t.Fatalf("查询 RLS 开关失败: %v", err)
	}
	if !rlsOn || !rlsForced {
		t.Fatalf("async_tasks RLS 未启用/未 FORCE（rowsecurity=%v forcerowsecurity=%v）", rlsOn, rlsForced)
	}

	policyRows, err := pool.Query(ctx, `
		SELECT polname, polpermissive
		FROM pg_policy WHERE polrelid = 'async_tasks'::regclass`)
	if err != nil {
		t.Fatalf("查询策略失败: %v", err)
	}
	policies := map[string]bool{}
	for policyRows.Next() {
		var name string
		var permissive bool
		if err := policyRows.Scan(&name, &permissive); err != nil {
			t.Fatalf("scan policy 失败: %v", err)
		}
		policies[name] = permissive
	}
	policyRows.Close()

	if _, exists := policies["tenant_isolation"]; exists || len(policies) == 0 {
		t.Fatalf("RESTRICTIVE-only tenant_isolation 仍存在或无任何策略 %v — 迁移 20260831_001 未执行，非 BYPASSRLS 角色将 fail-closed（查询恒 0 行）", policies)
	}
	for _, want := range []string{"async_tasks_platform_bypass", "async_tasks_self"} {
		permissive, exists := policies[want]
		if !exists || !permissive {
			t.Fatalf("缺少 PERMISSIVE 策略 %s（当前 %v）— 迁移 20260831_001 未执行", want, policies)
		}
	}
}

// TestRLSAsyncTasksTenantIsolation 用 MetadataAsyncTaskStore 同款 SQL 断言：
// 本租户可见、跨租户拦截、Create/Update 写路径、终态写保护、平台上下文全可见。
func TestRLSAsyncTasksTenantIsolation(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, rlsATtenantDSN())
	if err != nil {
		t.Fatalf("ani_app_user 连接失败: %v", err)
	}
	defer pool.Close()
	store := postgres.NewMetadataStore(pool)

	// --- 0. 平台上下文造数（platform_bypass WITH CHECK 允许；固定幂等键幂等） ---
	for _, item := range []struct {
		tid, taskID uuid.UUID
		key         string
	}{
		{rlsATTenantA, rlsATTaskA, "rls-at-seed-a"},
		{rlsATTenantB, rlsATTaskB, "rls-at-seed-b"},
	} {
		err := store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
			_, ierr := tx.Exec(ctx, `
				INSERT INTO async_tasks (tenant_id, id, idempotency_key, task_type, status,
					attempt_count, max_attempts, progress_pct, created_at)
				VALUES ($1::uuid, $2::uuid, $3, 'instance.create', 'running', 1, 1, 10, NOW())
				ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
			`, item.tid, item.taskID, item.key)
			return ierr
		})
		if err != nil {
			t.Fatalf("平台上下文造数失败（GRANT 缺失 = 迁移 20260831_001 未执行）: %v", err)
		}
	}

	// --- 0.5 平台上下文全可见（platform_bypass） ---
	// 在任何 WithTenantTx 之前断言：set_config(..., is_local) 事务结束后会在池化
	// 连接上留下空串 current_tenant_id，裸 IS NULL 形态的 platform_bypass 会把空串
	// 误判为租户上下文（20260831_001 已用 NULLIF 形态修复；其他表的同款跨表问题
	// 记录在 TASKCENTER-A2 遗留风险，不在本批次范围）。
	var platformCount int64
	err = store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		return tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM async_tasks WHERE id IN ($1, $2)`, rlsATTaskA, rlsATTaskB).Scan(&platformCount)
	})
	if err != nil {
		t.Fatalf("WithPlatformTx 查询失败: %v", err)
	}
	if platformCount != 2 {
		t.Fatalf("平台上下文应看到 2 行测试数据，实际 %d（platform_bypass 未生效）", platformCount)
	}

	// --- 1. 本租户可见（List/Get 前提） ---
	var ownCount int64
	err = store.WithTenantTx(rlsATtenantACtx(ctx), func(ctx context.Context, tx ports.MetadataTx) error {
		return tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM async_tasks WHERE tenant_id = $1::uuid`, rlsATTenantA).Scan(&ownCount)
	})
	if err != nil {
		t.Fatalf("本租户查询失败: %v", err)
	}
	if ownCount == 0 {
		t.Fatalf("本租户查询 0 行 — self 策略未生效（fail-closed）")
	}

	// --- 2. 跨租户拦截（核心断言） ---
	var crossCount int64
	err = store.WithTenantTx(rlsATtenantACtx(ctx), func(ctx context.Context, tx ports.MetadataTx) error {
		return tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM async_tasks WHERE tenant_id = $1::uuid`, rlsATTenantB).Scan(&crossCount)
	})
	if err != nil {
		t.Fatalf("跨租户查询失败: %v", err)
	}
	if crossCount != 0 {
		t.Fatalf("跨租户拦截失效: 租户 A 上下文看到租户 B 的 %d 行", crossCount)
	}

	// --- 3. Get 同款 SQL：A 上下文 + B 的 task id（伪造 id 探测） ---
	var crossGet int64
	err = store.WithTenantTx(rlsATtenantACtx(ctx), func(ctx context.Context, tx ports.MetadataTx) error {
		return tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM async_tasks WHERE tenant_id = $1::uuid AND id = $2::uuid`,
			rlsATTenantA, rlsATTaskB).Scan(&crossGet)
	})
	if err != nil {
		t.Fatalf("跨租户 Get 失败: %v", err)
	}
	if crossGet != 0 {
		t.Fatalf("Get 同款 SQL 跨租户泄漏: %d 行", crossGet)
	}

	// --- 4. 对称：B 上下文只看到自己 ---
	var ownB int64
	err = store.WithTenantTx(rlsATtenantBCtx(ctx), func(ctx context.Context, tx ports.MetadataTx) error {
		return tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM async_tasks WHERE tenant_id = $1::uuid AND id = $2::uuid`,
			rlsATTenantB, rlsATTaskB).Scan(&ownB)
	})
	if err != nil {
		t.Fatalf("B 本租户查询失败: %v", err)
	}
	if ownB != 1 {
		t.Fatalf("租户 B 应恰好看到自己的 1 行，实际 %d", ownB)
	}

	// --- 5. Create 同款 INSERT + 幂等重放回读（ON CONFLICT DO NOTHING） ---
	var insertedID string
	err = store.WithTenantTx(rlsATtenantACtx(ctx), func(ctx context.Context, tx ports.MetadataTx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO async_tasks (
				tenant_id, id, idempotency_key, task_type, resource_type, resource_id,
				status, attempt_count, max_attempts, progress_pct, result, error_message,
				dead_letter_at, created_at, completed_at
			) VALUES (
				$1::uuid, $2::uuid, $3, $4, NULLIF($5, ''), NULLIF($6, '')::uuid,
				$7, $8, $9, $10, $11::jsonb, NULLIF($12, ''), $13, NOW(), $14
			) ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
			RETURNING id::text
		`, rlsATTenantA, rlsATTaskC, "rls-at-create", "instance.create", "instance", "",
			"running", 1, 1, 10, "{}", "", nil, nil).Scan(&insertedID)
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("Create 同款 INSERT 失败（GRANT/RLS WITH CHECK 拒绝）: %v", err)
	}
	if insertedID == "" {
		// 幂等重放：CREATE 曾执行过，同键重放必须读回既有行（Create 的 replay 语义）
		err = store.WithTenantTx(rlsATtenantACtx(ctx), func(ctx context.Context, tx ports.MetadataTx) error {
			return tx.QueryRow(ctx,
				`SELECT id::text FROM async_tasks WHERE tenant_id = $1::uuid AND idempotency_key = $2`,
				rlsATTenantA, "rls-at-create").Scan(&insertedID)
		})
		if err != nil {
			t.Fatalf("幂等重放回读失败: %v", err)
		}
	}

	// --- 6. Update 同款 SQL：懒同步推进（终态同值写守卫放行 → 重跑幂等） ---
	var newStatus string
	var newProgress int
	err = store.WithTenantTx(rlsATtenantACtx(ctx), func(ctx context.Context, tx ports.MetadataTx) error {
		return tx.QueryRow(ctx, `
			UPDATE async_tasks SET status=$3, attempt_count=$4, progress_pct=$5,
				result=$6::jsonb, error_message=NULLIF($7, ''), dead_letter_at=$8,
				completed_at=$9, updated_at=NOW()
			WHERE tenant_id=$1::uuid AND id=$2::uuid
				AND (status NOT IN ('completed','failed','cancelled','dead_letter') OR status=$3)
			RETURNING status, progress_pct
		`, rlsATTenantA, rlsATTaskC, "completed", 1, 100, "{}", "", nil, nil).Scan(&newStatus, &newProgress)
	})
	if err != nil {
		t.Fatalf("Update 同款 SQL 失败（懒同步写路径被拒）: %v", err)
	}
	if newStatus != "completed" || newProgress != 100 {
		t.Fatalf("Update 返回值异常: status=%s progress=%d", newStatus, newProgress)
	}

	// --- 7. 终态写保护守卫：completed 不可被改回 running（0 行） ---
	var revertStatus string
	err = store.WithTenantTx(rlsATtenantACtx(ctx), func(ctx context.Context, tx ports.MetadataTx) error {
		return tx.QueryRow(ctx, `
			UPDATE async_tasks SET status=$3, attempt_count=$4, progress_pct=$5,
				result=$6::jsonb, error_message=NULLIF($7, ''), dead_letter_at=$8,
				completed_at=$9, updated_at=NOW()
			WHERE tenant_id=$1::uuid AND id=$2::uuid
				AND (status NOT IN ('completed','failed','cancelled','dead_letter') OR status=$3)
			RETURNING status
		`, rlsATTenantA, rlsATTaskC, "running", 1, 10, "{}", "", nil, nil).Scan(&revertStatus)
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("终态写保护守卫失效（期望 0 行 ErrNoRows）: err=%v status=%q", err, revertStatus)
	}
}
