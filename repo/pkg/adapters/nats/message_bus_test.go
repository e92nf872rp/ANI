package nats

import (
	"context"
	"sync"
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

// =============================================================================
// fakeMessage — ports.Message 的纯数据 fake 实现（无 Ack/Nack，业务侧不再具备该能力）
// =============================================================================

type fakeMessage struct {
	subject string
	data    []byte
	headers map[string][]string
}

func newFakeMessage(subject string, data []byte, headers map[string][]string) *fakeMessage {
	return &fakeMessage{
		subject: subject,
		data:    data,
		headers: headers,
	}
}

func (f *fakeMessage) Subject() string              { return f.subject }
func (f *fakeMessage) Data() []byte                 { return f.data }
func (f *fakeMessage) Headers() map[string][]string { return f.headers }

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

// TestHandlerBackgroundCtx 验证 handler 收到的 ctx 是 context.Background()（未被取消）。
// 覆盖改进一：每条消息独立上下文，不绑定 Subscribe 调用方 ctx。
func TestHandlerBackgroundCtx(t *testing.T) {
	js := newFakeJS()
	// 传入一个会被立即取消的 ctx，验证 handler 收到的仍是 Background（未被取消）。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	bus := NewMessageBus(js, nil)

	bus.msgFactory = func(natsMsg *natsgo.Msg) ports.Message {
		return newFakeMessage(natsMsg.Subject, natsMsg.Data, nil)
	}

	var gotCtx context.Context
	if _, err := bus.Subscribe(ctx, ports.SubscribeOptions{
		Subject: "event.test",
	}, func(ctx context.Context, msg ports.Message) error {
		gotCtx = ctx
		return nil
	}); err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}

	js.triggerCall(&natsgo.Msg{Subject: "event.test", Data: []byte("data")})

	if gotCtx == nil {
		t.Fatal("handler 未被调用，gotCtx 为 nil")
	}
	if gotCtx.Err() != nil {
		t.Errorf("handler ctx 期望未被取消（context.Background()），实际 err=%v", gotCtx.Err())
	}
}

// TestHandlerPanicNoCrash 验证 handler panic 时 adapter recover 兜底，进程不崩溃。
// ack/nak 正确性（panic → Nak）由集成测试覆盖，单测只验证不崩溃。
func TestHandlerPanicNoCrash(t *testing.T) {
	js := newFakeJS()
	bus := NewMessageBus(js, nil)

	bus.msgFactory = func(natsMsg *natsgo.Msg) ports.Message {
		return newFakeMessage(natsMsg.Subject, natsMsg.Data, nil)
	}

	if _, err := bus.Subscribe(context.Background(), ports.SubscribeOptions{
		Subject: "event.test",
	}, func(ctx context.Context, msg ports.Message) error {
		panic("intentional panic in handler")
	}); err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("adapter 未捕获 panic，外层崩溃: %v", r)
			}
		}()
		js.triggerCall(&natsgo.Msg{Subject: "event.test", Data: []byte("data")})
	}()
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
