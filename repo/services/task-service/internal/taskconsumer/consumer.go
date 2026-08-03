package taskconsumer

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// Consumer 是 task 流示例 consumer，验证 task 消息端到端链路连通性。
//
// 本 consumer 仅覆盖示例阶段：收到 task 消息后打印日志即返回 nil（让 adapter Ack），
// 不包含真实 task 处理逻辑（lease 抢占、进度上报等由真实 lease_reconciler 实现）。
// 业务侧用返回值表达 ack/nak 意图，adapter 统一执行；毒丸/跳过场景必须记日志后返回 nil，不可 return err 否则毒丸无限重投。
type Consumer struct {
	bus    ports.MessageBus
	logger *slog.Logger
	sub    ports.Subscription
}

// NewConsumer 创建 task 示例 consumer。
func NewConsumer(bus ports.MessageBus, logger *slog.Logger) *Consumer {
	return &Consumer{
		bus:    bus,
		logger: logger,
	}
}

// Start 订阅 ani.tasks.model.import，配置 AckWait=30s、MaxDeliver=10、MaxInflight=16。
func (c *Consumer) Start(ctx context.Context) error {
	sub, err := c.bus.Subscribe(ctx, ports.SubscribeOptions{
		Subject:     "ani.tasks.model.import",
		Consumer:    "task-example",
		Queue:       "task-workers",
		MaxInflight: 16,
		AckWait:     30 * time.Second,
		MaxDeliver:  10,
	}, c.handle)
	if err != nil {
		return err
	}
	c.sub = sub
	return nil
}

// Stop 关闭订阅。
func (c *Consumer) Stop(ctx context.Context) error {
	if c.sub != nil {
		return c.sub.Drain(ctx)
	}
	return nil
}

// safeLog 在 logger 为 nil 时跳过日志输出，便于单测无需注入 logger。
func (c *Consumer) safeLog(fn func(*slog.Logger)) {
	if c.logger != nil {
		fn(c.logger)
	}
}

// handle 是消息处理回调：从 headers 重建租户上下文，解析 payload，打印日志后返回 nil。
// 返回 nil → adapter Ack；返回 error → adapter Nak 重投。
// 毒丸/跳过场景必须记 error 日志后返回 nil（吞错误），不可 return err 否则毒丸无限重投。
func (c *Consumer) handle(ctx context.Context, msg ports.Message) error {
	// 1. 从 NATS Header 重建租户上下文。
	tenantID := msg.Headers()["tenant-id"]
	if len(tenantID) > 0 {
		c.safeLog(func(l *slog.Logger) { l.InfoContext(ctx, "recovered tenant context", "tenant-id", tenantID[0]) })
	}

	// 2. 解析 payload。
	var task modelImportTask
	if err := json.Unmarshal(msg.Data(), &task); err != nil {
		// 毒丸消息：记 error 日志后返回 nil，让 adapter Ack 跳过。
		c.safeLog(func(l *slog.Logger) {
			l.ErrorContext(ctx, "parse task failed (poison message), ack to skip", "err", err)
		})
		return nil
	}

	c.safeLog(func(l *slog.Logger) {
		l.InfoContext(ctx, "received task", "task_id", task.TaskID, "repo_id", task.RepoID)
	})

	// 3. 示例阶段：链路验证通过即返回 nil（让 adapter Ack）。
	// 真实逻辑接入后：lease 抢占 + task 执行，失败时 return error 触发 Nak 重投。
	return nil
}

// modelImportTask 示例用 payload 结构，对应 pkg/nats/messages.go ModelImportMsg。
type modelImportTask struct {
	TaskID string `json:"task_id"`
	RepoID string `json:"repo_id"`
}
