package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// testService 构造一个带可注入 persistFn 的 meteringCollectionService，
// 避免依赖真实 *pgxpool.Pool。
func testService(collectAll CollectAllFunc, persist persistFunc) *meteringCollectionService {
	return &meteringCollectionService{
		tickers:       make(map[string]*time.Ticker),
		stopChs:       make(map[string]chan struct{}),
		specs:         make(map[string]*ports.CollectionSpec),
		everCollected: make(map[string]bool),
		db:            nil,
		logger:         nil,
		collectAll:     collectAll,
		persistFn:      persist,
	}
}

func gpuSpec(count int) *ports.GPUEventSpec {
	return &ports.GPUEventSpec{Count: count}
}

func gpuContainerSpec(ref, tenant string, interval int, gpuCount int) ports.CollectionSpec {
	dims := []ports.CollectionDimension{
		{ResourceType: ports.MeteringResourceInstanceGPUSeconds, Source: "dcgm_gpu"},
		{ResourceType: ports.MeteringResourceInstanceCPUSeconds, Source: "kubelet_cpu"},
		{ResourceType: ports.MeteringResourceInstanceMemorySeconds, Source: "kubelet_mem"},
	}
	spec := ports.CollectionSpec{
		ResourceRef:  ref,
		TenantID:     tenant,
		WorkloadKind: "gpu_container",
		Dimensions:   dims,
		IntervalSec:  interval,
	}
	if gpuCount > 0 {
		spec.GPUSpec = gpuSpec(gpuCount)
	}
	return spec
}

// --- Start 幂等 ---

func TestStartCollectionIdempotent(t *testing.T) {
	svc := testService(nil, nil)

	spec := gpuContainerSpec("inst-001", "tenant-a", 60, 2)

	if err := svc.StartCollection(context.Background(), spec); err != nil {
		t.Fatalf("首次 StartCollection 返回错误: %v", err)
	}

	// 再次 Start 同一 resourceRef 应返回 nil（幂等 no-op）。
	if err := svc.StartCollection(context.Background(), spec); err != nil {
		t.Fatalf("重复 StartCollection 应幂等返回 nil, 实际: %v", err)
	}

	// 验证进程内只有一个 ticker。
	svc.mu.Lock()
	got := len(svc.tickers)
	svc.mu.Unlock()
	if got != 1 {
		t.Errorf("期望 1 个 ticker, 实际 %d", got)
	}

	// 清理：停止采集。
	_ = svc.StopCollection(context.Background(), "inst-001")
}

// --- Stop 幂等 ---

func TestStopCollectionIdempotent(t *testing.T) {
	svc := testService(nil, nil)

	// 未启动就 Stop，应返回 nil（幂等 no-op）。
	if err := svc.StopCollection(context.Background(), "nonexistent"); err != nil {
		t.Fatalf("Stop 未启动的实例应幂等返回 nil, 实际: %v", err)
	}

	spec := gpuContainerSpec("inst-002", "tenant-b", 60, 1)
	_ = svc.StartCollection(context.Background(), spec)

	// 第一次 Stop。
	if err := svc.StopCollection(context.Background(), "inst-002"); err != nil {
		t.Fatalf("首次 Stop 返回错误: %v", err)
	}

	// 再次 Stop 同一 ref，应返回 nil（幂等 no-op，map 已清理）。
	if err := svc.StopCollection(context.Background(), "inst-002"); err != nil {
		t.Fatalf("重复 Stop 应幂等返回 nil, 实际: %v", err)
	}

	svc.mu.Lock()
	got := len(svc.tickers)
	svc.mu.Unlock()
	if got != 0 {
		t.Errorf("期望 0 个 ticker, 实际 %d", got)
	}
}

// --- StartedAt.IsZero() 时设为 time.Now() ---

func TestStartCollectionSetsStartedAtWhenZero(t *testing.T) {
	svc := testService(nil, nil)

	spec := gpuContainerSpec("inst-003", "tenant-c", 60, 1)
	// 确认 StartedAt 为零值。
	if !spec.StartedAt.IsZero() {
		t.Fatalf("前置条件：StartedAt 应为零值")
	}

	_ = svc.StartCollection(context.Background(), spec)

	svc.mu.Lock()
	stored := svc.specs["inst-003"]
	svc.mu.Unlock()
	if stored == nil {
		t.Fatalf("spec 未存储")
	}
	if stored.StartedAt.IsZero() {
		t.Errorf("StartCollection 应在 StartedAt.IsZero() 时设为 time.Now()")
	}

	_ = svc.StopCollection(context.Background(), "inst-003")
}

func TestStartCollectionPreservesNonZeroStartedAt(t *testing.T) {
	svc := testService(nil, nil)

	preset := time.Now().Add(-10 * time.Minute)
	spec := gpuContainerSpec("inst-004", "tenant-d", 60, 1)
	spec.StartedAt = preset

	_ = svc.StartCollection(context.Background(), spec)

	svc.mu.Lock()
	stored := svc.specs["inst-004"]
	svc.mu.Unlock()
	if stored == nil {
		t.Fatalf("spec 未存储")
	}
	if !stored.StartedAt.Equal(preset) {
		t.Errorf("StartCollection 不应覆盖非零 StartedAt, 期望 %v, 实际 %v", preset, stored.StartedAt)
	}

	_ = svc.StopCollection(context.Background(), "inst-004")
}

// --- 保底采集触发 ---

func TestStopCollectionTriggersCollectFullLifetime(t *testing.T) {
	var persisted []ports.MeteringUsageRecord
	var persistMu sync.Mutex
	persist := func(ctx context.Context, tenantID string, records []ports.MeteringUsageRecord) error {
		persistMu.Lock()
		persisted = append(persisted, records...)
		persistMu.Unlock()
		return nil
	}

	svc := testService(nil, persist)

	// 构造一个短生命周期的 GPU 实例：StartedAt 设为 30s 前，everCollected=false。
	spec := gpuContainerSpec("inst-005", "tenant-e", 60, 2)
	spec.StartedAt = time.Now().Add(-30 * time.Second)

	_ = svc.StartCollection(context.Background(), spec)

	// 立即 Stop（未到首个 ticker 周期，everCollected 仍为 false）→ 触发保底采集。
	_ = svc.StopCollection(context.Background(), "inst-005")

	persistMu.Lock()
	got := len(persisted)
	persistMu.Unlock()
	if got == 0 {
		t.Fatalf("保底采集应产出记录并持久化, 实际 0 条")
	}

	// GPU 维度应产出 1 条记录: quantity = 2 * 30 ≈ 60。
	persistMu.Lock()
	defer persistMu.Unlock()
	var gpuRec *ports.MeteringUsageRecord
	for i := range persisted {
		if persisted[i].ResourceType == ports.MeteringResourceInstanceGPUSeconds {
			gpuRec = &persisted[i]
			break
		}
	}
	if gpuRec == nil {
		t.Fatalf("保底采集应包含 GPU 维度记录")
	}
	if gpuRec.TenantID != "tenant-e" {
		t.Errorf("GPU 记录 TenantID = %q, 期望 tenant-e", gpuRec.TenantID)
	}
	if gpuRec.ResourceRef != "inst-005" {
		t.Errorf("GPU 记录 ResourceRef = %q, 期望 inst-005", gpuRec.ResourceRef)
	}
	if gpuRec.Unit != "gpu_second" {
		t.Errorf("GPU 记录 Unit = %q, 期望 gpu_second", gpuRec.Unit)
	}
	// 30s elapsed, 2 cards → ~60 gpu_seconds, 允许小误差。
	if gpuRec.TotalQuantity < 59 || gpuRec.TotalQuantity > 61 {
		t.Errorf("GPU 记录 TotalQuantity = %v, 期望约 60", gpuRec.TotalQuantity)
	}
}

// --- 保底采集不触发（everCollected=true）---

func TestStopCollectionSkipsCollectFullLifetimeWhenEverCollected(t *testing.T) {
	var persisted []ports.MeteringUsageRecord
	var persistMu sync.Mutex
	persist := func(ctx context.Context, tenantID string, records []ports.MeteringUsageRecord) error {
		persistMu.Lock()
		persisted = append(persisted, records...)
		persistMu.Unlock()
		return nil
	}

	// collectAll 产出记录，让 runCollectionLoop 标记 everCollected=true。
	collectAll := func(ctx context.Context, spec ports.CollectionSpec, logger *slog.Logger) ([]ports.MeteringUsageRecord, error) {
		return []ports.MeteringUsageRecord{
			{
				TenantID:      spec.TenantID,
				ResourceRef:   spec.ResourceRef,
				ResourceType:  ports.MeteringResourceInstanceGPUSeconds,
				TotalQuantity: 120,
				Unit:          "gpu_second",
				Period:        time.Now().Format("2006-01-02T15:04"),
			},
		}, nil
	}

	svc := testService(collectAll, persist)

	spec := gpuContainerSpec("inst-006", "tenant-f", 1, 2) // 1s ticker 快速触发
	spec.StartedAt = time.Now().Add(-30 * time.Second)

	_ = svc.StartCollection(context.Background(), spec)

	// 等待至少一个 ticker 周期让 collectAll 执行。
	time.Sleep(1500 * time.Millisecond)

	// 确认 everCollected 已设为 true。
	svc.mu.Lock()
	ever := svc.everCollected["inst-006"]
	svc.mu.Unlock()
	if !ever {
		t.Fatalf("前置条件：everCollected 应为 true")
	}

	// 记录保底采集前的持久化数量。
	persistMu.Lock()
	before := len(persisted)
	persistMu.Unlock()

	// Stop 不应触发保底采集（everCollected=true）。
	_ = svc.StopCollection(context.Background(), "inst-006")

	// 等待可能的保底采集 goroutine 完成。
	time.Sleep(200 * time.Millisecond)

	persistMu.Lock()
	after := len(persisted)
	persistMu.Unlock()
	if after != before {
		t.Errorf("everCollected=true 时 Stop 不应触发保底采集, 持久化记录 before=%d after=%d", before, after)
	}
}

// --- collectFullLifetime 计算 ---

func TestCollectFullLifetimeGPU(t *testing.T) {
	svc := testService(nil, nil)

	spec := gpuContainerSpec("inst-007", "tenant-g", 60, 4)
	spec.StartedAt = time.Now().Add(-60 * time.Second)

	records, err := svc.collectFullLifetime(context.Background(), spec)
	if err != nil {
		t.Fatalf("collectFullLifetime 返回错误: %v", err)
	}

	// GPU 维度应产出 1 条: 4 cards * 60s = 240。
	var gpuRec *ports.MeteringUsageRecord
	for i := range records {
		if records[i].ResourceType == ports.MeteringResourceInstanceGPUSeconds {
			gpuRec = &records[i]
			break
		}
	}
	if gpuRec == nil {
		t.Fatalf("应包含 GPU 维度记录")
	}
	if gpuRec.TotalQuantity < 239 || gpuRec.TotalQuantity > 241 {
		t.Errorf("GPU TotalQuantity = %v, 期望约 240 (4 cards * 60s)", gpuRec.TotalQuantity)
	}
	// Period 应为分钟对齐格式。
	if _, err := time.Parse("2006-01-02T15:04", gpuRec.Period); err != nil {
		t.Errorf("Period 格式不是分钟对齐: %q, err=%v", gpuRec.Period, err)
	}
}

func TestCollectFullLifetimeGPUNilSkips(t *testing.T) {
	svc := testService(nil, nil)

	// GPU 卡数为 0 → GPUSpec 为 nil → GPU 维度跳过。
	spec := gpuContainerSpec("inst-008", "tenant-h", 60, 0)
	spec.StartedAt = time.Now().Add(-60 * time.Second)

	records, err := svc.collectFullLifetime(context.Background(), spec)
	if err != nil {
		t.Fatalf("collectFullLifetime 返回错误: %v", err)
	}
	for _, r := range records {
		if r.ResourceType == ports.MeteringResourceInstanceGPUSeconds {
			t.Errorf("GPUSpec=nil 时不应产出 GPU 维度记录")
		}
	}
}

func TestCollectFullLifetimeZeroElapsed(t *testing.T) {
	svc := testService(nil, nil)

	// StartedAt 设为未来时间 → elapsed <= 0 → 返回 nil。
	spec := gpuContainerSpec("inst-009", "tenant-i", 60, 2)
	spec.StartedAt = time.Now().Add(10 * time.Second)

	records, err := svc.collectFullLifetime(context.Background(), spec)
	if err != nil {
		t.Fatalf("collectFullLifetime 返回错误: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("elapsed<=0 时应返回空记录, 实际 %d 条", len(records))
	}
}

// --- persistRecords ON CONFLICT（通过 mock persistFn 验证调用）---

func TestPersistRecordsCallsInjectFn(t *testing.T) {
	called := false
	var gotRecords []ports.MeteringUsageRecord
	persist := func(ctx context.Context, tenantID string, records []ports.MeteringUsageRecord) error {
		called = true
		gotRecords = records
		if tenantID != "tenant-j" {
			t.Errorf("tenantID = %q, 期望 tenant-j", tenantID)
		}
		return nil
	}

	svc := testService(nil, persist)

	records := []ports.MeteringUsageRecord{
		{
			TenantID:      "tenant-j",
			ResourceRef:   "inst-010",
			ResourceType:  ports.MeteringResourceInstanceGPUSeconds,
			TotalQuantity: 120,
			Unit:          "gpu_second",
			Period:        "2026-01-01T00:00",
		},
	}
	if err := svc.persistRecords(context.Background(), "tenant-j", records); err != nil {
		t.Fatalf("persistRecords 返回错误: %v", err)
	}
	if !called {
		t.Errorf("persistFn 未被调用")
	}
	if len(gotRecords) != 1 {
		t.Errorf("收到 %d 条记录, 期望 1", len(gotRecords))
	}
}

func TestPersistRecordsEmptyNoop(t *testing.T) {
	called := false
	persist := func(ctx context.Context, tenantID string, records []ports.MeteringUsageRecord) error {
		called = true
		return nil
	}
	svc := testService(nil, persist)

	if err := svc.persistRecords(context.Background(), "tenant-k", nil); err != nil {
		t.Fatalf("persistRecords 空记录应返回 nil, 实际: %v", err)
	}
	if called {
		t.Errorf("空记录不应调用 persistFn")
	}
}

func TestPersistRecordsPropagatesError(t *testing.T) {
	errSentinel := errors.New("db down")
	persist := func(ctx context.Context, tenantID string, records []ports.MeteringUsageRecord) error {
		return errSentinel
	}
	svc := testService(nil, persist)

	records := []ports.MeteringUsageRecord{
		{TenantID: "tenant-l", ResourceRef: "inst-011", ResourceType: ports.MeteringResourceInstanceGPUSeconds, TotalQuantity: 60, Unit: "gpu_second", Period: "2026-01-01T00:00"},
	}
	err := svc.persistRecords(context.Background(), "tenant-l", records)
	if !errors.Is(err, errSentinel) {
		t.Errorf("persistRecords 应透传错误, 期望 %v, 实际 %v", errSentinel, err)
	}
}

// --- persistRecords DB nil ---

func TestPersistRecordsDBNilReturnsError(t *testing.T) {
	svc := testService(nil, nil) // persistFn=nil, db=nil

	records := []ports.MeteringUsageRecord{
		{TenantID: "tenant-m", ResourceRef: "inst-012", ResourceType: ports.MeteringResourceInstanceGPUSeconds, TotalQuantity: 60, Unit: "gpu_second", Period: "2026-01-01T00:00"},
	}
	err := svc.persistRecords(context.Background(), "tenant-m", records)
	if err == nil {
		t.Errorf("db=nil 且 persistFn=nil 时 persistRecords 应返回错误")
	}
}
