package internal

import (
	"context"
	"log/slog"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// Reconciler 周期校准采集 ticker 与 workload_instances 实际状态的偏差。
//
// 事件驱动停止链路（consumer 收到 stopped/failed/deleted → StopCollection）依赖
// 外部发布方可靠投递；发布缺失/丢失时，deleted/failed/stopped 实例的 ticker 会泄漏，
// 持续对不存在的资源采集（报错刷屏，且 deleted GPU 实例继续写入计费数据）。
// Reconciler 以 DB 为唯一真相源兜底：周期查询 state='running' 的实例集合，
// 调 MeteringCollectionService.StopStale 停止进程内已非 running 的采集。
type Reconciler struct {
	metadataStore ports.MetadataStore
	metering      ports.MeteringCollectionService
	logger        *slog.Logger
	interval      time.Duration
}

// NewReconciler 创建 reconciler。
// metadataStore: 用 WithPlatformTx 绕 RLS 跨租户查询 workload_instances。
// metering: 采集生命周期控制服务，StopStale 停止非 running 采集。
// logger: 结构化日志记录器，可为 nil（单测无需注入）。
// interval: 校准周期，Reconcile 按此间隔执行。
func NewReconciler(metadataStore ports.MetadataStore, metering ports.MeteringCollectionService, logger *slog.Logger, interval time.Duration) *Reconciler {
	return &Reconciler{
		metadataStore: metadataStore,
		metering:      metering,
		logger:        logger,
		interval:      interval,
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

// Reconcile 查询 DB 中当前 running 实例集合，停掉进程内已非 running 的采集。
// 用 WithPlatformTx 绕 RLS 跨租户查询，与 Rebuilder 同源（PG 为唯一 source of truth）。
func (r *Reconciler) Reconcile(ctx context.Context) error {
	const query = `SELECT instance_id FROM workload_instances WHERE state = 'running'`

	activeRefs := make(map[string]bool)
	err := r.metadataStore.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var instanceID string
			if err := rows.Scan(&instanceID); err != nil {
				return err
			}
			activeRefs[instanceID] = true
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
