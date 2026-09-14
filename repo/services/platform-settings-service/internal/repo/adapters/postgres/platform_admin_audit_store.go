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
	"github.com/kubercloud/ani/services/platform-settings-service/internal/repo/ports"
)

// PostgresPlatformAdminAuditStore 基于 PostgreSQL 实现 ports.PlatformAdminAuditStore
// （平台运营账号域审计，复用现有 audit_logs 分区表）。
type PostgresPlatformAdminAuditStore struct {
	db *pgxpool.Pool
}

var _ ports.PlatformAdminAuditStore = (*PostgresPlatformAdminAuditStore)(nil)

// NewPostgresPlatformAdminAuditStore 构造平台运营账号域审计存储实例。
func NewPostgresPlatformAdminAuditStore(db *pgxpool.Pool) ports.PlatformAdminAuditStore {
	return &PostgresPlatformAdminAuditStore{db: db}
}

// Create 写入一条平台账号域审计日志并返回其 ID。
func (s *PostgresPlatformAdminAuditStore) Create(ctx context.Context, log ports.AuditLog) (uuid.UUID, error) {
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

	// 步骤 1c：result 缺省按 success；resource 缺省 platform_user
	result := log.Result
	if result == "" {
		result = "success"
	}
	resource := strings.TrimSpace(log.Resource)
	if resource == "" {
		resource = "platform_user"
	}

	// 步骤 2：写入分区表 audit_logs（tenant_id 平台级为 NULL）
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
		resource,
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

// ListUserAuditLogs 按目标账号查询平台运营账号操作历史（游标分页）。
func (s *PostgresPlatformAdminAuditStore) ListUserAuditLogs(ctx context.Context, userID uuid.UUID, filter ports.AuditLogFilter) (ports.AuditLogListResult, error) {
	// 步骤 1：limit 边界（default 20，max 100）
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	// 步骤 2：固定平台账号域 + target_id + tenant_id IS NULL
	where := []string{
		"tenant_id IS NULL",
		"resource = 'platform_user'",
		"details->>'target_id' = $1",
	}
	args := []any{userID.String()}
	if action := strings.TrimSpace(filter.Action); action != "" {
		args = append(args, action)
		where = append(where, fmt.Sprintf("action = $%d", len(args)))
	}
	if result := strings.TrimSpace(filter.Result); result != "" {
		args = append(args, result)
		where = append(where, fmt.Sprintf("result = $%d", len(args)))
	}
	whereSQL := strings.Join(where, " AND ")

	// 步骤 3：游标（created_at DESC, id DESC）；非法 cursor → VALIDATION_FAILED
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

	// 步骤 4：多取 1 条判断是否还有下一页
	listArgs = append(listArgs, limit+1)
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, action, resource, result, details, created_at
		FROM audit_logs
		WHERE `+listWhere+`
		ORDER BY created_at DESC, id DESC
		LIMIT $`+fmt.Sprintf("%d", len(listArgs)), listArgs...)
	if err != nil {
		return ports.AuditLogListResult{}, fmt.Errorf("list platform admin audit logs: %w", err)
	}
	defer rows.Close()

	// 步骤 5：扫描本页行
	items := make([]ports.AuditLog, 0, limit)
	for rows.Next() {
		var (
			item       ports.AuditLog
			detailsRaw []byte
		)
		if err := rows.Scan(
			&item.ID, &item.UserID, &item.Action, &item.Resource, &item.Result, &detailsRaw, &item.CreatedAt,
		); err != nil {
			return ports.AuditLogListResult{}, fmt.Errorf("scan platform admin audit log: %w", err)
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
		return ports.AuditLogListResult{}, fmt.Errorf("iterate platform admin audit logs: %w", err)
	}

	// 步骤 6：有多余一行则截断并编码 next_cursor
	nextCursor := ""
	if len(items) > limit {
		last := items[limit-1]
		nextCursor = types.EncodeCursor(last.CreatedAt, last.ID)
		items = items[:limit]
	}

	return ports.AuditLogListResult{
		Items:      items,
		NextCursor: nextCursor,
	}, nil
}
