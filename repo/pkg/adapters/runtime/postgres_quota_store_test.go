package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kubercloud/ani/pkg/ports"
)

// testTenantID2 分页测试用的第二个租户 UUID，与 postgres_quota_test.go 的
// testTenantID（5dbb1d01-0000-4000-8000-000000000001）配合形成 keyset 顺序。
const testTenantID2 = "5dbb1d01-0000-4000-8000-000000000002"

// enabledMetaRow 返回一个 enabled=true 的 meta 行，供 Put 的 per-dimension pre-check 使用。
func enabledMetaRow() quotaFakeRow {
	return quotaFakeRow{values: []any{true}}
}

// reReadRow 构造 Put/GetMy 回读行：resource_type/total/reserved/used。
func reReadRow(rt string, total, reserved, used int64) quotaFakeRow {
	return quotaFakeRow{values: []any{rt, total, reserved, used}}
}

// TestPostgresQuotaStorePutInsert 验证 Put 新增（行不存在）→ UPSERT 建行成功，
// 回读返回多维度 QuotaView。
func TestPostgresQuotaStorePutInsert(t *testing.T) {
	tx := &quotaFakeTx{}
	// 两个维度：meta pre-check（enabled=true）各一次
	tx.enqueueRows(enabledMetaRow(), enabledMetaRow())
	// 回读全部维度：gpu_count=10, cpu_core=20（新行 used/reserved=0）
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		reReadRow(string(ports.QuotaGPUCount), 10, 0, 0),
		reReadRow(string(ports.QuotaCPUCore), 20, 0, 0),
	}})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	view, err := q.Put(context.Background(), "key-1", ports.QuotaPutRequest{
		TenantID: testTenantID,
		Total: map[ports.ResourceType]int64{
			ports.QuotaGPUCount: 10,
			ports.QuotaCPUCore:  20,
		},
		IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if view.TenantID != testTenantID {
		t.Fatalf("Put() TenantID = %q", view.TenantID)
	}
	if view.Total[ports.QuotaGPUCount] != 10 || view.Total[ports.QuotaCPUCore] != 20 {
		t.Fatalf("Put() Total = %v", view.Total)
	}
	// 必须执行 UPSERT 建行 SQL
	if !hasExec(tx, "INSERT INTO resource_quota") || !hasExec(tx, "ON CONFLICT") {
		t.Fatalf("Put() 未执行 UPSERT INSERT:\n%s", joinExecs(tx))
	}
}

// TestPostgresQuotaStorePutUpdate 验证 Put 修改（行存在）→ UPSERT 覆盖 total 成功。
func TestPostgresQuotaStorePutUpdate(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(enabledMetaRow())
	// 回读：gpu_count 已被覆盖为 100
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		reReadRow(string(ports.QuotaGPUCount), 100, 0, 0),
	}})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	view, err := q.Put(context.Background(), "key-2", ports.QuotaPutRequest{
		TenantID:       testTenantID,
		Total:          map[ports.ResourceType]int64{ports.QuotaGPUCount: 100},
		IdempotencyKey: "key-2",
	})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if view.Total[ports.QuotaGPUCount] != 100 {
		t.Fatalf("Put() Total = %v, want gpu=100", view.Total)
	}
	// UPSERT 必须是 DO UPDATE 覆盖 total，而非 DO NOTHING
	if !hasExec(tx, "DO UPDATE SET total = EXCLUDED.total") {
		t.Fatalf("Put() 未执行覆盖 total 的 UPSERT:\n%s", joinExecs(tx))
	}
}

// TestPostgresQuotaStorePutUnregistered 验证 Put 对未注册资源类型 →
// ErrQuotaResourceNotRegistered。
func TestPostgresQuotaStorePutUnregistered(t *testing.T) {
	tx := &quotaFakeTx{}
	// meta 无此类型（ErrNoRows）→ 未注册
	tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	_, err := q.Put(context.Background(), "key-3", ports.QuotaPutRequest{
		TenantID: testTenantID,
		Total:    map[ports.ResourceType]int64{ports.QuotaStorageGB: 64},
	})
	if err != ports.ErrQuotaResourceNotRegistered {
		t.Fatalf("Put() error = %v, want ErrQuotaResourceNotRegistered", err)
	}
}

// TestPostgresQuotaStorePutDisabledMeta 验证 Put 对 enabled=false 资源类型 →
// ErrQuotaResourceNotRegistered。
func TestPostgresQuotaStorePutDisabledMeta(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{false}}) // enabled=false
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	_, err := q.Put(context.Background(), "key-4", ports.QuotaPutRequest{
		TenantID: testTenantID,
		Total:    map[ports.ResourceType]int64{ports.QuotaGPUCount: 8},
	})
	if err != ports.ErrQuotaResourceNotRegistered {
		t.Fatalf("Put() error = %v, want ErrQuotaResourceNotRegistered", err)
	}
}

// TestPostgresQuotaStorePutCheckViolation 验证 Put 撞 CHECK 约束：total < used+reserved
// 时 UPSERT 透传 DB 错误（不 clamp、不吞错）。
func TestPostgresQuotaStorePutCheckViolation(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(enabledMetaRow())
	// UPSERT 阶段注入 CHECK 约束违反错误
	checkErr := errors.New("pq: check constraint \"resource_quota_check\" is violated")
	tx.execErr = func(sql string, _ []any) error {
		if strings.Contains(sql, "ON CONFLICT") {
			return checkErr
		}
		return nil
	}
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	_, err := q.Put(context.Background(), "key-5", ports.QuotaPutRequest{
		TenantID: testTenantID,
		Total:    map[ports.ResourceType]int64{ports.QuotaGPUCount: 1}, // < used+reserved
	})
	if err == nil {
		t.Fatalf("Put() 撞 CHECK 约束应透传错误，got nil")
	}
	if !errors.Is(err, checkErr) {
		t.Fatalf("Put() error = %v, want 透传 CHECK violation %v", err, checkErr)
	}
}

// TestPostgresQuotaStorePutMultipleDims 验证 Put 多维度同时 PUT → 全部成功。
func TestPostgresQuotaStorePutMultipleDims(t *testing.T) {
	tx := &quotaFakeTx{}
	// 3 个维度 pre-check
	tx.enqueueRows(enabledMetaRow(), enabledMetaRow(), enabledMetaRow())
	// 回读 3 个维度
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		reReadRow(string(ports.QuotaGPUCount), 4, 0, 0),
		reReadRow(string(ports.QuotaMemoryGB), 32, 0, 0),
		reReadRow(string(ports.QuotaStorageGB), 64, 0, 0),
	}})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	view, err := q.Put(context.Background(), "key-6", ports.QuotaPutRequest{
		TenantID: testTenantID,
		Total: map[ports.ResourceType]int64{
			ports.QuotaGPUCount:  4,
			ports.QuotaMemoryGB:  32,
			ports.QuotaStorageGB: 64,
		},
	})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if len(view.Total) != 3 {
		t.Fatalf("Put() Total 维度数 = %d, want 3", len(view.Total))
	}
	for rt, want := range map[ports.ResourceType]int64{
		ports.QuotaGPUCount: 4, ports.QuotaMemoryGB: 32, ports.QuotaStorageGB: 64,
	} {
		if view.Total[rt] != want {
			t.Fatalf("Put() Total[%s] = %d, want %d", rt, view.Total[rt], want)
		}
	}
}

// TestPostgresQuotaStoreListNoFilter 验证 List 无过滤 → 按租户级分页返回，
// 每页含完整多维度 QuotaView（不拆碎租户维度）。
func TestPostgresQuotaStoreListNoFilter(t *testing.T) {
	tx := &quotaFakeTx{}
	// step1：租户列表 [t1, t2]（limit 默认 50，未超限 → no more）
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{testTenantID}},
		{values: []any{testTenantID2}},
	}})
	// step2：两租户的全部维度（含 tenant_name）
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{testTenantID, "tenant-a", string(ports.QuotaGPUCount), int64(8), int64(2), int64(1)}},
		{values: []any{testTenantID, "tenant-a", string(ports.QuotaCPUCore), int64(16), int64(0), int64(4)}},
		{values: []any{testTenantID2, "tenant-b", string(ports.QuotaGPUCount), int64(4), int64(0), int64(0)}},
	}})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	result, err := q.List(context.Background(), ports.QuotaListRequest{Limit: 50})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("List() Items = %d, want 2", len(result.Items))
	}
	first := result.Items[0]
	if first.TenantID != testTenantID {
		t.Fatalf("List() Items[0].TenantID = %q", first.TenantID)
	}
	if len(first.Total) != 2 {
		t.Fatalf("List() Items[0] 应含租户完整维度，got %d (Total=%v)", len(first.Total), first.Total)
	}
	if first.Total[ports.QuotaGPUCount] != 8 || first.Used[ports.QuotaGPUCount] != 1 {
		t.Fatalf("List() Items[0] gpu = total%v used%v", first.Total[ports.QuotaGPUCount], first.Used[ports.QuotaGPUCount])
	}
	// 无超限 → NextCursor 为空
	if result.NextCursor != "" {
		t.Fatalf("List() NextCursor = %q, want 空", result.NextCursor)
	}
}

// TestPostgresQuotaStoreListTenantFilter 验证 List tenant_id 过滤 → 直接返回指定
// 租户全部维度（不分页）。
func TestPostgresQuotaStoreListTenantFilter(t *testing.T) {
	tx := &quotaFakeTx{}
	// GetMy 回读该租户全部维度
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		reReadRow(string(ports.QuotaGPUCount), 8, 2, 1),
		reReadRow(string(ports.QuotaCPUCore), 16, 0, 4),
	}})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	result, err := q.List(context.Background(), ports.QuotaListRequest{TenantID: testTenantID})
	if err != nil {
		t.Fatalf("List(tenant) error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("List(tenant) Items = %d, want 1", len(result.Items))
	}
	if result.Items[0].TenantID != testTenantID {
		t.Fatalf("List(tenant) TenantID = %q", result.Items[0].TenantID)
	}
	if len(result.Items[0].Total) != 2 {
		t.Fatalf("List(tenant) 应返回该租户全部维度，got %v", result.Items[0].Total)
	}
	if result.Total != 1 {
		t.Fatalf("List(tenant) Total = %d, want 1", result.Total)
	}
}

// TestPostgresQuotaStoreListPaginationCursor 验证 List 分页 cursor 衔接：
// 第一页 NextCursor = 末尾 tenant_id，第二页用该 cursor 正确衔接，不漏不重。
func TestPostgresQuotaStoreListPaginationCursor(t *testing.T) {
	// 第一页：limit=1，step1 返回 [t1, t2]（多查 1 条）→ hasMore，取 [t1]
	tx1 := &quotaFakeTx{}
	tx1.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{testTenantID}},
		{values: []any{testTenantID2}},
	}})
	tx1.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{testTenantID, "tenant-a", string(ports.QuotaGPUCount), int64(8), int64(0), int64(0)}},
	}})
	r1, err := NewPostgresQuota(&quotaFakeStore{tx: tx1}).List(context.Background(), ports.QuotaListRequest{Limit: 1})
	if err != nil {
		t.Fatalf("List() 第一页 error = %v", err)
	}
	if r1.NextCursor != testTenantID {
		t.Fatalf("List() 第一页 NextCursor = %q, want %q", r1.NextCursor, testTenantID)
	}
	if len(r1.Items) != 1 || r1.Items[0].TenantID != testTenantID {
		t.Fatalf("List() 第一页 Items = %+v", r1.Items)
	}

	// 第二页：cursor=t1，step1 返回 [t2] → 无 more
	tx2 := &quotaFakeTx{}
	tx2.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{testTenantID2}},
	}})
	tx2.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{testTenantID2, "tenant-b", string(ports.QuotaGPUCount), int64(4), int64(0), int64(0)}},
	}})
	r2, err := NewPostgresQuota(&quotaFakeStore{tx: tx2}).List(context.Background(), ports.QuotaListRequest{Limit: 1, Cursor: testTenantID})
	if err != nil {
		t.Fatalf("List() 第二页 error = %v", err)
	}
	if len(r2.Items) != 1 || r2.Items[0].TenantID != testTenantID2 {
		t.Fatalf("List() 第二页 Items = %+v, want 仅 %q", r2.Items, testTenantID2)
	}
	if r2.NextCursor != "" {
		t.Fatalf("List() 第二页 NextCursor = %q, want 空", r2.NextCursor)
	}
	// 两页共 t1+t2，不漏不重
	seen := map[string]bool{r1.Items[0].TenantID: true, r2.Items[0].TenantID: true}
	if len(seen) != 2 {
		t.Fatalf("List() 跨两页租户去重后 = %d, want 2（不漏不重）", len(seen))
	}
}

// TestPostgresQuotaStoreListEmpty 验证 List 空表 → 返回空 items、空 cursor。
func TestPostgresQuotaStoreListEmpty(t *testing.T) {
	tx := &quotaFakeTx{}
	// step1：无任何租户
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{}})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	result, err := q.List(context.Background(), ports.QuotaListRequest{Limit: 50})
	if err != nil {
		t.Fatalf("List() empty error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("List() empty Items = %d, want 0", len(result.Items))
	}
	if result.NextCursor != "" {
		t.Fatalf("List() empty NextCursor = %q, want 空", result.NextCursor)
	}
}

// TestPostgresQuotaStoreListHasMore 验证 List 超过 limit 的一页 → hasMore=true，
// NextCursor 指向本页最后一个租户。
func TestPostgresQuotaStoreListHasMore(t *testing.T) {
	tx := &quotaFakeTx{}
	// limit=1，step1 LIMIT 2 返回 [t1,t2]（limit+1 多查一条）→ hasMore
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{testTenantID}},
		{values: []any{testTenantID2}},
	}})
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{testTenantID, "tenant-a", string(ports.QuotaGPUCount), int64(8), int64(0), int64(0)}},
	}})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	result, err := q.List(context.Background(), ports.QuotaListRequest{Limit: 1})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("List() Items = %d, want 1（本页）", len(result.Items))
	}
	// NextCursor 指向本页最后一个（也是唯一一个）租户
	if result.NextCursor != testTenantID {
		t.Fatalf("List() NextCursor = %q, want %q", result.NextCursor, testTenantID)
	}
}

// TestPostgresQuotaStoreGetMy 验证 GetMy 返回当前租户多维度 map。
func TestPostgresQuotaStoreGetMy(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		reReadRow(string(ports.QuotaGPUCount), 8, 2, 1),
		reReadRow(string(ports.QuotaMemoryGB), 32, 4, 0),
		reReadRow(string(ports.QuotaTokenCount), 1000000, 0, 500000),
	}})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	view, err := q.GetMy(context.Background(), testTenantID)
	if err != nil {
		t.Fatalf("GetMy() error = %v", err)
	}
	if view.TenantID != testTenantID {
		t.Fatalf("GetMy() TenantID = %q", view.TenantID)
	}
	if len(view.Total) != 3 {
		t.Fatalf("GetMy() Total 维度数 = %d, want 3", len(view.Total))
	}
	if view.Total[ports.QuotaGPUCount] != 8 || view.Used[ports.QuotaGPUCount] != 1 || view.Reserved[ports.QuotaGPUCount] != 2 {
		t.Fatalf("GetMy() gpu = total%v used%v reserved%v", view.Total[ports.QuotaGPUCount], view.Used[ports.QuotaGPUCount], view.Reserved[ports.QuotaGPUCount])
	}
	if view.Total[ports.QuotaTokenCount] != 1000000 || view.Used[ports.QuotaTokenCount] != 500000 {
		t.Fatalf("GetMy() token = total%v used%v", view.Total[ports.QuotaTokenCount], view.Used[ports.QuotaTokenCount])
	}
}

// TestPostgresQuotaStoreGetTotalForUpdateTxFound 验证 GetTotalForUpdateTx 行存在 →
// 返回 total。
func TestPostgresQuotaStoreGetTotalForUpdateTxFound(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{int64(16)}})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	total, err := q.GetTotalForUpdateTx(context.Background(), tx, testTenantID, ports.QuotaGPUCount)
	if err != nil {
		t.Fatalf("GetTotalForUpdateTx() error = %v", err)
	}
	if total != 16 {
		t.Fatalf("GetTotalForUpdateTx() total = %d, want 16", total)
	}
}

// TestPostgresQuotaStoreGetTotalForUpdateTxNotFound 验证 GetTotalForUpdateTx 行不存在
// → ErrQuotaNotFound。
func TestPostgresQuotaStoreGetTotalForUpdateTxNotFound(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	_, err := q.GetTotalForUpdateTx(context.Background(), tx, testTenantID, ports.QuotaGPUCount)
	if err != ports.ErrQuotaNotFound {
		t.Fatalf("GetTotalForUpdateTx() error = %v, want ErrQuotaNotFound", err)
	}
}
