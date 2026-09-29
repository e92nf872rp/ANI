package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

// --- 复用同包既有 fake：reconcileFakeMetadataStore / reconcileFakeMetadataTx /
// reconcileFakeInstanceStoreTx / MockOutboxWriter / fakeInstanceStore ---

// newLifecycleOutboxTestRecord 构造一个带合法 UUID tenant/instance 的记录，
// 供 outbox 聚合校验（extractUUIDFromInstanceID / uuid.Parse）通过。
func newLifecycleOutboxTestRecord(state ports.WorkloadState, gpuCount int) ports.WorkloadInstanceRecord {
	record := newReconcileTestRecordWithQuota(state)
	record.Status.State = state
	if gpuCount > 0 {
		record.GPU = &ports.GPUInstanceStatus{Count: gpuCount}
	}
	return record
}

// decodeOutboxLifecyclePayload 将 outbox payload 解析为 map 便于断言。
func decodeOutboxLifecyclePayload(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode outbox payload: %v", err)
	}
	return decoded
}

// --- AC: 创建同步路径 provisioning→running 写 instance.confirmed（配额关闭也可用）---

func TestOrchestratorCreateTransitionWritesOutbox(t *testing.T) {
	orchestrator := NewLocalInstanceOrchestrator(nil, nil, nil, nil, nil, nil, nil, nil,
		WithInstanceStore(&fakeInstanceStore{}),
		WithInstanceOrchestratorMetadataStore(&reconcileFakeMetadataStore{}),
		WithInstanceOrchestratorStoreTx(&reconcileFakeInstanceStoreTx{}),
		WithInstanceOrchestratorOutboxWriter(&MockOutboxWriter{}),
	)
	// 不注入 quotaService：GPU_QUOTA_ENABLED=false 场景。
	outbox := &MockOutboxWriter{}
	orchestrator.outboxWriter = outbox

	record := newLifecycleOutboxTestRecord(ports.WorkloadStateRunning, 2)
	record.TenantID = "5dbb1d01-0000-4000-8000-000000000001"

	if err := orchestrator.persistWithQuotaTransition(context.Background(), record, ports.WorkloadStateProvisioning, ports.WorkloadStateRunning); err != nil {
		t.Fatalf("persistWithQuotaTransition 返回错误: %v", err)
	}

	if len(outbox.events) != 1 {
		t.Fatalf("outbox events = %d, want 1", len(outbox.events))
	}
	event := outbox.events[0]
	if event.EventType != "instance.confirmed" {
		t.Errorf("event_type = %q, want instance.confirmed", event.EventType)
	}
	if event.AggregateType != "workload_instance" {
		t.Errorf("aggregate_type = %q, want workload_instance", event.AggregateType)
	}
	payload := decodeOutboxLifecyclePayload(t, event.Payload)
	if payload["new_status"] != "running" {
		t.Errorf("new_status = %v, want running", payload["new_status"])
	}
	if payload["tenant_id"] != record.TenantID {
		t.Errorf("tenant_id = %v, want %s", payload["tenant_id"], record.TenantID)
	}
	gpuSpec, ok := payload["gpu_spec"].(map[string]any)
	if !ok || gpuSpec["count"] != float64(2) {
		t.Errorf("gpu_spec = %v, want count=2", payload["gpu_spec"])
	}
}

// --- AC: 非生命周期迁移（provisioning→provisioning）不写 outbox ---

func TestOrchestratorNonTransitionSkipsOutbox(t *testing.T) {
	outbox := &MockOutboxWriter{}
	orchestrator := NewLocalInstanceOrchestrator(nil, nil, nil, nil, nil, nil, nil, nil,
		WithInstanceStore(&fakeInstanceStore{}),
		WithInstanceOrchestratorMetadataStore(&reconcileFakeMetadataStore{}),
		WithInstanceOrchestratorStoreTx(&reconcileFakeInstanceStoreTx{}),
		WithInstanceOrchestratorOutboxWriter(outbox),
	)

	record := newLifecycleOutboxTestRecord(ports.WorkloadStateProvisioning, 0)
	record.TenantID = "5dbb1d01-0000-4000-8000-000000000001"

	if err := orchestrator.persistWithQuotaTransition(context.Background(), record, ports.WorkloadStateProvisioning, ports.WorkloadStateProvisioning); err != nil {
		t.Fatalf("persistWithQuotaTransition 返回错误: %v", err)
	}
	if len(outbox.events) != 0 {
		t.Errorf("outbox events = %d, want 0（非生命周期迁移）", len(outbox.events))
	}
}

// --- AC: InstanceService 删除路径在配额关闭时仍写 instance.deleted ---

func TestInstanceServiceDeleteWritesOutboxWithoutQuota(t *testing.T) {
	outbox := &MockOutboxWriter{}
	storeTx := &reconcileFakeInstanceStoreTx{}
	service := NewLocalInstanceServiceWithOptions(nil, &fakeInstanceStore{}, nil,
		WithInstanceMetadataStore(&reconcileFakeMetadataStore{}),
		WithInstanceStoreTx(storeTx),
		WithInstanceOutboxWriter(outbox),
	)
	// 不注入 quotaService：GPU_QUOTA_ENABLED=false 场景（原实现此处直接走
	// plain UpsertStatus，事件缺失——正是本次修复的缺口）。

	record := newLifecycleOutboxTestRecord(ports.WorkloadStateDeleted, 1)
	record.TenantID = "5dbb1d01-0000-4000-8000-000000000001"

	if err := service.persistLifecycleWithQuota(context.Background(), record, ports.WorkloadLifecycleDelete, ports.WorkloadStateRunning); err != nil {
		t.Fatalf("persistLifecycleWithQuota 返回错误: %v", err)
	}

	if len(outbox.events) != 1 {
		t.Fatalf("outbox events = %d, want 1", len(outbox.events))
	}
	event := outbox.events[0]
	if event.EventType != "instance.deleted" {
		t.Errorf("event_type = %q, want instance.deleted", event.EventType)
	}
	payload := decodeOutboxLifecyclePayload(t, event.Payload)
	if payload["new_status"] != "deleted" {
		t.Errorf("new_status = %v, want deleted", payload["new_status"])
	}
	if len(storeTx.txWrites) != 1 {
		t.Errorf("UpsertStatusTx writes = %d, want 1（状态与事件同事务）", len(storeTx.txWrites))
	}
}

// --- AC: InstanceService stop 写 instance.stopped ---

func TestInstanceServiceStopWritesOutbox(t *testing.T) {
	outbox := &MockOutboxWriter{}
	service := NewLocalInstanceServiceWithOptions(nil, &fakeInstanceStore{}, nil,
		WithInstanceMetadataStore(&reconcileFakeMetadataStore{}),
		WithInstanceStoreTx(&reconcileFakeInstanceStoreTx{}),
		WithInstanceOutboxWriter(outbox),
	)

	record := newLifecycleOutboxTestRecord(ports.WorkloadStateStopped, 0)
	record.TenantID = "5dbb1d01-0000-4000-8000-000000000001"

	if err := service.persistLifecycleWithQuota(context.Background(), record, ports.WorkloadLifecycleStop, ports.WorkloadStateRunning); err != nil {
		t.Fatalf("persistLifecycleWithQuota 返回错误: %v", err)
	}
	if len(outbox.events) != 1 {
		t.Fatalf("outbox events = %d, want 1", len(outbox.events))
	}
	if outbox.events[0].EventType != "instance.stopped" {
		t.Errorf("event_type = %q, want instance.stopped", outbox.events[0].EventType)
	}
}

// --- AC: 无生命周期语义的动作（如 snapshot）不写 outbox ---

func TestInstanceServiceUnrelatedActionSkipsOutbox(t *testing.T) {
	outbox := &MockOutboxWriter{}
	service := NewLocalInstanceServiceWithOptions(nil, &fakeInstanceStore{}, nil,
		WithInstanceMetadataStore(&reconcileFakeMetadataStore{}),
		WithInstanceStoreTx(&reconcileFakeInstanceStoreTx{}),
		WithInstanceOutboxWriter(outbox),
	)

	record := newLifecycleOutboxTestRecord(ports.WorkloadStateRunning, 0)
	record.TenantID = "5dbb1d01-0000-4000-8000-000000000001"

	if err := service.persistLifecycleWithQuota(context.Background(), record, ports.WorkloadLifecycleSnapshot, ports.WorkloadStateRunning); err != nil {
		t.Fatalf("persistLifecycleWithQuota 返回错误: %v", err)
	}
	if len(outbox.events) != 0 {
		t.Errorf("outbox events = %d, want 0（无生命周期语义的动作）", len(outbox.events))
	}
}
