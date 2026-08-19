package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kubercloud/ani/pkg/ports"
)

// quotaFakeRow 模拟单行查询结果。err 非空时优先返回，用于模拟
// pgx.ErrNoRows（幂等跳过）等错误路径。
type quotaFakeRow struct {
	values []any
	err    error
}

func (r quotaFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, target := range dest {
		switch ptr := target.(type) {
		case *string:
			*ptr = r.values[i].(string)
		case *bool:
			*ptr = r.values[i].(bool)
		case *int64:
			*ptr = r.values[i].(int64)
		case *time.Time:
			*ptr = r.values[i].(time.Time)
		case *[]byte:
			*ptr = r.values[i].([]byte)
		default:
			return ports.ErrUnsupported
		}
	}
	return nil
}

// quotaFakeTx 模拟 MetadataTx。queryRows 是 QueryRow 返回值队列（每调用消费一个），
// queryResults 是 Query 返回值队列（每调用消费一个）；execFn 决定 Exec 的
// RowsAffected（nil 默认 1），execErr 可注入 Exec 的错误（如 CHECK 约束违反），
// execSQLs 记录 Exec 调用供断言。
type quotaFakeTx struct {
	queryRows    []quotaFakeRow
	queryResults []*quotaFakeRows
	execFn       func(sql string, args []any) int64
	execErr      func(sql string, args []any) error
	execSQLs     []string
}

func (tx *quotaFakeTx) enqueueRows(rows ...quotaFakeRow) {
	tx.queryRows = append(tx.queryRows, rows...)
}

func (tx *quotaFakeTx) enqueueQuery(rows *quotaFakeRows) {
	tx.queryResults = append(tx.queryResults, rows)
}

func (tx *quotaFakeTx) Exec(_ context.Context, sql string, args ...any) (ports.CommandTag, error) {
	tx.execSQLs = append(tx.execSQLs, sql)
	if tx.execErr != nil {
		if err := tx.execErr(sql, args); err != nil {
			return ports.CommandTag{}, err
		}
	}
	ra := int64(1)
	if tx.execFn != nil {
		ra = tx.execFn(sql, args)
	}
	return ports.CommandTag{RowsAffected: ra}, nil
}

func (tx *quotaFakeTx) Query(context.Context, string, ...any) (ports.Rows, error) {
	if len(tx.queryResults) == 0 {
		return &quotaFakeRows{}, nil
	}
	r := tx.queryResults[0]
	tx.queryResults = tx.queryResults[1:]
	return r, nil
}

func (tx *quotaFakeTx) QueryRow(_ context.Context, _ string, _ ...any) ports.Row {
	if len(tx.queryRows) == 0 {
		return quotaFakeRow{err: ports.ErrUnsupported}
	}
	r := tx.queryRows[0]
	tx.queryRows = tx.queryRows[1:]
	return r
}

// quotaFakeRows 模拟 ports.Rows（List/GetMy/Put 的多行回读）。rows 依次被
// Next/Scan 消费；cursor 越界时 Next 返回 false，Scan 返回 ErrUnsupported。
type quotaFakeRows struct {
	rows   []quotaFakeRow
	err    error
	cursor int
}

func (r *quotaFakeRows) Close() {}

func (r *quotaFakeRows) Err() error { return r.err }

func (r *quotaFakeRows) Next() bool {
	return r.cursor < len(r.rows)
}

func (r *quotaFakeRows) Scan(dest ...any) error {
	if r.cursor >= len(r.rows) {
		return ports.ErrUnsupported
	}
	row := r.rows[r.cursor]
	r.cursor++
	return row.Scan(dest...)
}

// quotaFakeStore 模拟 MetadataStore。WithTenantTx 在 fn 返回 error 时置位
// tenantRolledBack，模拟真实 PG 事务回滚，用于验证 TryMany 原子性：
// 任一维度失败 → 整个事务回滚（无悬挂预占）。
type quotaFakeStore struct {
	tx                 *quotaFakeTx
	tenantRolledBack   bool
	platformRolledBack bool
	platformErr        error
}

func (s *quotaFakeStore) Ping(context.Context) error {
	return nil
}

func (s *quotaFakeStore) WithTenantTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	err := fn(ctx, s.tx)
	if err != nil {
		s.tenantRolledBack = true
	}
	return err
}

func (s *quotaFakeStore) WithPlatformTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	err := fn(ctx, s.tx)
	if err != nil {
		s.platformRolledBack = true
		return err
	}
	return s.platformErr
}

const testTenantID = "5dbb1d01-0000-4000-8000-000000000001"

// existingMeta 返回一个 enabled 且 default_quota=100 的 meta 行，供单维度 pre-check 使用。
func existingMeta() quotaFakeRow {
	return quotaFakeRow{values: []any{true, int64(100)}}
}

// TestPostgresQuotaTrySuccess 验证 Try 成功路径：meta enabled + lazy init +
// 预占成功，返回带 tx_id 和 expires_at 的预占流水。
func TestPostgresQuotaTrySuccess(t *testing.T) {
	tx := &quotaFakeTx{}
	expires := time.Unix(200, 0)
	tx.enqueueRows(existingMeta(), quotaFakeRow{values: []any{"tx-0001", expires}})
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	res, err := q.Try(context.Background(), ports.QuotaTryRequest{
		TenantID:     testTenantID,
		ResourceType: ports.QuotaGPUCount,
		Amount:       4,
	})
	if err != nil {
		t.Fatalf("Try() error = %v", err)
	}
	if res.TxID != "tx-0001" {
		t.Fatalf("Try() TxID = %q, want tx-0001", res.TxID)
	}
	if !res.ExpiresAt.Equal(expires) {
		t.Fatalf("Try() ExpiresAt = %v, want %v", res.ExpiresAt, expires)
	}
	// 预占 UPDATE 必须执行（lazy-init INSERT + 减扣 UPDATE 至少各一次）
	if !hasExec(tx, "INSERT INTO resource_quota") {
		t.Fatalf("Try() 未执行 lazy-init INSERT")
	}
	if !hasExec(tx, "UPDATE resource_quota") {
		t.Fatalf("Try() 未执行预占 UPDATE")
	}
}

// TestPostgresQuotaTryDisabledMeta 验证 meta disabled → ErrQuotaResourceNotRegistered。
func TestPostgresQuotaTryDisabledMeta(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{false, int64(100)}}) // enabled=false
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	_, err := q.Try(context.Background(), ports.QuotaTryRequest{
		TenantID:     testTenantID,
		ResourceType: ports.QuotaGPUCount,
		Amount:       4,
	})
	if err != ports.ErrQuotaResourceNotRegistered {
		t.Fatalf("Try() error = %v, want ErrQuotaResourceNotRegistered", err)
	}
}

// TestPostgresQuotaTryExceeded 验证余量不足 → pre-check UPDATE RowsAffected=0 →
// ErrQuotaExceeded。
func TestPostgresQuotaTryExceeded(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(existingMeta())
	// 预占/lazy-init 的 UPDATE 返回 0 → 余量不足
	tx.execFn = func(_ string, _ []any) int64 { return 0 }
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	_, err := q.Try(context.Background(), ports.QuotaTryRequest{
		TenantID:     testTenantID,
		ResourceType: ports.QuotaGPUCount,
		Amount:       500,
	})
	if err != ports.ErrQuotaExceeded {
		t.Fatalf("Try() error = %v, want ErrQuotaExceeded", err)
	}
}

// TestPostgresQuotaTryManySuccess 验证多维度批量预占全部成功。
func TestPostgresQuotaTryManySuccess(t *testing.T) {
	tx := &quotaFakeTx{}
	expires := time.Unix(300, 0)
	// 两个维度，每个维度两个 QueryRow（meta + insert returning）
	tx.enqueueRows(
		existingMeta(), quotaFakeRow{values: []any{"tx-0001", expires}},
		existingMeta(), quotaFakeRow{values: []any{"tx-0002", expires}},
	)
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	res, err := q.TryMany(context.Background(), []ports.QuotaTryRequest{
		{TenantID: testTenantID, ResourceType: ports.QuotaGPUCount, Amount: 4},
		{TenantID: testTenantID, ResourceType: ports.QuotaCPUCore, Amount: 8},
	})
	if err != nil {
		t.Fatalf("TryMany() error = %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("TryMany() len = %d, want 2", len(res))
	}
	if res[0].TxID != "tx-0001" || res[1].TxID != "tx-0002" {
		t.Fatalf("TryMany() TxIDs = %v, %v; want tx-0001, tx-0002", res[0].TxID, res[1].TxID)
	}
	if store.tenantRolledBack {
		t.Fatalf("TryMany() 全成功不应回滚")
	}
}

// TestPostgresQuotaTryManyAtomicRollback 验证 TryMany 原子性：第二维度余量不足 →
// 返回 ErrQuotaExceeded，且之前成功的维度随事务回滚（fake 记录 rollback 调用）。
func TestPostgresQuotaTryManyAtomicRollback(t *testing.T) {
	tx := &quotaFakeTx{}
	expires := time.Unix(300, 0)
	// 维度一：meta + insert returning（成功）
	tx.enqueueRows(existingMeta(), quotaFakeRow{values: []any{"tx-0001", expires}})
	// 维度二：meta 成功后，预占 UPDATE 返回 0 → 余量不足
	tx.enqueueRows(existingMeta())
	updateCount := 0
	tx.execFn = func(sql string, _ []any) int64 {
		// 按预占 UPDATE 出现次数区分维度：第一个 UPDATE（维度一）成功，
		// 第二个 UPDATE（维度二）余量不足 → 验证第一维度成功后随事务回滚。
		if strings.Contains(sql, "UPDATE resource_quota") {
			updateCount++
			if updateCount > 1 {
				return 0
			}
		}
		return 1
	}
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	_, err := q.TryMany(context.Background(), []ports.QuotaTryRequest{
		{TenantID: testTenantID, ResourceType: ports.QuotaGPUCount, Amount: 4},
		{TenantID: testTenantID, ResourceType: ports.QuotaCPUCore, Amount: 500},
	})
	if err != ports.ErrQuotaExceeded {
		t.Fatalf("TryMany() error = %v, want ErrQuotaExceeded", err)
	}
	if !store.tenantRolledBack {
		t.Fatalf("TryMany() 维度二失败后未触发事务回滚，存在悬挂预占风险")
	}
}

// TestPostgresQuotaConfirmIdempotent 验证 Confirm 幂等：
// 第一次 reserved→confirmed（reserved 减、used 增），第二次 state 非 reserved
// （返回 ErrNoRows）→ 跳过、不重复扣账、不报错。
func TestPostgresQuotaConfirmIdempotent(t *testing.T) {
	tx := &quotaFakeTx{}
	// 第一次 Confirm：reserved → confirmed
	tx.enqueueRows(quotaFakeRow{values: []any{"confirmed"}})
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	err := q.Confirm(context.Background(), tx, []string{"tx-0001"}, "res-1")
	if err != nil {
		t.Fatalf("Confirm() 第一次 error = %v", err)
	}
	// 幂等跳过不入队 ledger UPDATE，仅记录跳过（无 reserved/used 改动 SQL）
	ledgerConfirmed := hasExec(tx, "SET reserved = reserved - r.amount")
	if !ledgerConfirmed {
		t.Fatalf("Confirm() 未执行 confirmed 转账 UPDATE")
	}
	beforeSkip := len(tx.execSQLs)

	// 第二次 Confirm：state 已非 reserved → QueryRow 返回 ErrNoRows → 存在性校验通过 → 跳过
	tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows}, quotaFakeRow{values: []any{true}})
	if err := q.Confirm(context.Background(), tx, []string{"tx-0001"}, "res-1"); err != nil {
		t.Fatalf("Confirm() 重复 error = %v, want nil (幂等跳过)", err)
	}
	if len(tx.execSQLs) != beforeSkip {
		t.Fatalf("Confirm() 重复调用不应再执行额外 UPDATE，execSQLs 从 %d 增到 %d", beforeSkip, len(tx.execSQLs))
	}
}

// TestPostgresQuotaCancelIdempotent 验证 Cancel 幂等：
// 第一次 reserved→cancelled（reserved 减），第二次 non-reserved → 跳过、不重复扣账。
func TestPostgresQuotaCancelIdempotent(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{"cancelled"}})
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	if err := q.Cancel(context.Background(), tx, []string{"tx-0001"}); err != nil {
		t.Fatalf("Cancel() 第一次 error = %v", err)
	}
	if !hasExec(tx, "SET reserved = reserved - r.amount") {
		t.Fatalf("Cancel() 未执行 reserved 释放 UPDATE")
	}
	beforeSkip := len(tx.execSQLs)

	tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows}, quotaFakeRow{values: []any{true}})
	if err := q.Cancel(context.Background(), tx, []string{"tx-0001"}); err != nil {
		t.Fatalf("Cancel() 重复 error = %v, want nil (幂等跳过)", err)
	}
	if len(tx.execSQLs) != beforeSkip {
		t.Fatalf("Cancel() 重复调用不应再执行额外 UPDATE")
	}
}

// TestPostgresQuotaReleaseIdempotent 验证 Release 幂等：
// 第一次 confirmed→released（used 减），第二次 non-confirmed → 跳过、不重复扣账。
func TestPostgresQuotaReleaseIdempotent(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{"released"}})
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	if err := q.Release(context.Background(), tx, []string{"tx-0001"}); err != nil {
		t.Fatalf("Release() 第一次 error = %v", err)
	}
	if !hasExec(tx, "SET used = used - r.amount") {
		t.Fatalf("Release() 未执行 used 释放 UPDATE")
	}
	beforeSkip := len(tx.execSQLs)

	tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows}, quotaFakeRow{values: []any{true}})
	if err := q.Release(context.Background(), tx, []string{"tx-0001"}); err != nil {
		t.Fatalf("Release() 重复 error = %v, want nil (幂等跳过)", err)
	}
	if len(tx.execSQLs) != beforeSkip {
		t.Fatalf("Release() 重复调用不应再执行额外 UPDATE")
	}
}

// TestPostgresQuotaConfirmLedger 验证 Confirm 成功后账本变化：
// 流水 reserved→confirmed，同时 reserved 减、used 增。
func TestPostgresQuotaConfirmLedger(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{"confirmed"}})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	if err := q.Confirm(context.Background(), tx, []string{"tx-0001"}, "res-1"); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !hasExec(tx, "UPDATE resource_quota") {
		t.Fatalf("Confirm() 未执行账本 UPDATE")
	}
	if !strings.Contains(joinExecs(tx), "reserved = reserved - r.amount") {
		t.Fatalf("Confirm() 应减少 reserved")
	}
	if !strings.Contains(joinExecs(tx), "used = used + r.amount") {
		t.Fatalf("Confirm() 应增加 used")
	}
}

// TestPostgresQuotaCancelLedger 验证 Cancel 成功后 reserved 减少、used 不变。
func TestPostgresQuotaCancelLedger(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{"cancelled"}})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	if err := q.Cancel(context.Background(), tx, []string{"tx-0001"}); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if !strings.Contains(joinExecs(tx), "reserved = reserved - r.amount") {
		t.Fatalf("Cancel() 应减少 reserved")
	}
	if strings.Contains(joinExecs(tx), "used = used + r.amount") || strings.Contains(joinExecs(tx), "used = used - r.amount") {
		t.Fatalf("Cancel() 不应改动 used")
	}
}

// TestPostgresQuotaReleaseLedger 验证 Release 成功后 used 减少。
func TestPostgresQuotaReleaseLedger(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{"released"}})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	if err := q.Release(context.Background(), tx, []string{"tx-0001"}); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if !strings.Contains(joinExecs(tx), "used = used - r.amount") {
		t.Fatalf("Release() 应减少 used")
	}
	if strings.Contains(joinExecs(tx), "reserved = reserved") {
		t.Fatalf("Release() 不应改动 reserved")
	}
}

// TestPostgresQuotaReleaseSkipsNonConfirmed 验证 Release 对非 confirmed 流水
// （reserved/cancelled）→ ErrNoRows → 跳过，不改账本、不报错。
func TestPostgresQuotaReleaseSkipsNonConfirmed(t *testing.T) {
	tx := &quotaFakeTx{}
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	// 流水当前是 reserved（非 confirmed）→ 状态守卫返回 ErrNoRows → 存在性校验通过 → 跳过
	tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows}, quotaFakeRow{values: []any{true}})
	if err := q.Release(context.Background(), tx, []string{"tx-0001"}); err != nil {
		t.Fatalf("Release() 对非 confirmed 流水 error = %v, want nil (跳过)", err)
	}
	if len(tx.execSQLs) != 0 {
		t.Fatalf("Release() 对非 confirmed 流水不应执行 UPDATE，execSQLs = %d", len(tx.execSQLs))
	}
}

// hasExec 判断某个 exec SQL 前缀是否已执行。
func hasExec(tx *quotaFakeTx, substr string) bool {
	for _, sql := range tx.execSQLs {
		if strings.Contains(sql, substr) {
			return true
		}
	}
	return false
}

// joinExecs 拼接所有 exec SQL 便于子串断言。
func joinExecs(tx *quotaFakeTx) string {
	return strings.Join(tx.execSQLs, "\n")
}

// TestPostgresQuotaReservationNotFound 验证无效 tx_id（流水不存在）时
// Confirm/Cancel/Release 都返回 ErrReservationNotFound，而不是静默跳过。
func TestPostgresQuotaReservationNotFound(t *testing.T) {
	cases := []struct {
		name string
		call func(q *PostgresQuota, tx *quotaFakeTx) error
	}{
		{"Confirm", func(q *PostgresQuota, tx *quotaFakeTx) error {
			return q.Confirm(context.Background(), tx, []string{"no-such-tx"}, "res-1")
		}},
		{"Cancel", func(q *PostgresQuota, tx *quotaFakeTx) error {
			return q.Cancel(context.Background(), tx, []string{"no-such-tx"})
		}},
		{"Release", func(q *PostgresQuota, tx *quotaFakeTx) error {
			return q.Release(context.Background(), tx, []string{"no-such-tx"})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tx := &quotaFakeTx{}
			q := NewPostgresQuota(&quotaFakeStore{tx: tx})
			// 状态守卫返回 ErrNoRows；存在性校验返回 false（流水不存在）
			tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows}, quotaFakeRow{values: []any{false}})
			err := c.call(q, tx)
			if !errors.Is(err, ports.ErrReservationNotFound) {
				t.Fatalf("%s() error = %v, want ErrReservationNotFound", c.name, err)
			}
		})
	}
}

// --- TryTx / TryManyTx 单元测试（v2） ---

// TestPostgresQuotaTryTxSuccess 验证 TryTx 成功：meta enabled + lazy init +
// 预占成功，返回带 tx_id 和 expires_at 的预占流水。TryTx 直接使用传入的 tx，
// 不经过 WithTenantTx。
func TestPostgresQuotaTryTxSuccess(t *testing.T) {
	tx := &quotaFakeTx{}
	expires := time.Unix(200, 0)
	tx.enqueueRows(existingMeta(), quotaFakeRow{values: []any{"tx-0001", expires}})
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	res, err := q.TryTx(context.Background(), tx, ports.QuotaTryRequest{
		TenantID:     testTenantID,
		ResourceType: ports.QuotaGPUCount,
		Amount:       4,
	})
	if err != nil {
		t.Fatalf("TryTx() error = %v", err)
	}
	if res.TxID != "tx-0001" {
		t.Fatalf("TryTx() TxID = %q, want tx-0001", res.TxID)
	}
	if !res.ExpiresAt.Equal(expires) {
		t.Fatalf("TryTx() ExpiresAt = %v, want %v", res.ExpiresAt, expires)
	}
	if !hasExec(tx, "INSERT INTO resource_quota") {
		t.Fatalf("TryTx() 未执行 lazy-init INSERT")
	}
	if !hasExec(tx, "UPDATE resource_quota") {
		t.Fatalf("TryTx() 未执行预占 UPDATE")
	}
	// TryTx 不应触发 WithTenantTx（store.tenantRolledBack 保持 false）
	if store.tenantRolledBack {
		t.Fatalf("TryTx() 不应调用 WithTenantTx")
	}
}

// TestPostgresQuotaTryTxDisabledMeta 验证 TryTx 在 meta disabled 时返回
// ErrQuotaResourceNotRegistered。
func TestPostgresQuotaTryTxDisabledMeta(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{false, int64(100)}}) // enabled=false
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	_, err := q.TryTx(context.Background(), tx, ports.QuotaTryRequest{
		TenantID:     testTenantID,
		ResourceType: ports.QuotaGPUCount,
		Amount:       4,
	})
	if err != ports.ErrQuotaResourceNotRegistered {
		t.Fatalf("TryTx() error = %v, want ErrQuotaResourceNotRegistered", err)
	}
}

// TestPostgresQuotaTryTxMetaNotFound 验证 TryTx 在 meta 不存在时返回
// ErrQuotaResourceNotRegistered。
func TestPostgresQuotaTryTxMetaNotFound(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows})
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	_, err := q.TryTx(context.Background(), tx, ports.QuotaTryRequest{
		TenantID:     testTenantID,
		ResourceType: ports.QuotaGPUCount,
		Amount:       4,
	})
	if err != ports.ErrQuotaResourceNotRegistered {
		t.Fatalf("TryTx() error = %v, want ErrQuotaResourceNotRegistered", err)
	}
}

// TestPostgresQuotaTryTxExceeded 验证 TryTx 余量不足 → ErrQuotaExceeded。
func TestPostgresQuotaTryTxExceeded(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(existingMeta())
	tx.execFn = func(_ string, _ []any) int64 { return 0 } // UPDATE RowsAffected=0
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	_, err := q.TryTx(context.Background(), tx, ports.QuotaTryRequest{
		TenantID:     testTenantID,
		ResourceType: ports.QuotaGPUCount,
		Amount:       500,
	})
	if err != ports.ErrQuotaExceeded {
		t.Fatalf("TryTx() error = %v, want ErrQuotaExceeded", err)
	}
}

// TestPostgresQuotaTryTxInvalidAmount 验证 TryTx amount<=0 → ErrInvalid。
func TestPostgresQuotaTryTxInvalidAmount(t *testing.T) {
	tx := &quotaFakeTx{}
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	_, err := q.TryTx(context.Background(), tx, ports.QuotaTryRequest{
		TenantID:     testTenantID,
		ResourceType: ports.QuotaGPUCount,
		Amount:       0,
	})
	if err != ports.ErrInvalid {
		t.Fatalf("TryTx() error = %v, want ErrInvalid", err)
	}
}

// TestPostgresQuotaTryTxNoRollback 验证 TryTx 失败时不自己回滚：
// 返回 err 但不触发 WithTenantTx 回滚（回滚由调用方的外层事务负责）。
func TestPostgresQuotaTryTxNoRollback(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(existingMeta())
	tx.execFn = func(_ string, _ []any) int64 { return 0 } // 余量不足
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	_, err := q.TryTx(context.Background(), tx, ports.QuotaTryRequest{
		TenantID:     testTenantID,
		ResourceType: ports.QuotaGPUCount,
		Amount:       500,
	})
	if err == nil {
		t.Fatalf("TryTx() 应返回错误")
	}
	if store.tenantRolledBack {
		t.Fatalf("TryTx() 不应自己回滚，回滚由外层事务负责")
	}
}

// TestPostgresQuotaTryManyTxSuccess 验证 TryManyTx 多维度全占成功。
// TryManyTx 直接使用传入的 tx，不经过 WithTenantTx。
func TestPostgresQuotaTryManyTxSuccess(t *testing.T) {
	tx := &quotaFakeTx{}
	expires := time.Unix(300, 0)
	tx.enqueueRows(
		existingMeta(), quotaFakeRow{values: []any{"tx-0001", expires}},
		existingMeta(), quotaFakeRow{values: []any{"tx-0002", expires}},
	)
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	res, err := q.TryManyTx(context.Background(), tx, []ports.QuotaTryRequest{
		{TenantID: testTenantID, ResourceType: ports.QuotaGPUCount, Amount: 4},
		{TenantID: testTenantID, ResourceType: ports.QuotaCPUCore, Amount: 8},
	})
	if err != nil {
		t.Fatalf("TryManyTx() error = %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("TryManyTx() len = %d, want 2", len(res))
	}
	if res[0].TxID != "tx-0001" || res[1].TxID != "tx-0002" {
		t.Fatalf("TryManyTx() TxIDs = %v, %v; want tx-0001, tx-0002", res[0].TxID, res[1].TxID)
	}
	if store.tenantRolledBack {
		t.Fatalf("TryManyTx() 不应调用 WithTenantTx")
	}
}

// TestPostgresQuotaTryManyTxAtomicFailure 验证 TryManyTx 任一维度失败 →
// 返回 err + nil reservations（不自己回滚，由调用方的外层事务统一回滚）。
func TestPostgresQuotaTryManyTxAtomicFailure(t *testing.T) {
	tx := &quotaFakeTx{}
	expires := time.Unix(300, 0)
	// 维度一成功
	tx.enqueueRows(existingMeta(), quotaFakeRow{values: []any{"tx-0001", expires}})
	// 维度二 meta 成功后预占 UPDATE 返回 0 → 余量不足
	tx.enqueueRows(existingMeta())
	updateCount := 0
	tx.execFn = func(sql string, _ []any) int64 {
		if strings.Contains(sql, "UPDATE resource_quota") {
			updateCount++
			if updateCount > 1 {
				return 0 // 维度二余量不足
			}
		}
		return 1
	}
	store := &quotaFakeStore{tx: tx}
	q := NewPostgresQuota(store)

	res, err := q.TryManyTx(context.Background(), tx, []ports.QuotaTryRequest{
		{TenantID: testTenantID, ResourceType: ports.QuotaGPUCount, Amount: 4},
		{TenantID: testTenantID, ResourceType: ports.QuotaCPUCore, Amount: 500},
	})
	if err != ports.ErrQuotaExceeded {
		t.Fatalf("TryManyTx() error = %v, want ErrQuotaExceeded", err)
	}
	if res != nil {
		t.Fatalf("TryManyTx() 失败时应返回 nil reservations，got %v", res)
	}
	// TryManyTx 不自己回滚，不触发 WithTenantTx
	if store.tenantRolledBack {
		t.Fatalf("TryManyTx() 不应自己回滚，回滚由外层事务负责")
	}
}

// TestPostgresQuotaTryManyTxEmpty 验证 TryManyTx 空入参 → nil, nil。
func TestPostgresQuotaTryManyTxEmpty(t *testing.T) {
	tx := &quotaFakeTx{}
	q := NewPostgresQuota(&quotaFakeStore{tx: tx})

	res, err := q.TryManyTx(context.Background(), tx, nil)
	if err != nil {
		t.Fatalf("TryManyTx(nil) error = %v", err)
	}
	if res != nil {
		t.Fatalf("TryManyTx(nil) 应返回 nil，got %v", res)
	}

	res, err = q.TryManyTx(context.Background(), tx, []ports.QuotaTryRequest{})
	if err != nil {
		t.Fatalf("TryManyTx([]) error = %v", err)
	}
	if res != nil {
		t.Fatalf("TryManyTx([]) 应返回 nil，got %v", res)
	}
}
