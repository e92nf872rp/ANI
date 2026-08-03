package eventconsumer

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// Consumer 是 metering 示例 consumer，验证 headers 租户上下文重建和消息链路连通性。
//
// 本 consumer 仅覆盖示例阶段：链路验证通过即返回 nil（让 adapter Ack），不包含真实 StartCollection 逻辑（7b 阶段接入）。
// 业务侧用返回值表达 ack/nak 意图，adapter 统一执行；毒丸/跳过场景必须记日志后返回 nil，不可 return err 否则毒丸无限重投。
type Consumer struct {
	bus    ports.MessageBus
	logger *slog.Logger
	sub    ports.Subscription
}

// NewConsumer 创建 metering 示例 consumer。
func NewConsumer(bus ports.MessageBus, logger *slog.Logger) *Consumer {
	return &Consumer{
		bus:    bus,
		logger: logger,
	}
}

// Start 订阅 ani.events.instance.>，配置 AckWait=30s、MaxDeliver=10、MaxInflight=16。
func (c *Consumer) Start() error {
	sub, err := c.bus.Subscribe(ports.SubscribeOptions{
		Subject:     "ani.events.instance.>",
		Consumer:    "metering-example",
		Queue:       "metering",
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

// handle 是消息处理回调：从 headers 重建租户上下文，解析 payload，按返回值表达 ack/nak 意图。
// 返回 nil → adapter Ack；返回 error → adapter Nak 重投。
// 毒丸/跳过场景必须记 error 日志后返回 nil（吞错误），不可 return err 否则毒丸无限重投。
func (c *Consumer) handle(ctx context.Context, msg ports.Message) error {
	// 1. 从 NATS Header 重建租户上下文。
	tenantID := msg.Headers()["tenant-id"]
	if len(tenantID) > 0 {
		c.safeLog(func(l *slog.Logger) { l.InfoContext(ctx, "recovered tenant context", "tenant-id", tenantID[0]) })
	}

	// 2. 解析 payload。
	var event instanceEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		// 毒丸消息：记 error 日志后返回 nil，让 adapter Ack 跳过。
		c.safeLog(func(l *slog.Logger) {
			l.ErrorContext(ctx, "parse event failed (poison message), ack to skip", "err", err)
		})
		return nil
	}

	c.safeLog(func(l *slog.Logger) {
		l.InfoContext(ctx, "received instance event", "event_type", event.EventType, "instance_id", event.InstanceID)
	})

	// 3. 示例阶段：链路验证通过即返回 nil（让 adapter Ack）。
	// 真实逻辑接入后：
	//   if err := c.metering.StartCollection(ctx, ...); err != nil {
	//       return err  // 可恢复失败 → adapter Nak 重投
	//   }
	//   return nil     // 成功 → adapter Ack
	return nil
}

// instanceEvent 示例用 payload 结构，真实结构后续 PR 定义。
type instanceEvent struct {
	EventType  string `json:"event_type"`
	InstanceID string `json:"instance_id"`
}
