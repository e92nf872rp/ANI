package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
)

// PostgresQuota 实现 ports.QuotaService（TCC 预占/实扣状态机，issue-003）、
// ports.QuotaStoreService（配置查询，issue-004）和
// ports.QuotaAdminService（租户生命周期管理，issue-005）的 PG adapter。
// 持有 MetadataStore：扣减 Try/TryMany 自开租户事务，配置查询 Put/List 自开平台事务，
// GetMy 自开租户事务；Confirm/Cancel/Release/GetTotalForUpdateTx 接收外部事务；
// 管理方法（Create/Update/Get/Delete/ListQuotaMeta）自开平台事务走 RLS bypass。
type PostgresQuota struct {
	store ports.MetadataStore
}

// NewPostgresQuota 构造 quota adapter。
func NewPostgresQuota(store ports.MetadataStore) *PostgresQuota {
	return &PostgresQuota{store: store}
}

// 编译期接口断言：QuotaService（issue-003）+ QuotaStoreService（issue-004）
// + QuotaAdminService（issue-005）。
var _ ports.QuotaService = (*PostgresQuota)(nil)
var _ ports.QuotaStoreService = (*PostgresQuota)(nil)
var _ ports.QuotaAdminService = (*PostgresQuota)(nil)

// tenantCtx 基于 req.TenantID 构造带租户上下文的 context，供 WithTenantTx 注入 RLS。
func tenantCtx(ctx context.Context, tenantID string) (context.Context, error) {
	id, err := uuid.Parse(tenantID)
	if err != nil {
		return nil, err
	}
	return types.WithTenant(ctx, &types.TenantContext{
		TenantID: id,
		Roles:    []string{"user"},
	}), nil
}

// tryInTx 在给定 tx 上执行单维度预占，返回 QuotaReservation。
// Try 和 TryMany 共用此方法，区别在于是否在同一事务内循环。
func (q *PostgresQuota) tryInTx(ctx context.Context, tx ports.MetadataTx, req ports.QuotaTryRequest) (ports.QuotaReservation, error) {
	var res ports.QuotaReservation

	if req.Amount <= 0 {
		return res, ports.ErrInvalid // 预占金额必须为正，负值/零值属非法输入
	}

	// 1. 校验资源已注册且 enabled
	var enabled bool
	var defaultQuota int64
	err := tx.QueryRow(ctx, `
		SELECT enabled, default_quota FROM resource_quota_meta
		WHERE resource_type = $1
	`, req.ResourceType).Scan(&enabled, &defaultQuota)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return res, ports.ErrQuotaResourceNotRegistered
		}
		return res, err // 网络错误、连接断开等透传，不掩盖
	}
	if !enabled {
		return res, ports.ErrQuotaResourceNotRegistered
	}

	// 2. lazy init：无配置行则用 default_quota 建行（并发首次 ON CONFLICT DO NOTHING）
	if _, err := tx.Exec(ctx, `
		INSERT INTO resource_quota (tenant_id, resource_type, total)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, resource_type) DO NOTHING
	`, req.TenantID, req.ResourceType, defaultQuota); err != nil {
		return res, err
	}

	// 3. 单行原子预占：WHERE 校验余量，行锁串行化并发，不超卖
	tag, err := tx.Exec(ctx, `
		UPDATE resource_quota
		SET reserved = reserved + $1, updated_at = NOW()
		WHERE tenant_id = $2 AND resource_type = $3
		  AND reserved + used + $1 <= total
	`, req.Amount, req.TenantID, req.ResourceType)
	if err != nil {
		return res, err
	}
	if tag.RowsAffected == 0 {
		return res, ports.ErrQuotaExceeded // 余量不足
	}

	// 4. 插入预占流水，拿回 tx_id 和 expires_at
	err = tx.QueryRow(ctx, `
		INSERT INTO resource_reservations (tenant_id, resource_type, amount, expires_at)
		VALUES ($1, $2, $3, NOW() + INTERVAL '10 minutes')
		RETURNING tx_id::text, expires_at
	`, req.TenantID, req.ResourceType, req.Amount).Scan(&res.TxID, &res.ExpiresAt)
	return res, err
}

// Try 单维度预占。自开 WithTenantTx，单行原子 UPDATE + lazy init。
func (q *PostgresQuota) Try(ctx context.Context, req ports.QuotaTryRequest) (ports.QuotaReservation, error) {
	qctx, err := tenantCtx(ctx, req.TenantID)
	if err != nil {
		return ports.QuotaReservation{}, err
	}
	var res ports.QuotaReservation
	err = q.store.WithTenantTx(qctx, func(ctx context.Context, tx ports.MetadataTx) error {
		var err error
		res, err = q.tryInTx(ctx, tx, req)
		return err
	})
	if err != nil {
		return ports.QuotaReservation{}, err
	}
	return res, nil
}

// TryMany 多维度批量预占。单事务内循环 tryInTx，任一失败则整体回滚，无悬挂预占。
func (q *PostgresQuota) TryMany(ctx context.Context, reqs []ports.QuotaTryRequest) ([]ports.QuotaReservation, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	tenantID := reqs[0].TenantID
	qctx, err := tenantCtx(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	var reservations []ports.QuotaReservation
	err = q.store.WithTenantTx(qctx, func(ctx context.Context, tx ports.MetadataTx) error {
		for _, req := range reqs {
			// 校验所有请求的 tenant_id 一致（多维度预占同一租户）
			if req.TenantID != tenantID {
				return errors.New("quota TryMany: all requests must have same tenant_id")
			}
			res, err := q.tryInTx(ctx, tx, req)
			if err != nil {
				return err // 任一失败 → return err → 整个事务回滚 → 已成功的预占一并回滚
			}
			reservations = append(reservations, res)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reservations, nil
}

// TryTx 单维度预占，接受外部 tx。不自己开事务，在调用方传入的 tx 内执行预占逻辑。
// 调用方负责开 WithTenantTx 并注入 TenantContext；失败时只返回 err，由外层事务统一回滚。
func (q *PostgresQuota) TryTx(ctx context.Context, tx ports.MetadataTx, req ports.QuotaTryRequest) (ports.QuotaReservation, error) {
	return q.tryInTx(ctx, tx, req)
}

// TryManyTx 多维度批量预占，接受外部 tx。不自己开事务，在调用方传入的 tx 内循环 tryInTx。
// 调用方负责开 WithTenantTx 并注入 TenantContext；任一维度失败只返回 err，由外层事务统一回滚。
func (q *PostgresQuota) TryManyTx(ctx context.Context, tx ports.MetadataTx, reqs []ports.QuotaTryRequest) ([]ports.QuotaReservation, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	var reservations []ports.QuotaReservation
	for _, req := range reqs {
		res, err := q.tryInTx(ctx, tx, req)
		if err != nil {
			return nil, err // 不自己回滚，由调用方的外层事务统一回滚
		}
		reservations = append(reservations, res)
	}
	return reservations, nil
}

// reservationExists 判断指定 tx_id 的预占流水是否存在。用于 UPDATE 状态守卫返回
// ErrNoRows 时区分"流水存在但 state 已变更（幂等重放）"与"流水不存在（tx_id 无效）"，
// 避免把无效 tx_id 静默吞掉。存在返回 true；不存在返回 false；查询本身出错返回 err。
func reservationExists(ctx context.Context, tx ports.MetadataTx, txID string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM resource_reservations WHERE tx_id = $1)`,
		txID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// Confirm 预占转实扣。不自己开事务，用调用方传入的 tx。
// 幂等：WHERE state='reserved' RETURNING 空 = 已确认，跳过着（不重复扣减）。
func (q *PostgresQuota) Confirm(ctx context.Context, tx ports.MetadataTx, txIDs []string, resourceRef string) error {
	for _, txID := range txIDs {
		// 1. 流水 reserved → confirmed，WHERE state='reserved' 守卫幂等
		var state string
		err := tx.QueryRow(ctx, `
			UPDATE resource_reservations
			SET state = 'confirmed', resource_ref = $2, updated_at = NOW()
			WHERE tx_id = $1 AND state = 'reserved'
			RETURNING state
		`, txID, resourceRef).Scan(&state)
		if errors.Is(err, pgx.ErrNoRows) {
			// 区分两种情况：流水存在但 state 已非 reserved（幂等重放，跳过）
			// vs 流水不存在（tx_id 无效，报错），避免把无效 tx_id 静默吞掉。
			exists, err := reservationExists(ctx, tx, txID)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("quota Confirm: reservation %q not found: %w", txID, ports.ErrReservationNotFound)
			}
			slog.Warn("quota Confirm: reservation not in reserved state, skipping",
				"tx_id", txID, "resource_ref", resourceRef,
				"reason", "already confirmed/cancelled")
			continue // 已 confirmed/cancelled → 幂等成功，跳过
		}
		if err != nil {
			return err
		}

		// 2. reserved → used 转账（同一事务，只对刚 confirmed 的行）
		if _, err := tx.Exec(ctx, `
			UPDATE resource_quota q
			SET reserved = reserved - r.amount,
			    used = used + r.amount,
			    updated_at = NOW()
			FROM resource_reservations r
			WHERE r.tx_id = $1 AND r.state = 'confirmed'
			  AND q.tenant_id = r.tenant_id AND q.resource_type = r.resource_type
		`, txID); err != nil {
			return err
		}
	}
	return nil
}

// Cancel 释放预占。不自己开事务，用调用方传入的 tx。
// 幂等：同 Confirm。
func (q *PostgresQuota) Cancel(ctx context.Context, tx ports.MetadataTx, txIDs []string) error {
	for _, txID := range txIDs {
		// 1. 流水 reserved → cancelled，WHERE state='reserved' 守卫幂等
		var state string
		err := tx.QueryRow(ctx, `
			UPDATE resource_reservations
			SET state = 'cancelled', updated_at = NOW()
			WHERE tx_id = $1 AND state = 'reserved'
			RETURNING state
		`, txID).Scan(&state)
		if errors.Is(err, pgx.ErrNoRows) {
			// 区分两种情况：流水存在但 state 已非 reserved（幂等重放，跳过）
			// vs 流水不存在（tx_id 无效，报错），避免把无效 tx_id 静默吞掉。
			exists, err := reservationExists(ctx, tx, txID)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("quota Cancel: reservation %q not found: %w", txID, ports.ErrReservationNotFound)
			}
			slog.Warn("quota Cancel: reservation not in reserved state, skipping",
				"tx_id", txID,
				"reason", "already cancelled/confirmed")
			continue // 已 cancelled/confirmed → 幂等跳过
		}
		if err != nil {
			return err
		}

		// 2. 释放 reserved（同一事务，只对刚 cancelled 的行）
		if _, err := tx.Exec(ctx, `
			UPDATE resource_quota q
			SET reserved = reserved - r.amount,
			    updated_at = NOW()
			FROM resource_reservations r
			WHERE r.tx_id = $1 AND r.state = 'cancelled'
			  AND q.tenant_id = r.tenant_id AND q.resource_type = r.resource_type
		`, txID); err != nil {
			return err
		}
	}
	return nil
}

// Release 释放已实扣配额。不自己开事务，用调用方传入的 tx。
// 幂等：WHERE state='confirmed' RETURNING 空 = 已释放，跳过着（不重复扣减）。
func (q *PostgresQuota) Release(ctx context.Context, tx ports.MetadataTx, txIDs []string) error {
	for _, txID := range txIDs {
		// 1. 流水 confirmed → released，WHERE state='confirmed' 守卫幂等
		var state string
		err := tx.QueryRow(ctx, `
			UPDATE resource_reservations
			SET state = 'released', updated_at = NOW()
			WHERE tx_id = $1 AND state = 'confirmed'
			RETURNING state
		`, txID).Scan(&state)
		if errors.Is(err, pgx.ErrNoRows) {
			// 区分两种情况：流水存在但 state 已非 confirmed（幂等重放，跳过）
			// vs 流水不存在（tx_id 无效，报错），避免把无效 tx_id 静默吞掉。
			exists, err := reservationExists(ctx, tx, txID)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("quota Release: reservation %q not found: %w", txID, ports.ErrReservationNotFound)
			}
			slog.Warn("quota Release: reservation not in confirmed state, skipping",
				"tx_id", txID,
				"reason", "already released/cancelled")
			continue // 已 released/cancelled → 幂等跳过
		}
		if err != nil {
			return err
		}

		// 2. used 减回（同一事务，只对刚 released 的行）
		if _, err := tx.Exec(ctx, `
			UPDATE resource_quota q
			SET used = used - r.amount,
			    updated_at = NOW()
			FROM resource_reservations r
			WHERE r.tx_id = $1 AND r.state = 'released'
			  AND q.tenant_id = r.tenant_id AND q.resource_type = r.resource_type
		`, txID); err != nil {
			return err
		}
	}
	return nil
}

// Put 设置租户配额 total（BOSS 平台角色）。自开 WithPlatformTx (bypass RLS)。
// UPSERT 语义：不存在建行，存在覆盖 total。不 clamp（若 total < used+reserved 撞
// CHECK 约束则报错，由 handler 透传到 HTTP）。校验每个维度在 meta 已注册且 enabled。
func (q *PostgresQuota) Put(ctx context.Context, idempotencyKey string, req ports.QuotaPutRequest) (ports.QuotaView, error) {
	view := ports.QuotaView{
		TenantID: req.TenantID,
		Total:    make(map[ports.ResourceType]int64),
		Used:     make(map[ports.ResourceType]int64),
		Reserved: make(map[ports.ResourceType]int64),
	}

	err := q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// Put 是 UPSERT 覆盖语义，对相同输入重复调用结果天然幂等；幂等防重由调用方在 HTTP 层处理。
		for rt, total := range req.Total {
			// 1. 校验资源类型已注册且 enabled
			var enabled bool
			err := tx.QueryRow(ctx, `
				SELECT enabled FROM resource_quota_meta WHERE resource_type = $1
			`, rt).Scan(&enabled)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return ports.ErrQuotaResourceNotRegistered
				}
				return err
			}
			if !enabled {
				return ports.ErrQuotaResourceNotRegistered
			}

			// 2. UPSERT total，不 clamp（直接写，撞 CHECK 则报错透传）
			if _, err := tx.Exec(ctx, `
				INSERT INTO resource_quota (tenant_id, resource_type, total, reserved, used)
				VALUES ($1, $2, $3, 0, 0)
				ON CONFLICT (tenant_id, resource_type)
				DO UPDATE SET total = EXCLUDED.total, updated_at = NOW()
			`, req.TenantID, rt, total); err != nil {
				return err
			}
		}

		// 3. 回读所有维度
		rows, err := tx.Query(ctx, `
			SELECT resource_type, total, reserved, used
			FROM resource_quota
			WHERE tenant_id = $1
		`, req.TenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var rt string
			var total, reserved, used int64
			if err := rows.Scan(&rt, &total, &reserved, &used); err != nil {
				return err
			}
			view.Total[ports.ResourceType(rt)] = total
			view.Used[ports.ResourceType(rt)] = used
			view.Reserved[ports.ResourceType(rt)] = reserved
		}
		return rows.Err()
	})
	if err != nil {
		return ports.QuotaView{}, err
	}
	return view, nil
}

// List 列租户配额（BOSS）。自开 WithPlatformTx (bypass RLS)。
// 无 tenant_id 时按租户级 keyset 分页（cursor=tenant_id，多查 1 条判断 hasMore，
// limit 默认 50、上限 100）；有 tenant_id 时直接调 GetMy 不分页。
func (q *PostgresQuota) List(ctx context.Context, req ports.QuotaListRequest) (ports.QuotaListResult, error) {
	var result ports.QuotaListResult

	// 指定了 tenant_id 就不分页，直接返回该租户全部维度
	if req.TenantID != "" {
		view, err := q.GetMy(ctx, req.TenantID)
		if err != nil {
			return result, err
		}
		result.Items = []ports.QuotaView{view}
		result.Total = 1
		return result, nil
	}

	// 无 tenant_id：按租户级 keyset 分页
	err := q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		limit := req.Limit
		if limit <= 0 || limit > 100 {
			limit = 50
		}

		// 第一步：查一页租户列表（DISTINCT tenant_id，keyset 分页，cursor = 上页末尾 tenant_id）
		// 用原生 UUID 比较（$1::uuid）走索引；limit+1 多查一条判断是否还有下一页
		tenantRows, err := tx.Query(ctx, `
			SELECT DISTINCT tenant_id::text
			FROM resource_quota
			WHERE ($1 = '' OR tenant_id > $1::uuid)
			ORDER BY tenant_id
			LIMIT $2
		`, req.Cursor, limit+1)
		if err != nil {
			return err
		}

		var tenantIDs []string
		for tenantRows.Next() {
			var tid string
			if err := tenantRows.Scan(&tid); err != nil {
				tenantRows.Close()
				return err
			}
			tenantIDs = append(tenantIDs, tid)
		}
		if err := tenantRows.Err(); err != nil {
			tenantRows.Close()
			return err
		}
		tenantRows.Close()

		if len(tenantIDs) == 0 {
			return nil // 无数据
		}

		// 判断是否还有下一页，并去掉多查的那一条
		hasMore := len(tenantIDs) > limit
		if hasMore {
			tenantIDs = tenantIDs[:limit]
		}

		// 第二步：查这些租户的全部配额维度
		rows, err := tx.Query(ctx, `
			SELECT tenant_id::text, resource_type, total, reserved, used
			FROM resource_quota
			WHERE tenant_id::text = ANY($1)
			ORDER BY tenant_id::text, resource_type
		`, tenantIDs)
		if err != nil {
			return err
		}
		defer rows.Close()

		viewsByTenant := make(map[string]*ports.QuotaView)
		var order []string
		for rows.Next() {
			var tenantID, rt string
			var total, reserved, used int64
			if err := rows.Scan(&tenantID, &rt, &total, &reserved, &used); err != nil {
				return err
			}
			v, ok := viewsByTenant[tenantID]
			if !ok {
				v = &ports.QuotaView{
					TenantID: tenantID,
					Total:    make(map[ports.ResourceType]int64),
					Used:     make(map[ports.ResourceType]int64),
					Reserved: make(map[ports.ResourceType]int64),
				}
				viewsByTenant[tenantID] = v
				order = append(order, tenantID)
			}
			v.Total[ports.ResourceType(rt)] = total
			v.Used[ports.ResourceType(rt)] = used
			v.Reserved[ports.ResourceType(rt)] = reserved
		}
		if err := rows.Err(); err != nil {
			return err
		}

		for _, tid := range order {
			result.Items = append(result.Items, *viewsByTenant[tid])
		}
		result.Total = len(result.Items)

		// cursor = 本页最后一个 tenant_id（纯字符串）
		if hasMore {
			result.NextCursor = tenantIDs[len(tenantIDs)-1]
		}
		return nil
	})
	return result, err
}

// GetMy 查当前租户配额（Console 自查）。自开 WithTenantTx，RLS 自动过滤只看本租户。
func (q *PostgresQuota) GetMy(ctx context.Context, tenantID string) (ports.QuotaView, error) {
	qctx, err := tenantCtx(ctx, tenantID)
	if err != nil {
		return ports.QuotaView{}, err
	}

	view := ports.QuotaView{
		TenantID: tenantID,
		Total:    make(map[ports.ResourceType]int64),
		Used:     make(map[ports.ResourceType]int64),
		Reserved: make(map[ports.ResourceType]int64),
	}
	err = q.store.WithTenantTx(qctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, `
			SELECT resource_type, total, reserved, used
			FROM resource_quota
			WHERE tenant_id = $1
		`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var rt string
			var total, reserved, used int64
			if err := rows.Scan(&rt, &total, &reserved, &used); err != nil {
				return err
			}
			view.Total[ports.ResourceType(rt)] = total
			view.Used[ports.ResourceType(rt)] = used
			view.Reserved[ports.ResourceType(rt)] = reserved
		}
		return rows.Err()
	})
	if err != nil {
		return ports.QuotaView{}, err
	}
	return view, nil
}

// GetTotalForUpdateTx 预留校验锁行查询（GPU 预留场景）。
// 接收外部 tx，SELECT total ... FOR UPDATE 锁住 resource_quota 行，串行化并发预留。
// 只返回 total 数值，不做判断（比对 gpu_slices 计数由调用方 handler 完成）。
// 行不存在返回 ports.ErrQuotaNotFound。
// 调用方契约：与 Confirm/Cancel/Release 相同，传入的 ctx 必须带 TenantContext（用 WithTenantTx 开事务即自动注入）。
func (q *PostgresQuota) GetTotalForUpdateTx(ctx context.Context, tx ports.MetadataTx, tenantID string, rt ports.ResourceType) (int64, error) {
	var total int64
	err := tx.QueryRow(ctx, `
		SELECT total FROM resource_quota
		WHERE tenant_id = $1 AND resource_type = $2
		FOR UPDATE
	`, tenantID, rt).Scan(&total)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ports.ErrQuotaNotFound
		}
		return 0, err
	}
	return total, nil
}

// tenantExists 在平台事务下校验租户是否存在（tenants 表）。
func (q *PostgresQuota) tenantExists(ctx context.Context, tx ports.MetadataTx, tenantID string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM tenants WHERE id = $1::uuid)
	`, tenantID).Scan(&exists)
	return exists, err
}

// requireTenantExists 校验租户存在，不存在返回 ports.ErrTenantNotFound。
func (q *PostgresQuota) requireTenantExists(ctx context.Context, tx ports.MetadataTx, tenantID string) error {
	exists, err := q.tenantExists(ctx, tx, tenantID)
	if err != nil {
		return err
	}
	if !exists {
		return ports.ErrTenantNotFound
	}
	return nil
}

// getMetaDefault 查询某维度的 enabled 与 default_quota（enabled=false 或未注册返回
// ErrQuotaResourceNotRegistered）。
func (q *PostgresQuota) getMetaDefault(ctx context.Context, tx ports.MetadataTx, rt ports.ResourceType) (bool, int64, error) {
	var enabled bool
	var defaultQuota int64
	err := tx.QueryRow(ctx, `
		SELECT enabled, default_quota FROM resource_quota_meta WHERE resource_type = $1
	`, rt).Scan(&enabled, &defaultQuota)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, ports.ErrQuotaResourceNotRegistered
	}
	if err != nil {
		return false, 0, err
	}
	if !enabled {
		return false, 0, ports.ErrQuotaResourceNotRegistered
	}
	return enabled, defaultQuota, nil
}

// CreateTenantQuota 批量初始化租户配额（平台管理员）。自开 WithPlatformTx (bypass RLS)。
// 部分成功语义：校验租户存在 + 每维度 meta enabled → total<=0 取 default_quota →
// 已存在维度 ON CONFLICT DO NOTHING 跳过（不阻断其余维度创建，事务提交不回滚）；
// 只要存在跳过维度即返回 ports.ErrQuotaAlreadyExists（handler 映射 409
// QUOTA_ALREADY_EXISTS），否则返回 nil。已创建的维度已落库，最终状态可由调用方 GET。
func (q *PostgresQuota) CreateTenantQuota(ctx context.Context, tenantID string, items []ports.QuotaItemInput) ([]ports.QuotaInfo, error) {
	if len(items) == 0 {
		return nil, ports.ErrInvalid
	}

	infos := make([]ports.QuotaInfo, 0, len(items))
	// conflict 标记是否跳过（已存在）了某个维度，用于所有维度处理完后决定是否返回 409
	var conflict bool
	var err error
	err = q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		if err := q.requireTenantExists(ctx, tx, tenantID); err != nil {
			return err
		}

		for _, item := range items {
			_, defaultQuota, err := q.getMetaDefault(ctx, tx, item.ResourceType)
			if err != nil {
				return err
			}

			// total<=0 视为未提供，取 default_quota
			total := item.Total
			if total <= 0 {
				total = defaultQuota
			}

			// 已存在维度 ON CONFLICT DO NOTHING 跳过，不阻断其余维度创建（部分成功）。
			// RowsAffected=0 说明该行已被占用，仅标记冲突，不回滚、不中断循环。
			tag, err := tx.Exec(ctx, `
				INSERT INTO resource_quota (tenant_id, resource_type, total, reserved, used)
				VALUES ($1, $2, $3, 0, 0)
				ON CONFLICT (tenant_id, resource_type) DO NOTHING
			`, tenantID, item.ResourceType, total)
			if err != nil {
				return err
			}
			if tag.RowsAffected == 0 {
				conflict = true
			}
		}

		// 回读 items 涉及维度，带 meta 信息（用于校验；部分成功后此处不再作为响应使用）
		types := make([]ports.ResourceType, len(items))
		for i, item := range items {
			types[i] = item.ResourceType
		}
		infos, err = q.quotaInfoByTypes(ctx, tx, tenantID, types)
		return err
	})
	if err != nil {
		return nil, err
	}
	// 事务已提交（不回滚）：只要存在已跳过维度就返回 409 哨兵错误，handler 映射 QUOTA_ALREADY_EXISTS
	if conflict {
		return infos, ports.ErrQuotaAlreadyExists
	}
	return infos, nil
}

// UpdateTenantQuota 批量修改租户配额 total（平台管理员，缩容 clamp）。自开 WithPlatformTx。
// 每维度校验 meta enabled → SET total = GREATEST($3, reserved + used) 缩容 clamp（不违反
// CHECK 约束）→ 行不存在返回 ErrQuotaNotFound → 回读计算 tightened 标记
// （回读 total > 请求 total 时 tightened=true）。
func (q *PostgresQuota) UpdateTenantQuota(ctx context.Context, tenantID string, items []ports.QuotaItemUpdate) ([]ports.QuotaInfo, error) {
	if len(items) == 0 {
		return nil, ports.ErrInvalid
	}

	infos := make([]ports.QuotaInfo, 0, len(items))
	var err error
	err = q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		for _, item := range items {
			if _, _, err := q.getMetaDefault(ctx, tx, item.ResourceType); err != nil {
				return err
			}
			// GREATEST clamp：total 不低于 reserved+used
			tag, err := tx.Exec(ctx, `
				UPDATE resource_quota
				SET total = GREATEST($3, reserved + used), updated_at = NOW()
				WHERE tenant_id = $1 AND resource_type = $2
			`, tenantID, item.ResourceType, item.Total)
			if err != nil {
				return err
			}
			if tag.RowsAffected == 0 {
				return ports.ErrQuotaNotFound // 行不存在
			}
		}

		// 回读 items 涉及维度 + 计算 tightened 标记
		types := make([]ports.ResourceType, len(items))
		for i, item := range items {
			types[i] = item.ResourceType
		}
		infos, err = q.quotaInfoByTypes(ctx, tx, tenantID, types)
		return err
	})
	if err != nil {
		return nil, err
	}

	// tightened = 回读 total > 请求 total（GREATEST clamp 生效）
	reqTotal := make(map[ports.ResourceType]int64, len(items))
	for _, item := range items {
		reqTotal[item.ResourceType] = item.Total
	}
	for i := range infos {
		if req, ok := reqTotal[infos[i].ResourceType]; ok && infos[i].Total > req {
			infos[i].Tightened = true
		}
	}
	return infos, nil
}

// UpsertTenantQuota 批量 upsert 租户配额（平台管理员）。自开 WithPlatformTx。
// 已存在维度更新 total，不存在维度新建；任一维度失败则整体回滚。
// 提交阶段失败时返回 ErrQuotaUpdateUncertain，调用方不得自动重试。
func (q *PostgresQuota) UpsertTenantQuota(ctx context.Context, tenantID string, items []ports.QuotaItemInput) ([]ports.QuotaInfo, error) {
	if len(items) == 0 {
		return nil, ports.ErrInvalid
	}
	seen := make(map[ports.ResourceType]struct{}, len(items))
	for _, item := range items {
		if _, ok := seen[item.ResourceType]; ok {
			return nil, ports.ErrInvalid
		}
		seen[item.ResourceType] = struct{}{}
	}

	reqTotals := make(map[ports.ResourceType]int64, len(items))
	infos := make([]ports.QuotaInfo, 0, len(items))
	var err error
	err = q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		if err := q.requireTenantExists(ctx, tx, tenantID); err != nil {
			return err
		}

		for i, item := range items {
			if item.Total < 0 {
				return fmt.Errorf("%w: total 不能为负数，items[%d].total=%d", ports.ErrInvalid, i, item.Total)
			}
			_, defaultQuota, err := q.getMetaDefault(ctx, tx, item.ResourceType)
			if err != nil {
				if errors.Is(err, ports.ErrQuotaResourceNotRegistered) {
					return fmt.Errorf("%w: resource_type %q 未在 resource_quota_meta 中注册或已禁用", ports.ErrQuotaResourceNotRegistered, item.ResourceType)
				}
				return err
			}
			total := item.Total
			if total == 0 {
				total = defaultQuota
			}
			reqTotals[item.ResourceType] = total

			if _, err := tx.Exec(ctx, `
				INSERT INTO resource_quota (tenant_id, resource_type, total, reserved, used)
				VALUES ($1, $2, $3, 0, 0)
				ON CONFLICT (tenant_id, resource_type)
				DO UPDATE SET total = GREATEST(EXCLUDED.total, resource_quota.reserved + resource_quota.used),
				              updated_at = NOW()
			`, tenantID, item.ResourceType, total); err != nil {
				return err
			}
		}

		types := make([]ports.ResourceType, len(items))
		for i, item := range items {
			types[i] = item.ResourceType
		}
		infos, err = q.quotaInfoByTypes(ctx, tx, tenantID, types)
		return err
	})
	if err != nil {
		if errors.Is(err, ports.ErrMetadataPlatformTxCommit) {
			return nil, ports.ErrQuotaUpdateUncertain
		}
		return nil, err
	}

	for i := range infos {
		if req, ok := reqTotals[infos[i].ResourceType]; ok && infos[i].Total > req {
			infos[i].Tightened = true
		}
	}
	return infos, nil
}

// GetTenantQuota 查询租户所有维度配额（平台管理员）。自开 WithPlatformTx。
// JOIN resource_quota_meta 返回 unit/display_name/is_discrete，ORDER BY resource_type。
// 租户不存在返回 ports.ErrTenantNotFound（handler 映射 404 TENANT_NOT_FOUND）；
// 租户存在但无配额行时返回空 items（200）。
func (q *PostgresQuota) GetTenantQuota(ctx context.Context, tenantID string) ([]ports.QuotaInfo, error) {
	infos := []ports.QuotaInfo{}
	err := q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		if err := q.requireTenantExists(ctx, tx, tenantID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT q.tenant_id::text, q.resource_type, q.total, q.reserved, q.used,
			       m.unit, m.display_name, m.is_discrete, q.updated_at
			FROM resource_quota q
			JOIN resource_quota_meta m ON m.resource_type = q.resource_type
			WHERE q.tenant_id = $1::uuid
			ORDER BY q.resource_type
		`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var info ports.QuotaInfo
			var rt string
			if err := rows.Scan(&info.TenantID, &rt, &info.Total, &info.Reserved, &info.Used,
				&info.Unit, &info.DisplayName, &info.IsDiscrete, &info.UpdatedAt); err != nil {
				return err
			}
			info.ResourceType = ports.ResourceType(rt)
			infos = append(infos, info)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return infos, nil
}

// DeleteTenantQuota 整租户删除配额（平台管理员，清理语义）。自开 WithPlatformTx。
// 校验租户存在 → 删除 resource_reservations + resource_quota（不守卫 used/reserved）。
func (q *PostgresQuota) DeleteTenantQuota(ctx context.Context, tenantID string) error {
	return q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		if err := q.requireTenantExists(ctx, tx, tenantID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM resource_reservations WHERE tenant_id = $1::uuid`, tenantID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM resource_quota WHERE tenant_id = $1::uuid`, tenantID); err != nil {
			return err
		}
		return nil
	})
}

// ListQuotaMeta 查询可用配额维度目录（平台管理员）。自开 WithPlatformTx。
// 只返回 enabled=true 的维度，ORDER BY resource_type。
func (q *PostgresQuota) ListQuotaMeta(ctx context.Context) ([]ports.QuotaMeta, error) {
	metas := []ports.QuotaMeta{}
	err := q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, `
			SELECT resource_type, display_name, unit, default_quota, is_discrete
			FROM resource_quota_meta
			WHERE enabled = true
			ORDER BY resource_type
		`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var meta ports.QuotaMeta
			var rt string
			if err := rows.Scan(&rt, &meta.DisplayName, &meta.Unit, &meta.DefaultQuota, &meta.IsDiscrete); err != nil {
				return err
			}
			meta.ResourceType = ports.ResourceType(rt)
			metas = append(metas, meta)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return metas, nil
}

// quotaInfoByTypes 在平台事务内回读指定维度集合的配额行，JOIN meta 返回带展示信息的
// QuotaInfo。用于 Create/Update 之后回读涉及维度。
func (q *PostgresQuota) quotaInfoByTypes(ctx context.Context, tx ports.MetadataTx, tenantID string, types []ports.ResourceType) ([]ports.QuotaInfo, error) {
	infos := make([]ports.QuotaInfo, 0, len(types))
	rtStrings := make([]string, len(types))
	for i, rt := range types {
		rtStrings[i] = string(rt)
	}
	rows, err := tx.Query(ctx, `
		SELECT q.tenant_id::text, q.resource_type, q.total, q.reserved, q.used,
		       m.unit, m.display_name, m.is_discrete, q.updated_at
		FROM resource_quota q
		JOIN resource_quota_meta m ON m.resource_type = q.resource_type
		WHERE q.tenant_id = $1::uuid AND q.resource_type::text = ANY($2)
		ORDER BY q.resource_type
	`, tenantID, rtStrings)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var info ports.QuotaInfo
		var rt string
		if err := rows.Scan(&info.TenantID, &rt, &info.Total, &info.Reserved, &info.Used,
			&info.Unit, &info.DisplayName, &info.IsDiscrete, &info.UpdatedAt); err != nil {
			return nil, err
		}
		info.ResourceType = ports.ResourceType(rt)
		infos = append(infos, info)
	}
	return infos, rows.Err()
}
