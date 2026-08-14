package internal

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

// mockMeteringCollectionService 实现 ports.MeteringCollectionService，用于单测 mock。
type mockMeteringCollectionService struct {
	startSpecs  []ports.CollectionSpec
	stopRefs    []string
	startErr    error
	stopErr     error
}

func (m *mockMeteringCollectionService) StartCollection(ctx context.Context, spec ports.CollectionSpec) error {
	m.startSpecs = append(m.startSpecs, spec)
	if m.startErr != nil {
		return m.startErr
	}
	return nil
}

func (m *mockMeteringCollectionService) StopCollection(ctx context.Context, resourceRef string) error {
	m.stopRefs = append(m.stopRefs, resourceRef)
	if m.stopErr != nil {
		return m.stopErr
	}
	return nil
}

// mockMessage 实现 ports.Message，可控 Headers/Data。
type mockMessage struct {
	headers map[string][]string
	data    []byte
}

func (m *mockMessage) Subject() string              { return "" }
func (m *mockMessage) Data() []byte                 { return m.data }
func (m *mockMessage) Headers() map[string][]string { return m.headers }

// makeEventPayload 构造 InstanceLifecycleEvent 的 JSON payload。
func makeEventPayload(event ports.InstanceLifecycleEvent) []byte {
	b, _ := json.Marshal(event)
	return b
}

// --- AC: 成功路径推进 seenSeq ---

func TestHandleEventSuccessAdvancesSeenSeq(t *testing.T) {
	mockSvc := &mockMeteringCollectionService{}
	c := NewConsumer(mockSvc, nil)

	event := ports.InstanceLifecycleEvent{
		InstanceID:   "inst-001",
		TenantID:     "tenant-a",
		Name:         "demo-gpu-app",
		WorkloadKind: "gpu_container",
		NewStatus:    "running",
		EventSeq:     100,
		GPUSpec:      &ports.GPUEventSpec{Count: 2},
	}
	msg := &mockMessage{
		headers: map[string][]string{"tenant-id": {"tenant-a"}},
		data:    makeEventPayload(event),
	}

	if err := c.handleEvent(context.Background(), msg); err != nil {
		t.Fatalf("handleEvent 期望返回 nil（成功 Ack），实际 err=%v", err)
	}

	// 验证 StartCollection 被调用。
	if len(mockSvc.startSpecs) != 1 {
		t.Fatalf("StartCollection 调用次数 = %d, 期望 1", len(mockSvc.startSpecs))
	}
	spec := mockSvc.startSpecs[0]
	if spec.ResourceRef != "inst-001" {
		t.Errorf("spec.ResourceRef = %q, 期望 inst-001", spec.ResourceRef)
	}
	if spec.WorkloadName != "demo-gpu-app" {
		t.Errorf("spec.WorkloadName = %q, 期望 demo-gpu-app", spec.WorkloadName)
	}
	if spec.TenantID != "tenant-a" {
		t.Errorf("spec.TenantID = %q, 期望 tenant-a", spec.TenantID)
	}
	if spec.GPUSpec == nil || spec.GPUSpec.Count != 2 {
		t.Errorf("spec.GPUSpec = %v, 期望 count=2", spec.GPUSpec)
	}

	// 验证 seenSeq 已推进。
	c.mu.Lock()
	got := c.seenSeq["inst-001"]
	c.mu.Unlock()
	if got != 100 {
		t.Errorf("seenSeq[inst-001] = %d, 期望 100", got)
	}
}

// --- AC: 失败路径不推进 seenSeq ---

func TestHandleEventFailureDoesNotAdvanceSeenSeq(t *testing.T) {
	sentinel := errors.New("start collection failed")
	mockSvc := &mockMeteringCollectionService{startErr: sentinel}
	c := NewConsumer(mockSvc, nil)

	event := ports.InstanceLifecycleEvent{
		InstanceID:   "inst-002",
		TenantID:     "tenant-b",
		WorkloadKind: "gpu_container",
		NewStatus:    "running",
		EventSeq:     200,
	}
	msg := &mockMessage{
		headers: map[string][]string{"tenant-id": {"tenant-b"}},
		data:    makeEventPayload(event),
	}

	err := c.handleEvent(context.Background(), msg)
	if !errors.Is(err, sentinel) {
		t.Fatalf("handleEvent 期望返回 sentinel error（Nak 重投），实际 err=%v", err)
	}

	// 验证 seenSeq 未推进。
	c.mu.Lock()
	_, exists := c.seenSeq["inst-002"]
	c.mu.Unlock()
	if exists {
		t.Errorf("处理失败时 seenSeq 不应被推进，但 inst-002 已存在")
	}
}

// --- AC: 过期事件丢弃（seq <= seenSeq）---

func TestHandleEventStaleEventDiscarded(t *testing.T) {
	mockSvc := &mockMeteringCollectionService{}
	c := NewConsumer(mockSvc, nil)

	// 预置 seenSeq=500。
	c.mu.Lock()
	c.seenSeq["inst-003"] = 500
	c.mu.Unlock()

	// 发送 seq=500（等于 seenSeq）→ 丢弃。
	event := ports.InstanceLifecycleEvent{
		InstanceID: "inst-003",
		TenantID:   "tenant-c",
		NewStatus:  "running",
		EventSeq:   500,
	}
	msg := &mockMessage{
		headers: map[string][]string{"tenant-id": {"tenant-c"}},
		data:    makeEventPayload(event),
	}

	if err := c.handleEvent(context.Background(), msg); err != nil {
		t.Fatalf("过期事件应返回 nil（Ack 跳过），实际 err=%v", err)
	}

	// 验证 StartCollection 未被调用。
	if len(mockSvc.startSpecs) != 0 {
		t.Errorf("过期事件不应触发 StartCollection, 实际调用 %d 次", len(mockSvc.startSpecs))
	}

	// 验证 seenSeq 未被更新（仍为 500）。
	c.mu.Lock()
	got := c.seenSeq["inst-003"]
	c.mu.Unlock()
	if got != 500 {
		t.Errorf("过期事件不应推进 seenSeq, 期望 500, 实际 %d", got)
	}

	// 再发送 seq=499（小于 seenSeq）→ 也应丢弃。
	event2 := event
	event2.EventSeq = 499
	msg2 := &mockMessage{
		headers: map[string][]string{"tenant-id": {"tenant-c"}},
		data:    makeEventPayload(event2),
	}
	if err := c.handleEvent(context.Background(), msg2); err != nil {
		t.Fatalf("过期事件(seq<seenSeq)应返回 nil, 实际 err=%v", err)
	}
	if len(mockSvc.startSpecs) != 0 {
		t.Errorf("过期事件(seq<seenSeq)不应触发 StartCollection")
	}
}

// --- AC: 毒消息 Ack 跳过 ---

func TestHandleEventPoisonMessageAckSkip(t *testing.T) {
	mockSvc := &mockMeteringCollectionService{}
	c := NewConsumer(mockSvc, nil)

	msg := &mockMessage{
		headers: map[string][]string{"tenant-id": {"tenant-d"}},
		data:    []byte("invalid-json-payload"),
	}

	if err := c.handleEvent(context.Background(), msg); err != nil {
		t.Fatalf("毒消息应返回 nil（Ack 跳过），实际 err=%v", err)
	}

	// 验证 Start/Stop 未被调用。
	if len(mockSvc.startSpecs) != 0 {
		t.Errorf("毒消息不应触发 StartCollection")
	}
	if len(mockSvc.stopRefs) != 0 {
		t.Errorf("毒消息不应触发 StopCollection")
	}
}

// --- AC: 租户不匹配 Nak 重投 ---

func TestHandleEventTenantMismatchNakRedelivery(t *testing.T) {
	mockSvc := &mockMeteringCollectionService{}
	c := NewConsumer(mockSvc, nil)

	event := ports.InstanceLifecycleEvent{
		InstanceID: "inst-005",
		TenantID:   "tenant-payload",
		NewStatus:  "running",
		EventSeq:   300,
	}
	msg := &mockMessage{
		headers: map[string][]string{"tenant-id": {"tenant-header"}},
		data:    makeEventPayload(event),
	}

	err := c.handleEvent(context.Background(), msg)
	if err == nil {
		t.Fatalf("租户不匹配应返回 error（Nak 重投），实际返回 nil")
	}

	// 验证 Start/Stop 未被调用。
	if len(mockSvc.startSpecs) != 0 {
		t.Errorf("租户不匹配不应触发 StartCollection")
	}

	// 验证 seenSeq 未推进。
	c.mu.Lock()
	_, exists := c.seenSeq["inst-005"]
	c.mu.Unlock()
	if exists {
		t.Errorf("租户不匹配时 seenSeq 不应被推进")
	}
}

// --- AC: 未知状态 Ack 跳过 ---

func TestHandleEventUnknownStatusAckSkip(t *testing.T) {
	mockSvc := &mockMeteringCollectionService{}
	c := NewConsumer(mockSvc, nil)

	event := ports.InstanceLifecycleEvent{
		InstanceID: "inst-006",
		TenantID:   "tenant-f",
		NewStatus:   "unknown_status",
		EventSeq:   600,
	}
	msg := &mockMessage{
		headers: map[string][]string{"tenant-id": {"tenant-f"}},
		data:    makeEventPayload(event),
	}

	if err := c.handleEvent(context.Background(), msg); err != nil {
		t.Fatalf("未知状态应返回 nil（Ack 跳过），实际 err=%v", err)
	}

	// 验证 Start/Stop 未被调用。
	if len(mockSvc.startSpecs) != 0 {
		t.Errorf("未知状态不应触发 StartCollection")
	}
	if len(mockSvc.stopRefs) != 0 {
		t.Errorf("未知状态不应触发 StopCollection")
	}

	// 验证 seenSeq 未推进（未知状态跳过，不推进）。
	c.mu.Lock()
	_, exists := c.seenSeq["inst-006"]
	c.mu.Unlock()
	if exists {
		t.Errorf("未知状态跳过时 seenSeq 不应被推进")
	}
}

// --- AC: stopped/failed/deleted 路由到 StopCollection ---

func TestHandleEventStoppedRoutesToStopCollection(t *testing.T) {
	mockSvc := &mockMeteringCollectionService{}
	c := NewConsumer(mockSvc, nil)

	event := ports.InstanceLifecycleEvent{
		InstanceID: "inst-007",
		TenantID:   "tenant-g",
		NewStatus:  "stopped",
		EventSeq:   700,
	}
	msg := &mockMessage{
		headers: map[string][]string{"tenant-id": {"tenant-g"}},
		data:    makeEventPayload(event),
	}

	if err := c.handleEvent(context.Background(), msg); err != nil {
		t.Fatalf("stopped 事件应返回 nil（成功 Ack），实际 err=%v", err)
	}

	if len(mockSvc.stopRefs) != 1 {
		t.Fatalf("StopCollection 调用次数 = %d, 期望 1", len(mockSvc.stopRefs))
	}
	if mockSvc.stopRefs[0] != "inst-007" {
		t.Errorf("StopCollection ref = %q, 期望 inst-007", mockSvc.stopRefs[0])
	}

	// 验证 seenSeq 已推进。
	c.mu.Lock()
	got := c.seenSeq["inst-007"]
	c.mu.Unlock()
	if got != 700 {
		t.Errorf("seenSeq[inst-007] = %d, 期望 700", got)
	}
}

func TestHandleEventFailedRoutesToStopCollection(t *testing.T) {
	mockSvc := &mockMeteringCollectionService{}
	c := NewConsumer(mockSvc, nil)

	event := ports.InstanceLifecycleEvent{
		InstanceID: "inst-008",
		TenantID:   "tenant-h",
		NewStatus:  "failed",
		EventSeq:   800,
	}
	msg := &mockMessage{
		headers: map[string][]string{"tenant-id": {"tenant-h"}},
		data:    makeEventPayload(event),
	}

	if err := c.handleEvent(context.Background(), msg); err != nil {
		t.Fatalf("failed 事件应返回 nil, 实际 err=%v", err)
	}
	if len(mockSvc.stopRefs) != 1 || mockSvc.stopRefs[0] != "inst-008" {
		t.Errorf("StopCollection ref = %v, 期望 [inst-008]", mockSvc.stopRefs)
	}
}

func TestHandleEventDeletedRoutesToStopCollection(t *testing.T) {
	mockSvc := &mockMeteringCollectionService{}
	c := NewConsumer(mockSvc, nil)

	event := ports.InstanceLifecycleEvent{
		InstanceID: "inst-009",
		TenantID:   "tenant-i",
		NewStatus:  "deleted",
		EventSeq:   900,
	}
	msg := &mockMessage{
		headers: map[string][]string{"tenant-id": {"tenant-i"}},
		data:    makeEventPayload(event),
	}

	if err := c.handleEvent(context.Background(), msg); err != nil {
		t.Fatalf("deleted 事件应返回 nil, 实际 err=%v", err)
	}
	if len(mockSvc.stopRefs) != 1 || mockSvc.stopRefs[0] != "inst-009" {
		t.Errorf("StopCollection ref = %v, 期望 [inst-009]", mockSvc.stopRefs)
	}
}

// --- AC: StopCollection 失败不推进 seenSeq ---

func TestHandleEventStopFailureDoesNotAdvanceSeenSeq(t *testing.T) {
	sentinel := errors.New("stop collection failed")
	mockSvc := &mockMeteringCollectionService{stopErr: sentinel}
	c := NewConsumer(mockSvc, nil)

	event := ports.InstanceLifecycleEvent{
		InstanceID: "inst-010",
		TenantID:   "tenant-j",
		NewStatus:  "stopped",
		EventSeq:   1000,
	}
	msg := &mockMessage{
		headers: map[string][]string{"tenant-id": {"tenant-j"}},
		data:    makeEventPayload(event),
	}

	err := c.handleEvent(context.Background(), msg)
	if !errors.Is(err, sentinel) {
		t.Fatalf("StopCollection 失败应返回 sentinel error, 实际 err=%v", err)
	}

	c.mu.Lock()
	_, exists := c.seenSeq["inst-010"]
	c.mu.Unlock()
	if exists {
		t.Errorf("StopCollection 失败时 seenSeq 不应被推进")
	}
}

// --- AC: seenSeq 成功后才推进（严格递增更新）---

func TestHandleEventSeenSeqOnlyAdvancesOnSuccess(t *testing.T) {
	mockSvc := &mockMeteringCollectionService{}
	c := NewConsumer(mockSvc, nil)

	// 第一次成功处理 seq=100。
	event1 := ports.InstanceLifecycleEvent{
		InstanceID: "inst-011",
		TenantID:   "tenant-k",
		NewStatus:  "running",
		EventSeq:   100,
	}
	msg1 := &mockMessage{
		headers: map[string][]string{"tenant-id": {"tenant-k"}},
		data:    makeEventPayload(event1),
	}
	_ = c.handleEvent(context.Background(), msg1)

	// 第二次成功处理 seq=200。
	event2 := event1
	event2.EventSeq = 200
	msg2 := &mockMessage{
		headers: map[string][]string{"tenant-id": {"tenant-k"}},
		data:    makeEventPayload(event2),
	}
	_ = c.handleEvent(context.Background(), msg2)

	c.mu.Lock()
	got := c.seenSeq["inst-011"]
	c.mu.Unlock()
	if got != 200 {
		t.Errorf("seenSeq 应推进到 200, 实际 %d", got)
	}
}
