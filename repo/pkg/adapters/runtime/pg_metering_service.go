package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

// PgMeteringService 不持有 clock：查询路径（buildUsageQuery）不用时钟，时间由前端传入；
// ReportTokenUsage 委托 local 实例，用的是 local 自身 clock。
type PgMeteringService struct {
	store ports.MetadataStore   // 租户上下文连接（受 RLS 约束）
	local *LocalMeteringService // 仅委托已有的 token 内存写入实现
}

type PgMeteringServiceOption func(*PgMeteringService)

func NewPgMeteringService(store ports.MetadataStore, options ...PgMeteringServiceOption) *PgMeteringService {
	s := &PgMeteringService{
		store: store,
		local: NewLocalMeteringService(),
	}
	for _, opt := range options {
		opt(s)
	}
	return s
}

var _ ports.MeteringService = (*PgMeteringService)(nil)

// QueryUsage 租户视角查询：WithTenantTx 设置租户上下文后 RLS 自动过滤本租户数据。
func (s *PgMeteringService) QueryUsage(ctx context.Context, request ports.MeteringUsageQueryRequest) (ports.MeteringUsageResult, error) {
	if s.store == nil {
		return ports.MeteringUsageResult{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(request.TenantID) == "" {
		return ports.MeteringUsageResult{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}

	var items []ports.MeteringUsageRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		query, args := buildUsageQuery(request, false)
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("query metering usage: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanUsageRow(rows, request.TenantID)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return ports.MeteringUsageResult{}, err
	}
	return ports.MeteringUsageResult{Items: items, DevProfile: pgMeteringDevProfile()}, nil
}

// QueryPlatformUsage 平台视角跨租户查询：SET LOCAL ROLE ani_metering_writer（BYPASSRLS）
// 绕过 RLS 读取全部租户数据；事务级角色切换，commit/rollback 自动重置。
func (s *PgMeteringService) QueryPlatformUsage(ctx context.Context, request ports.MeteringUsageQueryRequest) (ports.MeteringUsageResult, error) {
	if s.store == nil {
		return ports.MeteringUsageResult{}, ports.ErrNotConfigured
	}

	var items []ports.MeteringUsageRecord
	err := s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 使用 SET LOCAL ROLE（事务级）切换到 ani_metering_writer（BYPASSRLS）绕过 RLS 跨租户读。
		// ani_metering_writer 已有 GRANT SELECT（20260731_001 migration）。
		// SET LOCAL ROLE 在 commit/rollback 时自动重置，无需显式 RESET ROLE，
		// 即使中途出错回滚，角色也不会泄漏到连接池。
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE ani_metering_writer"); err != nil {
			return fmt.Errorf("set local role ani_metering_writer: %w", err)
		}
		query, args := buildUsageQuery(request, true)
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("query platform metering usage: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanUsageRowPlatform(rows)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return ports.MeteringUsageResult{}, err
	}
	return ports.MeteringUsageResult{Items: items, DevProfile: pgMeteringDevProfile()}, nil
}

// ReportTokenUsage token 内存写入为已有实现，本批次不改变，委托 local adapter。
func (s *PgMeteringService) ReportTokenUsage(ctx context.Context, request ports.TokenUsageReportRequest) (ports.TokenUsageReportRecord, error) {
	return s.local.ReportTokenUsage(ctx, request)
}

// buildUsageQuery 构建聚合查询 SQL 和参数。
// SQL 始终输出固定列，确保 scan 列数与 SQL 列数一致：
//   租户视角: resource_type, total_quantity, unit, period（4 列）
//   平台视角: tenant_id, resource_type, total_quantity, unit, period（5 列）
// period 列在无时间聚合（group_by 为空/resource_type/tenant_id）时输出 NULL::text 占位。
//
// isPlatform=true 时，SQL 包含 tenant_id 输出列（平台视角），WHERE 不依赖 RLS。
// isPlatform=false 时，SQL 不含 tenant_id 输出列，WHERE 依赖 RLS 自动过滤（租户视角）。
func buildUsageQuery(request ports.MeteringUsageQueryRequest, isPlatform bool) (string, []any) {
	// period 表达式：有时间聚合时取子串，无聚合时输出 NULL
	var periodExpr, periodGroupExpr string
	switch request.GroupBy {
	case "day":
		// period 格式 "2026-08-18T10:05" → 取前 10 字符 "2026-08-18"
		periodExpr = "SUBSTR(period, 1, 10)"
		periodGroupExpr = "SUBSTR(period, 1, 10)"
	case "hour":
		// 取前 13 字符 "2026-08-18T10"
		periodExpr = "SUBSTR(period, 1, 13)"
		periodGroupExpr = "SUBSTR(period, 1, 13)"
	default:
		// 空 / resource_type / tenant_id：不按时间聚合，period 输出 NULL，不参与 GROUP BY。
		// 注意：平台视角下 tenant_id 始终参与 GROUP BY（见下方 groupByParts 构造），
		// 因此 group_by=tenant_id 与 group_by="" 产出的 SQL 完全相同，前端可传可不传。
		periodExpr = "NULL::text"
		periodGroupExpr = ""
	}

	// WHERE 条件：period 字符串比较 + resource_type 筛选
	var whereParts []string
	args := []any{}
	argIdx := 1

	// period 字符串比较：把前端 RFC3339 时间用 to_char 转成和 period 相同格式（分钟对齐）。
	// 时区契约：timestamptz 的 to_char 默认按会话 TimeZone 渲染，Go 侧传 .UTC() 不改变该行为。
	// 必须显式 `AT TIME ZONE 'UTC'` 把 timestamptz 转为 timestamp（无时区类型），
	// to_char 才按字面值渲染，与会话 TimeZone 无关；配合写入侧 .UTC() 形成完整 UTC 契约。
	// 格式串中的字面量 "T" 必须用双引号转义：否则 DDTHH24 中的 TH 会被 to_char
	// 解析为 DD 的序数后缀（TH），导致 HH24 失效输出字面文本（实测 bug）。
	if !request.StartTime.IsZero() {
		whereParts = append(whereParts, fmt.Sprintf("period >= to_char($%d::timestamptz AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI')", argIdx))
		args = append(args, request.StartTime.UTC())
		argIdx++
	}
	if !request.EndTime.IsZero() {
		whereParts = append(whereParts, fmt.Sprintf("period <= to_char($%d::timestamptz AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI')", argIdx))
		args = append(args, request.EndTime.UTC())
		argIdx++
	}

	// resource_type 筛选：空表示返回全部
	if request.ResourceType != "" {
		whereParts = append(whereParts, fmt.Sprintf("resource_type = $%d", argIdx))
		args = append(args, string(request.ResourceType))
		argIdx++
	}

	// 平台视角：可选筛选单租户
	if isPlatform && request.PlatformTenantID != "" {
		whereParts = append(whereParts, fmt.Sprintf("tenant_id = $%d::uuid", argIdx))
		args = append(args, request.PlatformTenantID)
		argIdx++
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "WHERE " + strings.Join(whereParts, " AND ")
	}

	// SELECT 列：平台视角额外输出 tenant_id
	tenantSelect := ""
	if isPlatform {
		tenantSelect = "tenant_id::text AS tenant_id, "
	}

	// GROUP BY / ORDER BY 子句：始终包含 resource_type, unit；有时间聚合时加 periodExpr
	groupByParts := []string{"resource_type", "unit"}
	orderByParts := []string{"resource_type", "unit"}
	if isPlatform {
		groupByParts = append([]string{"tenant_id"}, groupByParts...)
		orderByParts = append([]string{"tenant_id"}, orderByParts...)
	}
	if periodGroupExpr != "" {
		groupByParts = append(groupByParts, periodGroupExpr)
		orderByParts = append(orderByParts, periodGroupExpr)
	}

	query := fmt.Sprintf(`
        SELECT
            %s
            resource_type,
            SUM(quantity) AS total_quantity,
            unit,
            %s AS period
        FROM metering_usage_records
        %s
        GROUP BY %s
        ORDER BY %s
    `,
		tenantSelect,
		periodExpr,
		whereClause,
		strings.Join(groupByParts, ", "),
		strings.Join(orderByParts, ", "),
	)

	return query, args
}

// scanUsageRow 租户视角行映射（tenant_id 从 request 取）。
// 列顺序固定：resource_type, total_quantity, unit, period。
func scanUsageRow(rows ports.Rows, tenantID string) (ports.MeteringUsageRecord, error) {
	var (
		rtType string
		total  float64
		unit   string
		period *string
	)
	if err := rows.Scan(&rtType, &total, &unit, &period); err != nil {
		return ports.MeteringUsageRecord{}, fmt.Errorf("scan metering usage row: %w", err)
	}
	item := ports.MeteringUsageRecord{
		TenantID:      tenantID,
		ResourceType:  ports.MeteringResourceType(rtType),
		TotalQuantity: total,
		Unit:          unit,
	}
	if period != nil {
		item.Period = *period
	}
	return item, nil
}

// scanUsageRowPlatform 平台视角行映射（tenant_id 从 SQL 结果取）。
// 列顺序固定：tenant_id, resource_type, total_quantity, unit, period。
// ResourceRef 在聚合查询中不返回（按 resource_type 分组，不按 resource_ref 分组），
// 由 ports.MeteringUsageRecord 定义但扫描时不填，调用方不应依赖该字段。
func scanUsageRowPlatform(rows ports.Rows) (ports.MeteringUsageRecord, error) {
	var (
		tenantID string
		rtType   string
		total    float64
		unit     string
		period   *string
	)
	if err := rows.Scan(&tenantID, &rtType, &total, &unit, &period); err != nil {
		return ports.MeteringUsageRecord{}, fmt.Errorf("scan platform metering usage row: %w", err)
	}
	item := ports.MeteringUsageRecord{
		TenantID:      tenantID,
		ResourceType:  ports.MeteringResourceType(rtType),
		TotalQuantity: total,
		Unit:          unit,
	}
	if period != nil {
		item.Period = *period
	}
	return item, nil
}

func pgMeteringDevProfile() ports.DevProfileInfo {
	return ports.DevProfileInfo{
		Mode:         "postgres",
		Provider:     "pg-metering-service",
		RealProvider: true,
		Reason:       "postgres-backed metering usage query from metering_usage_records",
	}
}
