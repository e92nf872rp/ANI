package internal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// captureStopStaleService 实现 ports.MeteringCollectionService，
// 捕获 StopStale 收到的 activeRefs，便于断言 Reconcile 传递的 running 实例集合。
type captureStopStaleService struct {
	activeRefs map[string]bool
	stopErr    error // StopStale 返回的 error
}

func (c *captureStopStaleService) StartCollection(ctx context.Context, spec ports.CollectionSpec) error {
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
			{"inst-001"},
			{"inst-002"},
			{"inst-003"},
		},
	}
	store := &mockMetadataStore{tx: &mockTx{rows: rows}}
	svc := &captureStopStaleService{}
	r := NewReconciler(store, svc, nil, 5*time.Minute)

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

// --- AC: 无 running 实例时 StopStale 收到空集合 ---

func TestReconcileEmptyActiveSet(t *testing.T) {
	store := &mockMetadataStore{tx: &mockTx{rows: &mockRows{rows: nil}}}
	svc := &captureStopStaleService{}
	r := NewReconciler(store, svc, nil, 5*time.Minute)

	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile 返回错误: %v", err)
	}
	if svc.activeRefs == nil || len(svc.activeRefs) != 0 {
		t.Errorf("空 running 集合时应传空 activeRefs, 实际 %v", svc.activeRefs)
	}
}

// --- AC: WithPlatformTx 失败时 Reconcile 返回 error，不调 StopStale ---

func TestReconcileWithPlatformTxError(t *testing.T) {
	store := &mockMetadataStore{err: errors.New("db connection failed")}
	svc := &captureStopStaleService{}
	r := NewReconciler(store, svc, nil, 5*time.Minute)

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
	r := NewReconciler(store, svc, nil, 5*time.Minute)

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
	rows := &mockRows{rows: [][]any{{"inst-001"}}, err: rowsErr}
	store := &mockMetadataStore{tx: &mockTx{rows: rows}}
	svc := &captureStopStaleService{}
	r := NewReconciler(store, svc, nil, 5*time.Minute)

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
	r := NewReconciler(store, svc, nil, 5*time.Minute)

	err := r.Reconcile(context.Background())
	if !errors.Is(err, stopErr) {
		t.Fatalf("Reconcile 应透传 StopStale 错误, 期望 %v, 实际 %v", stopErr, err)
	}
}
