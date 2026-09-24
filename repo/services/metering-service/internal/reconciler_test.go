package internal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// captureStopStaleService 实现 ports.MeteringCollectionService，
// 捕获 StopStale 收到的 activeRefs 和 StartCollection 收到的 specs，
// 便于断言 Reconcile 补启动 + 停止双向校准。
type captureStopStaleService struct {
	startSpecs []ports.CollectionSpec
	activeRefs map[string]bool
	stopErr    error // StopStale 返回的 error
}

func (c *captureStopStaleService) StartCollection(ctx context.Context, spec ports.CollectionSpec) error {
	c.startSpecs = append(c.startSpecs, spec)
	return nil
}

func (c *captureStopStaleService) StopCollection(ctx context.Context, resourceRef string) error {
	return nil
}

func (c *captureStopStaleService) StopStale(ctx context.Context, activeRefs map[string]bool) error {
	c.activeRefs = activeRefs
	return c.stopErr
}

// --- AC: running 实例 ID 收集进 activeRefs ---

func TestReconcileCollectsRunningInstanceIDs(t *testing.T) {
	rows := &mockRows{
		rows: [][]any{
			{"tenant-a", "inst-001", "app-1", "gpu_container", []byte(`{"Count": 1}`)},
			{"tenant-b", "inst-002", "app-2", "vm", []byte(`{}`)},
			{"tenant-c", "inst-003", "app-3", "container", []byte(`{}`)},
		},
	}
	store := &mockMetadataStore{tx: &mockTx{rows: rows}}
	svc := &captureStopStaleService{}
	r := NewReconciler(store, svc, nil, 5*time.Minute, 60)

	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile 返回错误: %v", err)
	}

	if !store.called {
		t.Errorf("Reconcile 应调用 WithPlatformTx")
	}
	if svc.activeRefs == nil {
		t.Fatalf("StopStale 未被调用")
	}
	for _, id := range []string{"inst-001", "inst-002", "inst-003"} {
		if !svc.activeRefs[id] {
			t.Errorf("activeRefs 缺少 running 实例 %s", id)
		}
	}
	if len(svc.activeRefs) != 3 {
		t.Errorf("activeRefs 长度 = %d, 期望 3", len(svc.activeRefs))
	}
}

// --- AC: running 实例补启动 StartCollection（事件缺失兜底）---

func TestReconcileStartsCollectionForRunningInstances(t *testing.T) {
	rows := &mockRows{
		rows: [][]any{
			{"tenant-a", "inst-001", "demo-gpu-app", "gpu_container", []byte(`{"Count": 2}`)},
			{"tenant-b", "inst-002", "demo-vm", "vm", []byte(`{}`)},
		},
	}
	store := &mockMetadataStore{tx: &mockTx{rows: rows}}
	svc := &captureStopStaleService{}
	r := NewReconciler(store, svc, nil, 5*time.Minute, 60)

	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile 返回错误: %v", err)
	}

	if len(svc.startSpecs) != 2 {
		t.Fatalf("StartCollection 调用次数 = %d, 期望 2", len(svc.startSpecs))
	}
	spec0 := svc.startSpecs[0]
	if spec0.ResourceRef != "inst-001" {
		t.Errorf("spec0.ResourceRef = %q, 期望 inst-001", spec0.ResourceRef)
	}
	if spec0.TenantID != "tenant-a" {
		t.Errorf("spec0.TenantID = %q, 期望 tenant-a", spec0.TenantID)
	}
	if spec0.WorkloadKind != "gpu_container" {
		t.Errorf("spec0.WorkloadKind = %q, 期望 gpu_container", spec0.WorkloadKind)
	}
	if spec0.GPUSpec == nil || spec0.GPUSpec.Count != 2 {
		t.Errorf("spec0.GPUSpec = %v, 期望 count=2（大写 Count 解析）", spec0.GPUSpec)
	}
	spec1 := svc.startSpecs[1]
	if spec1.GPUSpec != nil {
		t.Errorf("spec1.GPUSpec = %v, 期望 nil（vm 无 GPU）", spec1.GPUSpec)
	}
}

// --- AC: 无 running 实例时 StopStale 收到空集合且不补启动 ---

func TestReconcileEmptyActiveSet(t *testing.T) {
	store := &mockMetadataStore{tx: &mockTx{rows: &mockRows{rows: nil}}}
	svc := &captureStopStaleService{}
	r := NewReconciler(store, svc, nil, 5*time.Minute, 60)

	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile 返回错误: %v", err)
	}
	if svc.activeRefs == nil || len(svc.activeRefs) != 0 {
		t.Errorf("空 running 集合时应传空 activeRefs, 实际 %v", svc.activeRefs)
	}
	if len(svc.startSpecs) != 0 {
		t.Errorf("无 running 实例时不应调用 StartCollection")
	}
}

// --- AC: WithPlatformTx 失败时 Reconcile 返回 error，不调 StopStale ---

func TestReconcileWithPlatformTxError(t *testing.T) {
	store := &mockMetadataStore{err: errors.New("db connection failed")}
	svc := &captureStopStaleService{}
	r := NewReconciler(store, svc, nil, 5*time.Minute, 60)

	err := r.Reconcile(context.Background())
	if err == nil {
		t.Fatalf("WithPlatformTx 失败时 Reconcile 应返回错误")
	}
	if svc.activeRefs != nil {
		t.Errorf("WithPlatformTx 失败时不应调用 StopStale")
	}
}

// --- AC: 查询失败时 Reconcile 返回 error ---

func TestReconcileQueryError(t *testing.T) {
	store := &mockMetadataStore{tx: &mockTx{err: errors.New("query failed")}}
	svc := &captureStopStaleService{}
	r := NewReconciler(store, svc, nil, 5*time.Minute, 60)

	err := r.Reconcile(context.Background())
	if err == nil {
		t.Fatalf("Query 失败时 Reconcile 应返回错误")
	}
	if svc.activeRefs != nil {
		t.Errorf("Query 失败时不应调用 StopStale")
	}
}

// --- AC: rows.Err() 传播 ---

func TestReconcileRowsErr(t *testing.T) {
	rowsErr := errors.New("rows iteration error")
	rows := &mockRows{rows: [][]any{
		{"tenant-a", "inst-001", "app-1", "gpu_container", []byte(`{}`)},
	}, err: rowsErr}
	store := &mockMetadataStore{tx: &mockTx{rows: rows}}
	svc := &captureStopStaleService{}
	r := NewReconciler(store, svc, nil, 5*time.Minute, 60)

	err := r.Reconcile(context.Background())
	if !errors.Is(err, rowsErr) {
		t.Fatalf("Reconcile 应传播 rows.Err(), 期望 %v, 实际 %v", rowsErr, err)
	}
}

// --- AC: StopStale 返回 error 时 Reconcile 透传 ---

func TestReconcileStopStaleErrorPropagates(t *testing.T) {
	store := &mockMetadataStore{tx: &mockTx{rows: &mockRows{rows: nil}}}
	stopErr := errors.New("stop stale failed")
	svc := &captureStopStaleService{stopErr: stopErr}
	r := NewReconciler(store, svc, nil, 5*time.Minute, 60)

	err := r.Reconcile(context.Background())
	if !errors.Is(err, stopErr) {
		t.Fatalf("Reconcile 应透传 StopStale 错误, 期望 %v, 实际 %v", stopErr, err)
	}
}

// --- AC: 单实例 StartCollection 失败不阻塞其余实例与 StopStale ---

func TestReconcileSingleStartFailureDoesNotBlock(t *testing.T) {
	rows := &mockRows{
		rows: [][]any{
			{"tenant-a", "inst-fail", "fail-app", "vm", []byte(`{}`)},
			{"tenant-b", "inst-ok", "ok-app", "container", []byte(`{}`)},
		},
	}
	store := &mockMetadataStore{tx: &mockTx{rows: rows}}
	svc := &failingStartService{failRef: "inst-fail"}
	r := NewReconciler(store, svc, nil, 5*time.Minute, 60)

	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("单实例补启动失败不应导致 Reconcile 返回错误, 实际: %v", err)
	}
	if len(svc.startSpecs) != 2 {
		t.Errorf("StartCollection 调用次数 = %d, 期望 2（失败实例也调用）", len(svc.startSpecs))
	}
	if svc.activeRefs == nil || !svc.activeRefs["inst-fail"] || !svc.activeRefs["inst-ok"] {
		t.Errorf("失败实例仍应进入 activeRefs, 实际 %v", svc.activeRefs)
	}
}

type failingStartService struct {
	startSpecs []ports.CollectionSpec
	activeRefs map[string]bool
	failRef    string
}

func (s *failingStartService) StartCollection(ctx context.Context, spec ports.CollectionSpec) error {
	s.startSpecs = append(s.startSpecs, spec)
	if spec.ResourceRef == s.failRef {
		return errors.New("start collection failed")
	}
	return nil
}

func (s *failingStartService) StopCollection(ctx context.Context, resourceRef string) error {
	return nil
}

func (s *failingStartService) StopStale(ctx context.Context, activeRefs map[string]bool) error {
	s.activeRefs = activeRefs
	return nil
}
