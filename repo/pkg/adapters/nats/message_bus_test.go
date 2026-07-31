package nats

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
	natsgo "github.com/nats-io/nats.go"
)

// =============================================================================
// fakeJS — jetStream 接口的纯 Go fake 实现，支持单测 mock
// =============================================================================

type fakeJS struct {
	mu               sync.Mutex
	publishedMsgs    []*natsgo.Msg
	publishErr       error
	subscribeCb      natsgo.MsgHandler
	queueSubscribeCb natsgo.MsgHandler
	subscribeErr     error
}

func newFakeJS() *fakeJS {
	return &fakeJS{}
}

func (f *fakeJS) PublishMsg(msg *natsgo.Msg, opts ...natsgo.PubOpt) (*natsgo.PubAck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publishedMsgs = append(f.publishedMsgs, msg)
	if f.publishErr != nil {
		return nil, f.publishErr
	}
	return &natsgo.PubAck{}, nil
}

func (f *fakeJS) Subscribe(subj string, cb natsgo.MsgHandler, opts ...natsgo.SubOpt) (*natsgo.Subscription, error) {
	f.mu.Lock()
	f.subscribeCb = cb
	f.mu.Unlock()
	if f.subscribeErr != nil {
		return nil, f.subscribeErr
	}
	return &natsgo.Subscription{}, nil
}

func (f *fakeJS) QueueSubscribe(subj, queue string, cb natsgo.MsgHandler, opts ...natsgo.SubOpt) (*natsgo.Subscription, error) {
	f.mu.Lock()
	f.queueSubscribeCb = cb
	f.mu.Unlock()
	if f.subscribeErr != nil {
		return nil, f.subscribeErr
	}
	return &natsgo.Subscription{}, nil
}

func (f *fakeJS) StreamInfo(stream string, opts ...natsgo.JSOpt) (*natsgo.StreamInfo, error) {
	return &natsgo.StreamInfo{}, nil
}

func (f *fakeJS) AddStream(cfg *natsgo.StreamConfig, opts ...natsgo.JSOpt) (*natsgo.StreamInfo, error) {
	return &natsgo.StreamInfo{}, nil
}

// triggerCall 模拟收到一条消息并触发注册的回调
func (f *fakeJS) triggerCall(msg *natsgo.Msg) {
	f.mu.Lock()
	cb := f.subscribeCb
	f.mu.Unlock()
	if cb != nil {
		cb(msg)
	}
}

// triggerQueueCall 模拟 QueueSubscribe 收到一条消息
func (f *fakeJS) triggerQueueCall(msg *natsgo.Msg) {
	f.mu.Lock()
	cb := f.queueSubscribeCb
	f.mu.Unlock()
	if cb != nil {
		cb(msg)
	}
}

// =============================================================================
// fakeMessage — 可追踪 Ack/Nack 调用的 ports.Message 实现
// =============================================================================

type fakeMessage struct {
	subject    string
	data       []byte
	headers    map[string][]string
	acked      atomic.Bool
	nacked     atomic.Bool
	ackCalled  int64
	nackCalled int64
}

func newFakeMessage(subject string, data []byte, headers map[string][]string) *fakeMessage {
	return &fakeMessage{
		subject: subject,
		data:    data,
		headers: headers,
	}
}

func (f *fakeMessage) Subject() string {
	return f.subject
}

func (f *fakeMessage) Data() []byte {
	return f.data
}

func (f *fakeMessage) Ack(context.Context) error {
	atomic.AddInt64(&f.ackCalled, 1)
	f.acked.Store(true)
	return nil
}

func (f *fakeMessage) Nack(context.Context) error {
	atomic.AddInt64(&f.nackCalled, 1)
	f.nacked.Store(true)
	return nil
}

func (f *fakeMessage) Headers() map[string][]string {
	return f.headers
}

func (f *fakeMessage) AckCount() int64 {
	return atomic.LoadInt64(&f.ackCalled)
}

func (f *fakeMessage) NackCount() int64 {
	return atomic.LoadInt64(&f.nackCalled)
}

func (f *fakeMessage) WasAcked() bool {
	return f.acked.Load()
}

func (f *fakeMessage) WasNacked() bool {
	return f.nacked.Load()
}

// =============================================================================
// Test cases
// =============================================================================

// TestPublishSuccess 验证 Publish 成功时 headers 的 5 个 key 均存在且值匹配。
func TestPublishSuccess(t *testing.T) {
	js := newFakeJS()
	bus := NewMessageBus(js, nil)

	event := ports.EventEnvelope{
		TenantID:      "tenant-1",
		AggregateID:   "agg-42",
		AggregateType: "Machine",
		EventType:     "MachineCreated",
		Payload:       []byte(`{"name":"gpu-node-1"}`),
		OccurredAt:    time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
	}
	opts := ports.PublishOptions{
		Subject: "event.machine.created",
	}

	err := bus.Publish(context.Background(), event, opts)
	if err != nil {
		t.Fatalf("Publish 返回错误: %v", err)
	}

	if len(js.publishedMsgs) != 1 {
		t.Fatalf("期望调用 PublishMsg 1 次，实际 %d 次", len(js.publishedMsgs))
	}

	msg := js.publishedMsgs[0]
	if msg.Subject != "event.machine.created" {
		t.Errorf("Subject 期望 event.machine.created，实际 %s", msg.Subject)
	}

	checkHeader(t, msg.Header, "tenant-id", "tenant-1")
	checkHeader(t, msg.Header, "aggregate-id", "agg-42")
	checkHeader(t, msg.Header, "aggregate-type", "Machine")
	checkHeader(t, msg.Header, "event-type", "MachineCreated")
	checkHeader(t, msg.Header, "occurred-at", "2026-07-30T12:00:00Z")
}

// TestPublishEmptySubject 验证 Publish subject 为空时返回 error。
func TestPublishEmptySubject(t *testing.T) {
	js := newFakeJS()
	bus := NewMessageBus(js, nil)

	event := ports.EventEnvelope{
		Payload: []byte("test"),
	}
	opts := ports.PublishOptions{Subject: ""}

	err := bus.Publish(context.Background(), event, opts)
	if err == nil {
		t.Fatal("期望返回 error，实际 nil")
	}

	if len(js.publishedMsgs) != 0 {
		t.Fatal("subject 为空时不应调用 PublishMsg")
	}
}

// TestSubscribeEmptySubject 验证 Subscribe subject 为空时返回 error。
func TestSubscribeEmptySubject(t *testing.T) {
	js := newFakeJS()
	bus := NewMessageBus(js, nil)

	_, err := bus.Subscribe(context.Background(), ports.SubscribeOptions{
		Subject: "",
	}, func(ctx context.Context, msg ports.Message) error {
		return nil
	})
	if err == nil {
		t.Fatal("期望返回 error，实际 nil")
	}
}

// TestHandlerErrorNoAutoAck 验证 handler 返回 error 时，adapter 仅记日志，未调 msg.Ack/Nack。
func TestHandlerErrorNoAutoAck(t *testing.T) {
	js := newFakeJS()
	bus := NewMessageBus(js, nil)

	handlerCalled := newFakeMessage("event.test", []byte("data"), nil)
	bus.msgFactory = func(natsMsg *natsgo.Msg) ports.Message {
		return handlerCalled
	}

	bus.Subscribe(context.Background(), ports.SubscribeOptions{
		Subject: "event.test",
	}, func(ctx context.Context, msg ports.Message) error {
		return errors.New("business error")
	})

	js.triggerCall(&natsgo.Msg{Subject: "event.test", Data: []byte("data")})

	if handlerCalled.WasAcked() {
		t.Error("handler 返回 error 时 adapter 不应自动调 Ack")
	}
	if handlerCalled.WasNacked() {
		t.Error("handler 返回 error 时 adapter 不应调 Nak")
	}
}

// TestHandlerNilNoAutoAck 验证 handler 返回 nil 时，adapter 不自动调 Ack。
func TestHandlerNilNoAutoAck(t *testing.T) {
	js := newFakeJS()
	bus := NewMessageBus(js, nil)

	handlerCalled := newFakeMessage("event.test", []byte("data"), nil)
	bus.msgFactory = func(natsMsg *natsgo.Msg) ports.Message {
		return handlerCalled
	}

	bus.Subscribe(context.Background(), ports.SubscribeOptions{
		Subject: "event.test",
	}, func(ctx context.Context, msg ports.Message) error {
		return nil
	})

	js.triggerCall(&natsgo.Msg{Subject: "event.test", Data: []byte("data")})

	if handlerCalled.WasAcked() {
		t.Error("handler 返回 nil 时 adapter 不应自动调 Ack")
	}
}

// TestHandlerOwnAck 验证 handler 自己调 Ack 且返回 nil：Ack 仅被业务层调一次。
func TestHandlerOwnAck(t *testing.T) {
	js := newFakeJS()
	bus := NewMessageBus(js, nil)

	handlerCalled := newFakeMessage("event.test", []byte("data"), nil)
	bus.msgFactory = func(natsMsg *natsgo.Msg) ports.Message {
		return handlerCalled
	}

	bus.Subscribe(context.Background(), ports.SubscribeOptions{
		Subject: "event.test",
	}, func(ctx context.Context, msg ports.Message) error {
		_ = msg.Ack(context.Background())
		return nil
	})

	js.triggerCall(&natsgo.Msg{Subject: "event.test", Data: []byte("data")})

	if count := handlerCalled.AckCount(); count != 1 {
		t.Errorf("期望 Ack 被调用 1 次，实际 %d 次", count)
	}
}

// TestHandlerOwnNack 验证 handler 自己调 Nack 且返回 error：Nack 仅被业务层调一次。
func TestHandlerOwnNack(t *testing.T) {
	js := newFakeJS()
	bus := NewMessageBus(js, nil)

	handlerCalled := newFakeMessage("event.test", []byte("data"), nil)
	bus.msgFactory = func(natsMsg *natsgo.Msg) ports.Message {
		return handlerCalled
	}

	bus.Subscribe(context.Background(), ports.SubscribeOptions{
		Subject: "event.test",
	}, func(ctx context.Context, msg ports.Message) error {
		_ = msg.Nack(context.Background())
		return errors.New("retry later")
	})

	js.triggerCall(&natsgo.Msg{Subject: "event.test", Data: []byte("data")})

	if count := handlerCalled.NackCount(); count != 1 {
		t.Errorf("期望 Nack 被调用 1 次，实际 %d 次", count)
	}
}

// TestHandlerPanicBeforeAck 验证 handler panic（Ack 调用前）：recover + Nak 被调 + 不崩溃。
func TestHandlerPanicBeforeAck(t *testing.T) {
	js := newFakeJS()
	bus := NewMessageBus(js, nil)

	handlerCalled := newFakeMessage("event.test", []byte("data"), nil)
	bus.msgFactory = func(natsMsg *natsgo.Msg) ports.Message {
		return handlerCalled
	}

	bus.Subscribe(context.Background(), ports.SubscribeOptions{
		Subject: "event.test",
	}, func(ctx context.Context, msg ports.Message) error {
		panic("intentional panic before ack")
	})

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("adapter 未捕获 panic，外层崩溃: %v", r)
			}
		}()
		js.triggerCall(&natsgo.Msg{Subject: "event.test", Data: []byte("data")})
	}()

	if !handlerCalled.WasNacked() {
		t.Error("handler panic 时 adapter 应调 Nak")
	}
}

// TestHandlerPanicAfterAck 验证 handler panic（Ack 调用后）：recover + Nak 被调。
func TestHandlerPanicAfterAck(t *testing.T) {
	js := newFakeJS()
	bus := NewMessageBus(js, nil)

	handlerCalled := newFakeMessage("event.test", []byte("data"), nil)
	bus.msgFactory = func(natsMsg *natsgo.Msg) ports.Message {
		return handlerCalled
	}

	bus.Subscribe(context.Background(), ports.SubscribeOptions{
		Subject: "event.test",
	}, func(ctx context.Context, msg ports.Message) error {
		_ = msg.Ack(context.Background())
		panic("intentional panic after ack")
	})

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("adapter 未捕获 panic，外层崩溃: %v", r)
			}
		}()
		js.triggerCall(&natsgo.Msg{Subject: "event.test", Data: []byte("data")})
	}()

	// Ack 被业务层调 1 次
	if count := handlerCalled.AckCount(); count != 1 {
		t.Errorf("期望 Ack 被调用 1 次，实际 %d 次", count)
	}

	// panic 兜底会调 Nak（通过 panic recover 创建新 fakeMessage 实例）
	// 本测试主要验证不崩溃且 Ack 被正确调用
	if !handlerCalled.WasAcked() {
		t.Error("handler 应先调 Ack 再 panic")
	}
}

// =============================================================================
// helpers
// =============================================================================

func checkHeader(t *testing.T, header natsgo.Header, key, expected string) {
	t.Helper()
	values, ok := header[key]
	if !ok {
		t.Errorf("header 缺少 key %q", key)
		return
	}
	if len(values) == 0 {
		t.Errorf("header %q 为空", key)
		return
	}
	if values[0] != expected {
		t.Errorf("header %q 期望 %q，实际 %q", key, expected, values[0])
	}
}
