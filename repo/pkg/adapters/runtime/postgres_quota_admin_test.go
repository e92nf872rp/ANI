package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// --- 管理方法（QuotaAdminService，issue-010）单元测试 ---
//
// 管理方法全部自开 WithPlatformTx（RLS bypass），因此 fake 仅使用
// quotaFakeStore.WithPlatformTx 路径。Create/Update 流程中的回读
// （quotaInfoByTypes）走 Query（多行），其余单值校验走 QueryRow。

// adminInfoRow 构造 quotaInfoByTypes / GetTenantQuota 的多行回读行，
// 扫描顺序与实现 Scan 目标一致：
// tenant_id, resource_type, total, reserved, used, unit, display_name,
// is_discrete, updated_at。
func adminInfoRow(tenantID, rt string, total, reserved, used int64, unit, displayName string, isDiscrete bool, updatedAt time.Time) quotaFakeRow {
	return quotaFakeRow{values: []any{tenantID, rt, total, reserved, used, unit, displayName, isDiscrete, updatedAt}}
}

// adminMetaRow 构造 resource_quota_meta 的 enabled/default_quota 校验行。
func adminMetaRow(enabled bool, defaultQuota int64) quotaFakeRow {
	return quotaFakeRow{values: []any{enabled, defaultQuota}}
}

// tenantExistsRow 构造租户存在性校验（EXISTS）返回行。
func tenantExistsRow(exists bool) quotaFakeRow {
	return quotaFakeRow{values: []any{exists}}
}

// TestPostgresQuotaAdminCreateTenantQuotaSuccess 验证 CreateTenantQuota 批量新建成功：
// 两个维度，其中 total 省略的维度取 default_quota，回读返回带 meta 信息的 QuotaInfo。
func TestPostgresQuotaAdminCreateTenantQuotaSuccess(t *testing.T) {
	tx := &quotaFakeTx{}
	now := time.Unix(100, 0)
	// 流程 QueryRow 顺序：租户 EXISTS → 维度一 meta → 维度二 meta
	tx.enqueueRows(
		tenantExistsRow(true),
		adminMetaRow(true, int64(100)), // gpu_count：total 省略 → 取 default 100
		adminMetaRow(true, int64(50)),  // cpu_core：显式 total=200
	)
	// 回读 quotaInfoByTypes -> Query（多行）
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		adminInfoRow(testTenantID, string(ports.QuotaGPUCount), 100, 0, 0, "张", "GPU 数量", true, now),
		adminInfoRow(testTenantID, string(ports.QuotaCPUCore), 200, 0, 0, "核", "CPU 核数", false, now),
	}})
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	infos, err := q.CreateTenantQuota(context.Background(), testTenantID, []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: 0}, // total 省略 → default
		{ResourceType: ports.QuotaCPUCore, Total: 200},
	})
	if err != nil {
		t.Fatalf("CreateTenantQuota() error = %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("CreateTenantQuota() len = %d, want 2", len(infos))
	}
	if infos[0].ResourceType != ports.QuotaGPUCount || infos[0].Total != 100 {
		t.Fatalf("GPU 维度 total = %d, want default 100", infos[0].Total)
	}
	if infos[1].ResourceType != ports.QuotaCPUCore || infos[1].Total != 200 {
		t.Fatalf("CPU 维度 total = %d, want 200", infos[1].Total)
	}
	if !infos[0].IsDiscrete || infos[1].IsDiscrete {
		t.Fatalf("IsDiscrete 解析错误：GPU=%v, CPU=%v", infos[0].IsDiscrete, infos[1].IsDiscrete)
	}
	if !hasExec(tx, "INSERT INTO resource_quota") {
		t.Fatalf("CreateTenantQuota() 未执行 INSERT")
	}
	if !hasExec(tx, "ON CONFLICT") {
		t.Fatalf("CreateTenantQuota() 应使用 ON CONFLICT DO NOTHING")
	}
}

// TestPostgresQuotaAdminCreateTenantQuotaTenantNotFound 验证 CreateTenantQuota
// 租户不存在 → ErrTenantNotFound（在任一维度 meta 校验之前短路）。
func TestPostgresQuotaAdminCreateTenantQuotaTenantNotFound(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(tenantExistsRow(false)) // 租户不存在
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	_, err := q.CreateTenantQuota(context.Background(), testTenantID, []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: 10},
	})
	if err != ports.ErrTenantNotFound {
		t.Fatalf("CreateTenantQuota() error = %v, want ErrTenantNotFound", err)
	}
}

// TestPostgresQuotaAdminCreateTenantQuotaResourceNotRegistered 验证 CreateTenantQuota
// 维度未注册/enabled=false → ErrQuotaResourceNotRegistered。
func TestPostgresQuotaAdminCreateTenantQuotaResourceNotRegistered(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		tenantExistsRow(true),
		quotaFakeRow{err: ports.ErrQuotaResourceNotRegistered}, // 维度 meta 未注册
	)
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	_, err := q.CreateTenantQuota(context.Background(), testTenantID, []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: 10},
	})
	if err != ports.ErrQuotaResourceNotRegistered {
		t.Fatalf("CreateTenantQuota() error = %v, want ErrQuotaResourceNotRegistered", err)
	}
}

// TestPostgresQuotaAdminCreateTenantQuotaConflict 验证 CreateTenantQuota 部分成功语义：
// 已存在维度 ON CONFLICT DO NOTHING 跳过（不中断循环、不回滚），其余维度正常创建并提交，
// 最终返回 ports.ErrQuotaAlreadyExists（handler 映射 409 QUOTA_ALREADY_EXISTS）。
func TestPostgresQuotaAdminCreateTenantQuotaConflict(t *testing.T) {
	tx := &quotaFakeTx{}
	now := time.Unix(100, 0)
	// 流程 QueryRow 顺序：租户 EXISTS → 两维度 meta 校验
	tx.enqueueRows(
		tenantExistsRow(true),
		adminMetaRow(true, int64(100)),
		adminMetaRow(true, int64(100)),
	)
	// 回读 quotaInfoByTypes -> Query（多行，两个维度都返回）
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		adminInfoRow(testTenantID, string(ports.QuotaGPUCount), 100, 0, 0, "张", "GPU 数量", true, now),
		adminInfoRow(testTenantID, string(ports.QuotaCPUCore), 200, 0, 0, "核", "CPU 核数", false, now),
	}})
	// 第一次 INSERT（已存在维度）RowsAffected=0 → 标记冲突并跳过；
	// 第二次 INSERT（新维度）RowsAffected=1 → 正常创建，循环不中断。
	insertCnt := 0
	tx.execFn = func(sql string, _ []any) int64 {
		if strings.Contains(sql, "INSERT INTO resource_quota") {
			insertCnt++
			return 0
		}
		return 1
	}
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	_, err := q.CreateTenantQuota(context.Background(), testTenantID, []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: 10},
		{ResourceType: ports.QuotaCPUCore, Total: 200},
	})
	if err != ports.ErrQuotaAlreadyExists {
		t.Fatalf("CreateTenantQuota() error = %v, want ErrQuotaAlreadyExists", err)
	}
	// 部分成功：两个维度的 INSERT 都应执行（冲突维度跳过不代表中断整个循环）
	if insertCnt != 2 {
		t.Fatalf("CreateTenantQuota() INSERT 执行次数 = %d, want 2（冲突维度跳过但其余维度继续创建）", insertCnt)
	}
	// 回读应执行，确认事务内 QUERY（quotaInfoByTypes）走完
	if len(tx.queryResults) != 0 {
		t.Fatalf("CreateTenantQuota() should consume 回读 query，remaining = %d", len(tx.queryResults))
	}
}

// TestPostgresQuotaAdminCreateTenantQuotaEmptyItems 验证 CreateTenantQuota
// items 为空 → 校验错误（ErrInvalid），不进入事务。
func TestPostgresQuotaAdminCreateTenantQuotaEmptyItems(t *testing.T) {
	tx := &quotaFakeTx{}
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	_, err := q.CreateTenantQuota(context.Background(), testTenantID, nil)
	if err != ports.ErrInvalid {
		t.Fatalf("CreateTenantQuota() error = %v, want ErrInvalid", err)
	}
}

// TestPostgresQuotaAdminUpdateTenantQuotaSuccess 验证 UpdateTenantQuota 批量改 total 成功，
// total >= used+reserved → tightened=false。
func TestPostgresQuotaAdminUpdateTenantQuotaSuccess(t *testing.T) {
	tx := &quotaFakeTx{}
	now := time.Unix(100, 0)
	// 每维度先做 meta 校验（QueryRow），再 Exec UPDATE
	tx.enqueueRows(
		adminMetaRow(true, int64(100)),
		adminMetaRow(true, int64(100)),
	)
	// 回读：total=200 >= used+reserved → 不缩容，tightened=false
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		adminInfoRow(testTenantID, string(ports.QuotaGPUCount), 200, 0, 0, "张", "GPU 数量", true, now),
		adminInfoRow(testTenantID, string(ports.QuotaCPUCore), 200, 0, 0, "核", "CPU 核数", false, now),
	}})
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	infos, err := q.UpdateTenantQuota(context.Background(), testTenantID, []ports.QuotaItemUpdate{
		{ResourceType: ports.QuotaGPUCount, Total: 200},
		{ResourceType: ports.QuotaCPUCore, Total: 200},
	})
	if err != nil {
		t.Fatalf("UpdateTenantQuota() error = %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("UpdateTenantQuota() len = %d, want 2", len(infos))
	}
	if infos[0].Tightened || infos[1].Tightened {
		t.Fatalf("UpdateTenantQuota() tightened 应为 false（total>=used+reserved）")
	}
	if !hasExec(tx, "GREATEST") {
		t.Fatalf("UpdateTenantQuota() 未执行缩容 clamp 的 GREATEST UPDATE")
	}
}

// TestPostgresQuotaAdminUpdateTenantQuotaNotFound 验证 UpdateTenantQuota
// 维度行不存在（UPDATE RowsAffected=0）→ ErrQuotaNotFound。
func TestPostgresQuotaAdminUpdateTenantQuotaNotFound(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(adminMetaRow(true, int64(100)))
	// UPDATE 返回 RowsAffected=0 → 行不存在
	tx.execFn = func(_ string, _ []any) int64 { return 0 }
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	_, err := q.UpdateTenantQuota(context.Background(), testTenantID, []ports.QuotaItemUpdate{
		{ResourceType: ports.QuotaGPUCount, Total: 100},
	})
	if err != ports.ErrQuotaNotFound {
		t.Fatalf("UpdateTenantQuota() error = %v, want ErrQuotaNotFound", err)
	}
}

// TestPostgresQuotaAdminUpdateTenantQuotaResourceNotRegistered 验证 UpdateTenantQuota
// 维度未注册/enabled=false → ErrQuotaResourceNotRegistered。
func TestPostgresQuotaAdminUpdateTenantQuotaResourceNotRegistered(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{err: ports.ErrQuotaResourceNotRegistered})
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	_, err := q.UpdateTenantQuota(context.Background(), testTenantID, []ports.QuotaItemUpdate{
		{ResourceType: ports.QuotaGPUCount, Total: 100},
	})
	if err != ports.ErrQuotaResourceNotRegistered {
		t.Fatalf("UpdateTenantQuota() error = %v, want ErrQuotaResourceNotRegistered", err)
	}
}

// TestPostgresQuotaAdminUpdateTenantQuotaTightened 验证 UpdateTenantQuota
// total < used+reserved（缩容）→ GREATEST clamp 生效，回读 total 高于请求 →
// tightened=true + 返回收紧后的 total。
func TestPostgresQuotaAdminUpdateTenantQuotaTightened(t *testing.T) {
	tx := &quotaFakeTx{}
	now := time.Unix(100, 0)
	tx.enqueueRows(adminMetaRow(true, int64(100))) // meta 校验通过
	// 请求 total=100，但 used=150 → SQL 层 clamp 到 150，回读 total=150
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		adminInfoRow(testTenantID, string(ports.QuotaGPUCount), 150, 0, 150, "张", "GPU 数量", true, now),
	}})
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	infos, err := q.UpdateTenantQuota(context.Background(), testTenantID, []ports.QuotaItemUpdate{
		{ResourceType: ports.QuotaGPUCount, Total: 100},
	})
	if err != nil {
		t.Fatalf("UpdateTenantQuota() error = %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("UpdateTenantQuota() len = %d, want 1", len(infos))
	}
	if !infos[0].Tightened {
		t.Fatalf("UpdateTenantQuota() tightened 应为 true（缩容被 clamp）")
	}
	if infos[0].Total != 150 {
		t.Fatalf("UpdateTenantQuota() total = %d, want 150（收紧后的 total）", infos[0].Total)
	}
}

// TestPostgresQuotaAdminUpdateTenantQuotaNotTightened 验证 UpdateTenantQuota
// total >= used+reserved → tightened=false。
func TestPostgresQuotaAdminUpdateTenantQuotaNotTightened(t *testing.T) {
	tx := &quotaFakeTx{}
	now := time.Unix(100, 0)
	tx.enqueueRows(adminMetaRow(true, int64(100)))
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		adminInfoRow(testTenantID, string(ports.QuotaGPUCount), 300, 50, 100, "张", "GPU 数量", true, now),
	}})
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	infos, err := q.UpdateTenantQuota(context.Background(), testTenantID, []ports.QuotaItemUpdate{
		{ResourceType: ports.QuotaGPUCount, Total: 300},
	})
	if err != nil {
		t.Fatalf("UpdateTenantQuota() error = %v", err)
	}
	if infos[0].Tightened {
		t.Fatalf("UpdateTenantQuota() tightened 应为 false")
	}
	if infos[0].Total != 300 {
		t.Fatalf("UpdateTenantQuota() total = %d, want 300", infos[0].Total)
	}
}

// TestPostgresQuotaAdminUpdateTenantQuotaEmptyItems 验证 UpdateTenantQuota
// items 为空 → 校验错误（ErrInvalid）。
func TestPostgresQuotaAdminUpdateTenantQuotaEmptyItems(t *testing.T) {
	tx := &quotaFakeTx{}
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	_, err := q.UpdateTenantQuota(context.Background(), testTenantID, nil)
	if err != ports.ErrInvalid {
		t.Fatalf("UpdateTenantQuota() error = %v, want ErrInvalid", err)
	}
}

// TestPostgresQuotaAdminUpsertTenantQuotaSuccess 验证 UpsertTenantQuota 批量成功：
// 已有维度走 ON CONFLICT DO UPDATE，新维度走 INSERT，total=0 取 default_quota。
func TestPostgresQuotaAdminUpsertTenantQuotaSuccess(t *testing.T) {
	tx := &quotaFakeTx{}
	now := time.Unix(100, 0)
	tx.enqueueRows(
		tenantExistsRow(true),
		adminMetaRow(true, int64(100)),
		adminMetaRow(true, int64(50)),
	)
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		adminInfoRow(testTenantID, string(ports.QuotaCPUCore), 200, 0, 0, "核", "CPU 核数", false, now),
		adminInfoRow(testTenantID, string(ports.QuotaGPUCount), 100, 0, 0, "张", "GPU 数量", true, now),
	}})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	infos, err := q.UpsertTenantQuota(context.Background(), testTenantID, []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: 0},
		{ResourceType: ports.QuotaCPUCore, Total: 200},
	})
	if err != nil {
		t.Fatalf("UpsertTenantQuota() error = %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("UpsertTenantQuota() len = %d, want 2", len(infos))
	}
	for _, info := range infos {
		if info.Tightened {
			t.Fatalf("UpsertTenantQuota() success path tightened 应为 false: %+v", info)
		}
	}
	if !hasExec(tx, "INSERT INTO resource_quota") || !hasExec(tx, "ON CONFLICT") || !hasExec(tx, "GREATEST") {
		t.Fatalf("UpsertTenantQuota() 应使用 INSERT ... ON CONFLICT DO UPDATE + GREATEST，execs=%s", joinExecs(tx))
	}
}

func TestPostgresQuotaAdminUpsertTenantQuotaTightened(t *testing.T) {
	tx := &quotaFakeTx{}
	now := time.Unix(100, 0)
	tx.enqueueRows(
		tenantExistsRow(true),
		adminMetaRow(true, int64(100)),
	)
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		adminInfoRow(testTenantID, string(ports.QuotaGPUCount), 150, 0, 150, "张", "GPU 数量", true, now),
	}})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	infos, err := q.UpsertTenantQuota(context.Background(), testTenantID, []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: 100},
	})
	if err != nil {
		t.Fatalf("UpsertTenantQuota() error = %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("UpsertTenantQuota() len = %d, want 1", len(infos))
	}
	if !infos[0].Tightened {
		t.Fatalf("UpsertTenantQuota() tightened 应为 true（缩容被 clamp）")
	}
	if infos[0].Total != 150 {
		t.Fatalf("UpsertTenantQuota() total = %d, want 150", infos[0].Total)
	}
}

func TestPostgresQuotaAdminUpsertTenantQuotaInvalidInput(t *testing.T) {
	cases := []struct {
		name  string
		items []ports.QuotaItemInput
	}{
		{name: "empty", items: nil},
		{name: "duplicate resource type", items: []ports.QuotaItemInput{
			{ResourceType: ports.QuotaGPUCount, Total: 100},
			{ResourceType: ports.QuotaGPUCount, Total: 200},
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tx := &quotaFakeTx{}
			_, err := NewPostgresQuota(&quotaFakeStore{tx: tx}).UpsertTenantQuota(context.Background(), testTenantID, test.items)
			if !errors.Is(err, ports.ErrInvalid) {
				t.Fatalf("UpsertTenantQuota() error = %v, want ErrInvalid", err)
			}
			if len(tx.execSQLs) != 0 {
				t.Fatalf("UpsertTenantQuota() invalid input 不应进入事务执行 SQL，execs=%s", joinExecs(tx))
			}
		})
	}
}

func TestPostgresQuotaAdminUpsertTenantQuotaNegativeTotal(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(tenantExistsRow(true))
	store := &quotaFakeStore{tx: tx}

	_, err := NewPostgresQuota(store).UpsertTenantQuota(context.Background(), testTenantID, []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: -1},
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("UpsertTenantQuota() error = %v, want ErrInvalid", err)
	}
	if !store.platformRolledBack {
		t.Fatalf("UpsertTenantQuota() 事务内校验失败应回滚")
	}
	if len(tx.execSQLs) != 0 {
		t.Fatalf("UpsertTenantQuota() negative total 不应执行写 SQL，execs=%s", joinExecs(tx))
	}
}

func TestPostgresQuotaAdminUpsertTenantQuotaResourceNotRegisteredRollback(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		tenantExistsRow(true),
		adminMetaRow(true, int64(100)),
		quotaFakeRow{err: ports.ErrQuotaResourceNotRegistered},
	)
	insertCnt := 0
	tx.execFn = func(sql string, _ []any) int64 {
		if strings.Contains(sql, "INSERT INTO resource_quota") {
			insertCnt++
		}
		return 1
	}
	store := &quotaFakeStore{tx: tx}

	_, err := NewPostgresQuota(store).UpsertTenantQuota(context.Background(), testTenantID, []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: 100},
		{ResourceType: ports.QuotaCPUCore, Total: 100},
	})
	if !errors.Is(err, ports.ErrQuotaResourceNotRegistered) {
		t.Fatalf("UpsertTenantQuota() error = %v, want ErrQuotaResourceNotRegistered", err)
	}
	if !store.platformRolledBack {
		t.Fatalf("UpsertTenantQuota() 任一维度失败应回滚整批")
	}
	if insertCnt != 1 {
		t.Fatalf("UpsertTenantQuota() 第二维度 meta 失败前只应写入一次，insertCnt=%d", insertCnt)
	}
}

func TestPostgresQuotaAdminUpsertTenantQuotaCommitUncertain(t *testing.T) {
	tx := &quotaFakeTx{}
	now := time.Unix(100, 0)
	tx.enqueueRows(
		tenantExistsRow(true),
		adminMetaRow(true, int64(100)),
	)
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		adminInfoRow(testTenantID, string(ports.QuotaGPUCount), 100, 0, 0, "张", "GPU 数量", true, now),
	}})
	store := &quotaFakeStore{
		tx:          tx,
		platformErr: fmt.Errorf("%w: connection lost", ports.ErrMetadataPlatformTxCommit),
	}

	_, err := NewPostgresQuota(store).UpsertTenantQuota(context.Background(), testTenantID, []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: 100},
	})
	if !errors.Is(err, ports.ErrQuotaUpdateUncertain) {
		t.Fatalf("UpsertTenantQuota() error = %v, want ErrQuotaUpdateUncertain", err)
	}
	if errors.Is(err, ports.ErrMetadataPlatformTxCommit) {
		t.Fatalf("UpsertTenantQuota() 不应向调用方暴露 metadata commit 哨兵")
	}
}

func TestPostgresQuotaAdminUpsertTenantQuotaInnerFailureNotUncertain(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		tenantExistsRow(true),
		quotaFakeRow{err: ports.ErrQuotaResourceNotRegistered},
	)
	store := &quotaFakeStore{
		tx:          tx,
		platformErr: fmt.Errorf("%w: connection lost", ports.ErrMetadataPlatformTxCommit),
	}

	_, err := NewPostgresQuota(store).UpsertTenantQuota(context.Background(), testTenantID, []ports.QuotaItemInput{
		{ResourceType: ports.QuotaGPUCount, Total: 100},
	})
	if !errors.Is(err, ports.ErrQuotaResourceNotRegistered) {
		t.Fatalf("UpsertTenantQuota() error = %v, want ErrQuotaResourceNotRegistered", err)
	}
	if errors.Is(err, ports.ErrQuotaUpdateUncertain) {
		t.Fatalf("UpsertTenantQuota() 事务内失败不应误报 ErrQuotaUpdateUncertain")
	}
}

// TestPostgresQuotaAdminGetTenantQuotaMapsMeta 验证 GetTenantQuota 返回多行，
// JOIN resource_quota_meta 的 unit/display_name/is_discrete/updated_at 正确解析。
func TestPostgresQuotaAdminGetTenantQuotaMapsMeta(t *testing.T) {
	tx := &quotaFakeTx{}
	now := time.Unix(100, 0)
	tx.enqueueRows(
		tenantExistsRow(true),
	)
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		adminInfoRow(testTenantID, string(ports.QuotaCPUCore), 100, 0, 0, "核", "CPU 核数", false, now),
		adminInfoRow(testTenantID, string(ports.QuotaGPUCount), 100, 0, 0, "张", "GPU 数量", true, now),
		adminInfoRow(testTenantID, string(ports.QuotaMemoryGB), 200, 0, 0, "GB", "内存", false, now),
	}})
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	infos, err := q.GetTenantQuota(context.Background(), testTenantID)
	if err != nil {
		t.Fatalf("GetTenantQuota() error = %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("GetTenantQuota() len = %d, want 3", len(infos))
	}
	if infos[0].ResourceType != ports.QuotaCPUCore || infos[0].Unit != "核" ||
		infos[0].DisplayName != "CPU 核数" || infos[0].IsDiscrete {
		t.Fatalf("CPU 维度 meta 解析错误：%+v", infos[0])
	}
	foundGPU := false
	for _, info := range infos {
		if info.ResourceType == ports.QuotaGPUCount {
			foundGPU = true
			if !info.IsDiscrete || info.Unit != "张" {
				t.Fatalf("GPU 维度 meta 解析错误：%+v", info)
			}
		}
	}
	if !foundGPU {
		t.Fatalf("GetTenantQuota() 未返回 GPU 维度")
	}
}

// TestPostgresQuotaAdminGetTenantQuotaEmpty 验证 GetTenantQuota 租户存在但无配额行
// → 返回空 items（不报错）。
func TestPostgresQuotaAdminGetTenantQuotaEmpty(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(tenantExistsRow(true))
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{}})
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	infos, err := q.GetTenantQuota(context.Background(), testTenantID)
	if err != nil {
		t.Fatalf("GetTenantQuota() error = %v", err)
	}
	if len(infos) != 0 {
		t.Fatalf("GetTenantQuota() len = %d, want 0", len(infos))
	}
}

// TestPostgresQuotaAdminGetTenantQuotaTenantNotFound 验证 GetTenantQuota 租户不存在
// → ErrTenantNotFound（handler 映射 404 TENANT_NOT_FOUND）。
func TestPostgresQuotaAdminGetTenantQuotaTenantNotFound(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(tenantExistsRow(false))
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	_, err := q.GetTenantQuota(context.Background(), testTenantID)
	if err != ports.ErrTenantNotFound {
		t.Fatalf("GetTenantQuota() error = %v, want ErrTenantNotFound", err)
	}
}

// TestPostgresQuotaAdminDeleteTenantQuotaSuccess 验证 DeleteTenantQuota 删除成功，
// 连同 resource_reservations 流水一并删除。
func TestPostgresQuotaAdminDeleteTenantQuotaSuccess(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(tenantExistsRow(true))
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	if err := q.DeleteTenantQuota(context.Background(), testTenantID); err != nil {
		t.Fatalf("DeleteTenantQuota() error = %v", err)
	}
	if !hasExec(tx, "DELETE FROM resource_reservations") {
		t.Fatalf("DeleteTenantQuota() 未删除 reservations 流水")
	}
	if !hasExec(tx, "DELETE FROM resource_quota") {
		t.Fatalf("DeleteTenantQuota() 未删除配额行")
	}
}

// TestPostgresQuotaAdminDeleteTenantQuotaTenantNotFound 验证 DeleteTenantQuota
// 租户不存在 → ErrTenantNotFound。
func TestPostgresQuotaAdminDeleteTenantQuotaTenantNotFound(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(tenantExistsRow(false))
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	if err := q.DeleteTenantQuota(context.Background(), testTenantID); err != ports.ErrTenantNotFound {
		t.Fatalf("DeleteTenantQuota() error = %v, want ErrTenantNotFound", err)
	}
}

// TestPostgresQuotaAdminDeleteTenantQuotaUsedOk 验证 DeleteTenantQuota 当 used>0
// 时仍可删除（清理语义，不守卫已使用配额）。
func TestPostgresQuotaAdminDeleteTenantQuotaUsedOk(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(tenantExistsRow(true))
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	// 已使用配额的状态由 SQL DELETE 直接清掉，adapter 不读取、不判断，
	// 直接执行两次 DELETE（流水 + 配额行）。
	if err := q.DeleteTenantQuota(context.Background(), testTenantID); err != nil {
		t.Fatalf("DeleteTenantQuota() error = %v", err)
	}
	if !hasExec(tx, "DELETE FROM resource_reservations") || !hasExec(tx, "DELETE FROM resource_quota") {
		t.Fatalf("DeleteTenantQuota() 应同时删除流水与配额行")
	}
}

// TestPostgresQuotaAdminListQuotaMeta 验证 ListQuotaMeta 返回 enabled=true 的维度列表，
// 含 display_name/unit/default_quota/is_discrete。
func TestPostgresQuotaAdminListQuotaMeta(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{string(ports.QuotaGPUCount), "GPU 数量", "张", int64(100), true}},
		{values: []any{string(ports.QuotaCPUCore), "CPU 核数", "核", int64(50), false}},
	}})
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	metas, err := q.ListQuotaMeta(context.Background())
	if err != nil {
		t.Fatalf("ListQuotaMeta() error = %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("ListQuotaMeta() len = %d, want 2", len(metas))
	}
	if metas[0].ResourceType != ports.QuotaGPUCount || metas[0].DisplayName != "GPU 数量" ||
		metas[0].Unit != "张" || metas[0].DefaultQuota != 100 || !metas[0].IsDiscrete {
		t.Fatalf("GPU meta 解析错误：%+v", metas[0])
	}
	if metas[1].IsDiscrete {
		t.Fatalf("CPU meta 应 is_discrete=false")
	}
}

// TestPostgresQuotaAdminListQuotaMetaExcludesDisabled 验证 ListQuotaMeta 不返回
// enabled=false 的维度：只回读 enabled=true 的行（SQL WHERE enabled=true 过滤），
// 因此结果中不含被禁用的维度（如 token_count）。
func TestPostgresQuotaAdminListQuotaMetaExcludesDisabled(t *testing.T) {
	tx := &quotaFakeTx{}
	// 模拟 SQL 已按 enabled=true 过滤后仅返回 GPU/CPU 两行，token_count 被排除
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{string(ports.QuotaGPUCount), "GPU 数量", "张", int64(100), true}},
		{values: []any{string(ports.QuotaCPUCore), "CPU 核数", "核", int64(50), false}},
	}})
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	metas, err := q.ListQuotaMeta(context.Background())
	if err != nil {
		t.Fatalf("ListQuotaMeta() error = %v", err)
	}
	for _, meta := range metas {
		if meta.ResourceType == ports.QuotaTokenCount {
			t.Fatalf("ListQuotaMeta() 不应返回 enabled=false 的 token_count")
		}
	}
}

// TestPostgresQuotaAdminListQuotaMetaEmpty 验证 ListQuotaMeta 空表 → 返回空 items。
func TestPostgresQuotaAdminListQuotaMetaEmpty(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{}})
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	metas, err := q.ListQuotaMeta(context.Background())
	if err != nil {
		t.Fatalf("ListQuotaMeta() error = %v", err)
	}
	if len(metas) != 0 {
		t.Fatalf("ListQuotaMeta() len = %d, want 0", len(metas))
	}
}
