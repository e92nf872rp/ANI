package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// meteringFakeRow 模拟单行查询结果（支持 string/float64/*string 目标）。
type meteringFakeRow struct {
	values []any
	err    error
}

func (r meteringFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, target := range dest {
		switch ptr := target.(type) {
		case *string:
			switch v := r.values[i].(type) {
			case string:
				*ptr = v
			case *string:
				if v != nil {
					*ptr = *v
				}
			default:
				return ports.ErrUnsupported
			}
		case **string:
			// period 目标为 *string（可空列），Scan 传入 **string。
			switch v := r.values[i].(type) {
			case *string:
				*ptr = v
			case string:
				*ptr = &v
			case nil:
				*ptr = nil
			default:
				return ports.ErrUnsupported
			}
		case *float64:
			*ptr = r.values[i].(float64)
		default:
			return ports.ErrUnsupported
		}
	}
	return nil
}

// meteringFakeRows 模拟 ports.Rows 多行结果。
type meteringFakeRows struct {
	rows   []meteringFakeRow
	cursor int
	err    error
}

func (r *meteringFakeRows) Close() {}

func (r *meteringFakeRows) Err() error { return r.err }

func (r *meteringFakeRows) Next() bool { return r.cursor < len(r.rows) }

func (r *meteringFakeRows) Scan(dest ...any) error {
	if r.cursor >= len(r.rows) {
		return ports.ErrUnsupported
	}
	row := r.rows[r.cursor]
	r.cursor++
	return row.Scan(dest...)
}

// meteringFakeTx 模拟 MetadataTx，记录 Query/Exec 的 SQL、参数和调用顺序
// （events 用于断言 SET LOCAL ROLE 先于查询执行）。
type meteringFakeTx struct {
	queryResults []*meteringFakeRows
	querySQLs    []string
	queryArgs    [][]any
	execSQLs     []string
	events       []string
}

func (tx *meteringFakeTx) Exec(_ context.Context, sql string, _ ...any) (ports.CommandTag, error) {
	tx.execSQLs = append(tx.execSQLs, sql)
	tx.events = append(tx.events, "exec:"+sql)
	return ports.CommandTag{RowsAffected: 1}, nil
}

func (tx *meteringFakeTx) Query(_ context.Context, sql string, args ...any) (ports.Rows, error) {
	tx.querySQLs = append(tx.querySQLs, sql)
	tx.queryArgs = append(tx.queryArgs, args)
	tx.events = append(tx.events, "query")
	if len(tx.queryResults) == 0 {
		return &meteringFakeRows{}, nil
	}
	r := tx.queryResults[0]
	tx.queryResults = tx.queryResults[1:]
	return r, nil
}

func (tx *meteringFakeTx) QueryRow(_ context.Context, _ string, _ ...any) ports.Row {
	return meteringFakeRow{err: ports.ErrUnsupported}
}

// meteringFakeStore 模拟 MetadataStore，区分 tenant/platform 事务入口
// （RLS 隔离语义由真实 PG 承载，单测断言走对的事务入口）。
type meteringFakeStore struct {
	tx             *meteringFakeTx
	tenantTxUsed   bool
	platformTxUsed bool
}

func (s *meteringFakeStore) Ping(context.Context) error { return nil }

func (s *meteringFakeStore) WithTenantTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	s.tenantTxUsed = true
	return fn(ctx, s.tx)
}

func (s *meteringFakeStore) WithPlatformTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	s.platformTxUsed = true
	return fn(ctx, s.tx)
}

func newMeteringFakeStore(rows *meteringFakeRows) *meteringFakeStore {
	tx := &meteringFakeTx{}
	if rows != nil {
		tx.queryResults = append(tx.queryResults, rows)
	}
	return &meteringFakeStore{tx: tx}
}

func TestPgMeteringServiceQueryUsageGroupByDay(t *testing.T) {
	store := newMeteringFakeStore(&meteringFakeRows{rows: []meteringFakeRow{
		{values: []any{"instance_gpu_seconds", 120.0, "gpu_second", strPtr("2026-08-25")}},
	}})
	svc := NewPgMeteringService(store)

	result, err := svc.QueryUsage(context.Background(), ports.MeteringUsageQueryRequest{
		TenantID:  "11111111-1111-1111-1111-111111111111",
		GroupBy:   "day",
		StartTime: time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("QueryUsage error = %v", err)
	}
	sql := store.tx.querySQLs[0]
	if !strings.Contains(sql, "SUBSTR(period, 1, 10)") {
		t.Fatalf("SQL missing day aggregation expr: %s", sql)
	}
	if !strings.Contains(sql, "GROUP BY resource_type, unit, SUBSTR(period, 1, 10)") {
		t.Fatalf("SQL missing GROUP BY day: %s", sql)
	}
	if len(result.Items) != 1 || result.Items[0].Period != "2026-08-25" || result.Items[0].TotalQuantity != 120.0 {
		t.Fatalf("items = %+v", result.Items)
	}
	if !result.DevProfile.RealProvider || result.DevProfile.Provider != "pg-metering-service" {
		t.Fatalf("dev profile = %+v", result.DevProfile)
	}
}

func TestPgMeteringServiceQueryUsageGroupByHour(t *testing.T) {
	store := newMeteringFakeStore(&meteringFakeRows{rows: []meteringFakeRow{
		{values: []any{"instance_cpu_seconds", 60.0, "cpu_second", strPtr("2026-08-25T10")}},
	}})
	svc := NewPgMeteringService(store)

	_, err := svc.QueryUsage(context.Background(), ports.MeteringUsageQueryRequest{
		TenantID: "11111111-1111-1111-1111-111111111111",
		GroupBy:  "hour",
	})
	if err != nil {
		t.Fatalf("QueryUsage error = %v", err)
	}
	sql := store.tx.querySQLs[0]
	if !strings.Contains(sql, "SUBSTR(period, 1, 13)") {
		t.Fatalf("SQL missing hour aggregation expr: %s", sql)
	}
	if !strings.Contains(sql, "GROUP BY resource_type, unit, SUBSTR(period, 1, 13)") {
		t.Fatalf("SQL missing GROUP BY hour: %s", sql)
	}
}

func TestPgMeteringServiceQueryUsageResourceTypeFilter(t *testing.T) {
	store := newMeteringFakeStore(nil)
	svc := NewPgMeteringService(store)

	_, err := svc.QueryUsage(context.Background(), ports.MeteringUsageQueryRequest{
		TenantID:     "11111111-1111-1111-1111-111111111111",
		ResourceType: ports.MeteringResourceInstanceGPUSeconds,
	})
	if err != nil {
		t.Fatalf("QueryUsage error = %v", err)
	}
	sql := store.tx.querySQLs[0]
	if !strings.Contains(sql, "resource_type = $1") {
		t.Fatalf("SQL missing resource_type filter: %s", sql)
	}
	if got := store.tx.queryArgs[0][0]; got != "instance_gpu_seconds" {
		t.Fatalf("resource_type arg = %v", got)
	}
}

func TestPgMeteringServiceQueryUsageTokenResourceTypesUsePostgres(t *testing.T) {
	// token_* 尚未写入 PG，返回空 items；断言 token 查询与 instance 查询走同一路径
	// （同一 metering_usage_records 表、同一 PgMeteringService）。
	store := newMeteringFakeStore(nil)
	svc := NewPgMeteringService(store)

	result, err := svc.QueryUsage(context.Background(), ports.MeteringUsageQueryRequest{
		TenantID:     "11111111-1111-1111-1111-111111111111",
		ResourceType: ports.MeteringResourceTokenInput,
	})
	if err != nil {
		t.Fatalf("QueryUsage error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("token items = %+v, want empty", result.Items)
	}
	sql := store.tx.querySQLs[0]
	if !strings.Contains(sql, "FROM metering_usage_records") {
		t.Fatalf("token query not hitting metering_usage_records: %s", sql)
	}
}

func TestPgMeteringServiceQueryUsagePeriodStringComparison(t *testing.T) {
	store := newMeteringFakeStore(nil)
	svc := NewPgMeteringService(store)

	start := time.Date(2026, 8, 25, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
	end := time.Date(2026, 8, 25, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	_, err := svc.QueryUsage(context.Background(), ports.MeteringUsageQueryRequest{
		TenantID:  "11111111-1111-1111-1111-111111111111",
		StartTime: start,
		EndTime:   end,
	})
	if err != nil {
		t.Fatalf("QueryUsage error = %v", err)
	}
	sql := store.tx.querySQLs[0]
	if !strings.Contains(sql, `period >= to_char($1::timestamptz AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI')`) {
		t.Fatalf("SQL missing start period comparison: %s", sql)
	}
	if !strings.Contains(sql, `period <= to_char($2::timestamptz AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI')`) {
		t.Fatalf("SQL missing end period comparison: %s", sql)
	}
	// 时区契约：非 UTC 本地时间入参必须归一化为 UTC（CST 10:00 → UTC 02:00）。
	wantStart := time.Date(2026, 8, 25, 2, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	if got := store.tx.queryArgs[0][0].(time.Time); !got.Equal(wantStart) || got.Location() != time.UTC {
		t.Fatalf("start arg = %v, want %v UTC", got, wantStart)
	}
	if got := store.tx.queryArgs[0][1].(time.Time); !got.Equal(wantEnd) || got.Location() != time.UTC {
		t.Fatalf("end arg = %v, want %v UTC", got, wantEnd)
	}
}

func TestPgMeteringServiceQueryPlatformUsageAggregatesAllTenants(t *testing.T) {
	store := newMeteringFakeStore(&meteringFakeRows{rows: []meteringFakeRow{
		{values: []any{"11111111-1111-1111-1111-111111111111", "instance_gpu_seconds", 120.0, "gpu_second", nil}},
		{values: []any{"22222222-2222-2222-2222-222222222222", "instance_gpu_seconds", 60.0, "gpu_second", nil}},
	}})
	svc := NewPgMeteringService(store)

	result, err := svc.QueryPlatformUsage(context.Background(), ports.MeteringUsageQueryRequest{
		GroupBy: "tenant_id",
	})
	if err != nil {
		t.Fatalf("QueryPlatformUsage error = %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %+v, want 2 tenants", result.Items)
	}
	if result.Items[0].TenantID != "11111111-1111-1111-1111-111111111111" ||
		result.Items[1].TenantID != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("tenant ids = %+v", result.Items)
	}
	if result.Items[0].Period != "" {
		t.Fatalf("period should be empty when NULL, got %q", result.Items[0].Period)
	}
	sql := store.tx.querySQLs[0]
	if !strings.Contains(sql, "tenant_id::text AS tenant_id") {
		t.Fatalf("SQL missing tenant_id output column: %s", sql)
	}
	if !strings.Contains(sql, "GROUP BY tenant_id, resource_type, unit") {
		t.Fatalf("SQL missing platform GROUP BY: %s", sql)
	}
	// 平台视角 WHERE 不依赖 RLS，不注入 tenant_id 条件（PlatformTenantID 为空时）。
	if strings.Contains(sql, "tenant_id = $") {
		t.Fatalf("unexpected tenant_id WHERE filter: %s", sql)
	}
}

func TestPgMeteringServiceQueryPlatformUsageTenantIDFilter(t *testing.T) {
	store := newMeteringFakeStore(nil)
	svc := NewPgMeteringService(store)

	_, err := svc.QueryPlatformUsage(context.Background(), ports.MeteringUsageQueryRequest{
		PlatformTenantID: "11111111-1111-1111-1111-111111111111",
	})
	if err != nil {
		t.Fatalf("QueryPlatformUsage error = %v", err)
	}
	sql := store.tx.querySQLs[0]
	if !strings.Contains(sql, "tenant_id = $1::uuid") {
		t.Fatalf("SQL missing tenant_id filter: %s", sql)
	}
}

func TestPgMeteringServiceQueryPlatformUsageRLSBypass(t *testing.T) {
	store := newMeteringFakeStore(&meteringFakeRows{rows: []meteringFakeRow{
		{values: []any{"11111111-1111-1111-1111-111111111111", "instance_gpu_seconds", 120.0, "gpu_second", nil}},
	}})
	svc := NewPgMeteringService(store)

	_, err := svc.QueryPlatformUsage(context.Background(), ports.MeteringUsageQueryRequest{})
	if err != nil {
		t.Fatalf("QueryPlatformUsage error = %v", err)
	}
	if !store.platformTxUsed || store.tenantTxUsed {
		t.Fatalf("platform query must use WithPlatformTx only (platform=%v tenant=%v)", store.platformTxUsed, store.tenantTxUsed)
	}
	if len(store.tx.execSQLs) != 1 || store.tx.execSQLs[0] != "SET LOCAL ROLE ani_metering_writer" {
		t.Fatalf("execSQLs = %v, want single SET LOCAL ROLE", store.tx.execSQLs)
	}
	// SET LOCAL ROLE 必须先于查询执行（角色切换后才查数据）。
	if store.tx.events[0] != "exec:SET LOCAL ROLE ani_metering_writer" || store.tx.events[1] != "query" {
		t.Fatalf("events = %v, want SET LOCAL ROLE before query", store.tx.events)
	}
}

func TestPgMeteringServiceQueryUsageRLSIsolation(t *testing.T) {
	store := newMeteringFakeStore(nil)
	svc := NewPgMeteringService(store)

	_, err := svc.QueryUsage(context.Background(), ports.MeteringUsageQueryRequest{
		TenantID: "11111111-1111-1111-1111-111111111111",
	})
	if err != nil {
		t.Fatalf("QueryUsage error = %v", err)
	}
	// 租户查询走 WithTenantTx（RLS 生效），且不做任何角色切换（不绕过 RLS）。
	if !store.tenantTxUsed || store.platformTxUsed {
		t.Fatalf("tenant query must use WithTenantTx only (platform=%v tenant=%v)", store.platformTxUsed, store.tenantTxUsed)
	}
	if len(store.tx.execSQLs) != 0 {
		t.Fatalf("tenant query must not switch role, execSQLs = %v", store.tx.execSQLs)
	}
	sql := store.tx.querySQLs[0]
	if strings.Contains(sql, "tenant_id") {
		t.Fatalf("tenant query SQL must not contain tenant_id (RLS filters it): %s", sql)
	}
}

func TestPgMeteringServiceReportTokenUsageDelegatesToLocal(t *testing.T) {
	svc := NewPgMeteringService(newMeteringFakeStore(nil))

	record, err := svc.ReportTokenUsage(context.Background(), ports.TokenUsageReportRequest{
		TenantID:       "11111111-1111-1111-1111-111111111111",
		IdempotencyKey: "pg-delegate-test",
		Source:         "model-service",
		Model:          "qwen2.5",
		InputTokens:    3,
		OutputTokens:   5,
	})
	if err != nil {
		t.Fatalf("ReportTokenUsage error = %v", err)
	}
	if record.TotalTokens != 8 || record.State != ports.TokenUsageReportAccepted {
		t.Fatalf("record = %+v", record)
	}
	// 委托落盘验证：local 的 QueryUsage 能读到刚上报的 token 数据。
	result, err := svc.local.QueryUsage(context.Background(), ports.MeteringUsageQueryRequest{
		TenantID: "11111111-1111-1111-1111-111111111111",
	})
	if err != nil {
		t.Fatalf("local QueryUsage error = %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("local items = %+v, want 3 token rows", result.Items)
	}
}

func TestPgMeteringServiceNotConfiguredAndInvalidTenant(t *testing.T) {
	svc := NewPgMeteringService(nil)
	if _, err := svc.QueryUsage(context.Background(), ports.MeteringUsageQueryRequest{TenantID: "t"}); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("nil store QueryUsage err = %v, want ErrNotConfigured", err)
	}
	if _, err := svc.QueryPlatformUsage(context.Background(), ports.MeteringUsageQueryRequest{}); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("nil store QueryPlatformUsage err = %v, want ErrNotConfigured", err)
	}

	svc2 := NewPgMeteringService(newMeteringFakeStore(nil))
	if _, err := svc2.QueryUsage(context.Background(), ports.MeteringUsageQueryRequest{}); err == nil || !strings.Contains(err.Error(), "tenant_id is required") {
		t.Fatalf("empty tenant QueryUsage err = %v, want ErrInvalid", err)
	}
}


