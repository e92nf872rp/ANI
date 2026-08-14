package internal

import (
	"context"
	"log/slog"

	"github.com/kubercloud/ani/pkg/ports"
)

// Rebuilder 在 metering-service 启动时跨租户查所有 running 实例并重建 ticker。
//
// 用 WithPlatformTx 绕 RLS 跨租户查询 workload_instances，不新增真相源，
// PG 为唯一 source of truth。单实例 StartCollection 失败不阻塞，继续重建其余实例。
type Rebuilder struct {
	metadataStore ports.MetadataStore
	metering      ports.MeteringCollectionService
	logger        *slog.Logger
}

// NewRebuilder 创建 rebuilder。
// metadataStore: 用 WithPlatformTx 绕 RLS 跨租户查询 workload_instances。
// metering: 采集生命周期控制服务，对每个 running 实例调 StartCollection。
// logger: 结构化日志记录器，可为 nil（单测无需注入）。
func NewRebuilder(metadataStore ports.MetadataStore, metering ports.MeteringCollectionService, logger *slog.Logger) *Rebuilder {
	return &Rebuilder{
		metadataStore: metadataStore,
		metering:      metering,
		logger:        logger,
	}
}

// Rebuild 启动时跨租户查所有 running 实例并建 ticker（SPEC §5.1.5）。
//
// 用 WithPlatformTx 绕 RLS 查 workload_instances WHERE state='running'，
// 解析 gpu_status JSONB 获取 GPU 卡数，对每个实例调 buildSpec + StartCollection。
// 单个实例 StartCollection 失败不阻塞，记 Error 日志继续重建其余实例。
func (r *Rebuilder) Rebuild(ctx context.Context) error {
	const query = `SELECT tenant_id::text, instance_id, name, workload_kind, gpu_status
		FROM workload_instances
		WHERE state = 'running'
		ORDER BY updated_at ASC`

	var count int
	err := r.metadataStore.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var tenantID, instanceID, name, kind string
			var gpuStatusJSON []byte
			if err := rows.Scan(&tenantID, &instanceID, &name, &kind, &gpuStatusJSON); err != nil {
				return err
			}

			gpuCount := parseGPUCount(gpuStatusJSON)
			spec := buildSpec(tenantID, instanceID, name, kind, gpuCount)
			if err := r.metering.StartCollection(ctx, spec); err != nil {
				// 单实例失败不阻塞，记 Error 日志继续重建其余实例。
				r.safeLog(func(l *slog.Logger) {
					l.ErrorContext(ctx, "rebuild: StartCollection failed for instance, skipping",
						"instance_id", instanceID, "tenant_id", tenantID, "err", err)
				})
			}
			count++
		}
		return rows.Err()
	})
	if err != nil {
		r.safeLog(func(l *slog.Logger) {
			l.ErrorContext(ctx, "rebuild: query running instances failed", "err", err)
		})
		return err
	}

	r.safeLog(func(l *slog.Logger) {
		l.InfoContext(ctx, "rebuild done", "running_instances", count)
	})
	return nil
}

// safeLog 在 logger 为 nil 时跳过日志输出，便于单测无需注入 logger。
func (r *Rebuilder) safeLog(fn func(*slog.Logger)) {
	if r.logger != nil {
		fn(r.logger)
	}
}
