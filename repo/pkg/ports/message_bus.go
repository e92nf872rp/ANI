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

type Message interface {
	Subject() string
	Data() []byte
	Ack(ctx context.Context) error
	Nack(ctx context.Context) error
	Headers() map[string][]string
}

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
