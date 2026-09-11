package runtime

import (
	"context"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kubercloud/ani/pkg/ports"
)

// PostgresGPUDeviceSurface 实现 ports.GPUDeviceSurfaceStore：GPU 设备台账
// （状态覆盖 / 设备事件流）的 PG adapter。
// 两张表均为平台级台账（RLS platform_bypass 单策略），全部方法自开
// WithPlatformTx（RLS bypass），不依赖调用方租户上下文。
// 表结构见 deploy/migrations/20260911_001_gpu_device_surface.sql。
type PostgresGPUDeviceSurface struct {
	store ports.MetadataStore
}

// NewPostgresGPUDeviceSurface 构造 GPU 设备台账 adapter。
func NewPostgresGPUDeviceSurface(store ports.MetadataStore) *PostgresGPUDeviceSurface {
	return &PostgresGPUDeviceSurface{store: store}
}

var _ ports.GPUDeviceSurfaceStore = (*PostgresGPUDeviceSurface)(nil)

// SetDeviceOverlay 写入/更新人工状态覆盖（UPSERT）。
func (s *PostgresGPUDeviceSurface) SetDeviceOverlay(ctx context.Context, overlay ports.GPUDeviceOverlay) error {
	id, err := uuid.Parse(overlay.DeviceID)
	if err != nil {
		return ports.ErrInvalid
	}
	return s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO gpu_device_overlays (device_id, status, reason, updated_by, updated_at)
			VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NOW())
			ON CONFLICT (device_id) DO UPDATE
			SET status = EXCLUDED.status,
			    reason = EXCLUDED.reason,
			    updated_by = EXCLUDED.updated_by,
			    updated_at = NOW()
		`, id, overlay.Status, overlay.Reason, overlay.UpdatedBy)
		return err
	})
}

func scanOverlayRow(row ports.Row) (ports.GPUDeviceOverlay, error) {
	var o ports.GPUDeviceOverlay
	var reason, updatedBy *string
	var deviceID uuid.UUID
	err := row.Scan(&deviceID, &o.Status, &reason, &updatedBy, &o.UpdatedAt)
	if err != nil {
		return o, err
	}
	o.DeviceID = deviceID.String()
	o.Reason = derefString(reason)
	o.UpdatedBy = derefString(updatedBy)
	return o, nil
}

const overlaySelect = `SELECT device_id, status, reason, updated_by, updated_at FROM gpu_device_overlays`

// DeleteDeviceOverlay 清除人工覆盖（idle 翻转）；无覆盖返回 ErrNotFound。
func (s *PostgresGPUDeviceSurface) DeleteDeviceOverlay(ctx context.Context, deviceID string) error {
	id, err := uuid.Parse(deviceID)
	if err != nil {
		return ports.ErrInvalid
	}
	var deleted bool
	err = s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM gpu_device_overlays WHERE device_id = $1`, id)
		if err != nil {
			return err
		}
		deleted = tag.RowsAffected > 0
		return nil
	})
	if err != nil {
		return err
	}
	if !deleted {
		return ports.ErrNotFound
	}
	return nil
}

// GetDeviceOverlay 返回单卡覆盖；无覆盖返回 ErrNotFound。
func (s *PostgresGPUDeviceSurface) GetDeviceOverlay(ctx context.Context, deviceID string) (ports.GPUDeviceOverlay, error) {
	id, err := uuid.Parse(deviceID)
	if err != nil {
		return ports.GPUDeviceOverlay{}, ports.ErrInvalid
	}
	var overlay ports.GPUDeviceOverlay
	err = s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		var qerr error
		overlay, qerr = scanOverlayRow(tx.QueryRow(ctx, overlaySelect+" WHERE device_id = $1", id))
		return qerr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ports.GPUDeviceOverlay{}, ports.ErrNotFound
		}
		return ports.GPUDeviceOverlay{}, err
	}
	return overlay, nil
}

// ListDeviceOverlays 返回全部覆盖。
func (s *PostgresGPUDeviceSurface) ListDeviceOverlays(ctx context.Context) ([]ports.GPUDeviceOverlay, error) {
	var overlays []ports.GPUDeviceOverlay
	err := s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, overlaySelect+" ORDER BY updated_at DESC")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			o, err := scanOverlayRow(rows)
			if err != nil {
				return err
			}
			overlays = append(overlays, o)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return overlays, nil
}

// AppendDeviceEvent 追加设备事件。
func (s *PostgresGPUDeviceSurface) AppendDeviceEvent(ctx context.Context, event ports.GPUDeviceEvent) error {
	var deviceID *uuid.UUID
	if event.DeviceID != "" {
		id, err := uuid.Parse(event.DeviceID)
		if err != nil {
			return ports.ErrInvalid
		}
		deviceID = &id
	}
	return s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO gpu_device_events (device_id, node_name, gpu_type, event_type, reason, actor)
			VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), $4, NULLIF($5, ''), NULLIF($6, ''))
		`, deviceID, event.NodeName, event.GPUType, event.EventType, event.Reason, event.Actor)
		return err
	})
}

// ListDeviceEvents 按过滤条件返回事件（created_at 倒序）。
func (s *PostgresGPUDeviceSurface) ListDeviceEvents(ctx context.Context, filter ports.GPUDeviceEventFilter) ([]ports.GPUDeviceEvent, error) {
	query := eventSelect + ` WHERE 1=1`
	var args []any
	if filter.DeviceID != "" {
		id, err := uuid.Parse(filter.DeviceID)
		if err != nil {
			return nil, ports.ErrInvalid
		}
		args = append(args, id)
		query += " AND device_id = $" + itoa(len(args))
	}
	if filter.EventType != "" {
		args = append(args, filter.EventType)
		query += " AND event_type = $" + itoa(len(args))
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	args = append(args, limit)
	query += " ORDER BY created_at DESC LIMIT $" + itoa(len(args))

	var events []ports.GPUDeviceEvent
	err := s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEventRow(rows)
			if err != nil {
				return err
			}
			events = append(events, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

const eventSelect = `SELECT id, device_id, node_name, gpu_type, event_type, reason, actor, created_at FROM gpu_device_events`

func scanEventRow(row ports.Row) (ports.GPUDeviceEvent, error) {
	var e ports.GPUDeviceEvent
	var id uuid.UUID
	var deviceID *uuid.UUID
	var nodeName, gpuType, reason, actor *string
	err := row.Scan(&id, &deviceID, &nodeName, &gpuType, &e.EventType, &reason, &actor, &e.CreatedAt)
	if err != nil {
		return e, err
	}
	e.ID = id.String()
	e.DeviceID = derefUUIDString(deviceID)
	e.NodeName = derefString(nodeName)
	e.GPUType = derefString(gpuType)
	e.Reason = derefString(reason)
	e.Actor = derefString(actor)
	return e, nil
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefUUIDString(p *uuid.UUID) string {
	if p == nil {
		return ""
	}
	return p.String()
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
