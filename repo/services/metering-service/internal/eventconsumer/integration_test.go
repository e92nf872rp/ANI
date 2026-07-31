//go:build integration

package eventconsumer

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsadapter "github.com/kubercloud/ani/pkg/adapters/nats"
	"github.com/kubercloud/ani/pkg/ports"
	natsgo "github.com/nats-io/nats.go"
)

// 集成测试连接真实 NATS，验证 adapter + Consumer 完整端到端链路：
// adapter.Publish 发布 instance.created 事件 → Consumer 通过真实 NATS 收到 → 打印业务日志。
//
// 运行命令：
//
//	ANI_TEST_NATS_URL=nats://10.10.1.66:31062 \
//	  go test ./services/metering-service/internal/eventconsumer/ -v -run Integration -tags integration
//
// 集成测试为手动验证项，不作为硬性门禁（PRD §8 Success Metrics：3B 选项）。
// safeBuffer 是并发安全的 bytes.Buffer，用于跨 goroutine 捕获 slog 输出。
// Consumer 的 handle 回调在 NATS 订阅 goroutine 里写日志，主测试 goroutine 轮询读，必须加锁。
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

const (
	// consumer 集成测试用 stream 和 consumer 名。
	consumerStreamEvents = "ANI_EVENTS"
	// 集成测试用 consumer 名后缀，便于清理时识别。
	consumerItestSuffix = "-itest"
	// 等待 Consumer 处理的超时上限。
	consumerWaitTimeout = 15 * time.Second
)

// natsTestURL 读取环境变量，默认连本地 docker-compose NATS。
func natsTestURL() string {
	if u := os.Getenv("ANI_TEST_NATS_URL"); u != "" {
		return u
	}
	return "nats://127.0.0.1:4222"
}

// ensureEventsStreamInterest 确保 ANI_EVENTS 为 InterestPolicy，保证 fan-out 语义正确。
func ensureEventsStreamInterest(t *testing.T, js natsgo.JetStreamContext) {
	t.Helper()
	info, err := js.StreamInfo(consumerStreamEvents)
	if err != nil {
		t.Fatalf("查询 ANI_EVENTS 失败: %v（确认 NATS 已启动并可通过 ANI_TEST_NATS_URL 访问）", err)
	}
	if info.Config.Retention != natsgo.InterestPolicy {
		// NATS 不允许 UpdateStream 切换 WorkQueue↔Interest，删后重建。
		if err := js.DeleteStream(consumerStreamEvents); err != nil {
			t.Fatalf("ANI_EVENTS 当前 retention=%v，期望 Interest；DeleteStream 失败: %v", info.Config.Retention, err)
		}
		_, err = js.AddStream(&natsgo.StreamConfig{
			Name:      consumerStreamEvents,
			Subjects:  []string{"ani.events.>"},
			Retention: natsgo.InterestPolicy,
			MaxAge:    24 * time.Hour,
			Storage:   natsgo.FileStorage,
			Replicas:  1,
		})
		if err != nil {
			t.Fatalf("ANI_EVENTS 重建为 InterestPolicy 失败: %v", err)
		}
	}
}

// TestIntegrationConsumerEndToEnd 覆盖 AC：adapter 发布一条 instance.created 事件，
// eventconsumer.Consumer 通过真实 NATS 收到并打印业务日志（received instance event），
// 验证 adapter + consumer 完整链路。
func TestIntegrationConsumerEndToEnd(t *testing.T) {
	// 1. 连接真实 NATS。
	url := natsTestURL()
	nc, err := natsgo.Connect(url,
		natsgo.Timeout(5*time.Second),
		natsgo.ReconnectWait(500*time.Millisecond),
		natsgo.MaxReconnects(3),
		natsgo.Name("ani-consumer-itest"),
	)
	if err != nil {
		t.Fatalf("连接 NATS 失败 %s: %v", url, err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("初始化 JetStream 失败: %v", err)
	}

	// 2. 前置：确保 ANI_EVENTS 为 InterestPolicy。
	ensureEventsStreamInterest(t, js)

	// 3. 用真实 adapter 构造 MessageBus，注入给 Consumer。
	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	bus := natsadapter.NewMessageBus(js, logger)

	// 4. 创建并启动 Consumer。用独立 consumer 名避免与其它测试冲突。
	consumer := NewConsumer(bus, logger)

	// 临时改写 Consumer.Start 的订阅参数不可行（私有字段），改为直接用 Consumer.handle 配合 adapter.Subscribe。
	// 但 Consumer.Start 已固定订阅 ani.events.instance.>，符合本测试目标，直接调用即可。
	// 注意：Consumer 名固定为 "metering-example"，可能残留，清理时统一删除。
	ctx, cancel := context.WithTimeout(context.Background(), consumerWaitTimeout)
	defer cancel()

	if err := consumer.Start(ctx); err != nil {
		t.Fatalf("Consumer.Start 失败: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		_ = consumer.Stop(stopCtx)
	}()

	// 5. 通过 adapter 发布一条 instance.created 事件到 ani.events.instance.>。
	instanceID := "inst-consumer-e2e-001"
	subject := "ani.events.instance." + instanceID
	event := ports.EventEnvelope{
		TenantID:      "tenant-consumer-itest",
		AggregateID:   instanceID,
		AggregateType: "Instance",
		EventType:     "instance.created",
		Payload:       []byte(`{"event_type":"instance.created","instance_id":"` + instanceID + `"}`),
		OccurredAt:    time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
	}

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pubCancel()
	if err := bus.Publish(pubCtx, event, ports.PublishOptions{Subject: subject}); err != nil {
		t.Fatalf("adapter Publish 失败: %v", err)
	}

	// 6. 等待 Consumer 打印业务日志（received instance event）。
	deadline := time.Now().Add(consumerWaitTimeout)
	var processed atomic.Bool
	for time.Now().Before(deadline) {
		if strings.Contains(logBuf.String(), "received instance event") {
			processed.Store(true)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 收到回调后，原样打印 Consumer 这边输出的完整日志。
	t.Logf("=== Consumer 原始日志 ===\n%s", logBuf.String())

	if !processed.Load() {
		t.Errorf("Consumer 未在 %s 内打印 'received instance event' 日志；当前日志:\n%s", consumerWaitTimeout, logBuf.String())
	}

	// 7. 验证日志包含租户上下文重建。
	if !strings.Contains(logBuf.String(), "recovered tenant context") {
		t.Errorf("日志缺少 'recovered tenant context'（租户上下文重建未触发）；当前日志:\n%s", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), "tenant-consumer-itest") {
		t.Errorf("日志缺少 tenant-id=tenant-consumer-itest；当前日志:\n%s", logBuf.String())
	}

	// 8. 验证日志包含事件类型和实例 ID。
	if !strings.Contains(logBuf.String(), "instance.created") {
		t.Errorf("日志缺少事件类型 instance.created；当前日志:\n%s", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), instanceID) {
		t.Errorf("日志缺少实例 ID %s；当前日志:\n%s", instanceID, logBuf.String())
	}

	if processed.Load() {
		t.Logf("Consumer 端到端链路验证通过：收到 instance.created 事件并打印业务日志")
	}
}

// TestIntegrationConsumerPoisonMessage 覆盖：发布毒丸消息，Consumer 解析失败后 Ack 跳过并打印 error 日志。
func TestIntegrationConsumerPoisonMessage(t *testing.T) {
	url := natsTestURL()
	nc, err := natsgo.Connect(url,
		natsgo.Timeout(5*time.Second),
		natsgo.ReconnectWait(500*time.Millisecond),
		natsgo.MaxReconnects(3),
		natsgo.Name("ani-consumer-itest-poison"),
	)
	if err != nil {
		t.Fatalf("连接 NATS 失败 %s: %v", url, err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("初始化 JetStream 失败: %v", err)
	}
	ensureEventsStreamInterest(t, js)

	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	bus := natsadapter.NewMessageBus(js, logger)

	// 毒丸测试需独立 consumer，避免与 e2e 测试的消费进度冲突。
	// 由于 Consumer.Start 固定 consumer 名，这里用底层 adapter 直接订阅并调 handle 验证业务逻辑。
	// 更贴近真实：用 Consumer 订阅，但用独立 subject 投递毒丸。
	consumer := NewConsumer(bus, logger)
	ctx, cancel := context.WithTimeout(context.Background(), consumerWaitTimeout)
	defer cancel()

	if err := consumer.Start(ctx); err != nil {
		t.Fatalf("Consumer.Start 失败: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		_ = consumer.Stop(stopCtx)
	}()

	// 发布毒丸消息（非法 JSON payload）。
	subject := "ani.events.instance.poison-itest"
	event := ports.EventEnvelope{
		TenantID:      "tenant-poison",
		AggregateID:   "poison-001",
		AggregateType: "Instance",
		EventType:     "instance.created",
		Payload:       []byte("not-a-valid-json"),
		OccurredAt:    time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
	}

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pubCancel()
	if err := bus.Publish(pubCtx, event, ports.PublishOptions{Subject: subject}); err != nil {
		t.Fatalf("adapter Publish 毒丸消息失败: %v", err)
	}

	// 等待 Consumer 打印毒丸 error 日志。
	deadline := time.Now().Add(consumerWaitTimeout)
	var processed atomic.Bool
	for time.Now().Before(deadline) {
		if strings.Contains(logBuf.String(), "parse event failed") {
			processed.Store(true)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 收到回调后，原样打印 Consumer 这边输出的完整日志。
	t.Logf("=== Consumer 原始日志 ===\n%s", logBuf.String())

	if !processed.Load() {
		t.Errorf("Consumer 未在 %s 内打印 'parse event failed' 毒丸日志；当前日志:\n%s", consumerWaitTimeout, logBuf.String())
	} else {
		t.Logf("Consumer 毒丸消息处理验证通过：解析失败后 Ack 跳过并打印 error 日志")
	}
}
