package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/pkg/types"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// PostgresTenantPlanAuditStore 基于 PostgreSQL 实现 ports.TenantPlanAuditStore
// （配额套餐域审计，复用现有 audit_logs 分区表）。
type PostgresTenantPlanAuditStore struct {
	db *pgxpool.Pool
}

var _ ports.TenantPlanAuditStore = (*PostgresTenantPlanAuditStore)(nil)

// NewPostgresTenantPlanAuditStore 构造配额套餐域审计存储实例。
func NewPostgresTenantPlanAuditStore(db *pgxpool.Pool) ports.TenantPlanAuditStore {
	return &PostgresTenantPlanAuditStore{db: db}
}

// Create 写入一条配额套餐域审计日志并返回其 ID。
//
// 分步：
//  1. 规范化 details / request_id / result
//  2. INSERT audit_logs（连接池）并返回 id
func (s *PostgresTenantPlanAuditStore) Create(ctx context.Context, log ports.AuditLog) (uuid.UUID, error) {
	// 步骤 1a：details 默认空对象，避免 NULL JSONB
	details := log.Details
	if details == nil {
		details = map[string]any{}
	}
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal details: %w", err)
	}

	// 步骤 1b：request_id 列 NOT NULL；优先用网关透传值，缺省再生成
	requestID := strings.TrimSpace(log.RequestID)
	if requestID == "" {
		requestID = uuid.New().String()
	}

	// 步骤 1c：result 缺省按 success
	result := log.Result
	if result == "" {
		result = "success"
	}

	// 步骤 2：写入分区表 audit_logs
	var id uuid.UUID
	var createdAt time.Time
	err = s.db.QueryRow(ctx, `
		INSERT INTO audit_logs (
			tenant_id, user_id, request_id, action, resource, result, details, ip_address, user_agent
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7::jsonb,
			NULLIF($8, '')::inet,
			NULLIF($9, '')
		)
		RETURNING id, created_at
	`,
		log.TenantID,
		log.UserID,
		requestID,
		log.Action,
		log.Resource,
		result,
		detailsJSON,
		log.IPAddress,
		log.UserAgent,
	).Scan(&id, &createdAt)
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert audit_logs: %w", err)
	}
	return id, nil
}

func (s *PostgresTenantPlanAuditStore) ListPlanAuditLogs(ctx context.Context, planID uuid.UUID, filter ports.AuditLogFilter) (ports.AuditLogListResult, error) {
	// 步骤 1：limit 边界（default 20，max 100）
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	// 步骤 2：固定套餐域 + details.plan_id
	where := []string{
		"resource = 'tenant_plan'",
		"details->>'plan_id' = $1",
	}
	args := []any{planID.String()}
	whereSQL := strings.Join(where, " AND ")

	// 步骤 3：总数不含游标
	var total int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return ports.AuditLogListResult{}, fmt.Errorf("count plan audit logs: %w", err)
	}

	// 步骤 4：游标（created_at DESC, id DESC）；非法 cursor → VALIDATION_FAILED
	listArgs := append([]any{}, args...)
	listWhere := whereSQL
	if cursor := strings.TrimSpace(filter.Cursor); cursor != "" {
		createdAt, id, err := types.DecodeCursor(cursor)
		if err != nil {
			return ports.AuditLogListResult{}, fmt.Errorf("%w: invalid cursor", ports.ErrValidationFailed)
		}
		listArgs = append(listArgs, createdAt, id)
		listWhere += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", len(listArgs)-1, len(listArgs))
	}

	// 步骤 5：多取 1 条判断是否还有下一页
	listArgs = append(listArgs, limit+1)
	rows, err := s.db.Query(ctx, `
		SELECT id, action, result, details, created_at
		FROM audit_logs
		WHERE `+listWhere+`
		ORDER BY created_at DESC, id DESC
		LIMIT $`+fmt.Sprintf("%d", len(listArgs)), listArgs...)
	if err != nil {
		return ports.AuditLogListResult{}, fmt.Errorf("list plan audit logs: %w", err)
	}
	defer rows.Close()

	// 步骤 6a：扫描本页行（响应字段 id/action/result/details/created_at；id 同时用于游标）
	items := make([]ports.AuditLog, 0, limit)
	for rows.Next() {
		var (
			item       ports.AuditLog
			detailsRaw []byte
		)
		if err := rows.Scan(
			&item.ID, &item.Action, &item.Result, &detailsRaw, &item.CreatedAt,
		); err != nil {
			return ports.AuditLogListResult{}, fmt.Errorf("scan plan audit log: %w", err)
		}
		if len(detailsRaw) > 0 {
			details := map[string]any{}
			if err := json.Unmarshal(detailsRaw, &details); err != nil {
				return ports.AuditLogListResult{}, fmt.Errorf("decode audit details: %w", err)
			}
			item.Details = details
		} else {
			item.Details = map[string]any{}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ports.AuditLogListResult{}, fmt.Errorf("iterate plan audit logs: %w", err)
	}

	// 步骤 6b：有多余一行则截断并编码 next_cursor
	nextCursor := ""
	if len(items) > limit {
		last := items[limit-1]
		nextCursor = types.EncodeCursor(last.CreatedAt, last.ID)
		items = items[:limit]
	}

	return ports.AuditLogListResult{
		Items:      items,
		Total:      total,
		NextCursor: nextCursor,
	}, nil
}

func (s *PostgresTenantPlanAuditStore) ListTenantAuditLogs(context.Context, uuid.UUID, ports.TenantAuditLogFilter) (ports.AuditLogListResult, error) {
	return ports.AuditLogListResult{}, ports.ErrNotImplemented
}
