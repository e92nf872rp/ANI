package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/pkg/ports"
)

// CollectAllFunc 是周期采集函数类型，签名与 pkg/adapters/metering.CollectAll 一致。
// PR-M2 落地后由 main.go 注入真实实现；PR-M1 阶段可为 nil（runCollectionLoop 跳过采集，等待 PR-M2 接入）。
type CollectAllFunc func(ctx context.Context, spec ports.CollectionSpec, logger *slog.Logger) ([]ports.MeteringUsageRecord, error)

// persistFunc 是记录持久化函数类型，便于单测注入 mock 替代真实 DB 交互。
type persistFunc func(ctx context.Context, tenantID string, records []ports.MeteringUsageRecord) error

// meteringCollectionService 实现 ports.MeteringCollectionService，
// 持有 *pgxpool.Pool 直连 Core DB，管理 per-instance ticker 并写入 metering_usage_records。
type meteringCollectionService struct {
	mu            sync.Mutex
	tickers       map[string]*time.Ticker
	stopChs       map[string]chan struct{}
	specs         map[string]*ports.CollectionSpec
	everCollected map[string]bool
	db            *pgxpool.Pool
	logger        *slog.Logger
	collectAll    CollectAllFunc
	persistFn     persistFunc // 测试注入；nil 时 persistRecords 使用 s.db 默认实现
}

// NewMeteringCollectionService 创建 meteringCollectionService。
// db: 直连 Core DB 的连接池（连接用户需是 ani_metering_writer 角色成员）。
// logger: 结构化日志记录器，可为 nil。
// collectAll: 周期采集函数，PR-M2 落地后注入真实实现；nil 时 runCollectionLoop 跳过采集。
func NewMeteringCollectionService(db *pgxpool.Pool, logger *slog.Logger, collectAll CollectAllFunc) ports.MeteringCollectionService {
	return &meteringCollectionService{
		tickers:       make(map[string]*time.Ticker),
		stopChs:       make(map[string]chan struct{}),
		specs:         make(map[string]*ports.CollectionSpec),
		everCollected: make(map[string]bool),
		db:            db,
		logger:        logger,
		collectAll:    collectAll,
	}
}

// StartCollection 启动指定资源的周期采集。幂等语义：进程内 map 已有 ticker 时返回 nil（no-op）。
func (s *meteringCollectionService) StartCollection(ctx context.Context, spec ports.CollectionSpec) error {
	ref := spec.ResourceRef

	s.mu.Lock()
	if _, exists := s.tickers[ref]; exists {
		s.mu.Unlock()
		return nil // 幂等 no-op
	}
	if spec.StartedAt.IsZero() {
		spec.StartedAt = time.Now()
	}
	interval := spec.IntervalSec
	if interval <= 0 {
		interval = 60
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	stopCh := make(chan struct{})
	s.tickers[ref] = ticker
	s.stopChs[ref] = stopCh
	s.specs[ref] = &spec
	s.everCollected[ref] = false
	s.mu.Unlock()

	go s.runCollectionLoop(spec, ticker, stopCh)
	return nil
}

// runCollectionLoop 是采集循环 goroutine。
// ticker.C 触发时调用 CollectAll 采集 → persistRecords 写 DB；
// stopCh 关闭时停止 ticker 并退出。
// CollectAll 失败时记 Error 日志并 continue（不停 ticker，下个周期重试）。
func (s *meteringCollectionService) runCollectionLoop(spec ports.CollectionSpec, ticker *time.Ticker, stopCh chan struct{}) {
	ref := spec.ResourceRef
	for {
		select {
		case <-ticker.C:
			ctx := context.Background()
			if s.collectAll == nil {
				continue
			}
			records, err := s.collectAll(ctx, spec, s.logger)
			if err != nil {
				s.safeLog(func(l *slog.Logger) {
					l.ErrorContext(ctx, "runCollectionLoop: CollectAll failed", "resource_ref", ref, "err", err)
				})
				continue
			}
			if err := s.persistRecords(ctx, spec.TenantID, records); err != nil {
				s.safeLog(func(l *slog.Logger) {
					l.ErrorContext(ctx, "runCollectionLoop: persistRecords failed", "resource_ref", ref, "err", err)
				})
				continue
			}
			if len(records) > 0 {
				s.mu.Lock()
				s.everCollected[ref] = true
				s.mu.Unlock()
			}
		case <-stopCh:
			ticker.Stop()
			return
		}
	}
}

// StopCollection 停止指定资源的周期采集。幂等语义：无 ticker 时返回 nil（no-op）。
// 锁外执行保底采集：everCollected[ref]==false && spec != nil 时调 collectFullLifetime 补采一次全周期量。
func (s *meteringCollectionService) StopCollection(ctx context.Context, resourceRef string) error {
	s.mu.Lock()
	ticker, hasTicker := s.tickers[resourceRef]
	if !hasTicker {
		s.mu.Unlock()
		return nil // 幂等 no-op
	}
	stopCh := s.stopChs[resourceRef]
	ever := s.everCollected[resourceRef]
	spec := s.specs[resourceRef]

	// 缩小锁范围：快速清理 map entries，慢 I/O 在锁外执行。
	ticker.Stop()
	close(stopCh)
	delete(s.tickers, resourceRef)
	delete(s.stopChs, resourceRef)
	delete(s.everCollected, resourceRef)
	delete(s.specs, resourceRef)
	s.mu.Unlock()

	// 锁外保底采集：该实例从未产出周期记录时，补采一次全周期量。
	if !ever && spec != nil {
		records, err := s.collectFullLifetime(ctx, *spec)
		if err != nil {
			s.safeLog(func(l *slog.Logger) {
				l.ErrorContext(ctx, "StopCollection: collectFullLifetime failed", "resource_ref", resourceRef, "err", err)
			})
			return nil
		}
		if len(records) > 0 {
			if err := s.persistRecords(ctx, spec.TenantID, records); err != nil {
				s.safeLog(func(l *slog.Logger) {
					l.ErrorContext(ctx, "StopCollection: persistRecords failed", "resource_ref", resourceRef, "err", err)
				})
			}
		}
	}
	return nil
}

// collectFullLifetime 按从 Start 到 Stop 的完整存活时长计算一次性量。
// 仅在 StopCollection 时且 !everCollected（该实例从未产出周期记录）时触发。
// Period 用 Stop 时刻分钟对齐。产出的记录若与已有周期记录碰撞，ON CONFLICT DO NOTHING 兜底丢弃。
func (s *meteringCollectionService) collectFullLifetime(ctx context.Context, spec ports.CollectionSpec) ([]ports.MeteringUsageRecord, error) {
	elapsed := time.Since(spec.StartedAt)
	if elapsed <= 0 {
		return nil, nil
	}
	// period 按 UTC 写入，与查询侧 to_char(... AT TIME ZONE 'UTC') 形成完整 UTC 契约。
	period := time.Now().UTC().Format("2006-01-02T15:04")
	elapsedSec := elapsed.Seconds()
	var out []ports.MeteringUsageRecord
	for _, dim := range spec.Dimensions {
		switch dim.ResourceType {
		case ports.MeteringResourceInstanceGPUSeconds:
			// GPU 占用时长不查 DCGM，纯持有时长计算。
			if spec.GPUSpec == nil {
				continue
			}
			quantity := float64(spec.GPUSpec.Count) * elapsedSec
			out = append(out, ports.MeteringUsageRecord{
				TenantID:      spec.TenantID,
				ResourceRef:   spec.ResourceRef,
				ResourceType:  dim.ResourceType,
				TotalQuantity: quantity,
				Unit:          "gpu_second",
				Period:        period,
			})
		case ports.MeteringResourceInstanceCPUSeconds:
			// CPU 维度需要 Prometheus 查询，PR-M2 Collector 接入后完善。
		case ports.MeteringResourceInstanceMemorySeconds:
			// Mem 维度需要 Prometheus 查询，PR-M2 Collector 接入后完善。
		}
	}
	return out, nil
}

// persistRecords 将采集记录批量写入 metering_usage_records。
// 使用 SET ROLE ani_metering_writer（BYPASSRLS）绕过 RLS 跨租户写入，
// INSERT 用 ON CONFLICT DO NOTHING 兜底写入幂等。
// INSERT 列名用 quantity（对应 Go struct TotalQuantity 字段）。
func (s *meteringCollectionService) persistRecords(ctx context.Context, tenantID string, records []ports.MeteringUsageRecord) error {
	if len(records) == 0 {
		return nil
	}
	if s.persistFn != nil {
		return s.persistFn(ctx, tenantID, records)
	}
	if s.db == nil {
		return fmt.Errorf("persistRecords: db is nil")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("persistRecords: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// 切换到 ani_metering_writer（BYPASSRLS），绕过 RLS 跨租户写入
	if _, err := tx.Exec(ctx, "SET ROLE ani_metering_writer"); err != nil {
		return fmt.Errorf("persistRecords: set role: %w", err)
	}
	const insertSQL = `
		INSERT INTO metering_usage_records (tenant_id, resource_ref, resource_type, period, quantity, unit)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, resource_ref, resource_type, period) DO NOTHING
	`
	for _, r := range records {
		_, err := tx.Exec(ctx, insertSQL, tenantID, r.ResourceRef, string(r.ResourceType), r.Period, r.TotalQuantity, r.Unit)
		if err != nil {
			return fmt.Errorf("persistRecords: insert %s/%s/%s: %w", r.ResourceRef, r.ResourceType, r.Period, err)
		}
	}
	// 恢复原角色，避免影响连接池中后续查询
	if _, err := tx.Exec(ctx, "RESET ROLE"); err != nil {
		return fmt.Errorf("persistRecords: reset role: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("persistRecords: commit: %w", err)
	}
	return nil
}

// safeLog 在 logger 为 nil 时跳过日志输出，便于单测无需注入 logger。
func (s *meteringCollectionService) safeLog(fn func(*slog.Logger)) {
	if s.logger != nil {
		fn(s.logger)
	}
}
