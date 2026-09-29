package internal

import (
	"context"
	"log/slog"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// Reconciler 周期校准采集 ticker 与 workload_instances 实际状态的偏差。
//
// 事件驱动链路（consumer 收到 running → StartCollection；stopped/failed/deleted
// → StopCollection）依赖外部发布方可靠投递；发布缺失/丢失时会出现两个方向的
// 偏差：running 实例没有 ticker（漏计量），deleted/failed/stopped 实例的
// ticker 泄漏（对不存在的资源采集刷屏，且继续写入计费数据）。
// Reconciler 以 DB 为唯一真相源兜底：周期查询 state='running' 的实例集合，
// 对每个实例调幂等的 StartCollection 补启动缺失 ticker（已有 ticker 则 no-op），
// 再调 MeteringCollectionService.StopStale 停止进程内已非 running 的采集。
type Reconciler struct {
	metadataStore ports.MetadataStore
	metering      ports.MeteringCollectionService
	logger        *slog.Logger
	interval      time.Duration
	intervalSec   int
}

// NewReconciler 创建 reconciler。
// metadataStore: 用 WithPlatformTx 绕 RLS 跨租户查询 workload_instances。
// metering: 采集生命周期控制服务，StartCollection 补启动、StopStale 停止非 running 采集。
// logger: 结构化日志记录器，可为 nil（单测无需注入）。
// interval: 校准周期，Reconcile 按此间隔执行。
// intervalSec: 采集周期（秒），补启动时传入 buildSpec 设置 CollectionSpec.IntervalSec。
func NewReconciler(metadataStore ports.MetadataStore, metering ports.MeteringCollectionService, logger *slog.Logger, interval time.Duration, intervalSec int) *Reconciler {
	return &Reconciler{
		metadataStore: metadataStore,
		metering:      metering,
		logger:        logger,
		interval:      interval,
		intervalSec:   intervalSec,
	}
}

// Run 启动周期校准循环，直到 ctx 取消。首轮立即执行一次，随后按 interval 间隔执行。
func (r *Reconciler) Run(ctx context.Context) {
	if r.interval <= 0 {
		r.interval = 5 * time.Minute
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		if err := r.Reconcile(ctx); err != nil {
			r.safeLog(func(l *slog.Logger) {
				l.ErrorContext(ctx, "reconcile: run failed", "err", err)
			})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Reconcile 查询 DB 中当前 running 实例集合，补启动缺失的采集并停掉进程内
// 已非 running 的采集。用 WithPlatformTx 绕 RLS 跨租户查询，与 Rebuilder 同源
// （PG 为唯一 source of truth）。StartCollection 幂等：进程内已有 ticker 时 no-op。
func (r *Reconciler) Reconcile(ctx context.Context) error {
	const query = `SELECT tenant_id::text, instance_id, name, workload_kind, gpu_status
		FROM workload_instances
		WHERE state = 'running'`

	activeRefs := make(map[string]bool)
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
			activeRefs[instanceID] = true

			// 补启动：事件 running 缺失/丢失时进程内可能没有 ticker。
			// 已有 ticker 时 StartCollection 幂等 no-op。
			spec := buildSpec(tenantID, instanceID, name, kind, parseGPUCount(gpuStatusJSON), r.intervalSec)
			if err := r.metering.StartCollection(ctx, spec); err != nil {
				r.safeLog(func(l *slog.Logger) {
					l.ErrorContext(ctx, "reconcile: StartCollection failed for instance, skipping",
						"instance_id", instanceID, "tenant_id", tenantID, "err", err)
				})
			}
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}

	return r.metering.StopStale(ctx, activeRefs)
}

// safeLog 在 logger 为 nil 时跳过日志输出，便于单测无需注入 logger。
func (r *Reconciler) safeLog(fn func(*slog.Logger)) {
	if r.logger != nil {
		fn(r.logger)
	}
}
