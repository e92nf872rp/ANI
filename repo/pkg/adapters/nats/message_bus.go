package nats

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
	natsgo "github.com/nats-io/nats.go"
)

// MessageBus NATS JetStream 消息总线 adapter。
// js 使用内部 jetStream 接口，生产路径注入 natsgo.JetStreamContext，测试路径可注入 fake 实现。
type MessageBus struct {
	js jetStream

	// msgFactory 用于创建 ports.Message，生产路径为 nil（使用默认实现），测试路径可注入 mock factory。
	msgFactory func(*natsgo.Msg) ports.Message

	logger *slog.Logger
}

var _ ports.MessageBus = (*MessageBus)(nil)

// NewMessageBus 创建 NATS JetStream 消息总线 adapter，注入 logger 用于记录错误路径。
// 生产路径传入 natsgo.JetStreamContext，测试路径可传入内部 jetStream 实现（如 fakeJS）。
func NewMessageBus(js jetStream, logger *slog.Logger) *MessageBus {
	return &MessageBus{js: js, logger: logger}
}

func (b *MessageBus) Publish(ctx context.Context, event ports.EventEnvelope, opts ports.PublishOptions) error {
	if opts.Subject == "" {
		return fmt.Errorf("message bus publish: subject required")
	}
	// 将 EventEnvelope 元数据写入 NATS Message Header（小写连字符 key），供消费侧重建租户上下文。
	header := natsgo.Header{
		"tenant-id":      []string{event.TenantID},
		"aggregate-id":   []string{event.AggregateID},
		"aggregate-type": []string{event.AggregateType},
		"event-type":     []string{event.EventType},
		"occurred-at":    []string{event.OccurredAt.Format(time.RFC3339Nano)},
	}
	pubOpts := []natsgo.PubOpt{natsgo.Context(ctx)}
	if opts.Key != "" {
		pubOpts = append(pubOpts, natsgo.MsgId(opts.Key))
	}
	_, err := b.js.PublishMsg(&natsgo.Msg{
		Subject: opts.Subject,
		Data:    event.Payload,
		Header:  header,
	}, pubOpts...)
	if err != nil {
		return fmt.Errorf("message bus publish: %w", err)
	}
	return nil
}

// Subscribe 订阅 subject。
// ctx 仅用于建立订阅的语义契约，当前 NATS 异步回调模型未使用该 ctx；
// handler 固定收到 context.Background()，消息处理不受订阅 ctx 生命周期影响。
func (b *MessageBus) Subscribe(ctx context.Context, opts ports.SubscribeOptions, handler ports.MessageHandler) (ports.Subscription, error) {
	if opts.Subject == "" {
		return nil, fmt.Errorf("message bus subscribe: subject required")
	}
	subOpts := []natsgo.SubOpt{natsgo.ManualAck()}
	if opts.Consumer != "" {
		subOpts = append(subOpts, natsgo.Durable(opts.Consumer))
	}
	if opts.MaxInflight > 0 {
		subOpts = append(subOpts, natsgo.MaxAckPending(opts.MaxInflight))
	}
	handlerFunc := func(msg *natsgo.Msg) {
		var pMsg ports.Message
		if b.msgFactory != nil {
			pMsg = b.msgFactory(msg)
		} else {
			pMsg = message{msg: msg}
		}
		defer func() {
			if r := recover(); r != nil {
				if b.logger != nil {
					b.logger.Error("handler panic recovered, nacking for redelivery",
						"subject", msg.Subject, "panic", r)
				}
				// panic 兜底：消息状态未知，Nak 触发重投
				_ = msg.Nak()
			}
		}()
		// 每条消息独立上下文：不绑定 Subscribe 调用方 ctx，
		// 避免订阅 ctx 取消时正在处理的消息被中断；业务侧需 timeout 自行 WithTimeout
		if err := handler(context.Background(), pMsg); err != nil {
			if b.logger != nil {
				b.logger.Warn("handler returned error, nacking for redelivery",
					"subject", msg.Subject, "err", err)
			}
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	}
	var (
		sub *natsgo.Subscription
		err error
	)
	if opts.AckWait > 0 {
		subOpts = append(subOpts, natsgo.AckWait(opts.AckWait))
	}
	if opts.MaxDeliver > 0 {
		subOpts = append(subOpts, natsgo.MaxDeliver(opts.MaxDeliver))
	}
	if opts.Queue != "" {
		sub, err = b.js.QueueSubscribe(opts.Subject, opts.Queue, handlerFunc, subOpts...)
	} else {
		sub, err = b.js.Subscribe(opts.Subject, handlerFunc, subOpts...)
	}
	if err != nil {
		return nil, fmt.Errorf("message bus subscribe: %w", err)
	}
	return subscription{sub: sub}, nil
}

type message struct {
	msg *natsgo.Msg
}

func (m message) Subject() string {
	return m.msg.Subject
}

func (m message) Data() []byte {
	return m.msg.Data
}

// Headers 返回 NATS 消息头的 map 视图。
// 当消息头为 nil 时返回 nil，与 ports.Message 契约一致。
func (m message) Headers() map[string][]string {
	return map[string][]string(m.msg.Header)
}

type subscription struct {
	sub *natsgo.Subscription
}

func (s subscription) Drain(context.Context) error {
	return s.sub.Drain()
}
