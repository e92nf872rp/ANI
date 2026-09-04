package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
)

// PostgresTenant implements tenant read / tenant-plan adapters against the control-plane DB.
type PostgresTenant struct {
	store ports.MetadataStore
}

// NewPostgresTenant constructs a TenantService backed by MetadataStore (platform tx).
func NewPostgresTenant(store ports.MetadataStore) *PostgresTenant {
	return &PostgresTenant{store: store}
}

var _ ports.TenantService = (*PostgresTenant)(nil)

const tenantAdminRoleNameForCount = "tenant-admin"

// GetTenant returns the tenant detail view (counts + auth summary).
func (t *PostgresTenant) GetTenant(ctx context.Context, tenantID string) (ports.Tenant, error) {
	id, err := parseTenantUUID(tenantID)
	if err != nil {
		return ports.Tenant{}, err
	}

	var out ports.Tenant
	err = t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		loaded, loadErr := loadTenantDetail(ctx, tx, id)
		if loadErr != nil {
			return loadErr
		}
		out = loaded
		return nil
	})
	if err != nil {
		return ports.Tenant{}, err
	}
	return out, nil
}

func (t *PostgresTenant) CreateTenant(ctx context.Context, in ports.CreateTenantInput) (ports.Tenant, error) {
	// 步骤 1：规范化并校验必填字段 / plan_id UUID
	name := strings.TrimSpace(in.Name)
	displayName := strings.TrimSpace(in.DisplayName)
	contactEmail := strings.TrimSpace(in.ContactEmail)
	adminEmail := strings.TrimSpace(in.AdminEmail)
	adminName := strings.TrimSpace(in.AdminName)
	passwordHash := strings.TrimSpace(in.AdminPasswordHash)
	if name == "" || displayName == "" || contactEmail == "" || adminEmail == "" || adminName == "" || passwordHash == "" {
		return ports.Tenant{}, fmt.Errorf("%w: name, display_name, email, admin_email, admin_name, admin_password_hash required", ports.ErrInvalid)
	}
	planID, err := parseTenantUUID(in.PlanID)
	if err != nil {
		return ports.Tenant{}, fmt.Errorf("%w: plan_id must be a uuid", ports.ErrInvalid)
	}

	// 步骤 2：平台事务内原子写入（任一失败全部回滚）
	var out ports.Tenant
	err = t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 2a：先查 name 是否已存在 → ErrTenantNameConflict
		var nameExists bool
		if scanErr := tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM tenants WHERE name = $1)
		`, name).Scan(&nameExists); scanErr != nil {
			return fmt.Errorf("check tenant name: %w", scanErr)
		}
		if nameExists {
			return ports.ErrTenantNameConflict
		}

		// 步骤 2b：INSERT tenants（status=active）；并发下 UNIQUE 仍映射冲突
		var (
			rowID       uuid.UUID
			rowPlanID   uuid.UUID
			createdAt   time.Time
			updatedAt   time.Time
			rowName     string
			rowDisplay  string
			rowStatus   string
			rowContact  string
			rowFrozen   *time.Time
			rowDisabled *time.Time
		)
		scanErr := tx.QueryRow(ctx, `
			INSERT INTO tenants (name, display_name, status, plan_id, contact_email)
			VALUES ($1, $2, 'active', $3, $4)
			RETURNING id, name, display_name, status, plan_id, contact_email, frozen_at, disabled_at, created_at, updated_at
		`, name, displayName, planID, contactEmail).Scan(
			&rowID, &rowName, &rowDisplay, &rowStatus, &rowPlanID, &rowContact, &rowFrozen, &rowDisabled, &createdAt, &updatedAt,
		)
		if scanErr != nil {
			if isPGUniqueViolation(scanErr) {
				return ports.ErrTenantNameConflict
			}
			return fmt.Errorf("insert tenants: %w", scanErr)
		}

		// 步骤 2c：INSERT tenant_auth（默认 sso_enabled=false / mfa_required=false）
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO tenant_auth (tenant_id) VALUES ($1)
		`, rowID); execErr != nil {
			return fmt.Errorf("insert tenant_auth: %w", execErr)
		}

		// 步骤 2d：INSERT users（admin_name→username；password_hash 已 bcrypt）
		var userID uuid.UUID
		if scanErr := tx.QueryRow(ctx, `
			INSERT INTO users (tenant_id, username, email, password_hash, status, display_name)
			VALUES ($1, $2, $3, $4, 'active', $5)
			RETURNING id
		`, rowID, adminName, adminEmail, passwordHash, adminName).Scan(&userID); scanErr != nil {
			if isPGUniqueViolation(scanErr) {
				return fmt.Errorf("%w: admin email or username conflict", ports.ErrInvalid)
			}
			return fmt.Errorf("insert users: %w", scanErr)
		}

		// 步骤 2e：绑定内置 tenant-admin 角色
		var roleID uuid.UUID
		if scanErr := tx.QueryRow(ctx, `
			SELECT id FROM roles
			WHERE tenant_id IS NULL AND name = 'tenant-admin'
			LIMIT 1
		`).Scan(&roleID); scanErr != nil {
			return fmt.Errorf("lookup tenant-admin role: %w", scanErr)
		}
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)
		`, userID, roleID); execErr != nil {
			return fmt.Errorf("insert user_roles: %w", execErr)
		}

		// 步骤 2f：INSERT tenant_lifecycle('create')（归因来自 Gateway 注入的 ctx）
		actor, requestID := lifecycleAttributionArgs(ctx)
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO tenant_lifecycle (tenant_id, action, user_id, request_id)
			VALUES ($1, 'create', $2, $3)
		`, rowID, actor, requestID); execErr != nil {
			return fmt.Errorf("insert tenant_lifecycle: %w", execErr)
		}

		// 步骤 2g：组装返回视图（新建租户 admin_count=1）
		out = ports.Tenant{
			ID:           rowID.String(),
			Name:         rowName,
			DisplayName:  rowDisplay,
			Status:       ports.TenantStatus(rowStatus),
			PlanID:       rowPlanID.String(),
			ContactEmail: rowContact,
			FrozenAt:     rowFrozen,
			DisabledAt:   rowDisabled,
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
			AdminCount:   1,
		}
		return nil
	})
	// 步骤 3：上抛事务错误或返回租户
	if err != nil {
		return ports.Tenant{}, err
	}
	return out, nil
}

func isPGUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (t *PostgresTenant) ListTenants(ctx context.Context, filter ports.ListTenantsFilter) (ports.TenantListResult, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	status, err := ports.ParseTenantStatusFilter(string(filter.Status))
	if err != nil {
		return ports.TenantListResult{}, err
	}
	search := strings.TrimSpace(filter.Search)

	var cursorCreatedAt *time.Time
	var cursorID *uuid.UUID
	if raw := strings.TrimSpace(filter.Cursor); raw != "" {
		createdAt, id, err := types.DecodeCursor(raw)
		if err != nil {
			return ports.TenantListResult{}, fmt.Errorf("%w: invalid cursor", ports.ErrInvalid)
		}
		cursorCreatedAt = &createdAt
		cursorID = &id
	}

	var items []ports.TenantListItem
	err = t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		args := []any{tenantAdminRoleNameForCount, string(status), search}
		where := `
			WHERE ($2 = '' OR t.status = $2)
			  AND ($3 = '' OR t.name ILIKE '%' || $3 || '%' OR t.display_name ILIKE '%' || $3 || '%')
		`
		if cursorCreatedAt != nil && cursorID != nil {
			args = append(args, *cursorCreatedAt, *cursorID)
			where += fmt.Sprintf(" AND (t.created_at, t.id) < ($%d, $%d)", len(args)-1, len(args))
		}
		args = append(args, limit+1)
		limitArg := len(args)

		rows, queryErr := tx.Query(ctx, `
			SELECT t.id, t.name, t.display_name, t.status, t.plan_id, t.created_at,
			       COALESCE(ac.admin_count, 0)
			FROM tenants t
			LEFT JOIN LATERAL (
				SELECT COUNT(*)::bigint AS admin_count
				FROM users u
				JOIN user_roles ur ON ur.user_id = u.id
				JOIN roles r ON r.id = ur.role_id
				WHERE u.tenant_id = t.id
				  AND COALESCE(u.is_deleted, FALSE) = FALSE
				  AND r.tenant_id IS NULL
				  AND r.name = $1
			) ac ON TRUE
			`+where+`
			ORDER BY t.created_at DESC, t.id DESC
			LIMIT $`+fmt.Sprintf("%d", limitArg), args...)
		if queryErr != nil {
			return fmt.Errorf("list tenants: %w", queryErr)
		}
		defer rows.Close()

		out := make([]ports.TenantListItem, 0, limit+1)
		for rows.Next() {
			var (
				id          uuid.UUID
				planID      uuid.UUID
				createdAt   time.Time
				name        string
				displayName string
				statusVal   string
				adminCount  int64
			)
			if scanErr := rows.Scan(&id, &name, &displayName, &statusVal, &planID, &createdAt, &adminCount); scanErr != nil {
				return fmt.Errorf("scan tenant list item: %w", scanErr)
			}
			out = append(out, ports.TenantListItem{
				ID:          id.String(),
				Name:        name,
				DisplayName: displayName,
				Status:      ports.TenantStatus(statusVal),
				PlanID:      planID.String(),
				AdminCount:  adminCount,
				CreatedAt:   createdAt,
			})
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate tenants: %w", err)
		}
		items = out
		return nil
	})
	if err != nil {
		return ports.TenantListResult{}, err
	}

	nextCursor := ""
	if len(items) > limit {
		last := items[limit-1]
		lastID, parseErr := uuid.Parse(last.ID)
		if parseErr != nil {
			return ports.TenantListResult{}, fmt.Errorf("encode cursor: %w", parseErr)
		}
		nextCursor = types.EncodeCursor(last.CreatedAt, lastID)
		items = items[:limit]
	}
	return ports.TenantListResult{Items: items, NextCursor: nextCursor}, nil
}

// UpdateTenant 部分更新 display_name / contact_email；不触碰 name / status / plan_id。
func (t *PostgresTenant) UpdateTenant(ctx context.Context, tenantID string, in ports.UpdateTenantInput) (ports.Tenant, error) {
	id, err := parseTenantUUID(tenantID)
	if err != nil {
		return ports.Tenant{}, err
	}
	if in.DisplayName == nil && in.ContactEmail == nil {
		return ports.Tenant{}, fmt.Errorf("%w: display_name or contact_email required", ports.ErrInvalid)
	}

	var displayName *string
	if in.DisplayName != nil {
		v := strings.TrimSpace(*in.DisplayName)
		if v == "" || len(v) > 128 {
			return ports.Tenant{}, fmt.Errorf("%w: display_name required (1-128)", ports.ErrInvalid)
		}
		displayName = &v
	}
	var contactEmail *string
	if in.ContactEmail != nil {
		v := strings.TrimSpace(*in.ContactEmail)
		if v == "" {
			return ports.Tenant{}, fmt.Errorf("%w: contact_email required", ports.ErrInvalid)
		}
		contactEmail = &v
	}

	var out ports.Tenant
	err = t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 动态 SET（仅提供的字段）+ updated_at；disabled 与不存在一律 0 行 → NotFound
		sets := []string{"updated_at = now()"}
		args := []any{id}
		argN := 2
		if displayName != nil {
			sets = append(sets, fmt.Sprintf("display_name = $%d", argN))
			args = append(args, *displayName)
			argN++
		}
		if contactEmail != nil {
			sets = append(sets, fmt.Sprintf("contact_email = $%d", argN))
			args = append(args, *contactEmail)
		}
		q := fmt.Sprintf(`
			UPDATE tenants
			SET %s
			WHERE id = $1 AND status <> 'disabled'
			RETURNING id
		`, strings.Join(sets, ", "))
		var updatedID uuid.UUID
		if scanErr := tx.QueryRow(ctx, q, args...).Scan(&updatedID); scanErr != nil {
			if errors.Is(scanErr, pgx.ErrNoRows) {
				return ports.ErrTenantNotFound
			}
			return fmt.Errorf("update tenant: %w", scanErr)
		}

		loaded, loadErr := loadTenantDetail(ctx, tx, id)
		if loadErr != nil {
			return loadErr
		}
		out = loaded
		return nil
	})
	if err != nil {
		return ports.Tenant{}, err
	}
	return out, nil
}

// loadTenantDetail 在已有平台事务内读取租户详情（counts + auth summary）。
func loadTenantDetail(ctx context.Context, tx ports.MetadataTx, id uuid.UUID) (ports.Tenant, error) {
	var (
		rowID       uuid.UUID
		planID      uuid.UUID
		createdAt   time.Time
		updatedAt   time.Time
		name        string
		displayName string
		status      string
		contact     *string
		frozenAt    *time.Time
		disabledAt  *time.Time
		userCount   int64
		adminCount  int64
		ssoEnabled  bool
		mfaRequired bool
	)
	scanErr := tx.QueryRow(ctx, `
		SELECT t.id, t.name, t.display_name, t.status, t.plan_id, t.contact_email,
		       t.frozen_at, t.disabled_at, t.created_at, t.updated_at,
		       COALESCE(uc.user_count, 0),
		       COALESCE(ac.admin_count, 0),
		       COALESCE(ta.sso_enabled, FALSE),
		       COALESCE(ta.mfa_required, FALSE)
		FROM tenants t
		LEFT JOIN tenant_auth ta ON ta.tenant_id = t.id
		LEFT JOIN LATERAL (
			SELECT COUNT(*)::bigint AS user_count
			FROM users u
			WHERE u.tenant_id = t.id
			  AND COALESCE(u.is_deleted, FALSE) = FALSE
		) uc ON TRUE
		LEFT JOIN LATERAL (
			SELECT COUNT(*)::bigint AS admin_count
			FROM users u
			JOIN user_roles ur ON ur.user_id = u.id
			JOIN roles r ON r.id = ur.role_id
			WHERE u.tenant_id = t.id
			  AND COALESCE(u.is_deleted, FALSE) = FALSE
			  AND r.tenant_id IS NULL
			  AND r.name = $2
		) ac ON TRUE
		WHERE t.id = $1
	`, id, tenantAdminRoleNameForCount).Scan(
		&rowID, &name, &displayName, &status, &planID, &contact,
		&frozenAt, &disabledAt, &createdAt, &updatedAt,
		&userCount, &adminCount, &ssoEnabled, &mfaRequired,
	)
	if errors.Is(scanErr, pgx.ErrNoRows) {
		return ports.Tenant{}, ports.ErrTenantNotFound
	}
	if scanErr != nil {
		return ports.Tenant{}, fmt.Errorf("get tenant: %w", scanErr)
	}
	out := ports.Tenant{
		ID:          rowID.String(),
		Name:        name,
		DisplayName: displayName,
		Status:      ports.TenantStatus(status),
		PlanID:      planID.String(),
		FrozenAt:    frozenAt,
		DisabledAt:  disabledAt,
		UserCount:   userCount,
		AdminCount:  adminCount,
		Auth: &ports.TenantAuthSummary{
			SsoEnabled:  ssoEnabled,
			MfaRequired: mfaRequired,
		},
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	if contact != nil {
		out.ContactEmail = *contact
	}
	return out, nil
}

// FreezeTenant：active → frozen；同事务写 lifecycle('freeze') + 回读详情。
func (t *PostgresTenant) FreezeTenant(ctx context.Context, tenantID string) (ports.Tenant, error) {
	id, err := parseTenantUUID(tenantID)
	if err != nil {
		return ports.Tenant{}, err
	}

	var out ports.Tenant
	err = t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 1：仅 active 可冻结
		var updatedID uuid.UUID
		scanErr := tx.QueryRow(ctx, `
			UPDATE tenants
			SET status = 'frozen', frozen_at = now(), updated_at = now()
			WHERE id = $1 AND status = $2
			RETURNING id
		`, id, ports.TenantStatusActive).Scan(&updatedID)
		if scanErr != nil {
			if !errors.Is(scanErr, pgx.ErrNoRows) {
				return fmt.Errorf("freeze tenant: %w", scanErr)
			}
			return tenantStateTransitionReject(ctx, tx, id)
		}

		// 步骤 2：同事务写入 lifecycle（归因来自 Gateway 注入的 ctx）
		actor, reqID := lifecycleAttributionArgs(ctx)
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO tenant_lifecycle (tenant_id, action, user_id, request_id)
			VALUES ($1, 'freeze', $2, $3)
		`, id, actor, reqID); execErr != nil {
			return fmt.Errorf("insert tenant_lifecycle(freeze): %w", execErr)
		}

		// 步骤 3：回读详情
		loaded, loadErr := loadTenantDetail(ctx, tx, id)
		if loadErr != nil {
			return loadErr
		}
		out = loaded
		return nil
	})
	if err != nil {
		return ports.Tenant{}, err
	}
	return out, nil
}

// UnfreezeTenant：frozen → active，清空 frozen_at；同事务写 lifecycle('unfreeze') + 回读详情。
func (t *PostgresTenant) UnfreezeTenant(ctx context.Context, tenantID string) (ports.Tenant, error) {
	id, err := parseTenantUUID(tenantID)
	if err != nil {
		return ports.Tenant{}, err
	}

	var out ports.Tenant
	err = t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 1：仅 frozen 可解冻
		var updatedID uuid.UUID
		scanErr := tx.QueryRow(ctx, `
			UPDATE tenants
			SET status = 'active', frozen_at = NULL, updated_at = now()
			WHERE id = $1 AND status = $2
			RETURNING id
		`, id, ports.TenantStatusFrozen).Scan(&updatedID)
		if scanErr != nil {
			if !errors.Is(scanErr, pgx.ErrNoRows) {
				return fmt.Errorf("unfreeze tenant: %w", scanErr)
			}
			return tenantStateTransitionReject(ctx, tx, id)
		}

		// 步骤 2：同事务写入 lifecycle
		actor, reqID := lifecycleAttributionArgs(ctx)
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO tenant_lifecycle (tenant_id, action, user_id, request_id)
			VALUES ($1, 'unfreeze', $2, $3)
		`, id, actor, reqID); execErr != nil {
			return fmt.Errorf("insert tenant_lifecycle(unfreeze): %w", execErr)
		}

		// 步骤 3：回读详情
		loaded, loadErr := loadTenantDetail(ctx, tx, id)
		if loadErr != nil {
			return loadErr
		}
		out = loaded
		return nil
	})
	if err != nil {
		return ports.Tenant{}, err
	}
	return out, nil
}

// DisableTenant：active/frozen → disabled；同事务写 lifecycle('disable') + 回读详情。
// 禁用前置 used+reserved 校验由 tenant-service 编排；本方法不释放资源。
func (t *PostgresTenant) DisableTenant(ctx context.Context, tenantID string) (ports.Tenant, error) {
	id, err := parseTenantUUID(tenantID)
	if err != nil {
		return ports.Tenant{}, err
	}

	var out ports.Tenant
	err = t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 1：仅 active/frozen 可禁用
		var updatedID uuid.UUID
		scanErr := tx.QueryRow(ctx, `
			UPDATE tenants
			SET status = 'disabled', disabled_at = now(), frozen_at = NULL, updated_at = now()
			WHERE id = $1 AND status IN ($2, $3)
			RETURNING id
		`, id, ports.TenantStatusActive, ports.TenantStatusFrozen).Scan(&updatedID)
		if scanErr != nil {
			if !errors.Is(scanErr, pgx.ErrNoRows) {
				return fmt.Errorf("disable tenant: %w", scanErr)
			}
			return tenantStateTransitionReject(ctx, tx, id)
		}

		// 步骤 2：同事务写入 lifecycle
		actor, reqID := lifecycleAttributionArgs(ctx)
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO tenant_lifecycle (tenant_id, action, user_id, request_id)
			VALUES ($1, 'disable', $2, $3)
		`, id, actor, reqID); execErr != nil {
			return fmt.Errorf("insert tenant_lifecycle(disable): %w", execErr)
		}

		// 步骤 3：回读详情
		loaded, loadErr := loadTenantDetail(ctx, tx, id)
		if loadErr != nil {
			return loadErr
		}
		out = loaded
		return nil
	})
	if err != nil {
		return ports.Tenant{}, err
	}
	return out, nil
}

type tenantLifecycleAttrKey struct{}

// WithTenantLifecycleAttribution 由 Core Gateway 统一注入 request_id / actor_user_id，
// 供 PostgresTenant 写 tenant_lifecycle（create/freeze/unfreeze/disable）。
func WithTenantLifecycleAttribution(ctx context.Context, requestID, actorUserID string) context.Context {
	requestID = strings.TrimSpace(requestID)
	actorUserID = strings.TrimSpace(actorUserID)
	if requestID == "" && actorUserID == "" {
		return ctx
	}
	return context.WithValue(ctx, tenantLifecycleAttrKey{}, [2]string{requestID, actorUserID})
}

func tenantLifecycleAttributionFromCtx(ctx context.Context) (requestID, actorUserID string) {
	v, _ := ctx.Value(tenantLifecycleAttrKey{}).([2]string)
	return strings.TrimSpace(v[0]), strings.TrimSpace(v[1])
}

// lifecycleAttributionArgs 从 Gateway 注入的 ctx 取 lifecycle.user_id / request_id。
func lifecycleAttributionArgs(ctx context.Context) (actor any, requestID any) {
	reqID, actorID := tenantLifecycleAttributionFromCtx(ctx)
	if id, err := uuid.Parse(actorID); err == nil {
		actor = id
	}
	if reqID != "" {
		requestID = reqID
	}
	return actor, requestID
}

// tenantStateTransitionReject：UPDATE 未命中时区分租户不存在 vs 状态非法。
func tenantStateTransitionReject(ctx context.Context, tx ports.MetadataTx, id uuid.UUID) error {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM tenants WHERE id = $1`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ErrTenantNotFound
	}
	if err != nil {
		return fmt.Errorf("lookup tenant status: %w", err)
	}
	return fmt.Errorf("%w: current status is %s", ports.ErrTenantStateInvalid, status)
}

func (t *PostgresTenant) GetTenantAuth(ctx context.Context, tenantID string) (ports.TenantAuth, error) {
	id, err := parseTenantUUID(tenantID)
	if err != nil {
		return ports.TenantAuth{}, err
	}
	var out ports.TenantAuth
	err = t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 1：租户必须存在，否则 404（与缺 auth 行默认值路径区分）
		var exists bool
		if qErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants WHERE id = $1)`, id).Scan(&exists); qErr != nil {
			return fmt.Errorf("check tenant exists: %w", qErr)
		}
		if !exists {
			return ports.ErrTenantNotFound
		}
		// 步骤 2：读 tenant_auth；缺行返回默认值（SPEC §5.4-6）
		var (
			ssoEnabled  bool
			ssoProvider *string
			mfaRequired bool
			updatedAt   time.Time
		)
		qErr := tx.QueryRow(ctx, `
			SELECT sso_enabled, sso_provider, mfa_required, updated_at
			FROM tenant_auth
			WHERE tenant_id = $1
		`, id).Scan(&ssoEnabled, &ssoProvider, &mfaRequired, &updatedAt)
		if errors.Is(qErr, pgx.ErrNoRows) {
			out = ports.TenantAuth{
				TenantID:    id.String(),
				SsoEnabled:  false,
				SsoProvider: nil,
				MfaRequired: false,
				UpdatedAt:   time.Now().UTC(),
			}
			return nil
		}
		if qErr != nil {
			return fmt.Errorf("get tenant_auth: %w", qErr)
		}
		out = ports.TenantAuth{
			TenantID:    id.String(),
			SsoEnabled:  ssoEnabled,
			SsoProvider: ssoProvider,
			MfaRequired: mfaRequired,
			UpdatedAt:   updatedAt,
		}
		return nil
	})
	if err != nil {
		return ports.TenantAuth{}, err
	}
	return out, nil
}

func (t *PostgresTenant) UpdateTenantAuth(ctx context.Context, tenantID string, patch ports.TenantAuthPatch) (ports.TenantAuth, error) {
	id, err := parseTenantUUID(tenantID)
	if err != nil {
		return ports.TenantAuth{}, err
	}
	if patch.SsoEnabled == nil && patch.SsoProvider == nil && patch.MfaRequired == nil {
		return ports.TenantAuth{}, fmt.Errorf("%w: sso_enabled, provider, or mfa_required required", ports.ErrInvalid)
	}

	var out ports.TenantAuth
	err = t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 1：租户必须存在；disabled 终态不可改 Auth → TENANT_STATE_INVALID（409）
		var status string
		if qErr := tx.QueryRow(ctx, `SELECT status FROM tenants WHERE id = $1`, id).Scan(&status); qErr != nil {
			if errors.Is(qErr, pgx.ErrNoRows) {
				return ports.ErrTenantNotFound
			}
			return fmt.Errorf("lookup tenant status: %w", qErr)
		}
		if ports.TenantStatus(status) == ports.TenantStatusDisabled {
			return fmt.Errorf("%w: tenant is disabled", ports.ErrTenantStateInvalid)
		}
		// 步骤 2：确保 1:1 auth 行（存量缺行防御）
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO tenant_auth (tenant_id) VALUES ($1)
			ON CONFLICT (tenant_id) DO NOTHING
		`, id); execErr != nil {
			return fmt.Errorf("ensure tenant_auth: %w", execErr)
		}
		// 步骤 3：部分更新（仅提供的字段）+ updated_at
		sets := []string{"updated_at = now()"}
		args := []any{id}
		argN := 2
		if patch.SsoEnabled != nil {
			sets = append(sets, fmt.Sprintf("sso_enabled = $%d", argN))
			args = append(args, *patch.SsoEnabled)
			argN++
		}
		if patch.SsoProvider != nil {
			sets = append(sets, fmt.Sprintf("sso_provider = $%d", argN))
			v := strings.TrimSpace(*patch.SsoProvider)
			if v == "" {
				args = append(args, nil)
			} else {
				args = append(args, v)
			}
			argN++
		}
		if patch.MfaRequired != nil {
			sets = append(sets, fmt.Sprintf("mfa_required = $%d", argN))
			args = append(args, *patch.MfaRequired)
			argN++
		}
		var (
			ssoEnabled  bool
			ssoProvider *string
			mfaRequired bool
			updatedAt   time.Time
		)
		qErr := tx.QueryRow(ctx, fmt.Sprintf(`
			UPDATE tenant_auth
			SET %s
			WHERE tenant_id = $1
			RETURNING sso_enabled, sso_provider, mfa_required, updated_at
		`, strings.Join(sets, ", ")), args...).Scan(&ssoEnabled, &ssoProvider, &mfaRequired, &updatedAt)
		if qErr != nil {
			return fmt.Errorf("update tenant_auth: %w", qErr)
		}
		out = ports.TenantAuth{
			TenantID:    id.String(),
			SsoEnabled:  ssoEnabled,
			SsoProvider: ssoProvider,
			MfaRequired: mfaRequired,
			UpdatedAt:   updatedAt,
		}
		return nil
	})
	if err != nil {
		return ports.TenantAuth{}, err
	}
	return out, nil
}

func (t *PostgresTenant) ListTenantLifecycle(context.Context, string, ports.TenantLifecycleFilter) (ports.TenantLifecycleListResult, error) {
	return ports.TenantLifecycleListResult{}, ports.ErrUnsupported
}

func parseTenantUUID(raw string) (uuid.UUID, error) {
	return parseAdminUUID(raw, "tenant_id")
}

// ListAvailableTenants 返回 status <> 'disabled' 的租户列表，按 created_at DESC 排序。
func (t *PostgresTenant) ListAvailableTenants(ctx context.Context) ([]ports.TenantSummary, error) {
	// 步骤 1：准备结果切片，在平台事务内查询
	out := make([]ports.TenantSummary, 0)
	err := t.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 2：查询非 disabled 租户，按 created_at DESC
		rows, queryErr := tx.Query(ctx, `
			SELECT id, name, display_name, status
			FROM tenants
			WHERE status <> 'disabled'
			ORDER BY created_at DESC
		`)
		if queryErr != nil {
			return fmt.Errorf("list available tenants: %w", queryErr)
		}
		defer rows.Close()
		// 步骤 3：逐行扫描并写入 TenantSummary
		for rows.Next() {
			var (
				id          uuid.UUID
				name        string
				displayName string
				status      string
			)
			if scanErr := rows.Scan(&id, &name, &displayName, &status); scanErr != nil {
				return fmt.Errorf("scan available tenant: %w", scanErr)
			}
			out = append(out, ports.TenantSummary{
				ID:          id.String(),
				Name:        name,
				DisplayName: displayName,
				Status:      ports.TenantStatus(status),
			})
		}
		if rows.Err() != nil {
			return fmt.Errorf("iterate available tenants: %w", rows.Err())
		}
		return nil
	})
	// 步骤 4：事务失败则上抛；成功返回列表
	if err != nil {
		return nil, err
	}
	return out, nil
}

func parseAdminUUID(raw, field string) (uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return uuid.Nil, fmt.Errorf("%w: %s required", ports.ErrInvalid, field)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %s must be a uuid", ports.ErrInvalid, field)
	}
	return id, nil
}

func isPGForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
