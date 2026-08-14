package internal

import (
	"context"
	"errors"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

// --- mock MetadataStore / MetadataTx / Rows ---

// mockRows 实现 ports.Rows，按预置行数据驱动扫描。
type mockRows struct {
	rows [][]any
	idx  int
	err  error // Err() 返回值
}

func (m *mockRows) Close()             {}
func (m *mockRows) Err() error          { return m.err }
func (m *mockRows) Next() bool          { m.idx++; return m.idx <= len(m.rows) }
func (m *mockRows) Scan(dest ...any) error {
	row := m.rows[m.idx-1]
	for i := range dest {
		switch d := dest[i].(type) {
		case *string:
			if v, ok := row[i].(string); ok {
				*d = v
			}
		case *[]byte:
			if v, ok := row[i].([]byte); ok {
				*d = v
			}
		default:
			return errors.New("unsupported scan type")
		}
	}
	return nil
}

// mockTx 实现 ports.MetadataTx，返回预置的 Rows。
type mockTx struct {
	rows ports.Rows
	err  error // Query 返回的 error
}

func (m *mockTx) Exec(ctx context.Context, sql string, args ...any) (ports.CommandTag, error) {
	return ports.CommandTag{}, nil
}
func (m *mockTx) Query(ctx context.Context, sql string, args ...any) (ports.Rows, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.rows, nil
}
func (m *mockTx) QueryRow(ctx context.Context, sql string, args ...any) ports.Row {
	return nil
}

// mockMetadataStore 实现 ports.MetadataStore，捕获 WithPlatformTx 调用。
type mockMetadataStore struct {
	called bool
	tx     ports.MetadataTx
	err    error // WithPlatformTx 直接返回的 error
}

func (m *mockMetadataStore) Ping(ctx context.Context) error { return nil }
func (m *mockMetadataStore) WithTenantTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	return nil
}
func (m *mockMetadataStore) WithPlatformTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	m.called = true
	if m.err != nil {
		return m.err
	}
	return fn(ctx, m.tx)
}

// --- AC: WithPlatformTx 调用 ---

func TestRebuildCallsWithPlatformTx(t *testing.T) {
	rows := &mockRows{rows: nil} // 无 running 实例
	store := &mockMetadataStore{
		tx: &mockTx{rows: rows},
	}
	mockSvc := &mockMeteringCollectionService{}
	r := NewRebuilder(store, mockSvc, nil)

	if err := r.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild 返回错误: %v", err)
	}

	if !store.called {
		t.Errorf("Rebuild 应调用 WithPlatformTx")
	}
}

// --- AC: running 实例建 ticker（StartCollection 被调用）---

func TestRebuildStartsCollectionForRunningInstances(t *testing.T) {
	rows := &mockRows{
		rows: [][]any{
			{"tenant-a", "inst-001", "demo-gpu-app", "gpu_container", []byte(`{"count": 2}`)},
			{"tenant-b", "inst-002", "demo-vm", "vm", []byte(`{}`)},
		},
	}
	store := &mockMetadataStore{tx: &mockTx{rows: rows}}
	mockSvc := &mockMeteringCollectionService{}
	r := NewRebuilder(store, mockSvc, nil)

	if err := r.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild 返回错误: %v", err)
	}

	if len(mockSvc.startSpecs) != 2 {
		t.Fatalf("StartCollection 调用次数 = %d, 期望 2", len(mockSvc.startSpecs))
	}

	// 验证第一个实例（gpu_container, 2 GPU 卡）。
	spec0 := mockSvc.startSpecs[0]
	if spec0.ResourceRef != "inst-001" {
		t.Errorf("spec0.ResourceRef = %q, 期望 inst-001", spec0.ResourceRef)
	}
	if spec0.WorkloadName != "demo-gpu-app" {
		t.Errorf("spec0.WorkloadName = %q, 期望 demo-gpu-app", spec0.WorkloadName)
	}
	if spec0.TenantID != "tenant-a" {
		t.Errorf("spec0.TenantID = %q, 期望 tenant-a", spec0.TenantID)
	}
	if spec0.WorkloadKind != "gpu_container" {
		t.Errorf("spec0.WorkloadKind = %q, 期望 gpu_container", spec0.WorkloadKind)
	}
	if spec0.GPUSpec == nil || spec0.GPUSpec.Count != 2 {
		t.Errorf("spec0.GPUSpec = %v, 期望 count=2", spec0.GPUSpec)
	}

	// 验证第二个实例（vm, 无 GPU）。
	spec1 := mockSvc.startSpecs[1]
	if spec1.ResourceRef != "inst-002" {
		t.Errorf("spec1.ResourceRef = %q, 期望 inst-002", spec1.ResourceRef)
	}
	if spec1.GPUSpec != nil {
		t.Errorf("spec1.GPUSpec = %v, 期望 nil（vm 无 GPU）", spec1.GPUSpec)
	}
}

// --- AC: gpu_status 解析 ---

func TestRebuildParsesGPUStatus(t *testing.T) {
	rows := &mockRows{
		rows: [][]any{
			{"tenant-a", "inst-gpu-1", "gpu-app-1", "gpu_container", []byte(`{"count": 8}`)},
			{"tenant-b", "inst-gpu-2", "gpu-app-2", "gpu_container", []byte(`{}`)},          // 缺失 count → 0
			{"tenant-c", "inst-gpu-3", "gpu-app-3", "gpu_container", []byte(`null`)},        // null → 0
			{"tenant-d", "inst-gpu-4", "gpu-app-4", "gpu_container", nil},                   // nil → 0
			{"tenant-e", "inst-gpu-5", "gpu-app-5", "gpu_container", []byte(`invalid`)},     // 畸形 → 0
		},
	}
	store := &mockMetadataStore{tx: &mockTx{rows: rows}}
	mockSvc := &mockMeteringCollectionService{}
	r := NewRebuilder(store, mockSvc, nil)

	if err := r.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild 返回错误: %v", err)
	}

	if len(mockSvc.startSpecs) != 5 {
		t.Fatalf("StartCollection 调用次数 = %d, 期望 5", len(mockSvc.startSpecs))
	}

	// inst-gpu-1: count=8 → GPUSpec.Count=8
	if mockSvc.startSpecs[0].GPUSpec == nil || mockSvc.startSpecs[0].GPUSpec.Count != 8 {
		t.Errorf("inst-gpu-1 GPUSpec = %v, 期望 count=8", mockSvc.startSpecs[0].GPUSpec)
	}
	// inst-gpu-2 ~ inst-gpu-5: count 缺失/0/nil/畸形 → GPUSpec=nil
	for i := 1; i < 5; i++ {
		if mockSvc.startSpecs[i].GPUSpec != nil {
			t.Errorf("startSpecs[%d].GPUSpec = %v, 期望 nil", i, mockSvc.startSpecs[i].GPUSpec)
		}
	}
}

// --- AC: 单实例 StartCollection 失败不阻塞 ---

func TestRebuildSingleInstanceFailureDoesNotBlock(t *testing.T) {
	rows := &mockRows{
		rows: [][]any{
			{"tenant-a", "inst-ok-1", "ok-app-1", "gpu_container", []byte(`{"count": 1}`)},
			{"tenant-b", "inst-fail", "fail-app", "vm", []byte(`{}`)},
			{"tenant-c", "inst-ok-2", "ok-app-2", "container", []byte(`{}`)},
		},
	}
	store := &mockMetadataStore{tx: &mockTx{rows: rows}}

	// 用一个可控的 mock：对 inst-fail 返回 error。
	controllableSvc := &conditionalStartService{
		failRef: "inst-fail",
		err:     errors.New("start collection failed"),
	}

	r := NewRebuilder(store, controllableSvc, nil)

	if err := r.Rebuild(context.Background()); err != nil {
		t.Fatalf("单实例失败不应导致 Rebuild 返回错误, 实际: %v", err)
	}

	// 三个实例都应被尝试 StartCollection。
	if len(controllableSvc.startSpecs) != 3 {
		t.Fatalf("StartCollection 调用次数 = %d, 期望 3（失败实例也调用）", len(controllableSvc.startSpecs))
	}

	// 验证失败的实例也在调用列表中。
	foundFail := false
	for _, spec := range controllableSvc.startSpecs {
		if spec.ResourceRef == "inst-fail" {
			foundFail = true
		}
	}
	if !foundFail {
		t.Errorf("inst-fail 应在 StartCollection 调用列表中（失败前被调用）")
	}
}

// conditionalStartService 对指定 resourceRef 返回 error，其余成功。
type conditionalStartService struct {
	startSpecs []ports.CollectionSpec
	stopRefs   []string
	failRef    string
	err        error
}

func (c *conditionalStartService) StartCollection(ctx context.Context, spec ports.CollectionSpec) error {
	c.startSpecs = append(c.startSpecs, spec)
	if spec.ResourceRef == c.failRef {
		return c.err
	}
	return nil
}

func (c *conditionalStartService) StopCollection(ctx context.Context, resourceRef string) error {
	c.stopRefs = append(c.stopRefs, resourceRef)
	return nil
}

// --- AC: WithPlatformTx 返回 error 时 Rebuild 返回 error ---

func TestRebuildWithPlatformTxError(t *testing.T) {
	store := &mockMetadataStore{err: errors.New("db connection failed")}
	mockSvc := &mockMeteringCollectionService{}
	r := NewRebuilder(store, mockSvc, nil)

	err := r.Rebuild(context.Background())
	if err == nil {
		t.Fatalf("WithPlatformTx 失败时 Rebuild 应返回错误")
	}

	if len(mockSvc.startSpecs) != 0 {
		t.Errorf("WithPlatformTx 失败时不应调用 StartCollection")
	}
}

// --- AC: 查询返回 error 时 Rebuild 返回 error ---

func TestRebuildQueryError(t *testing.T) {
	store := &mockMetadataStore{
		tx: &mockTx{err: errors.New("query failed")},
	}
	mockSvc := &mockMeteringCollectionService{}
	r := NewRebuilder(store, mockSvc, nil)

	err := r.Rebuild(context.Background())
	if err == nil {
		t.Fatalf("Query 失败时 Rebuild 应返回错误")
	}

	if len(mockSvc.startSpecs) != 0 {
		t.Errorf("Query 失败时不应调用 StartCollection")
	}
}

// --- AC: 无 running 实例时 Rebuild 成功 ---

func TestRebuildNoRunningInstances(t *testing.T) {
	rows := &mockRows{rows: nil}
	store := &mockMetadataStore{tx: &mockTx{rows: rows}}
	mockSvc := &mockMeteringCollectionService{}
	r := NewRebuilder(store, mockSvc, nil)

	if err := r.Rebuild(context.Background()); err != nil {
		t.Fatalf("无 running 实例时 Rebuild 应返回 nil, 实际: %v", err)
	}

	if len(mockSvc.startSpecs) != 0 {
		t.Errorf("无 running 实例时不应调用 StartCollection")
	}
}

// --- AC: rows.Err() 传播 ---

func TestRebuildRowsErr(t *testing.T) {
	rowsErr := errors.New("rows iteration error")
	rows := &mockRows{rows: [][]any{
		{"tenant-a", "inst-001", "gpu-app-1", "gpu_container", []byte(`{"count": 1}`)},
	}, err: rowsErr}
	store := &mockMetadataStore{tx: &mockTx{rows: rows}}
	mockSvc := &mockMeteringCollectionService{}
	r := NewRebuilder(store, mockSvc, nil)

	err := r.Rebuild(context.Background())
	if !errors.Is(err, rowsErr) {
		t.Fatalf("Rebuild 应传播 rows.Err(), 期望 %v, 实际 %v", rowsErr, err)
	}
}
