package taskconsumer

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

// mockMessage 实现 ports.Message，可控 Headers/Data/Ack/Nack。
type mockMessage struct {
	headers     map[string][]string
	data        []byte
	ackCalled   int
	nackCalled  int
	ackErr      error
	nackErr     error
}

func (m *mockMessage) Subject() string { return "" }
func (m *mockMessage) Data() []byte    { return m.data }
func (m *mockMessage) Ack(ctx context.Context) error {
	m.ackCalled++
	return m.ackErr
}
func (m *mockMessage) Nack(ctx context.Context) error {
	m.nackCalled++
	return m.nackErr
}
func (m *mockMessage) Headers() map[string][]string { return m.headers }

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
	if opts.Subject != "ani.tasks.model.import" {
		t.Errorf("Subject = %q, want %q", opts.Subject, "ani.tasks.model.import")
	}
	if opts.Consumer != "task-example" {
		t.Errorf("Consumer = %q, want %q", opts.Consumer, "task-example")
	}
	if opts.Queue != "task-workers" {
		t.Errorf("Queue = %q, want %q", opts.Queue, "task-workers")
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
		t.Fatalf("handle unexpected error: %v", err)
	}
	if msg.ackCalled != 1 {
		t.Fatalf("expected Ack called once for poison message, got %d", msg.ackCalled)
	}
	if msg.nackCalled != 0 {
		t.Fatalf("expected Nack not called, got %d", msg.nackCalled)
	}
}

func TestConsumerHandleSuccess(t *testing.T) {
	c := NewConsumer(nil, nil)

	payload := []byte(`{"task_id":"task-456","repo_id":"Qwen/Qwen2.5-72B"}`)
	msg := &mockMessage{
		headers: map[string][]string{"tenant-id": []string{"tenant-789"}},
		data:    payload,
	}

	err := c.handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("handle unexpected error: %v", err)
	}
	if msg.ackCalled != 1 {
		t.Fatalf("expected Ack called once for success, got %d", msg.ackCalled)
	}
	if msg.nackCalled != 0 {
		t.Fatalf("expected Nack not called, got %d", msg.nackCalled)
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
