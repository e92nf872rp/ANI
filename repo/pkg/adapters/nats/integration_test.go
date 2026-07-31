//go:build integration

package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
	natsgo "github.com/nats-io/nats.go"
)

// 集成测试连接真实 NATS（本地 docker-compose 或 port-forward）。
// 默认连接 nats://127.0.0.1:4222，可通过 ANI_TEST_NATS_URL 环境变量覆盖。
//
// 运行命令：
//
//	ANI_TEST_NATS_URL=nats://10.10.1.66:31062 \
//	  go test ./pkg/adapters/nats/ -v -run Integration -tags integration
//
// 集成测试为手动验证项，不作为硬性门禁（PRD §8 Success Metrics：3B 选项）。

const (
	integrationStreamEvents = "ANI_EVENTS"
	integrationStreamTasks  = "ANI_TASKS"
	// 集成测试用 consumer 前缀，便于清理时识别。
	integrationConsumerPrefix = "ITEST"
	// 集成测试用 subject 前缀，避免与真实业务事件冲突。
	integrationSubjectPrefix = "ani.events.integration"
	// 测试超时上限：单条用例总等待时间。
	integrationWaitTimeout = 15 * time.Second
	// MaxDeliver 测试用上限（设小值以加速测试）。
	integrationMaxDeliver = 3
	// AckWait 测试用值（设小值以加速重投）。
	integrationAckWait = 1 * time.Second
)

// natsTestURL 读取环境变量，默认连本地 docker-compose NATS。
func natsTestURL() string {
	if u := os.Getenv("ANI_TEST_NATS_URL"); u != "" {
		return u
	}
	return "nats://127.0.0.1:4222"
}

// integrationEnv 封装一个集成测试用 NATS 连接和 adapter，统一管理生命周期。
type integrationEnv struct {
	nc   *natsgo.Conn
	js   natsgo.JetStreamContext
	bus  *MessageBus
	t    *testing.T
	mu   sync.Mutex
	subs []*natsgo.Subscription
}

// newIntegrationEnv 建立连接、校验 stream 前置条件、返回 env。
// 前置：ANI_EVENTS=InterestPolicy、ANI_TASKS=WorkQueuePolicy。
func newIntegrationEnv(t *testing.T) *integrationEnv {
	t.Helper()
	url := natsTestURL()
	nc, err := natsgo.Connect(url,
		natsgo.Timeout(5*time.Second),
		natsgo.ReconnectWait(500*time.Millisecond),
		natsgo.MaxReconnects(3),
		natsgo.Name("ani-integration-test"),
	)
	if err != nil {
		t.Fatalf("连接 NATS 失败 %s: %v（确认 NATS 已启动并可通过 ANI_TEST_NATS_URL 访问）", url, err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		t.Fatalf("初始化 JetStream 失败: %v", err)
	}

	env := &integrationEnv{nc: nc, js: js, bus: NewMessageBus(js, slog.Default()), t: t}
	t.Cleanup(env.cleanup)

	// 校验并修复 stream 前置条件（AC: 测试前确保 ANI_EVENTS 为 InterestPolicy，ANI_TASKS 为 WorkQueuePolicy）。
	env.ensureStreamsPolicy()
	return env
}

// ensureStreamsPolicy 确保 stream 存在且 policy 正确。
// 若 stream 不存在则按目标 policy 创建；若存在但 policy 不符则报错提示需手动清理。
func (e *integrationEnv) ensureStreamsPolicy() {
	e.ensureStream(integrationStreamEvents, []string{"ani.events.>"}, natsgo.InterestPolicy)
	e.ensureStream(integrationStreamTasks, []string{"ani.tasks.>"}, natsgo.WorkQueuePolicy)
}

func (e *integrationEnv) ensureStream(name string, subjects []string, retention natsgo.RetentionPolicy) {
	info, err := e.js.StreamInfo(name)
	if errors.Is(err, natsgo.ErrStreamNotFound) {
		_, err = e.js.AddStream(&natsgo.StreamConfig{
			Name:      name,
			Subjects:  subjects,
			Retention: retention,
			MaxAge:    24 * time.Hour,
			Storage:   natsgo.FileStorage,
			Replicas:  1,
		})
		if err != nil {
			e.t.Fatalf("创建 stream %s 失败: %v", name, err)
		}
		return
	}
	if err != nil {
		e.t.Fatalf("查询 stream %s 失败: %v", name, err)
	}
	if info.Config.Retention != retention {
		// AC: 测试前确保 stream 配置正确。
		// NATS 不允许通过 UpdateStream 在 WorkQueue 与 Interest 之间切换 retention，必须删 stream 重建。
		// 按 SPEC §3.4 Migration Plan 步骤 2：rm 后重建。仅在测试环境执行。
		if err := e.js.DeleteStream(name); err != nil {
			e.t.Fatalf("stream %s 当前 retention=%v，期望 %v；DeleteStream 重建失败: %v", name, info.Config.Retention, retention, err)
		}
		_, err = e.js.AddStream(&natsgo.StreamConfig{
			Name:      name,
			Subjects:  subjects,
			Retention: retention,
			MaxAge:    24 * time.Hour,
			Storage:   natsgo.FileStorage,
			Replicas:  1,
		})
		if err != nil {
			e.t.Fatalf("stream %s 重建为 %v 失败: %v", name, retention, err)
		}
	}
}

// cleanup 关闭订阅并清理本测试产生的 consumer 和 stream 消息（AC: 测试后清理 stream）。
func (e *integrationEnv) cleanup() {
	e.mu.Lock()
	subs := append([]*natsgo.Subscription(nil), e.subs...)
	e.mu.Unlock()

	// 先 Drain 订阅，确保回调退出，避免清理时 panic。
	for _, sub := range subs {
		if sub != nil && sub.IsValid() {
			_ = sub.Drain()
		}
	}
	// Drain 是异步的，给一点时间让回调退出。
	time.Sleep(300 * time.Millisecond)

	// 删除集成测试 consumer。
	for _, sub := range subs {
		if sub == nil {
			continue
		}
		if info, err := sub.ConsumerInfo(); err == nil && info != nil {
			_ = e.js.DeleteConsumer(info.Stream, info.Name)
		}
	}

	// Purge stream 中的集成测试消息（避免污染后续测试），保留 stream 配置。
	// 用 subject 过滤只清理本测试产生的消息，不影响真实业务消息。
	for _, stream := range []string{integrationStreamEvents, integrationStreamTasks} {
		_ = e.js.PurgeStream(stream, &natsgo.StreamPurgeRequest{
			Subject: integrationSubjectPrefix + ".>",
		})
	}

	if e.nc != nil {
		e.nc.Close()
	}
}

// trackSub 记录订阅以便统一清理。
func (e *integrationEnv) trackSub(sub *natsgo.Subscription) {
	e.mu.Lock()
	e.subs = append(e.subs, sub)
	e.mu.Unlock()
}

// publishEvent 发布一条带元数据的事件到指定 subject。
func (e *integrationEnv) publishEvent(subject string, event ports.EventEnvelope) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.bus.Publish(ctx, event, ports.PublishOptions{Subject: subject}); err != nil {
		e.t.Fatalf("Publish 失败 subject=%s: %v", subject, err)
	}
}

// sampleEvent 构造一条 instance.created 事件。
func sampleEvent(instanceID string) ports.EventEnvelope {
	return ports.EventEnvelope{
		TenantID:      "tenant-integration",
		AggregateID:   instanceID,
		AggregateType: "Instance",
		EventType:     "instance.created",
		Payload:       []byte(fmt.Sprintf(`{"instance_id":%q}`, instanceID)),
		OccurredAt:    time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
	}
}

// waitForCondition 轮询条件直到超时，用于等待 NATS 异步投递。
func waitForCondition(t *testing.T, name string, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("等待条件超时: %s (timeout=%s)", name, timeout)
}

// =============================================================================
// 前置条件：stream policy 校验
// =============================================================================

// TestIntegrationStreamPoliciesPrecondition 前置：确保 ANI_EVENTS 为 InterestPolicy、ANI_TASKS 为 WorkQueuePolicy。
func TestIntegrationStreamPoliciesPrecondition(t *testing.T) {
	env := newIntegrationEnv(t)

	events, err := env.js.StreamInfo(integrationStreamEvents)
	if err != nil {
		t.Fatalf("查询 ANI_EVENTS 失败: %v", err)
	}
	if events.Config.Retention != natsgo.InterestPolicy {
		t.Errorf("ANI_EVENTS retention 期望 InterestPolicy，实际 %v", events.Config.Retention)
	}

	tasks, err := env.js.StreamInfo(integrationStreamTasks)
	if err != nil {
		t.Fatalf("查询 ANI_TASKS 失败: %v", err)
	}
	if tasks.Config.Retention != natsgo.WorkQueuePolicy {
		t.Errorf("ANI_TASKS retention 期望 WorkQueuePolicy，实际 %v", tasks.Config.Retention)
	}
}

// =============================================================================
// 场景 1：Publish + Subscribe 端到端，验证 headers 5 个 key 匹配
// =============================================================================

// TestIntegrationPublishSubscribeHeaders 覆盖 AC: 发布一条 instance.created 事件，
// consumer 收到后验证 headers（tenant-id/aggregate-id/aggregate-type/event-type/occurred-at 均匹配）。
func TestIntegrationPublishSubscribeHeaders(t *testing.T) {
	env := newIntegrationEnv(t)
	subject := integrationSubjectPrefix + ".instance.created"
	instanceID := "inst-headers-001"

	var (
		got       atomic.Value // ports.Message
		received  atomic.Bool
	)
	sub, err := env.bus.Subscribe(context.Background(), ports.SubscribeOptions{
		Subject:    subject,
		Consumer:   integrationConsumerPrefix + "-headers",
		MaxInflight: 16,
		AckWait:    30 * time.Second,
		MaxDeliver: 10,
	}, func(ctx context.Context, msg ports.Message) error {
		got.Store(msg)
		received.Store(true)
		return msg.Ack(ctx)
	})
	if err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}
	env.trackSub(toNatsSub(sub))

	env.publishEvent(subject, sampleEvent(instanceID))

	waitForCondition(t, "收到事件", integrationWaitTimeout, received.Load)

	msg, ok := got.Load().(ports.Message)
	if !ok {
		t.Fatal("未收到消息")
	}
	headers := msg.Headers()
	if headers == nil {
		t.Fatal("Headers 为 nil")
	}
	assertHeader(t, headers, "tenant-id", "tenant-integration")
	assertHeader(t, headers, "aggregate-id", instanceID)
	assertHeader(t, headers, "aggregate-type", "Instance")
	assertHeader(t, headers, "event-type", "instance.created")
	assertHeader(t, headers, "occurred-at", "2026-07-30T12:00:00Z")
}

// =============================================================================
// 场景 2：Ack/Nak 业务层决定，adapter 不干预
// =============================================================================

// TestIntegrationAckBusinessDecision 覆盖 AC: handler 自己调 Ack，
// adapter 不干预，消息不再重投。
func TestIntegrationAckBusinessDecision(t *testing.T) {
	env := newIntegrationEnv(t)
	subject := integrationSubjectPrefix + ".ack"
	instanceID := "inst-ack-002"

	var deliverCount atomic.Int64
	sub, err := env.bus.Subscribe(context.Background(), ports.SubscribeOptions{
		Subject:    subject,
		Consumer:   integrationConsumerPrefix + "-ack",
		MaxInflight: 16,
		AckWait:    integrationAckWait,
		MaxDeliver: integrationMaxDeliver,
	}, func(ctx context.Context, msg ports.Message) error {
		deliverCount.Add(1)
		return msg.Ack(ctx)
	})
	if err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}
	env.trackSub(toNatsSub(sub))

	env.publishEvent(subject, sampleEvent(instanceID))

	// 等待首次投递。
	waitForCondition(t, "首次投递", integrationWaitTimeout, func() bool {
		return deliverCount.Load() >= 1
	})

	// 等待足够时间确认没有重投（超过 AckWait + 缓冲）。
	time.Sleep(integrationAckWait + 1500*time.Millisecond)
	if got := deliverCount.Load(); got != 1 {
		t.Errorf("Ack 后期望仅投递 1 次，实际 %d 次（说明 Ack 未生效或被重投）", got)
	}
}

// =============================================================================
// 场景 3：panic recover，handler panic → 消息被 Nak → 进程不崩溃
// =============================================================================

// TestIntegrationPanicRecover 覆盖 AC: handler panic → 消息被 Nak → 进程不崩溃。
// adapter 的 defer recover 兜底调 Nak，使消息进入重投队列。
func TestIntegrationPanicRecover(t *testing.T) {
	env := newIntegrationEnv(t)
	subject := integrationSubjectPrefix + ".panic"
	instanceID := "inst-panic-003"

	var (
		deliverCount atomic.Int64
		panicked     atomic.Bool
	)
	sub, err := env.bus.Subscribe(context.Background(), ports.SubscribeOptions{
		Subject:    subject,
		Consumer:   integrationConsumerPrefix + "-panic",
		MaxInflight: 16,
		AckWait:    integrationAckWait,
		MaxDeliver: integrationMaxDeliver,
	}, func(ctx context.Context, msg ports.Message) error {
		n := deliverCount.Add(1)
		if n == 1 {
			panicked.Store(true)
			panic("intentional panic in handler")
		}
		// 第二次投递正常 Ack，结束循环。
		return msg.Ack(ctx)
	})
	if err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}
	env.trackSub(toNatsSub(sub))

	// 外层 defer 确认进程不崩溃（测试继续运行即证明 recover 生效）。
	env.publishEvent(subject, sampleEvent(instanceID))

	waitForCondition(t, "panic 后重投", integrationWaitTimeout, func() bool {
		return deliverCount.Load() >= 2
	})

	if !panicked.Load() {
		t.Error("期望第一次投递触发 panic")
	}
	// 进程未崩溃 + 第二次收到即证明 panic recover + Nak 兜底生效。
	if got := deliverCount.Load(); got < 2 {
		t.Errorf("期望至少投递 2 次（panic 后重投），实际 %d 次", got)
	}
}

// =============================================================================
// 场景 4：Nak 延迟重投，handler 调 Nak → 延迟后重投 → 第二次 Ack
// =============================================================================

// TestIntegrationNakDelayedRedelivery 覆盖 AC: handler 调 Nak → 消息延迟重投 → 第二次 handler 调 Ack。
func TestIntegrationNakDelayedRedelivery(t *testing.T) {
	env := newIntegrationEnv(t)
	subject := integrationSubjectPrefix + ".nak"
	instanceID := "inst-nak-004"

	var deliverCount atomic.Int64
	sub, err := env.bus.Subscribe(context.Background(), ports.SubscribeOptions{
		Subject:    subject,
		Consumer:   integrationConsumerPrefix + "-nak",
		MaxInflight: 16,
		AckWait:    integrationAckWait,
		MaxDeliver: integrationMaxDeliver,
	}, func(ctx context.Context, msg ports.Message) error {
		n := deliverCount.Add(1)
		if n == 1 {
			// 第一次：Nak 触发重投。
			return msg.Nack(ctx)
		}
		// 第二次：Ack 结束。
		return msg.Ack(ctx)
	})
	if err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}
	env.trackSub(toNatsSub(sub))

	env.publishEvent(subject, sampleEvent(instanceID))

	waitForCondition(t, "Nak 后第二次投递", integrationWaitTimeout, func() bool {
		return deliverCount.Load() >= 2
	})

	if got := deliverCount.Load(); got < 2 {
		t.Errorf("期望 Nak 后至少投递 2 次，实际 %d 次", got)
	}
}

// =============================================================================
// 场景 5：MaxDeliver 满后停投
// =============================================================================

// TestIntegrationMaxDeliverStop 覆盖 AC: handler 持续 Nak → 到顶后 NATS 不再投递。
func TestIntegrationMaxDeliverStop(t *testing.T) {
	env := newIntegrationEnv(t)
	subject := integrationSubjectPrefix + ".maxdeliver"
	instanceID := "inst-maxdeliver-005"

	var deliverCount atomic.Int64
	sub, err := env.bus.Subscribe(context.Background(), ports.SubscribeOptions{
		Subject:    subject,
		Consumer:   integrationConsumerPrefix + "-maxdeliver",
		MaxInflight: 16,
		AckWait:    integrationAckWait,
		MaxDeliver: integrationMaxDeliver,
	}, func(ctx context.Context, msg ports.Message) error {
		deliverCount.Add(1)
		// 持续 Nak，直到 MaxDeliver 上限。
		return msg.Nack(ctx)
	})
	if err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}
	env.trackSub(toNatsSub(sub))

	env.publishEvent(subject, sampleEvent(instanceID))

	// 等待达到 MaxDeliver 上限。
	waitForCondition(t, "达到 MaxDeliver 上限", integrationWaitTimeout, func() bool {
		return deliverCount.Load() >= int64(integrationMaxDeliver)
	})

	// 继续等待足够时间，确认不再有额外投递。
	time.Sleep(integrationAckWait + 1500*time.Millisecond)
	if got := deliverCount.Load(); got != int64(integrationMaxDeliver) {
		t.Errorf("MaxDeliver=%d 期望最多投递 %d 次，实际 %d 次", integrationMaxDeliver, integrationMaxDeliver, got)
	}
}

// =============================================================================
// 场景 6：Interest fan-out，两个 durable consumer 各自收到同一事件
// =============================================================================

// TestIntegrationInterestFanout 覆盖 AC: 两个 durable consumer 各自收到同一事件，
// 验证 InterestPolicy 生效（非 WorkQueue 删除语义）。
func TestIntegrationInterestFanout(t *testing.T) {
	env := newIntegrationEnv(t)
	subject := integrationSubjectPrefix + ".fanout"
	instanceID := "inst-fanout-006"

	var (
		c1Count atomic.Int64
		c2Count atomic.Int64
	)

	sub1, err := env.bus.Subscribe(context.Background(), ports.SubscribeOptions{
		Subject:    subject,
		Consumer:   integrationConsumerPrefix + "-fanout-1",
		MaxInflight: 16,
		AckWait:    30 * time.Second,
		MaxDeliver: 10,
	}, func(ctx context.Context, msg ports.Message) error {
		c1Count.Add(1)
		return msg.Ack(ctx)
	})
	if err != nil {
		t.Fatalf("Subscribe consumer1 失败: %v", err)
	}
	env.trackSub(toNatsSub(sub1))

	sub2, err := env.bus.Subscribe(context.Background(), ports.SubscribeOptions{
		Subject:    subject,
		Consumer:   integrationConsumerPrefix + "-fanout-2",
		MaxInflight: 16,
		AckWait:    30 * time.Second,
		MaxDeliver: 10,
	}, func(ctx context.Context, msg ports.Message) error {
		c2Count.Add(1)
		return msg.Ack(ctx)
	})
	if err != nil {
		t.Fatalf("Subscribe consumer2 失败: %v", err)
	}
	env.trackSub(toNatsSub(sub2))

	env.publishEvent(subject, sampleEvent(instanceID))

	waitForCondition(t, "两个 consumer 都收到", integrationWaitTimeout, func() bool {
		return c1Count.Load() >= 1 && c2Count.Load() >= 1
	})

	if got := c1Count.Load(); got < 1 {
		t.Errorf("consumer1 期望至少收到 1 次，实际 %d 次", got)
	}
	if got := c2Count.Load(); got < 1 {
		t.Errorf("consumer2 期望至少收到 1 次，实际 %d 次", got)
	}
	// InterestPolicy 下两个 durable consumer 各自独立消费，验证 fan-out 生效。
	if c1Count.Load() >= 1 && c2Count.Load() >= 1 {
		t.Logf("InterestPolicy fan-out 验证通过：consumer1=%d, consumer2=%d", c1Count.Load(), c2Count.Load())
	}
}

// =============================================================================
// helpers
// =============================================================================

// toNatsSub 从 ports.Subscription 取出底层 *natsgo.Subscription（集成测试用）。
// 生产 adapter 的 subscription 实现是同包私有类型，集成测试在同包内可直接类型断言。
func toNatsSub(sub ports.Subscription) *natsgo.Subscription {
	if s, ok := sub.(subscription); ok {
		return s.sub
	}
	return nil
}

// assertHeader 校验 headers 中指定 key 的首个值与期望一致。
func assertHeader(t *testing.T, headers map[string][]string, key, expected string) {
	t.Helper()
	values, ok := headers[key]
	if !ok {
		t.Errorf("headers 缺少 key %q", key)
		return
	}
	if len(values) == 0 {
		t.Errorf("headers %q 为空", key)
		return
	}
	if values[0] != expected {
		t.Errorf("headers %q 期望 %q，实际 %q", key, expected, values[0])
	}
}
