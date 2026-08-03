package eventconsumer

import (
	"context"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// mockMessageBus 实现 ports.MessageBus，用于单测 mock。
type mockMessageBus struct {
	subscribeOpts []ports.SubscribeOptions
	subscribeHdlr []ports.MessageHandler
	subscribeSub  *mockSubscription
	subscribeErr  error
}

func (m *mockMessageBus) Publish(ctx context.Context, event ports.EventEnvelope, opts ports.PublishOptions) error {
	return nil
}

func (m *mockMessageBus) Subscribe(ctx context.Context, opts ports.SubscribeOptions, handler ports.MessageHandler) (ports.Subscription, error) {
	m.subscribeOpts = append(m.subscribeOpts, opts)
	m.subscribeHdlr = append(m.subscribeHdlr, handler)
	if m.subscribeSub == nil {
		m.subscribeSub = &mockSubscription{}
	}
	return m.subscribeSub, m.subscribeErr
}

// mockSubscription 实现 ports.Subscription。
type mockSubscription struct{}

func (m *mockSubscription) Drain(ctx context.Context) error {
	return nil
}

// mockMessage 实现 ports.Message，可控 Headers/Data。
type mockMessage struct {
	headers map[string][]string
	data    []byte
}

func (m *mockMessage) Subject() string                   { return "" }
func (m *mockMessage) Data() []byte                       { return m.data }
func (m *mockMessage) Headers() map[string][]string        { return m.headers }

func TestConsumerStart(t *testing.T) {
	mbus := &mockMessageBus{}
	c := NewConsumer(mbus, nil)

	ctx := context.Background()
	err := c.Start(ctx)
	if err != nil {
		t.Fatalf("Start unexpected error: %v", err)
	}

	// 验证 Subscribe 被调用且参数正确。
	if len(mbus.subscribeOpts) != 1 {
		t.Fatalf("expected Subscribe called once, got %d", len(mbus.subscribeOpts))
	}
	opts := mbus.subscribeOpts[0]
	if opts.Subject != "ani.events.instance.>" {
		t.Errorf("Subject = %q, want %q", opts.Subject, "ani.events.instance.>")
	}
	if opts.Consumer != "metering-example" {
		t.Errorf("Consumer = %q, want %q", opts.Consumer, "metering-example")
	}
	if opts.Queue != "metering" {
		t.Errorf("Queue = %q, want %q", opts.Queue, "metering")
	}
	if opts.MaxInflight != 16 {
		t.Errorf("MaxInflight = %d, want 16", opts.MaxInflight)
	}
	if opts.AckWait != 30*time.Second {
		t.Errorf("AckWait = %v, want 30s", opts.AckWait)
	}
	if opts.MaxDeliver != 10 {
		t.Errorf("MaxDeliver = %d, want 10", opts.MaxDeliver)
	}
}

func TestConsumerHandlePoisonMessage(t *testing.T) {
	c := NewConsumer(nil, nil)

	msg := &mockMessage{
		headers: map[string][]string{"tenant-id": []string{"tenant-123"}},
		data:    []byte("invalid-json-payload"),
	}

	err := c.handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("handle 期望返回 nil（毒丸吞错误让 adapter Ack），实际 err=%v", err)
	}
}

func TestConsumerHandleSuccess(t *testing.T) {
	c := NewConsumer(nil, nil)

	payload := []byte(`{"event_type":"instance.created","instance_id":"inst-456"}`)
	msg := &mockMessage{
		headers: map[string][]string{"tenant-id": []string{"tenant-789"}},
		data:    payload,
	}

	err := c.handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("handle 期望返回 nil（成功让 adapter Ack），实际 err=%v", err)
	}
}

func TestConsumerStop(t *testing.T) {
	mbus := &mockMessageBus{}
	c := NewConsumer(mbus, nil)

	ctx := context.Background()
	err := c.Start(ctx)
	if err != nil {
		t.Fatalf("Start unexpected error: %v", err)
	}

	err = c.Stop(ctx)
	if err != nil {
		t.Fatalf("Stop unexpected error: %v", err)
	}
}

func TestConsumerStopWithoutStart(t *testing.T) {
	c := NewConsumer(nil, nil)
	err := c.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop without Start should return nil, got error: %v", err)
	}
}
