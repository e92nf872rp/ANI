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

// PostgresAuditStore 基于 PostgreSQL 实现 ports.AuditStore（复用 audit_logs 分区表）。
type PostgresAuditStore struct {
	db *pgxpool.Pool
}

var _ ports.AuditStore = (*PostgresAuditStore)(nil)

// NewPostgresAuditStore 构造审计存储实例。
func NewPostgresAuditStore(db *pgxpool.Pool) ports.AuditStore {
	return &PostgresAuditStore{db: db}
}

// Create 写入一条审计日志并返回其 ID。
func (s *PostgresAuditStore) Create(ctx context.Context, log ports.AuditLog) (uuid.UUID, error) {
	details := log.Details
	if details == nil {
		details = map[string]any{}
	}
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal details: %w", err)
	}

	requestID := strings.TrimSpace(log.RequestID)
	if requestID == "" {
		requestID = uuid.New().String()
	}

	result := log.Result
	if result == "" {
		result = "success"
	}

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

func (s *PostgresAuditStore) ListPlanAuditLogs(ctx context.Context, planID uuid.UUID, filter ports.AuditLogFilter) (ports.AuditLogListResult, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	where := []string{
		"resource = 'tenant_plan'",
		"details->>'plan_id' = $1",
	}
	args := []any{planID.String()}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return ports.AuditLogListResult{}, fmt.Errorf("count plan audit logs: %w", err)
	}

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

func (s *PostgresAuditStore) ListTenantAuditLogs(context.Context, uuid.UUID, ports.TenantAuditLogFilter) (ports.AuditLogListResult, error) {
	return ports.AuditLogListResult{}, ports.ErrNotImplemented
}

// ListTenantAdminAuditLogs 按租户 + 目标管理员查询操作历史（details.target_id），游标分页。
// resource 固定 tenant_admin。
func (s *PostgresAuditStore) ListTenantAdminAuditLogs(ctx context.Context, tenantID, userID uuid.UUID, filter ports.TenantAuditLogFilter) (ports.AuditLogListResult, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	where := []string{
		"tenant_id = $1",
		"resource = 'tenant_admin'",
		"details->>'target_id' = $2",
	}
	args := []any{tenantID, userID.String()}

	if action := strings.TrimSpace(filter.Action); action != "" {
		args = append(args, action)
		where = append(where, fmt.Sprintf("action = $%d", len(args)))
	}
	if result := strings.TrimSpace(filter.Result); result != "" {
		args = append(args, result)
		where = append(where, fmt.Sprintf("result = $%d", len(args)))
	}
	whereSQL := strings.Join(where, " AND ")

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

	listArgs = append(listArgs, limit+1)
	rows, err := s.db.Query(ctx, `
		SELECT id, action, resource, result, user_id, details, created_at
		FROM audit_logs
		WHERE `+listWhere+`
		ORDER BY created_at DESC, id DESC
		LIMIT $`+fmt.Sprintf("%d", len(listArgs)), listArgs...)
	if err != nil {
		return ports.AuditLogListResult{}, fmt.Errorf("list tenant admin audit logs: %w", err)
	}
	defer rows.Close()

	items := make([]ports.AuditLog, 0, limit)
	for rows.Next() {
		var (
			item       ports.AuditLog
			detailsRaw []byte
		)
		if err := rows.Scan(
			&item.ID, &item.Action, &item.Resource, &item.Result, &item.UserID, &detailsRaw, &item.CreatedAt,
		); err != nil {
			return ports.AuditLogListResult{}, fmt.Errorf("scan tenant admin audit log: %w", err)
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
		return ports.AuditLogListResult{}, fmt.Errorf("iterate tenant admin audit logs: %w", err)
	}

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
