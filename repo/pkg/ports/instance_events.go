package ports

// InstanceLifecycleEvent 表示实例生命周期事件的 payload schema。
// 由上游 reconciler 通过 NATS JetStream 发布，metering-service consumer 订阅消费。
type InstanceLifecycleEvent struct {
	InstanceID   string        `json:"instance_id"`
	TenantID     string        `json:"tenant_id"`
	Name         string        `json:"name"`
	WorkloadKind string        `json:"workload_kind"`
	NewStatus    string        `json:"new_status"`
	EventSeq     uint64        `json:"event_seq"`
	GPUSpec      *GPUEventSpec `json:"gpu_spec,omitempty"`
	ErrorMsg     string        `json:"error_msg,omitempty"`
}

// GPUEventSpec 描述实例关联的 GPU 卡数信息。
type GPUEventSpec struct {
	Count int `json:"count"`
}
