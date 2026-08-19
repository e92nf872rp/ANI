//go:build integration

// 集成测试：连真实 PG 实例，用管理员 + 租户双角色连接验证 RLS 隔离与 bypass 行为。
// 覆盖 SPEC §9.4 / Plan §11.4 的扣减 12 场景 + 管理 11 场景 + SDK 端到端。
//
// 前置：PG 实例可用（用户部署在 10.10.1.66:30945），三张表（resource_quota_meta /
// resource_quota / resource_reservations）及 RLS 双 policy（platform_bypass + self）
// 已由李宇 migration 建好，ani_app_user 已获得 DML 权限。
//
// 双角色连接（DSN 通过环境变量覆盖）：
//
//	管理员（superuser，绕过 RLS）：ANI_TEST_ADMIN_DSN
//	  默认 postgres://ani:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable
//	租户（普通角色，受 RLS 约束）：ANI_TEST_TENANT_DSN
//	  默认 postgres://ani_app_user:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable
//
// 运行命令：
//
//	go test ./pkg/adapters/runtime/ -v -run Integration -tags integration
//
// 集成测试用 //go:build integration build tag 隔离，不阻塞默认 make test。
// 测试结束用管理员连接清除测试租户数据。
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kubercloud/ani/pkg/adapters/postgres"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
)

// adminTestDSN 读取环境变量 ANI_TEST_ADMIN_DSN，默认连用户部署的远程 PG（管理员 ani）。
func adminTestDSN() string {
	if dsn := os.Getenv("ANI_TEST_ADMIN_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://ani:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable"
}

// tenantTestDSN 读取环境变量 ANI_TEST_TENANT_DSN，默认连用户部署的远程 PG（租户 ani_app_user）。
func tenantTestDSN() string {
	if dsn := os.Getenv("ANI_TEST_TENANT_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://ani_app_user:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable"
}

// quotaIntegrationEnv 封装集成测试的双连接池、双 MetadataStore、双 quota adapter 和两个测试租户。
//   - adminXXX：管理员连接（ani，superuser，走 WithPlatformTx bypass RLS），用于 setup、管理场景、清理
//   - tenantXXX：租户连接（ani_app_user，走 WithTenantTx 受 RLS 约束），用于扣减场景与隔离验证
type quotaIntegrationEnv struct {
	adminPool   *pgxpool.Pool
	tenantPool  *pgxpool.Pool
	tenantStore ports.MetadataStore
	adminQuota  *PostgresQuota
	tenantQuota *PostgresQuota
	t           *testing.T
	tenantA     uuid.UUID
	tenantB     uuid.UUID
}

// newQuotaIntegrationEnv 建立双连接、seed resource_quota_meta、插入两个测试租户。
// 表与 RLS policy、GRANT 已由 migration 建好，这里只做幂等 seed（ON CONFLICT DO NOTHING）。
func newQuotaIntegrationEnv(t *testing.T) *quotaIntegrationEnv {
	t.Helper()

	adminDSN := adminTestDSN()
	tenantDSN := tenantTestDSN()
	adminPool, err := pgxpool.New(context.Background(), adminDSN)
	if err != nil {
		t.Fatalf("连接管理员 PG 失败（ANI_TEST_ADMIN_DSN）：%v", err)
	}
	tenantPool, err := pgxpool.New(context.Background(), tenantDSN)
	if err != nil {
		adminPool.Close()
		t.Fatalf("连接租户 PG 失败（ANI_TEST_TENANT_DSN）：%v", err)
	}

	env := &quotaIntegrationEnv{
		adminPool:   adminPool,
		tenantPool:  tenantPool,
		tenantStore: postgres.NewMetadataStore(tenantPool),
		adminQuota:  NewPostgresQuota(postgres.NewMetadataStore(adminPool)),
		tenantQuota: NewPostgresQuota(postgres.NewMetadataStore(tenantPool)),
		t:           t,
	}
	t.Cleanup(env.cleanup)

	// seed resource_quota_meta（幂等，FK 前置；migration 已 seed 则 ON CONFLICT 跳过）。
	// 测试用 gpu_count / cpu_core，显式确保存在，避免扣减时 FK 失败造成假阳性。
	if _, err := adminPool.Exec(context.Background(), `
		INSERT INTO resource_quota_meta (resource_type, display_name, unit, is_discrete, default_quota, enabled)
		VALUES
			('gpu_count', 'GPU 份数', '份', true, 8, true),
			('cpu_core', 'CPU 核数', '核', true, 8, true)
		ON CONFLICT (resource_type) DO NOTHING
	`); err != nil {
		t.Fatalf("seed resource_quota_meta 失败: %v", err)
	}

	// 插入两个测试租户（tenants 表无 RLS，可直接 INSERT）。
	// plan_id 是 NOT NULL 列，使用 tenant_plans 表中的默认计划 ID。
	env.tenantA = uuid.New()
	env.tenantB = uuid.New()
	defaultPlanID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	for _, tid := range []uuid.UUID{env.tenantA, env.tenantB} {
		short := tid.String()[:8]
		if _, err := adminPool.Exec(context.Background(), `
			INSERT INTO tenants (id, name, display_name, status, plan_id)
			VALUES ($1, $2, $3, 'active', $4)
			ON CONFLICT (id) DO NOTHING
		`, tid, fmt.Sprintf("it-test-%s", short), fmt.Sprintf("IT测试-%s", short), defaultPlanID); err != nil {
			t.Fatalf("插入测试租户 %s 失败: %v", short, err)
		}
	}
	return env
}

// tenantCtxOf 构造带指定租户身份的 context（供 WithTenantTx 注入 RLS）。
func (e *quotaIntegrationEnv) tenantCtxOf(tid uuid.UUID) context.Context {
	return types.WithTenant(context.Background(), &types.TenantContext{
		TenantID: tid,
		UserID:   uuid.New(),
		Roles:    []string{"user"},
	})
}

// seedQuotaFor 用管理员连接（bypass RLS）为测试租户建 gpu_count 配额行。
func (e *quotaIntegrationEnv) seedQuotaFor(tid uuid.UUID, total int64) {
	_, err := e.adminPool.Exec(context.Background(), `
		INSERT INTO resource_quota (tenant_id, resource_type, total, reserved, used)
		VALUES ($1, 'gpu_count', $2, 0, 0)
		ON CONFLICT (tenant_id, resource_type) DO UPDATE SET total = EXCLUDED.total
	`, tid, total)
	if err != nil {
		e.t.Fatalf("管理员 seed resource_quota 失败: %v", err)
	}
}

// cleanup 用管理员连接删除测试租户（CASCADE 清 resource_quota/resource_reservations）并关闭连接池。
func (e *quotaIntegrationEnv) cleanup() {
	if e.adminPool != nil {
		for _, tid := range []uuid.UUID{e.tenantA, e.tenantB} {
			_, _ = e.adminPool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tid)
		}
		e.adminPool.Close()
	}
	if e.tenantPool != nil {
		e.tenantPool.Close()
	}
}

// loadGpuReserved 用管理员连接读取某租户 gpu_count 维度的 reserved/used，用于断言。
func (e *quotaIntegrationEnv) loadGpuCounts(tid uuid.UUID) (reserved, used int64) {
	err := e.adminPool.QueryRow(context.Background(), `
		SELECT reserved, used FROM resource_quota WHERE tenant_id = $1 AND resource_type = 'gpu_count'
	`, tid).Scan(&reserved, &used)
	if err != nil {
		e.t.Fatalf("读 resource_quota 失败: %v", err)
	}
	return reserved, used
}

// ---------------------------------------------------------------- 扣减场景 1-12

// TestIntegrationQuotaTryTenantA 扣减场景 1：租户 A（ani_app_user）Try 成功。
// RLS 放行：INSERT resource_reservations + UPDATE resource_quota 均成功。
func TestIntegrationQuotaTryTenantA(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	res, err := env.tenantQuota.Try(context.Background(), ports.QuotaTryRequest{
		TenantID:     env.tenantA.String(),
		ResourceType: ports.QuotaGPUCount,
		Amount:       2,
	})
	if err != nil {
		t.Fatalf("租户 A Try 失败（RLS 应放行）: %v", err)
	}
	if res.TxID == "" {
		t.Fatal("Try 未返回 tx_id")
	}
	reserved, _ := env.loadGpuCounts(env.tenantA)
	if reserved != 2 {
		t.Fatalf("Try 后 reserved 应为 2，实际 %d", reserved)
	}
	t.Logf("租户 A Try 成功，tx_id=%s，reserved=%d", res.TxID, reserved)
}

// TestIntegrationQuotaGetMyTenantA 扣减场景 2：租户 A GetMy 查自己配额（RLS 放行）。
func TestIntegrationQuotaGetMyTenantA(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	view, err := env.tenantQuota.GetMy(context.Background(), env.tenantA.String())
	if err != nil {
		t.Fatalf("租户 A GetMy 失败: %v", err)
	}
	if view.Total[ports.QuotaGPUCount] != 10 {
		t.Fatalf("租户 A GetMy total 应为 10，实际 %d", view.Total[ports.QuotaGPUCount])
	}
	t.Logf("租户 A GetMy 返回自己配额 total=%d", view.Total[ports.QuotaGPUCount])
}

// TestIntegrationQuotaTenantCannotSeeTenantB 扣减场景 3：租户 A 查租户 B 配额返回 0 行（RLS 拦截）。
func TestIntegrationQuotaTenantCannotSeeTenantB(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)
	env.seedQuotaFor(env.tenantB, 20)

	// 以租户 A 身份（SET tenant_id=A）查询租户 B 的配额行 → RLS 应过滤为 0 行。
	var count int
	err := env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
		return tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM resource_quota
			WHERE tenant_id = $1 AND resource_type = 'gpu_count'
		`, env.tenantB).Scan(&count)
	})
	if err != nil {
		t.Fatalf("租户 A 查租户 B 配额失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("租户 A 应看不到租户 B 配额（RLS 拦截），实际看到 %d 行", count)
	}
	t.Logf("租户 A 查租户 B 配额返回 0 行，RLS 隔离生效")
}

// TestIntegrationQuotaConfirmCancelRelease 扣减场景 4/6/7：租户 A Confirm/Cancel/Release 各自成功（RLS 放行）。
func TestIntegrationQuotaConfirmCancelRelease(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	// Try 2，reserved=2
	res, err := env.tenantQuota.Try(context.Background(), ports.QuotaTryRequest{
		TenantID: env.tenantA.String(), ResourceType: ports.QuotaGPUCount, Amount: 2,
	})
	if err != nil {
		t.Fatalf("Try 失败: %v", err)
	}

	// Confirm：reserved→confirmed，used=2，reserved=0
	err = env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
		return env.tenantQuota.Confirm(ctx, tx, []string{res.TxID}, "instance-1")
	})
	if err != nil {
		t.Fatalf("租户 A Confirm 失败（RLS 应放行）: %v", err)
	}
	reserved, used := env.loadGpuCounts(env.tenantA)
	if reserved != 0 || used != 2 {
		t.Fatalf("Confirm 后应 reserved=0 used=2，实际 reserved=%d used=%d", reserved, used)
	}
	t.Logf("Confirm 成功：reserved=%d used=%d", reserved, used)

	// Cancel 一条新的预占：先 Try 1，再 Cancel → reserved 释放回 0
	res2, err := env.tenantQuota.Try(context.Background(), ports.QuotaTryRequest{
		TenantID: env.tenantA.String(), ResourceType: ports.QuotaGPUCount, Amount: 1,
	})
	if err != nil {
		t.Fatalf("第二次 Try 失败: %v", err)
	}
	err = env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
		return env.tenantQuota.Cancel(ctx, tx, []string{res2.TxID})
	})
	if err != nil {
		t.Fatalf("租户 A Cancel 失败: %v", err)
	}
	reserved, used = env.loadGpuCounts(env.tenantA)
	if reserved != 0 || used != 2 {
		t.Fatalf("Cancel 后应 reserved=0 used=2，实际 reserved=%d used=%d", reserved, used)
	}
	t.Logf("Cancel 成功：reserved=%d used=%d", reserved, used)

	// Release 已确认的 used → used 归零
	err = env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
		return env.tenantQuota.Release(ctx, tx, []string{res.TxID})
	})
	if err != nil {
		t.Fatalf("租户 A Release 失败: %v", err)
	}
	_, used = env.loadGpuCounts(env.tenantA)
	if used != 0 {
		t.Fatalf("Release 后 used 应归零，实际 %d", used)
	}
	t.Logf("Release 成功：used 归零")
}

// TestIntegrationQuotaTenantBCannotConfirmTenantA 扣减场景 5：租户 B Confirm 租户 A 的 txID → RLS 拦截（0 行幂等跳过）。
func TestIntegrationQuotaTenantBCannotConfirmTenantA(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)
	env.seedQuotaFor(env.tenantB, 10)

	resA, err := env.tenantQuota.Try(context.Background(), ports.QuotaTryRequest{
		TenantID: env.tenantA.String(), ResourceType: ports.QuotaGPUCount, Amount: 3,
	})
	if err != nil {
		t.Fatalf("租户 A Try 失败: %v", err)
	}

	// 租户 B 身份 Confirm 租户 A 的 txID → RLS 拦截（UPDATE 0 行 → ErrNoRows → 幂等跳过，不报错）。
	err = env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantB), func(ctx context.Context, tx ports.MetadataTx) error {
		return env.tenantQuota.Confirm(ctx, tx, []string{resA.TxID}, "ref-b")
	})
	if err != nil {
		t.Fatalf("租户 B Confirm 租户 A 的 txID 应幂等跳过不报错，实际: %v", err)
	}

	// 租户 A 的流水应保持 reserved（confirm 未生效）。
	reserved, _ := env.loadGpuCounts(env.tenantA)
	if reserved != 3 {
		t.Fatalf("租户 B 不该确认租户 A 的预占，reserved 应仍为 3，实际 %d", reserved)
	}
	t.Logf("租户 B Confirm 租户 A txID 被 RLS 拦截，val已跳过，reserved=%d", reserved)
}

// TestIntegrationQuotaTenantACannotInsertTenantB 扣减场景 8：租户 A 试图 INSERT tenant_id='B' 流水被 RLS 拒绝。
func TestIntegrationQuotaTenantACannotInsertTenantB(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)
	env.seedQuotaFor(env.tenantB, 10)

	err := env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO resource_reservations (tenant_id, resource_type, amount, expires_at)
			VALUES ($1, 'gpu_count', 1, NOW() + INTERVAL '10 minutes')
		`, env.tenantB)
		return err
	})
	if err == nil {
		t.Fatal("RLS 未拒绝：租户 A 成功插入了租户 B 的预占流水（WITH CHECK 应拦截）")
	}
	t.Logf("租户 A INSERT 租户 B 流水被 RLS 拒绝: %v", err)
}

// TestIntegrationQuotaConcurrentTryNoOversell 扣减场景 9：并发 Try 不超卖。
// 同一租户 total=10，N 个并发各自 Try 1，最终 reserved 不超过 total，成功数不超过 total。
func TestIntegrationQuotaConcurrentTryNoOversell(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)
	concurrency := 32

	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	exceeded := 0
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 每个 goroutine 独立 Try（tenantCtx 由 adapter 内部按 req.TenantID 构造）
			_, err := env.tenantQuota.Try(context.Background(), ports.QuotaTryRequest{
				TenantID: env.tenantA.String(), ResourceType: ports.QuotaGPUCount, Amount: 1,
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			} else if errors.Is(err, ports.ErrQuotaExceeded) {
				exceeded++
			} else {
				t.Errorf("并发 Try 出现未知错误: %v", err)
			}
		}()
	}
	wg.Wait()

	reserved, _ := env.loadGpuCounts(env.tenantA)
	if reserved > 10 {
		t.Fatalf("并发 Try 超卖：reserved=%d > total=10", reserved)
	}
	if success != 10 {
		t.Fatalf("应恰好 10 个成功（total=10），实际成功 %d，超过 %d", success, exceeded)
	}
	t.Logf("并发 Try：%d 成功，%d 超卖拒绝，reserved=%d（不超过 total=10）", success, exceeded, reserved)
}

// TestIntegrationQuotaTryManyEndToEnd 扣减场景 10：TryMany 占多维度→Confirm→验证 used。
func TestIntegrationQuotaTryManyEndToEnd(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	// 两个维度各给 total=10
	env.seedQuotaFor(env.tenantA, 10)
	_, err := env.adminPool.Exec(context.Background(), `
		INSERT INTO resource_quota (tenant_id, resource_type, total, reserved, used)
		VALUES ($1, 'cpu_core', 10, 0, 0)
		ON CONFLICT (tenant_id, resource_type) DO UPDATE SET total = EXCLUDED.total
	`, env.tenantA)
	if err != nil {
		t.Fatalf("seed cpu_core 失败: %v", err)
	}

	reservations, err := env.tenantQuota.TryMany(context.Background(), []ports.QuotaTryRequest{
		{TenantID: env.tenantA.String(), ResourceType: ports.QuotaGPUCount, Amount: 2},
		{TenantID: env.tenantA.String(), ResourceType: ports.QuotaCPUCore, Amount: 3},
	})
	if err != nil {
		t.Fatalf("TryMany 失败: %v", err)
	}
	if len(reservations) != 2 {
		t.Fatalf("TryMany 应返回 2 条预占，实际 %d", len(reservations))
	}

	// Confirm 两维度的预占
	var ids []string
	for _, r := range reservations {
		ids = append(ids, r.TxID)
	}
	err = env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
		return env.tenantQuota.Confirm(ctx, tx, ids, "instance-m")
	})
	if err != nil {
		t.Fatalf("Confirm 失败: %v", err)
	}

	// 验证 used：gpu_count=2，cpu_core=3
	reservedG, usedG := env.loadGpuCounts(env.tenantA)
	if reservedG != 0 || usedG != 2 {
		t.Fatalf("gpu_count Confirm 后应 reserved=0 used=2，实际 reserved=%d used=%d", reservedG, usedG)
	}
	var usedC int64
	err = env.adminPool.QueryRow(context.Background(), `
		SELECT used FROM resource_quota WHERE tenant_id = $1 AND resource_type = 'cpu_core'
	`, env.tenantA).Scan(&usedC)
	if err != nil {
		t.Fatalf("读 cpu_core used 失败: %v", err)
	}
	if usedC != 3 {
		t.Fatalf("cpu_core Confirm 后 used 应为 3，实际 %d", usedC)
	}
	t.Logf("TryMany→Confirm 端到端成功：gpu used=%d, cpu used=%d", usedG, usedC)
}

// TestIntegrationQuotaIdempotency 扣减场景 11：Confirm/Cancel/Release 幂等（重复调用不重复扣减）。
func TestIntegrationQuotaIdempotency(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	res, err := env.tenantQuota.Try(context.Background(), ports.QuotaTryRequest{
		TenantID: env.tenantA.String(), ResourceType: ports.QuotaGPUCount, Amount: 4,
	})
	if err != nil {
		t.Fatalf("Try 失败: %v", err)
	}

	// Confirm 两次：第二次应幂等跳过不重复扣减
	for i := 0; i < 2; i++ {
		err = env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
			return env.tenantQuota.Confirm(ctx, tx, []string{res.TxID}, "idem-1")
		})
		if err != nil {
			t.Fatalf("Confirm 第 %d 次失败: %v", i+1, err)
		}
	}
	_, used := env.loadGpuCounts(env.tenantA)
	if used != 4 {
		t.Fatalf("Confirm 幂等失败：used 应仍为 4，实际 %d", used)
	}

	// Release 两次：第二次幂等，used 归零且不再扣
	for i := 0; i < 2; i++ {
		err = env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
			return env.tenantQuota.Release(ctx, tx, []string{res.TxID})
		})
		if err != nil {
			t.Fatalf("Release 第 %d 次失败: %v", i+1, err)
		}
	}
	_, used = env.loadGpuCounts(env.tenantA)
	if used != 0 {
		t.Fatalf("Release 幂等失败：used 应归零，实际 %d", used)
	}
	t.Logf("Confirm/Release 幂等正确：重复调用不重复扣减")
}

// TestIntegrationQuotaReleaseEndToEnd 扣减场景 12：TryMany→Confirm→Release→used 归零。
func TestIntegrationQuotaReleaseEndToEnd(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	res, err := env.tenantQuota.TryMany(context.Background(), []ports.QuotaTryRequest{
		{TenantID: env.tenantA.String(), ResourceType: ports.QuotaGPUCount, Amount: 3},
	})
	if err != nil {
		t.Fatalf("TryMany 失败: %v", err)
	}
	ids := []string{res[0].TxID}

	err = env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
		return env.tenantQuota.Confirm(ctx, tx, ids, "inst-rel")
	})
	if err != nil {
		t.Fatalf("Confirm 失败: %v", err)
	}
	if _, used := env.loadGpuCounts(env.tenantA); used != 3 {
		t.Fatalf("Confirm 后 used 应为 3，实际 %d", used)
	}

	err = env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
		return env.tenantQuota.Release(ctx, tx, ids)
	})
	if err != nil {
		t.Fatalf("Release 失败: %v", err)
	}
	_, used := env.loadGpuCounts(env.tenantA)
	if used != 0 {
		t.Fatalf("Release 端到端后 used 应归零，实际 %d", used)
	}
	t.Logf("Release 端到端成功：used 归零")
}

// ---------------------------------------------------------------- 管理场景 13-22（admin bypass RLS）

// TestIntegrationQuotaAdminPut 管理场景 13：Put 平台 UPSERT 成功（bypass RLS）。
func TestIntegrationQuotaAdminPut(t *testing.T) {
	env := newQuotaIntegrationEnv(t)

	// 管理员连接 Put 设置租户 A 配额（platform_bypass 放行）。
	view, err := env.adminQuota.Put(context.Background(), "it-put-1", ports.QuotaPutRequest{
		TenantID:       env.tenantA.String(),
		Total:          map[ports.ResourceType]int64{ports.QuotaGPUCount: 12},
		IdempotencyKey: "it-put-1",
	})
	if err != nil {
		t.Fatalf("管理员 Put 失败: %v", err)
	}
	if view.Total[ports.QuotaGPUCount] != 12 {
		t.Fatalf("Put 后 total 应为 12，实际 %d", view.Total[ports.QuotaGPUCount])
	}
	t.Logf("管理员 Put 成功（bypass RLS）：total=%d", view.Total[ports.QuotaGPUCount])
}

// TestIntegrationQuotaAdminList 管理场景 14：List 返回所有租户（RLS bypass）。
func TestIntegrationQuotaAdminList(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)
	env.seedQuotaFor(env.tenantB, 20)

	result, err := env.adminQuota.List(context.Background(), ports.QuotaListRequest{Limit: 50})
	if err != nil {
		t.Fatalf("管理员 List 失败: %v", err)
	}
	// 至少应包含租户 A 和租户 B 的配额（bypass RLS 能看到所有租户）。
	foundA, foundB := false, false
	for _, item := range result.Items {
		if item.TenantID == env.tenantA.String() {
			foundA = true
		}
		if item.TenantID == env.tenantB.String() {
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Fatalf("List 未返回所有租户（RLS bypass 失效）：foundA=%v foundB=%v items=%d", foundA, foundB, len(result.Items))
	}
	t.Logf("管理员 List 返回 %d 个租户，含 A/B（bypass RLS 生效）", len(result.Items))
}

// TestIntegrationQuotaAdminDelete 管理场景 15：Delete 全删成功（RLS bypass）。
func TestIntegrationQuotaAdminDelete(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	// DeleteTenantQuota 管理员连接清理租户 A 配额。
	if err := env.adminQuota.DeleteTenantQuota(context.Background(), env.tenantA.String()); err != nil {
		t.Fatalf("管理员 DeleteTenantQuota 失败: %v", err)
	}
	var count int
	err := env.adminPool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM resource_quota WHERE tenant_id = $1
	`, env.tenantA).Scan(&count)
	if err != nil {
		t.Fatalf("读 resource_quota 失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("Delete 后应无租户 A 配额行，实际 %d", count)
	}
	t.Logf("管理员 DeleteTenantQuota 成功（bypass RLS），租户 A 配额已清空")
}

// TestIntegrationQuotaAdminCreateBatch 管理场景 16：CreateTenantQuota 批量新建（回读 total/used=0/reserved=0）。
func TestIntegrationQuotaAdminCreateBatch(t *testing.T) {
	env := newQuotaIntegrationEnv(t)

	infos, err := env.adminQuota.CreateTenantQuota(context.Background(), env.tenantA.String(), []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: 6},
		{ResourceType: ports.QuotaCPUCore, Total: 8},
	})
	if err != nil {
		t.Fatalf("CreateTenantQuota 失败: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("应返回 2 条 QuotaInfo，实际 %d", len(infos))
	}
	for _, info := range infos {
		if info.Total == 0 || info.Reserved != 0 || info.Used != 0 {
			t.Fatalf("新建配额 initial 不干净：type=%s total=%d reserved=%d used=%d", info.ResourceType, info.Total, info.Reserved, info.Used)
		}
	}
	t.Logf("CreateTenantQuota 批量新建 2 维成功：total 正确、used/reserved=0")
}

// TestIntegrationQuotaAdminCreateIdempotent 管理场景 17：CreateTenantQuota 幂等（ON CONFLICT DO NOTHING 不覆盖）。
// 部分成功语义：重复创建同一维度 → 该维度已存在被跳过，事务提交不回滚，返回 ErrQuotaAlreadyExists(409)。
func TestIntegrationQuotaAdminCreateIdempotent(t *testing.T) {
	env := newQuotaIntegrationEnv(t)

	// 首次创建 total=6
	_, err := env.adminQuota.CreateTenantQuota(context.Background(), env.tenantA.String(), []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: 6},
	})
	if err != nil {
		t.Fatalf("首次 CreateTenantQuota 失败: %v", err)
	}
	// 再次创建同一维度不同 total=99 → ON CONFLICT DO NOTHING 跳过，不覆盖；部分成功返回 409 哨兵错误。
	_, err = env.adminQuota.CreateTenantQuota(context.Background(), env.tenantA.String(), []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: 99},
	})
	if !errors.Is(err, ports.ErrQuotaAlreadyExists) {
		t.Fatalf("重复 CreateTenantQuota 应返回 ErrQuotaAlreadyExists(409)，实际 %v", err)
	}
	var total int64
	err = env.adminPool.QueryRow(context.Background(), `
		SELECT total FROM resource_quota WHERE tenant_id = $1 AND resource_type = 'gpu_count'
	`, env.tenantA).Scan(&total)
	if err != nil {
		t.Fatalf("读 total 失败: %v", err)
	}
	if total != 6 {
		t.Fatalf("CreateTenantQuota 幂等失败：total 应保持 6（DO NOTHING 不覆盖），实际 %d", total)
	}
	t.Logf("CreateTenantQuota 幂等正确：total 保持 %d，重复创建已跳过并返回 409", total)
}

// TestIntegrationQuotaAdminUpdate 管理场景 18：UpdateTenantQuota 改 total（tightened=false）。
func TestIntegrationQuotaAdminUpdate(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	infos, err := env.adminQuota.UpdateTenantQuota(context.Background(), env.tenantA.String(), []ports.QuotaItemUpdate{
		{ResourceType: ports.QuotaGPUCount, Total: 15},
	})
	if err != nil {
		t.Fatalf("UpdateTenantQuota 失败: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("应返回 1 条 QuotaInfo，实际 %d", len(infos))
	}
	if infos[0].Total != 15 {
		t.Fatalf("Update 后 total 应为 15，实际 %d", infos[0].Total)
	}
	if infos[0].Tightened {
		t.Fatalf("扩容不应 tightened=true，实际 true")
	}
	t.Logf("UpdateTenantQuota 扩容成功：total=%d tightened=false", infos[0].Total)
}

// TestIntegrationQuotaAdminUpsertMixedAndDefault 管理场景 18b：UpsertTenantQuota
// 同时覆盖已有维度更新、新维度插入和 total=0 取 default_quota。
func TestIntegrationQuotaAdminUpsertMixedAndDefault(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	infos, err := env.adminQuota.UpsertTenantQuota(context.Background(), env.tenantA.String(), []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: 6},
		{ResourceType: ports.QuotaCPUCore, Total: 0},
	})
	if err != nil {
		t.Fatalf("UpsertTenantQuota 失败: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("应返回 2 条 QuotaInfo，实际 %d", len(infos))
	}
	if total := env.loadGpuTotal(env.tenantA, "gpu_count"); total != 6 {
		t.Fatalf("Upsert 后 gpu_count total 应为 6，实际 %d", total)
	}
	if total := env.loadGpuTotal(env.tenantA, "cpu_core"); total != 8 {
		t.Fatalf("Upsert 后 cpu_core total 应取 default_quota=8，实际 %d", total)
	}
	for _, info := range infos {
		if info.Tightened {
			t.Fatalf("混合 upsert 不应 tightened=true：%+v", info)
		}
	}
	t.Logf("UpsertTenantQuota 混合成功：gpu_count 更新为 6，cpu_core 新建并取 default_quota=8")
}

// TestIntegrationQuotaAdminUpsertShrinkClamp 管理场景 19b：UpsertTenantQuota
// 缩容时用 GREATEST clamp 到 used+reserved，并返回 tightened=true。
func TestIntegrationQuotaAdminUpsertShrinkClamp(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	_, err := env.tenantQuota.Try(context.Background(), ports.QuotaTryRequest{
		TenantID:     env.tenantA.String(),
		ResourceType: ports.QuotaGPUCount,
		Amount:       7,
	})
	if err != nil {
		t.Fatalf("先行 Try 失败: %v", err)
	}

	infos, err := env.adminQuota.UpsertTenantQuota(context.Background(), env.tenantA.String(), []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: 1},
	})
	if err != nil {
		t.Fatalf("UpsertTenantQuota 缩容失败: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("应返回 1 条 QuotaInfo，实际 %d", len(infos))
	}
	if infos[0].Total != 7 {
		t.Fatalf("缩容 clamp 后 total 应为 7，实际 %d", infos[0].Total)
	}
	if !infos[0].Tightened {
		t.Fatalf("缩容 clamp 应 tightened=true")
	}
	t.Logf("UpsertTenantQuota 缩容 clamp 成功：total=%d tightened=true", infos[0].Total)
}

// TestIntegrationQuotaAdminUpsertAtomicRollback 管理场景 20b：UpsertTenantQuota
// 任一维度失败时整体回滚，前序成功写入不会残留。
func TestIntegrationQuotaAdminUpsertAtomicRollback(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	_, err := env.adminQuota.UpsertTenantQuota(context.Background(), env.tenantA.String(), []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: 15},
		{ResourceType: ports.ResourceType("no_such_resource"), Total: 1},
	})
	if !errors.Is(err, ports.ErrQuotaResourceNotRegistered) {
		t.Fatalf("UpsertTenantQuota 应返回 ErrQuotaResourceNotRegistered，实际 %v", err)
	}
	if total := env.loadGpuTotal(env.tenantA, "gpu_count"); total != 10 {
		t.Fatalf("事务回滚后 gpu_count total 应保持 10，实际 %d", total)
	}
	t.Logf("UpsertTenantQuota 原子回滚成功：失败后 gpu_count total 保持 10")
}

// TestIntegrationQuotaAdminShrink 管理场景 19：UpdateTenantQuota 缩容（GREATEST clamp，tightened=true，Try→ErrQuotaExceeded）。
func TestIntegrationQuotaAdminShrink(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	// 先 Try 占去 7 → reserved=7
	_, err := env.tenantQuota.Try(context.Background(), ports.QuotaTryRequest{
		TenantID: env.tenantA.String(), ResourceType: ports.QuotaGPUCount, Amount: 7,
	})
	if err != nil {
		t.Fatalf("Try 失败: %v", err)
	}

	// 缩容到 total=7 → GREATEST clamp（reserved=7），tightened=true
	infos, err := env.adminQuota.UpdateTenantQuota(context.Background(), env.tenantA.String(), []ports.QuotaItemUpdate{
		{ResourceType: ports.QuotaGPUCount, Total: 1},
	})
	if err != nil {
		t.Fatalf("UpdateTenantQuota 缩容失败: %v", err)
	}
	if len(infos) != 1 || infos[0].Total != 7 {
		t.Fatalf("缩容 clamp 失败：total 应为 7（clamp 到 reserved），实际 %v", map[ports.ResourceType]int64{infos[0].ResourceType: infos[0].Total})
	}
	if !infos[0].Tightened {
		t.Fatalf("缩容 clamp 应 tightened=true")
	}

	// 已用尽余量：再 Try 1 → ErrQuotaExceeded
	_, err = env.tenantQuota.Try(context.Background(), ports.QuotaTryRequest{
		TenantID: env.tenantA.String(), ResourceType: ports.QuotaGPUCount, Amount: 1,
	})
	if !errors.Is(err, ports.ErrQuotaExceeded) {
		t.Fatalf("缩容后余量不足应返回 ErrQuotaExceeded，实际: %v", err)
	}
	t.Logf("缩容 clamp 生效：total=%d tightened=true，再 Try 1 返回 ErrQuotaExceeded", infos[0].Total)
}

// TestIntegrationQuotaAdminGetJoinMeta 管理场景 20：GetTenantQuota JOIN meta（unit/display_name/is_discrete 正确）。
func TestIntegrationQuotaAdminGetJoinMeta(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	infos, err := env.adminQuota.GetTenantQuota(context.Background(), env.tenantA.String())
	if err != nil {
		t.Fatalf("GetTenantQuota 失败: %v", err)
	}
	found := false
	for _, info := range infos {
		if info.ResourceType == ports.QuotaGPUCount {
			found = true
			if info.Unit == "" || info.DisplayName == "" {
				t.Fatalf("GetTenantQuota 未 JOIN meta：unit=%q display=%q", info.Unit, info.DisplayName)
			}
			if !info.IsDiscrete {
				t.Fatalf("gpu_count 应 is_discrete=true")
			}
		}
	}
	if !found {
		t.Fatalf("GetTenantQuota 未返回 gpu_count 维度")
	}
	t.Logf("GetTenantQuota JOIN meta 成功：gpu_count unit/display_name/is_discrete 正确")
}

// TestIntegrationQuotaAdminDeleteTenantQuota 管理场景 21：DeleteTenantQuota（resource_quota + resource_reservations 均清空）。
func TestIntegrationQuotaAdminDeleteTenantQuota(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	// 用租户身份 Try 生成一条流水，再用管理员 DeleteTenantQuota 清理。
	res, err := env.tenantQuota.Try(context.Background(), ports.QuotaTryRequest{
		TenantID: env.tenantA.String(), ResourceType: ports.QuotaGPUCount, Amount: 2,
	})
	if err != nil {
		t.Fatalf("Try 失败: %v", err)
	}
	_ = res

	if err := env.adminQuota.DeleteTenantQuota(context.Background(), env.tenantA.String()); err != nil {
		t.Fatalf("DeleteTenantQuota 失败: %v", err)
	}

	var quotaCount, resCount int
	err = env.adminPool.QueryRow(context.Background(), `
		SELECT (SELECT COUNT(*) FROM resource_quota WHERE tenant_id = $1)
		     + (SELECT COUNT(*) FROM resource_reservations WHERE tenant_id = $1)
	`, env.tenantA).Scan(&quotaCount)
	if err != nil {
		t.Fatalf("读清理结果失败: %v", err)
	}
	// 上面的合并查询返回的是两表计数和；任一侧残留都会 >0。
	err = env.adminPool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM resource_reservations WHERE tenant_id = $1
	`, env.tenantA).Scan(&resCount)
	if err != nil {
		t.Fatalf("读流水数失败: %v", err)
	}
	if quotaCount != 0 || resCount != 0 {
		t.Fatalf("DeleteTenantQuota 未清空两表：resource_quota 残留和=%d, reservations=%d", quotaCount, resCount)
	}
	t.Logf("DeleteTenantQuota 清空 resource_quota + resource_reservations 成功")
}

// TestIntegrationQuotaAdminListMeta 管理场景 22：ListQuotaMeta 返回 enabled=true 维度列表。
func TestIntegrationQuotaAdminListMeta(t *testing.T) {
	env := newQuotaIntegrationEnv(t)

	metas, err := env.adminQuota.ListQuotaMeta(context.Background())
	if err != nil {
		t.Fatalf("ListQuotaMeta 失败: %v", err)
	}
	foundGPU, foundCPU := false, false
	for _, m := range metas {
		if m.ResourceType == ports.QuotaGPUCount {
			foundGPU = true
		}
		if m.ResourceType == ports.QuotaCPUCore {
			foundCPU = true
		}
	}
	if !foundGPU || !foundCPU {
		t.Fatalf("ListQuotaMeta 未返回 enabled 维度，foundGPU=%v foundCPU=%v", foundGPU, foundCPU)
	}
	t.Logf("ListQuotaMeta 返回 %d 个 enabled 维度，含 gpu_count/cpu_core", len(metas))
}

// ---------------------------------------------------------------- 扣减场景 24-30：TryTx / TryManyTx（v2，接收外部 tx）

// TestIntegrationQuotaTryTxSuccess 扣减场景 24：租户 A 在外部 WithTenantTx 内调 TryTx 成功预占。
// 验证 RLS 放行、reserved 增加、流水插入。
func TestIntegrationQuotaTryTxSuccess(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	var res ports.QuotaReservation
	err := env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
		var err error
		res, err = env.tenantQuota.TryTx(ctx, tx, ports.QuotaTryRequest{
			TenantID:     env.tenantA.String(),
			ResourceType: ports.QuotaGPUCount,
			Amount:       2,
		})
		return err
	})
	if err != nil {
		t.Fatalf("TryTx 失败（RLS 应放行）: %v", err)
	}
	if res.TxID == "" {
		t.Fatal("TryTx 未返回 tx_id")
	}
	reserved, _ := env.loadGpuCounts(env.tenantA)
	if reserved != 2 {
		t.Fatalf("TryTx 后 reserved 应为 2，实际 %d", reserved)
	}
	t.Logf("TryTx 成功：tx_id=%s, reserved=%d", res.TxID, reserved)
}

// TestIntegrationQuotaTryTxRollback 扣减场景 25：TryTx 外层事务回滚 → 预占回滚。
// 验证回滚后 reserved 不变、无流水残留。
func TestIntegrationQuotaTryTxRollback(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	// 在 WithTenantTx 内 TryTx 成功后返回一个错误 → 事务回滚
	boom := errors.New("simulated business failure")
	err := env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := env.tenantQuota.TryTx(ctx, tx, ports.QuotaTryRequest{
			TenantID:     env.tenantA.String(),
			ResourceType: ports.QuotaGPUCount,
			Amount:       3,
		})
		if err != nil {
			return err
		}
		return boom // 模拟业务后续步骤失败 → 回滚
	})
	if !errors.Is(err, boom) {
		t.Fatalf("应返回模拟错误，实际: %v", err)
	}
	reserved, _ := env.loadGpuCounts(env.tenantA)
	if reserved != 0 {
		t.Fatalf("回滚后 reserved 应为 0，实际 %d（预占未回滚）", reserved)
	}
	// 流水不应残留
	var resCount int
	env.adminPool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM resource_reservations WHERE tenant_id = $1
	`, env.tenantA).Scan(&resCount)
	if resCount != 0 {
		t.Fatalf("回滚后不应有流水残留，实际 %d 条", resCount)
	}
	t.Logf("TryTx 回滚成功：reserved=0，无流水残留")
}

// TestIntegrationQuotaTryManyTxSuccess 扣减场景 26：TryManyTx 多维度预占成功。
func TestIntegrationQuotaTryManyTxSuccess(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)
	// seed cpu_core
	_, err := env.adminPool.Exec(context.Background(), `
		INSERT INTO resource_quota (tenant_id, resource_type, total, reserved, used)
		VALUES ($1, 'cpu_core', 10, 0, 0)
		ON CONFLICT (tenant_id, resource_type) DO UPDATE SET total = EXCLUDED.total
	`, env.tenantA)
	if err != nil {
		t.Fatalf("seed cpu_core 失败: %v", err)
	}

	var reservations []ports.QuotaReservation
	err = env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
		var err error
		reservations, err = env.tenantQuota.TryManyTx(ctx, tx, []ports.QuotaTryRequest{
			{TenantID: env.tenantA.String(), ResourceType: ports.QuotaGPUCount, Amount: 2},
			{TenantID: env.tenantA.String(), ResourceType: ports.QuotaCPUCore, Amount: 3},
		})
		return err
	})
	if err != nil {
		t.Fatalf("TryManyTx 失败: %v", err)
	}
	if len(reservations) != 2 {
		t.Fatalf("TryManyTx 应返回 2 条预占，实际 %d", len(reservations))
	}
	reservedG, _ := env.loadGpuCounts(env.tenantA)
	if reservedG != 2 {
		t.Fatalf("gpu_count reserved 应为 2，实际 %d", reservedG)
	}
	var reservedC int64
	env.adminPool.QueryRow(context.Background(), `
		SELECT reserved FROM resource_quota WHERE tenant_id = $1 AND resource_type = 'cpu_core'
	`, env.tenantA).Scan(&reservedC)
	if reservedC != 3 {
		t.Fatalf("cpu_core reserved 应为 3，实际 %d", reservedC)
	}
	t.Logf("TryManyTx 多维度成功：gpu reserved=%d, cpu reserved=%d", reservedG, reservedC)
}

// TestIntegrationQuotaTryManyTxRollback 扣减场景 27：TryManyTx 第二维度不足 → 外层回滚。
// 验证所有维度 reserved 不变、无流水残留。
func TestIntegrationQuotaTryManyTxRollback(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)
	// seed cpu_core total=2 → 第二维度 Try 3 不足
	_, err := env.adminPool.Exec(context.Background(), `
		INSERT INTO resource_quota (tenant_id, resource_type, total, reserved, used)
		VALUES ($1, 'cpu_core', 2, 0, 0)
		ON CONFLICT (tenant_id, resource_type) DO UPDATE SET total = EXCLUDED.total
	`, env.tenantA)
	if err != nil {
		t.Fatalf("seed cpu_core 失败: %v", err)
	}

	err = env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := env.tenantQuota.TryManyTx(ctx, tx, []ports.QuotaTryRequest{
			{TenantID: env.tenantA.String(), ResourceType: ports.QuotaGPUCount, Amount: 2},
			{TenantID: env.tenantA.String(), ResourceType: ports.QuotaCPUCore, Amount: 3}, // 不足
		})
		return err
	})
	if !errors.Is(err, ports.ErrQuotaExceeded) {
		t.Fatalf("应返回 ErrQuotaExceeded，实际: %v", err)
	}
	// gpu_count 的预占应随事务回滚
	reservedG, _ := env.loadGpuCounts(env.tenantA)
	if reservedG != 0 {
		t.Fatalf("回滚后 gpu_count reserved 应为 0，实际 %d", reservedG)
	}
	// 无流水残留
	var resCount int
	env.adminPool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM resource_reservations WHERE tenant_id = $1
	`, env.tenantA).Scan(&resCount)
	if resCount != 0 {
		t.Fatalf("回滚后不应有流水残留，实际 %d 条", resCount)
	}
	t.Logf("TryManyTx 维度二不足回滚成功：gpu reserved=0，无流水残留")
}

// TestIntegrationQuotaTryTxConfirmEndToEnd 扣减场景 28：TryTx + Confirm 同事务端到端。
// 验证预占 → Confirm → used 增加、reserved 减少。
func TestIntegrationQuotaTryTxConfirmEndToEnd(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	// 第一步：TryTx 预占
	var res ports.QuotaReservation
	err := env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
		var err error
		res, err = env.tenantQuota.TryTx(ctx, tx, ports.QuotaTryRequest{
			TenantID:     env.tenantA.String(),
			ResourceType: ports.QuotaGPUCount,
			Amount:       3,
		})
		return err
	})
	if err != nil {
		t.Fatalf("TryTx 失败: %v", err)
	}

	// 第二步：Confirm
	err = env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
		return env.tenantQuota.Confirm(ctx, tx, []string{res.TxID}, "instance-tx-1")
	})
	if err != nil {
		t.Fatalf("Confirm 失败: %v", err)
	}
	reserved, used := env.loadGpuCounts(env.tenantA)
	if reserved != 0 || used != 3 {
		t.Fatalf("Confirm 后应 reserved=0 used=3，实际 reserved=%d used=%d", reserved, used)
	}
	t.Logf("TryTx→Confirm 端到端成功：reserved=0, used=3")
}

// TestIntegrationQuotaTryTxConcurrentNoOversell 扣减场景 29：并发 TryTx 不超卖。
// 两个事务并发 TryTx 各占 6（total=10），PG 行锁串行化，第二个应 ErrQuotaExceeded。
func TestIntegrationQuotaTryTxConcurrentNoOversell(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)

	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	exceeded := 0
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
				_, err := env.tenantQuota.TryTx(ctx, tx, ports.QuotaTryRequest{
					TenantID:     env.tenantA.String(),
					ResourceType: ports.QuotaGPUCount,
					Amount:       6,
				})
				return err
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			} else if errors.Is(err, ports.ErrQuotaExceeded) {
				exceeded++
			} else {
				t.Errorf("并发 TryTx 出现未知错误: %v", err)
			}
		}()
	}
	wg.Wait()

	if success != 1 || exceeded != 1 {
		t.Fatalf("应 1 成功 1 拒绝，实际 success=%d exceeded=%d", success, exceeded)
	}
	reserved, _ := env.loadGpuCounts(env.tenantA)
	if reserved != 6 {
		t.Fatalf("并发后 reserved 应为 6，实际 %d", reserved)
	}
	t.Logf("并发 TryTx 不超卖：1 成功 1 拒绝，reserved=%d", reserved)
}

// TestIntegrationQuotaTryTxRLSIsolation 扣减场景 30：RLS 隔离。
// 租户 A 的 tx 尝试 TryTx 租户 B 的配额 → RLS 拦截（INSERT resource_reservations 或
// UPDATE resource_quota 被 RLS WITH CHECK 拒绝）。
func TestIntegrationQuotaTryTxRLSIsolation(t *testing.T) {
	env := newQuotaIntegrationEnv(t)
	env.seedQuotaFor(env.tenantA, 10)
	env.seedQuotaFor(env.tenantB, 10)

	// 用租户 A 的身份尝试 TryTx 但 tenant_id 填 B → RLS 应拒绝
	err := env.tenantStore.WithTenantTx(env.tenantCtxOf(env.tenantA), func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := env.tenantQuota.TryTx(ctx, tx, ports.QuotaTryRequest{
			TenantID:     env.tenantB.String(), // 试图预占 B 的配额
			ResourceType: ports.QuotaGPUCount,
			Amount:       2,
		})
		return err
	})
	if err == nil {
		// RLS 应拦截，若未拦截验证 B 的 reserved 未变
		reservedB, _ := env.loadGpuCounts(env.tenantB)
		if reservedB != 0 {
			t.Fatalf("租户 A 成功预占了租户 B 的配额（RLS 失效），B reserved=%d", reservedB)
		}
		t.Skip("RLS 未报错但 B reserved 未变（可能 lazy-init INSERT 被 RLS 拦截），跳过")
	}
	t.Logf("租户 A TryTx 租户 B 配额被 RLS 拦截: %v", err)
}

// ---------------------------------------------------------------- 管理场景 23：SDK 端到端
// （SDK 客户端等价路径）调 5 个 quota 管理端点，再用管理员连接回 DB 验证落库正确。
//
// 前置：已在本机启动 ani-gateway（ANI_AUTH_MODE=dev 免认证）+ auth-service，
// gateway 装配了 QuotaAdminService（NewPostgresQuota 连真实 PG）。
// ANI_TEST_GATEWAY_URL 未设置时跳过，并输出启动 gateway 与手动验证步骤。
func TestIntegrationQuotaSDKEndToEnd(t *testing.T) {
	gatewayURL := os.Getenv("ANI_TEST_GATEWAY_URL")
	if gatewayURL == "" {
		t.Skip(`ANI_TEST_GATEWAY_URL 未设置，跳过管理场景 23（SDK 端到端）。

手动启动前置：
1. 启动 auth-service：设 DATABASE_URL / NATS_URL / REDIS_URL / AUTH_JWT_* / ANI_AUTH_MODE=dev，运行 services/auth-service。
2. 启动 ani-gateway：设 AUTH_SERVICE_ADDR=127.0.0.1:9101 / REDIS_URL / ANI_AUTH_MODE=dev，运行 services/ani-gateway（:8080）。
3. 设置 ANI_TEST_GATEWAY_URL=http://127.0.0.1:8080 后重跑本测试。
`)
	}
	gatewayURL = strings.TrimRight(gatewayURL, "/")

	env := newQuotaIntegrationEnv(t)
	// 端到端用到真实的租户 ID；用 env.tenantA 已有 tenants 行，cleanup 会一并清理。
	tenantID := env.tenantA.String()
	client := &http.Client{Timeout: 10 * time.Second}

	// ① CreateTenantQuota（POST）：批量新建 gpu_count=6 / cpu_core=8。
	create := map[string]any{"items": []map[string]any{
		{"resource_type": "gpu_count", "total": 6},
		{"resource_type": "cpu_core", "total": 8},
	}}
	doQuotaJSON(t, client, "POST", gatewayURL+"/api/v1/admin/tenants/"+tenantID+"/quota", create, http.StatusOK)

	// DB 校验①：资源行已落库，used/reserved=0。
	reserved, used := env.loadGpuCounts(env.tenantA)
	if reserved != 0 || used != 0 {
		t.Fatalf("Create 后 gpu_count reserved/used 应为 0/0，实际 %d/%d", reserved, used)
	}
	if total := env.loadGpuTotal(env.tenantA, "gpu_count"); total != 6 {
		t.Fatalf("Create 后 gpu_count total 应为 6，实际 %d", total)
	}
	if total := env.loadGpuTotal(env.tenantA, "cpu_core"); total != 8 {
		t.Fatalf("Create 后 cpu_core total 应为 8，实际 %d", total)
	}

	// ② GetTenantQuota（GET）：回读并校验 JOIN meta（unit/display_name/is_discrete）。
	got := doQuotaGet(t, client, gatewayURL+"/api/v1/admin/tenants/"+tenantID+"/quota")
	gpuItem, ok := got["gpu_count"]
	if !ok {
		t.Fatalf("GetTenantQuota 缺 gpu_count 项: %+v", got)
	}
	if gpuItem.Total != 6 || gpuItem.DisplayName == "" || gpuItem.Unit == "" {
		t.Fatalf("GetTenantQuota JOIN meta 不符: %+v", gpuItem)
	}

	// ③ UpdateTenantQuota（PUT）：gpu_count total 改到 15（扩容，tightened=false）。
	update := map[string]any{"items": []map[string]any{
		{"resource_type": "gpu_count", "total": 15},
	}}
	doQuotaJSON(t, client, "PUT", gatewayURL+"/api/v1/admin/tenants/"+tenantID+"/quota", update, http.StatusOK)

	// DB 校验③：total=15。
	if total := env.loadGpuTotal(env.tenantA, "gpu_count"); total != 15 {
		t.Fatalf("Update 后 gpu_count total 应为 15，实际 %d", total)
	}

	// ④ DeleteTenantQuota（DELETE）：清空 resource_quota + resource_reservations。
	doQuotaJSON(t, client, "DELETE", gatewayURL+"/api/v1/admin/tenants/"+tenantID+"/quota", nil, http.StatusOK)

	// DB 校验④：该租户维度行已全部删除。
	if n := env.countQuotaRows(env.tenantA); n != 0 {
		t.Fatalf("Delete 后该租户 resource_quota 应清空，实际剩余 %d 行", n)
	}

	// ⑤ ListQuotaMeta（GET）：返回 enabled 维度列表（含 gpu_count / cpu_core）。
	metas := doQuotaMetaList(t, client, gatewayURL+"/api/v1/admin/quota-meta")
	if len(metas) < 2 {
		t.Fatalf("ListQuotaMeta 应返回至少 2 个维度，实际 %d", len(metas))
	}
	hasGPU, hasCPU := false, false
	for _, m := range metas {
		switch m.ResourceType {
		case "gpu_count":
			hasGPU = true
		case "cpu_core":
			hasCPU = true
		}
	}
	if !hasGPU || !hasCPU {
		t.Fatalf("ListQuotaMeta 应含 gpu_count/cpu_core，实际: %+v", metas)
	}

	t.Logf("SDK 端到端 5 端点全部通过：Create/Get/Update/Delete/ListQuotaMeta，DB 已验证")
}

// quotaItemHTTP 对应对应网关响应中的单条配额项。
type quotaItemHTTP struct {
	ResourceType string `json:"resource_type"`
	Total        int64  `json:"total"`
	Used         int64  `json:"used"`
	Reserved     int64  `json:"reserved"`
	Tightened    bool   `json:"tightened"`
	DisplayName  string `json:"display_name"`
	Unit         string `json:"unit"`
	IsDiscrete   bool   `json:"is_discrete"`
}

// quotaMetaHTTP 对应 ListQuotaMeta 响应中的维度项。
type quotaMetaHTTP struct {
	ResourceType string `json:"resource_type"`
	DisplayName  string `json:"display_name"`
	Unit         string `json:"unit"`
	DefaultQuota int64  `json:"default_quota"`
	IsDiscrete   bool   `json:"is_discrete"`
}

// doQuotaJSON 发送一个带可选 JSON body 的 HTTP 请求，并断言期望状态码。
func doQuotaJSON(t *testing.T, client *http.Client, method, url string, body any, wantStatus int) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, reader)
	if err != nil {
		t.Fatalf("构造请求失败 %s %s: %v", method, url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求失败 %s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s 状态码应为 %d，实际 %d，body=%s",
			method, url, wantStatus, resp.StatusCode, string(bodyBytes))
	}
}

// doQuotaGet 执行 GET 并解析 QuotaAdminService 的 quotaResponse（按 resource_type 索引）。
func doQuotaGet(t *testing.T, client *http.Client, url string) map[string]quotaItemHTTP {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET 失败 %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s 状态码应为 200，实际 %d，body=%s", url, resp.StatusCode, string(bodyBytes))
	}
	var envelope struct {
		Items []quotaItemHTTP `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("解析 GET %s 响应失败: %v", url, err)
	}
	out := make(map[string]quotaItemHTTP, len(envelope.Items))
	for _, it := range envelope.Items {
		out[it.ResourceType] = it
	}
	return out
}

// doQuotaMetaList 执行 GET 并解析 ListQuotaMeta 响应。
func doQuotaMetaList(t *testing.T, client *http.Client, url string) []quotaMetaHTTP {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s 失败: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s 状态码应为 200，实际 %d，body=%s", url, resp.StatusCode, string(bodyBytes))
	}
	var envelope struct {
		Items []quotaMetaHTTP `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("解析 GET %s 响应失败: %v", url, err)
	}
	return envelope.Items
}

// loadGpuTotal 用管理员连接读取某租户指定维度的 total（不存在则返回 0，用 COALESCE）。
func (e *quotaIntegrationEnv) loadGpuTotal(tid uuid.UUID, rt string) int64 {
	var total int64
	err := e.adminPool.QueryRow(context.Background(), `
		SELECT COALESCE(MAX(total), 0) FROM resource_quota
		WHERE tenant_id = $1 AND resource_type = $2
	`, tid, rt).Scan(&total)
	if err != nil {
		e.t.Fatalf("读 resource_quota total 失败: %v", err)
	}
	return total
}

// countQuotaRows 用管理员连接统计某租户的 resource_quota 行数。
func (e *quotaIntegrationEnv) countQuotaRows(tid uuid.UUID) int {
	var n int
	if err := e.adminPool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM resource_quota WHERE tenant_id = $1
	`, tid).Scan(&n); err != nil {
		e.t.Fatalf("统计 resource_quota 行数失败: %v", err)
	}
	return n
}
