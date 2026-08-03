//go:build integration

package taskconsumer

import (
	"bytes"
	"context"
	"fmt"
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

// 集成测试连接真实 NATS，验证 task 流端到端链路：
// adapter.Publish 发布 model.import task → taskconsumer.Consumer 通过真实 NATS 收到 → 打印业务日志。
//
// 运行命令：
//
//	ANI_TEST_NATS_URL=nats://10.10.1.66:31062 \
//	  go test ./services/task-service/internal/taskconsumer/ -v -run Integration -tags integration
//
// 集成测试为手动验证项，不作为硬性门禁（PRD §8 Success Metrics：3B 选项）。
const (
	// task 流集成测试用 stream 名。
	taskStreamName = "ANI_TASKS"
	// 等待 Consumer 处理的超时上限。
	taskWaitTimeout = 15 * time.Second
)

// natsTestURL 读取环境变量，默认连本地 docker-compose NATS。
func natsTestURL() string {
	if u := os.Getenv("ANI_TEST_NATS_URL"); u != "" {
		return u
	}
	return "nats://127.0.0.1:4222"
}

// safeBuffer 是并发安全的 bytes.Buffer，用于跨 goroutine 捕获 slog 输出。
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

// ensureTasksStreamWorkQueue 确保 ANI_TASKS 为 WorkQueuePolicy。
func ensureTasksStreamWorkQueue(t *testing.T, js natsgo.JetStreamContext) {
	t.Helper()
	info, err := js.StreamInfo(taskStreamName)
	if err != nil {
		t.Fatalf("查询 ANI_TASKS 失败: %v（确认 NATS 已启动并可通过 ANI_TEST_NATS_URL 访问）", err)
	}
	if info.Config.Retention != natsgo.WorkQueuePolicy {
		// NATS 不允许 UpdateStream 切换 WorkQueue↔Interest，删后重建。
		if err := js.DeleteStream(taskStreamName); err != nil {
			t.Fatalf("ANI_TASKS 当前 retention=%v，期望 WorkQueue；DeleteStream 失败: %v", info.Config.Retention, err)
		}
		_, err = js.AddStream(&natsgo.StreamConfig{
			Name:      taskStreamName,
			Subjects:  []string{"ani.tasks.>"},
			Retention: natsgo.WorkQueuePolicy,
			MaxAge:    24 * time.Hour,
			Storage:   natsgo.FileStorage,
			Replicas:  1,
		})
		if err != nil {
			t.Fatalf("ANI_TASKS 重建为 WorkQueuePolicy 失败: %v", err)
		}
	}
}

// TestIntegrationTaskConsumerEndToEnd 覆盖 AC：adapter 发布一条 model.import task，
// taskconsumer.Consumer 通过真实 NATS 收到并打印业务日志（received task），
// 验证 task 流 adapter + consumer 完整链路。
func TestIntegrationTaskConsumerEndToEnd(t *testing.T) {
	// 1. 连接真实 NATS。
	url := natsTestURL()
	nc, err := natsgo.Connect(url,
		natsgo.Timeout(5*time.Second),
		natsgo.ReconnectWait(500*time.Millisecond),
		natsgo.MaxReconnects(3),
		natsgo.Name("ani-task-itest"),
	)
	if err != nil {
		t.Fatalf("连接 NATS 失败 %s: %v", url, err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("初始化 JetStream 失败: %v", err)
	}

	// 2. 前置：确保 ANI_TASKS 为 WorkQueuePolicy。
	ensureTasksStreamWorkQueue(t, js)

	// 3. 用真实 adapter 构造 MessageBus，注入给 Consumer。
	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	bus := natsadapter.NewMessageBus(js, logger)

	// 4. 创建并启动 Consumer。
	consumer := NewConsumer(bus, logger)

	ctx, cancel := context.WithTimeout(context.Background(), taskWaitTimeout)
	defer cancel()
	_ = ctx

	if err := consumer.Start(); err != nil {
		t.Fatalf("Consumer.Start 失败: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		_ = consumer.Stop(stopCtx)
	}()

	// 5. 通过 adapter 发布一条 model.import task。
	taskID := "task-e2e-001"
	repoID := "Qwen/Qwen2.5-72B-Instruct"
	subject := "ani.tasks.model.import"
	event := ports.EventEnvelope{
		TenantID:      "tenant-task-itest",
		AggregateID:   taskID,
		AggregateType: "ModelImport",
		EventType:     "model.import",
		Payload:       []byte(fmt.Sprintf(`{"task_id":"%s","repo_id":"%s"}`, taskID, repoID)),
		OccurredAt:    time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
	}

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pubCancel()
	if err := bus.Publish(pubCtx, event, ports.PublishOptions{Subject: subject}); err != nil {
		t.Fatalf("adapter Publish 失败: %v", err)
	}

	// 6. 等待 Consumer 打印业务日志（received task）。
	deadline := time.Now().Add(taskWaitTimeout)
	var processed atomic.Bool
	for time.Now().Before(deadline) {
		if strings.Contains(logBuf.String(), "received task") {
			processed.Store(true)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !processed.Load() {
		t.Errorf("Consumer 未在 %s 内打印 'received task' 日志；当前日志:\n%s", taskWaitTimeout, logBuf.String())
	}

	// 7. 验证日志包含租户上下文重建。
	if !strings.Contains(logBuf.String(), "recovered tenant context") {
		t.Errorf("日志缺少 'recovered tenant context'（租户上下文重建未触发）；当前日志:\n%s", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), "tenant-task-itest") {
		t.Errorf("日志缺少 tenant-id=tenant-task-itest；当前日志:\n%s", logBuf.String())
	}

	// 8. 验证日志包含 task_id 和 repo_id。
	if !strings.Contains(logBuf.String(), taskID) {
		t.Errorf("日志缺少 task_id %s；当前日志:\n%s", taskID, logBuf.String())
	}
	if !strings.Contains(logBuf.String(), repoID) {
		t.Errorf("日志缺少 repo_id %s；当前日志:\n%s", repoID, logBuf.String())
	}

	// 打印 Consumer 原始日志，便于直观验证端到端链路。
	t.Logf("=== Consumer 日志 ===\n%s", logBuf.String())

	if processed.Load() {
		t.Logf("Task 端到端链路验证通过：收到 model.import task 并打印业务日志")
	}

	// 9. 验证 WorkQueuePolicy 语义：消息被消费后从 queue 移除。
	// Consumer 名 task-example 已消费并 Ack 该消息，stream 的消息数应回到 0（或不含本条）。
	// 注意：WorkQueuePolicy 下 Ack 后消息立即从 stream 删除。
	_ = consumer.Stop(ctx)
	streamInfo, err := js.StreamInfo(taskStreamName)
	if err != nil {
		t.Logf("查询 stream info 失败（忽略 WorkQueue 清理验证）: %v", err)
	} else if streamInfo.State.Msgs > 0 {
		t.Logf("WorkQueuePolicy 清理验证：stream 剩余 %d 条消息（可能含其它测试残留，非硬性断言）", streamInfo.State.Msgs)
	} else {
		t.Logf("WorkQueuePolicy 清理验证通过：消息被 Ack 后已从 stream 移除")
	}

	// 清理：purge 本测试 subject 的残留消息。
	_ = js.PurgeStream(taskStreamName, &natsgo.StreamPurgeRequest{Subject: subject})
}

// TestIntegrationTaskConsumerPoisonMessage 覆盖：发布毒丸消息（非法 JSON），Consumer 解析失败后 Ack 跳过并打印 error 日志。
func TestIntegrationTaskConsumerPoisonMessage(t *testing.T) {
	url := natsTestURL()
	nc, err := natsgo.Connect(url,
		natsgo.Timeout(5*time.Second),
		natsgo.ReconnectWait(500*time.Millisecond),
		natsgo.MaxReconnects(3),
		natsgo.Name("ani-task-itest-poison"),
	)
	if err != nil {
		t.Fatalf("连接 NATS 失败 %s: %v", url, err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("初始化 JetStream 失败: %v", err)
	}
	ensureTasksStreamWorkQueue(t, js)

	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	bus := natsadapter.NewMessageBus(js, logger)

	consumer := NewConsumer(bus, logger)
	ctx, cancel := context.WithTimeout(context.Background(), taskWaitTimeout)
	defer cancel()
	_ = ctx

	if err := consumer.Start(); err != nil {
		t.Fatalf("Consumer.Start 失败: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		_ = consumer.Stop(stopCtx)
	}()

	// 发布毒丸消息（非法 JSON payload）。用独立 subject 避免与 e2e 测试冲突。
	// 注意：ANI_TASKS stream 的 subject 是 ani.tasks.>，毒丸也必须匹配此前缀。
	subject := "ani.tasks.model.import"
	event := ports.EventEnvelope{
		TenantID:      "tenant-poison-task",
		AggregateID:   "poison-task-001",
		AggregateType: "ModelImport",
		EventType:     "model.import",
		Payload:       []byte("not-a-valid-json"),
		OccurredAt:    time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
	}

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pubCancel()
	if err := bus.Publish(pubCtx, event, ports.PublishOptions{Subject: subject}); err != nil {
		t.Fatalf("adapter Publish 毒丸消息失败: %v", err)
	}

	// 等待 Consumer 打印毒丸 error 日志。
	deadline := time.Now().Add(taskWaitTimeout)
	var processed atomic.Bool
	for time.Now().Before(deadline) {
		if strings.Contains(logBuf.String(), "parse task failed") {
			processed.Store(true)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !processed.Load() {
		t.Errorf("Consumer 未在 %s 内打印 'parse task failed' 毒丸日志；当前日志:\n%s", taskWaitTimeout, logBuf.String())
	} else {
		t.Logf("Task 毒丸消息处理验证通过：解析失败后 Ack 跳过并打印 error 日志")
	}

	// 打印 Consumer 原始日志。
	t.Logf("=== Consumer 日志 ===\n%s", logBuf.String())

	// 清理。
	_ = js.PurgeStream(taskStreamName, &natsgo.StreamPurgeRequest{Subject: subject})
}
