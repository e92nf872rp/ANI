package ports

import (
	"context"
	"time"
)

type EventEnvelope struct {
	TenantID      string
	AggregateID   string
	AggregateType string
	EventType     string
	Payload       []byte
	OccurredAt    time.Time
}

type PublishOptions struct {
	Subject string
	Key     string
}

// Message 表示一条 JetStream 消息的只读视图。
// ack/nak 不暴露给业务层，由 adapter 根据 MessageHandler 返回值统一执行：
//   - handler 返回 nil   → adapter 调 Ack
//   - handler 返回 error → adapter 调 Nak
//   - handler panic      → adapter recover 后调 Nak
type Message interface {
	Subject() string
	Data() []byte
	Headers() map[string][]string
}

// MessageHandler 是业务侧消息处理回调。
//
// 返回值即 ack/nak 意图，业务侧不再显式调 Ack/Nack：
//   - 返回 nil   → adapter 调 Ack（消息已处理）
//   - 返回 error → adapter 调 Nak（消息需重投）
//   - panic      → adapter recover 后调 Nak（兜底重投）
//
// 业务侧兼容契约（重要）：
//   - 处理成功 / 毒丸跳过 / 幂等跳过 → 返回 nil（业务侧自行记 error/warn 日志后吞掉错误）
//   - 可恢复失败（DB 不可用等）       → 返回 error（adapter 自动 Nak 重投）
//   - 业务侧需自行判断"该跳过"还是"该重投"，吞错误时必须记日志便于排查
//
// handler 收到的 ctx 固定为 context.Background()（每条消息独立上下文），
// 不绑定 Subscribe 调用方 ctx；需要 timeout 时业务侧自行 context.WithTimeout。
type MessageHandler func(context.Context, Message) error

type Subscription interface {
	Drain(ctx context.Context) error
}

type SubscribeOptions struct {
	Subject     string
	Consumer    string
	Queue       string
	MaxInflight int
	AckWait     time.Duration
	MaxDeliver  int
}

type MessageBus interface {
	Publish(ctx context.Context, event EventEnvelope, opts PublishOptions) error
	Subscribe(ctx context.Context, opts SubscribeOptions, handler MessageHandler) (Subscription, error)
}
