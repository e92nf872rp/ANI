package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/kubercloud/ani/pkg/ports"
)

// Consumer 订阅实例生命周期事件，驱动 MeteringCollectionService 的 Start/Stop。
//
// seenSeq 处理成功后才推进，避免 Nak 重投误判过期永久丢失（V1 缺陷修复）。
// MaxInflight=1 串行消费保证顺序，seenSeq 严格递增判定才有效。
// seenSeq 是进程内存态，重启归零（接受此边界，不持久化）。
type Consumer struct {
	metering ports.MeteringCollectionService
	logger   *slog.Logger
	mu       sync.Mutex
	seenSeq  map[string]uint64
}

// NewConsumer 创建 metering consumer。
// metering: 采集生命周期控制服务，handleEvent 根据事件状态调用 Start/StopCollection。
// logger: 结构化日志记录器，可为 nil（单测无需注入）。
func NewConsumer(metering ports.MeteringCollectionService, logger *slog.Logger) *Consumer {
	return &Consumer{
		metering: metering,
		logger:   logger,
		seenSeq:  make(map[string]uint64),
	}
}

// handleEvent 处理 InstanceLifecycleEvent，是 MessageBus Subscribe 的回调。
//
// 返回值语义：
//   - 返回 nil   → adapter Ack（消息已处理）
//   - 返回 error → adapter Nak（消息需重投）
//
// 处理流程（SPEC §5.1.4）：
//  1. 从 msg.Headers()["tenant-id"] 读租户 ID，与 payload tenant_id 校验一致
//  2. json.Unmarshal 失败的毒消息记 Error 日志后返回 nil（Ack 跳过，不重投）
//  3. seenSeq 乱序过滤：event.EventSeq <= seenSeq[instance_id] 时丢弃（返回 nil）
//  4. 路由：running → StartCollection；stopped/failed/deleted → StopCollection；未知 → Ack 跳过
//  5. 处理失败返回 error（Nak 重投），不推进 seenSeq
//  6. 处理成功后才推进 seenSeq
func (c *Consumer) handleEvent(ctx context.Context, msg ports.Message) error {
	// 1. 从 NATS Header 读取租户 ID。
	headerTenant := ""
	if vals := msg.Headers()["tenant-id"]; len(vals) > 0 {
		headerTenant = vals[0]
	}

	// 2. 解析 payload。毒消息 Ack 跳过，不重投。
	var event ports.InstanceLifecycleEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		c.safeLog(func(l *slog.Logger) {
			l.ErrorContext(ctx, "parse event failed (poison message), ack to skip", "err", err)
		})
		return nil
	}

	// 3. 租户上下文校验：header tenant-id 与 payload tenant_id 必须一致，不一致 → Nak 重投。
	if event.TenantID != headerTenant {
		c.safeLog(func(l *slog.Logger) {
			l.ErrorContext(ctx, "tenant context mismatch, nak for redelivery",
				"header_tenant_id", headerTenant, "payload_tenant_id", event.TenantID)
		})
		return fmt.Errorf("tenant context mismatch: header=%q payload=%q", headerTenant, event.TenantID)
	}

	// 4. seenSeq 乱序过滤：已见过且 seq <= last 时丢弃过期事件。
	c.mu.Lock()
	last, seen := c.seenSeq[event.InstanceID]
	c.mu.Unlock()
	if seen && event.EventSeq <= last {
		c.safeLog(func(l *slog.Logger) {
			l.WarnContext(ctx, "stale event discarded (seq <= seenSeq)",
				"instance_id", event.InstanceID, "event_seq", event.EventSeq, "seen_seq", last)
		})
		return nil
	}

	// 5. 路由处理。
	var processErr error
	switch event.NewStatus {
	case "running":
		gpuCount := 0
		if event.GPUSpec != nil {
			gpuCount = event.GPUSpec.Count
		}
		spec := buildSpec(event.TenantID, event.InstanceID, event.Name, event.WorkloadKind, gpuCount)
		processErr = c.metering.StartCollection(ctx, spec)
	case "stopped", "failed", "deleted":
		processErr = c.metering.StopCollection(ctx, event.InstanceID)
	default:
		c.safeLog(func(l *slog.Logger) {
			l.WarnContext(ctx, "unknown status, ack to skip",
				"instance_id", event.InstanceID, "new_status", event.NewStatus)
		})
		return nil
	}

	// 6. 处理失败 → Nak 重投，不推进 seenSeq。
	if processErr != nil {
		c.safeLog(func(l *slog.Logger) {
			l.ErrorContext(ctx, "process event failed, nak for redelivery",
				"instance_id", event.InstanceID, "new_status", event.NewStatus, "err", processErr)
		})
		return processErr
	}

	// 7. 处理成功 → 推进 seenSeq（仅在 event.EventSeq > 当前值时更新）。
	c.mu.Lock()
	if event.EventSeq > c.seenSeq[event.InstanceID] {
		c.seenSeq[event.InstanceID] = event.EventSeq
	}
	c.mu.Unlock()

	return nil
}

// HandleEvent 返回消息处理回调函数，供 MessageBus.Subscribe 使用。
// 内部委托给未导出的 handleEvent，保持消息处理逻辑封装在包内。
func (c *Consumer) HandleEvent() ports.MessageHandler {
	return c.handleEvent
}

// safeLog 在 logger 为 nil 时跳过日志输出，便于单测无需注入 logger。
func (c *Consumer) safeLog(fn func(*slog.Logger)) {
	if c.logger != nil {
		fn(c.logger)
	}
}
