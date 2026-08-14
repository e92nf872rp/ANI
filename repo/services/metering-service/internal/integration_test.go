//go:build integration

// Package internal 提供集成测试，覆盖端到端链路。
// 集成测试使用真实 NATS JetStream + 真实 PG（含 migration）+ 真实 Prometheus。
//
// 运行命令（需先确保 NATS、PG 和 Prometheus 已启动）：
//
//	ANI_TEST_NATS_URL=nats://10.10.1.66:31062 \
//	ANI_TEST_ADMIN_DSN=postgres://ani:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable \
//	ANI_TEST_PROMETHEUS_URL=http://10.10.1.66:31990/ \
//	go test ./services/metering-service/internal/ -v -run TestIntegration -tags integration -timeout 300s
//
// 集成测试为手动验证项，不作为硬性门禁。
package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/pkg/adapters/metering"
	natsadapter "github.com/kubercloud/ani/pkg/adapters/nats"
	pgmetadata "github.com/kubercloud/ani/pkg/adapters/postgres"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/metering-service/internal/service"
	natsgo "github.com/nats-io/nats.go"
)

// 集成测试常量
const (
	itestStreamEvents   = "ANI_EVENTS"
	itestConsumerPrefix = "metering-itest"
	itestSubjectPrefix  = "ani.events.instance"
	itestWaitTimeout    = 20 * time.Second
	itestPollInterval   = 50 * time.Millisecond
	itestShortInterval  = 2 // 测试用短采集周期（秒），避免等待默认 60s
	itestAckWait        = 2 * time.Second
	itestMaxDeliver     = 5
)

// itestRunID 是每次测试运行的唯一 ID，用于 consumer 名称隔离。
// 避免跨测试运行时 durable consumer subject 绑定冲突。
var itestRunID = fmt.Sprintf("%d", time.Now().UnixNano()%100000)

// itestEnv 封装集成测试环境，统一管理 NATS + PG + Prometheus 生命周期。
type itestEnv struct {
	t         *testing.T
	nc        *natsgo.Conn
	js        natsgo.JetStreamContext
	bus       *natsadapter.MessageBus
	adminDB   *pgxpool.Pool // 管理员连接（建表/建租户/清理/写入 metering_usage_records）
	metaStore ports.MetadataStore
	mockPromo *httptest.Server // mock Prometheus fallback server，cleanup 时关闭
	logger    *slog.Logger

	mu           sync.Mutex
	subs         []ports.Subscription
	consumers    []string                          // consumer 名称列表，用于 cleanup 时删除
	meteringSvcs []ports.MeteringCollectionService // 持有所有创建的 service，cleanup 时停止 ticker
	resources    []string                          // 持有所有 StartCollection 的 resourceRef，cleanup 时 StopCollection
}

// itestAdminDSN 返回管理员 DSN，用于建表/建租户/清理和写入 metering_usage_records。
// 默认连 10.10.1.66 测试环境，ani 超级用户绕过 RLS。
func itestAdminDSN() string {
	if dsn := os.Getenv("ANI_TEST_ADMIN_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://ani:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable"
}

// itestNATSURL 返回 NATS 连接 URL。
func itestNATSURL() string {
	if u := os.Getenv("ANI_TEST_NATS_URL"); u != "" {
		return u
	}
	return "nats://127.0.0.1:4222"
}

// itestPrometheusURL 返回真实 Prometheus HTTP API 地址。
// 默认连 10.10.1.66 测试环境，可通过 ANI_TEST_PROMETHEUS_URL 覆盖。
func itestPrometheusURL() string {
	if u := os.Getenv("ANI_TEST_PROMETHEUS_URL"); u != "" {
		return u
	}
	return "http://10.10.1.66:31990/"
}

// newItestEnv 创建集成测试环境：连接 NATS + PG，注册 collector，确保 stream。
// 创建时立即清理 DB 中的残留测试数据，避免跨测试污染。
func newItestEnv(t *testing.T) *itestEnv {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// 1. 连接 NATS
	url := itestNATSURL()
	nc, err := natsgo.Connect(url,
		natsgo.Timeout(5*time.Second),
		natsgo.ReconnectWait(500*time.Millisecond),
		natsgo.MaxReconnects(3),
		natsgo.Name("ani-metering-itest"),
	)
	if err != nil {
		t.Fatalf("连接 NATS 失败 %s: %v（确认 NATS 已启动并可通过 ANI_TEST_NATS_URL 访问）", url, err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		t.Fatalf("初始化 JetStream 失败: %v", err)
	}

	// 2. 连接 PG（管理员，用于建表/建租户/清理/写入）
	adminDSN := itestAdminDSN()
	adminCfg, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		nc.Close()
		t.Fatalf("解析 admin DSN 失败: %v", err)
	}
	adminDB, err := pgxpool.NewWithConfig(context.Background(), adminCfg)
	if err != nil {
		nc.Close()
		t.Fatalf("连接 admin DB 失败: %v", err)
	}

	// 3. 注册 collector：GPU（无外部依赖）+ CPU/Mem（真实 Prometheus + mock fallback）。
	// GPU collector 不查 Prometheus（纯持有时长计算）。
	metering.RegisterCollector("dcgm_gpu", metering.DCGMGPUCollector{})
	promoURL := itestPrometheusURL()
	promoClient := &http.Client{Timeout: 2 * time.Second}
	mockServer := newMockPrometheusServer()
	realCPUColl := metering.NewKubeletCPUCollector(promoURL, promoClient)
	mockCPUColl := metering.NewKubeletCPUCollector(mockServer.URL, nil)
	realMemColl := metering.NewKubeletMemCollector(promoURL, promoClient)
	mockMemColl := metering.NewKubeletMemCollector(mockServer.URL, nil)
	metering.RegisterCollector("kubelet_cpu", &fallbackCollector{primary: realCPUColl, fallback: mockCPUColl})
	metering.RegisterCollector("kubelet_mem", &fallbackCollector{primary: realMemColl, fallback: mockMemColl})

	// 4. 确保 ANI_EVENTS stream 为 InterestPolicy
	ensureItestStream(t, js)

	// 4a. Purge stream 中所有旧消息，消除跨测试运行的残留消息干扰
	// InterestPolicy stream 在没有 consumer 时消息会保留，需主动 purge
	if err := js.PurgeStream(itestStreamEvents); err != nil {
		t.Logf("PurgeStream %s 失败（可忽略，可能 stream 无消息）: %v", itestStreamEvents, err)
	}

	// 4b. 删除所有旧 itest durable consumer，避免 subject 绑定冲突
	deleteOldItestConsumers(t, js)

	env := &itestEnv{
		t:         t,
		nc:        nc,
		js:        js,
		bus:       natsadapter.NewMessageBus(js, logger),
		adminDB:   adminDB,
		metaStore: pgmetadata.NewMetadataStore(adminDB),
		mockPromo: mockServer,
		logger:    logger,
	}

	// 5. 立即清理残留测试数据，避免前一个测试的 running 实例干扰 rebuilder
	env.cleanTestData()

	t.Cleanup(env.cleanup)
	return env
}

// newMockPrometheusServer 创建一个返回固定 PromQL 响应的 HTTP mock server。
// 当真实 Prometheus 无测试实例指标数据时，作为 fallback 提供可预测的 CPU/Mem 值。
func newMockPrometheusServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Prometheus instant query 响应格式：返回固定值 0.5
		fmt.Fprintf(w, `{
			"status": "success",
			"data": {
				"resultType": "vector",
				"result": [{
					"metric": {},
					"value": ["%d", "0.5"]
				}]
			}
		}`, time.Now().Unix())
	}))
}

// ensureItestStream 确保 ANI_EVENTS stream 为 InterestPolicy。
func ensureItestStream(t *testing.T, js natsgo.JetStreamContext) {
	t.Helper()
	info, err := js.StreamInfo(itestStreamEvents)
	if err != nil {
		_, err = js.AddStream(&natsgo.StreamConfig{
			Name:      itestStreamEvents,
			Subjects:  []string{"ani.events.>"},
			Retention: natsgo.InterestPolicy,
			MaxAge:    24 * time.Hour,
			Storage:   natsgo.FileStorage,
			Replicas:  1,
		})
		if err != nil {
			t.Fatalf("创建 ANI_EVENTS stream 失败: %v", err)
		}
		return
	}
	if info.Config.Retention != natsgo.InterestPolicy {
		if err := js.DeleteStream(itestStreamEvents); err != nil {
			t.Fatalf("DeleteStream ANI_EVENTS 失败: %v", err)
		}
		_, err = js.AddStream(&natsgo.StreamConfig{
			Name:      itestStreamEvents,
			Subjects:  []string{"ani.events.>"},
			Retention: natsgo.InterestPolicy,
			MaxAge:    24 * time.Hour,
			Storage:   natsgo.FileStorage,
			Replicas:  1,
		})
		if err != nil {
			t.Fatalf("重建 ANI_EVENTS 为 InterestPolicy 失败: %v", err)
		}
	}
}

// deleteOldItestConsumers 删除所有旧 itest durable consumer，避免 subject 绑定冲突。
// 旧测试运行可能用通配符或不同 subject 创建了 durable consumer，新测试用特定 subject 订阅时会冲突。
func deleteOldItestConsumers(t *testing.T, js natsgo.JetStreamContext) {
	t.Helper()
	// 枚举 stream 中所有 consumer，删除以 itestConsumerPrefix 开头的
	for name := range js.ConsumerNames(itestStreamEvents) {
		if len(name) >= len(itestConsumerPrefix) && name[:len(itestConsumerPrefix)] == itestConsumerPrefix {
			_ = js.DeleteConsumer(itestStreamEvents, name)
		}
	}
}

// cleanup 清理测试环境：停止所有 ticker → 关闭订阅 → 删除 consumer → 清理 DB 数据 → 关闭连接。
func (e *itestEnv) cleanup() {
	// 1. 先停止所有 ticker（StopCollection），防止后台 goroutine 写已关闭的 pool
	e.mu.Lock()
	svcs := append([]ports.MeteringCollectionService(nil), e.meteringSvcs...)
	resources := append([]string(nil), e.resources...)
	e.mu.Unlock()

	ctx := context.Background()
	for _, ref := range resources {
		for _, svc := range svcs {
			_ = svc.StopCollection(ctx, ref)
		}
	}
	// 等待 ticker goroutine 退出
	time.Sleep(200 * time.Millisecond)

	// 2. 关闭订阅
	e.mu.Lock()
	subs := append([]ports.Subscription(nil), e.subs...)
	consumerNames := append([]string(nil), e.consumers...)
	e.mu.Unlock()

	for _, sub := range subs {
		if sub != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = sub.Drain(ctx)
			cancel()
		}
	}
	time.Sleep(300 * time.Millisecond)

	// 3. 删除 consumer
	for _, name := range consumerNames {
		_ = e.js.DeleteConsumer(itestStreamEvents, name)
	}

	// 4. 清理测试数据
	e.cleanTestData()

	// 5. 关闭连接
	if e.mockPromo != nil {
		e.mockPromo.Close()
	}
	if e.adminDB != nil {
		e.adminDB.Close()
	}
	if e.nc != nil {
		e.nc.Close()
	}
}

// cleanTestData 清理所有测试产生的 DB 数据。
func (e *itestEnv) cleanTestData() {
	ctx := context.Background()
	_, _ = e.adminDB.Exec(ctx, `DELETE FROM metering_usage_records WHERE resource_ref LIKE 'inst-itest-%'`)
	_, _ = e.adminDB.Exec(ctx, `DELETE FROM workload_instances WHERE instance_id LIKE 'inst-itest-%'`)
	_, _ = e.adminDB.Exec(ctx, `DELETE FROM tenants WHERE name LIKE 'tenant-itest-%'`)
}

// trackSub 记录订阅和 consumer 名称以便统一清理。
func (e *itestEnv) trackSub(sub ports.Subscription, consumerName string) {
	e.mu.Lock()
	e.subs = append(e.subs, sub)
	e.consumers = append(e.consumers, consumerName)
	e.mu.Unlock()
}

// trackMeteringSvc 记录 MeteringCollectionService 和 resourceRef，用于 cleanup 时停止 ticker。
func (e *itestEnv) trackMeteringSvc(svc ports.MeteringCollectionService, resourceRef string) {
	e.mu.Lock()
	e.meteringSvcs = append(e.meteringSvcs, svc)
	e.resources = append(e.resources, resourceRef)
	e.mu.Unlock()
}

// ensureTestTenant 创建测试租户并返回 tenant_id（UUID 字符串）。
// 注意：tenants 表有 plan_id NOT NULL 外键引用 tenant_plans，需先获取一个可用 plan_id。
func (e *itestEnv) ensureTestTenant(name string) string {
	e.t.Helper()
	ctx := context.Background()

	// 查询一个可用的 plan_id（status != 'deleted' 且 is_deleted = false）
	var planID string
	err := e.adminDB.QueryRow(ctx,
		`SELECT id::text FROM tenant_plans WHERE is_deleted = false LIMIT 1`).Scan(&planID)
	if err != nil {
		e.t.Fatalf("查询可用 plan_id 失败: %v（确认 tenant_plans 表有数据）", err)
	}

	var tenantID string
	err = e.adminDB.QueryRow(ctx,
		`INSERT INTO tenants (name, display_name, plan_id) VALUES ($1, $1, $2)
		 ON CONFLICT (name) DO UPDATE SET display_name = EXCLUDED.display_name, plan_id = EXCLUDED.plan_id
		 RETURNING id`, name, planID).Scan(&tenantID)
	if err != nil {
		e.t.Fatalf("创建测试租户失败: %v", err)
	}
	return tenantID
}

// insertRunningInstance 在 workload_instances 表中插入一条 running 状态的实例。
func (e *itestEnv) insertRunningInstance(tenantID, instanceID, kind string, gpuCount int) {
	e.t.Helper()
	ctx := context.Background()
	// gpu_status 列为 NOT NULL DEFAULT '{}'，始终传入有效 JSON 对象
	gpuStatus := map[string]int{"count": gpuCount}
	gpuJSON, _ := json.Marshal(gpuStatus)
	_, err := e.adminDB.Exec(ctx,
		`INSERT INTO workload_instances (tenant_id, instance_id, name, workload_kind, state, gpu_status, created_at, updated_at)
		 VALUES ($1, $2, $2, $3, 'running', $4, NOW(), NOW())
		 ON CONFLICT (tenant_id, instance_id) DO UPDATE SET state='running', gpu_status=$4, updated_at=NOW()`,
		tenantID, instanceID, kind, gpuJSON)
	if err != nil {
		e.t.Fatalf("插入 running 实例失败: %v", err)
	}
}

// publishInstanceEvent 发布一条 InstanceLifecycleEvent 到 NATS。
func (e *itestEnv) publishInstanceEvent(subject, tenantID, instanceID, kind, status string, seq uint64, gpuCount int) {
	e.t.Helper()
	event := ports.InstanceLifecycleEvent{
		InstanceID:   instanceID,
		TenantID:     tenantID,
		WorkloadKind: kind,
		NewStatus:    status,
		EventSeq:     seq,
	}
	if gpuCount > 0 {
		event.GPUSpec = &ports.GPUEventSpec{Count: gpuCount}
	}
	payload, err := json.Marshal(event)
	if err != nil {
		e.t.Fatalf("序列化事件失败: %v", err)
	}
	pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.bus.Publish(pubCtx, ports.EventEnvelope{
		TenantID:      tenantID,
		AggregateID:   instanceID,
		AggregateType: "Instance",
		EventType:     fmt.Sprintf("instance.%s", status),
		Payload:       payload,
		OccurredAt:    time.Now(),
	}, ports.PublishOptions{Subject: subject}); err != nil {
		e.t.Fatalf("Publish 事件失败: %v", err)
	}
}

// waitForCondition 轮询条件直到超时。
func waitForCondition(t *testing.T, name string, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(itestPollInterval)
	}
	t.Fatalf("等待条件超时: %s (timeout=%s)", name, timeout)
}

// countMeteringRecords 查询指定实例的计量记录数。
func (e *itestEnv) countMeteringRecords(instanceID string) int {
	ctx := context.Background()
	var count int
	err := e.adminDB.QueryRow(ctx,
		`SELECT COUNT(*) FROM metering_usage_records WHERE resource_ref = $1`, instanceID).Scan(&count)
	if err != nil {
		e.t.Fatalf("查询计量记录数失败: %v", err)
	}
	return count
}

// getMeteringRecords 查询指定实例的计量记录列表。
func (e *itestEnv) getMeteringRecords(instanceID string) []struct {
	ResourceType string
	Unit         string
	Period       string
	Quantity     float64
} {
	ctx := context.Background()
	rows, err := e.adminDB.Query(ctx,
		`SELECT resource_type, unit, period, quantity FROM metering_usage_records WHERE resource_ref = $1 ORDER BY period, resource_type`,
		instanceID)
	if err != nil {
		e.t.Fatalf("查询计量记录失败: %v", err)
	}
	defer rows.Close()
	var out []struct {
		ResourceType string
		Unit         string
		Period       string
		Quantity     float64
	}
	for rows.Next() {
		var r struct {
			ResourceType string
			Unit         string
			Period       string
			Quantity     float64
		}
		if err := rows.Scan(&r.ResourceType, &r.Unit, &r.Period, &r.Quantity); err != nil {
			e.t.Fatalf("扫描计量记录失败: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// =============================================================================
// 场景 1：事件驱动采集——发布 instance.created(running) → StartCollection → ticker 产出记录写入 DB
// =============================================================================

// TestIntegrationEventDrivenCollection 覆盖 AC：
// 发布 instance.created(running) 事件 → consumer 调 StartCollection → ticker 产出记录写入 metering_usage_records。
func TestIntegrationEventDrivenCollection(t *testing.T) {
	env := newItestEnv(t)
	tenantID := env.ensureTestTenant("tenant-itest-sc1")
	instanceID := "inst-itest-sc1-001"
	subject := fmt.Sprintf("%s.%s", itestSubjectPrefix, instanceID)

	// 构造真实的 meteringCollectionService，使用短采集周期加速测试
	meteringSvc := newShortIntervalSvc(env.adminDB, env.logger)
	consumer := NewConsumer(meteringSvc, env.logger)

	consumerName := itestConsumerPrefix + "-sc1-" + itestRunID
	sub, err := env.bus.Subscribe(ports.SubscribeOptions{
		Subject:     subject,
		Consumer:    consumerName,
		MaxInflight: 1,
		AckWait:     itestAckWait,
		MaxDeliver:  itestMaxDeliver,
	}, consumer.HandleEvent())
	if err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}
	env.trackSub(sub, consumerName)

	// 发布 instance.created(running) 事件（gpu_container，2 GPU）
	env.publishInstanceEvent(subject, tenantID, instanceID, "gpu_container", "running", 1, 2)
	env.trackMeteringSvc(meteringSvc, instanceID)

	// 等待 ticker 产出记录（短采集周期为 2 秒）
	waitForCondition(t, "ticker 产出计量记录", itestWaitTimeout, func() bool {
		return env.countMeteringRecords(instanceID) > 0
	})

	records := env.getMeteringRecords(instanceID)
	t.Logf("场景1: 产出 %d 条记录", len(records))
	for _, r := range records {
		t.Logf("  resource_type=%s unit=%s period=%s quantity=%f", r.ResourceType, r.Unit, r.Period, r.Quantity)
	}

	if len(records) == 0 {
		t.Errorf("期望至少 1 条计量记录，实际 0 条")
	}

	// 验证 GPU 维度记录存在（gpu_container 有 GPU 维度）
	hasGPU := false
	for _, r := range records {
		if r.ResourceType == string(ports.MeteringResourceInstanceGPUSeconds) {
			hasGPU = true
			if r.Unit != "gpu_second" {
				t.Errorf("GPU 记录 unit 期望 gpu_second，实际 %s", r.Unit)
			}
		}
	}
	if !hasGPU {
		t.Errorf("缺少 GPU 维度记录（gpu_container 应有 GPU 维度）")
	}
}

// =============================================================================
// 场景 2：停止采集 + 保底——发布 instance.stopped → StopCollection → ticker 停止 → 保底采集触发
// =============================================================================

// TestIntegrationStopAndCollectFullLifetime 覆盖 AC：
// 发布 instance.stopped 事件 → consumer 调 StopCollection → ticker 停止 → 短生命周期保底采集触发。
func TestIntegrationStopAndCollectFullLifetime(t *testing.T) {
	env := newItestEnv(t)
	tenantID := env.ensureTestTenant("tenant-itest-sc2")
	instanceID := "inst-itest-sc2-001"
	subject := fmt.Sprintf("%s.%s", itestSubjectPrefix, instanceID)

	meteringSvc := newShortIntervalSvc(env.adminDB, env.logger)
	consumer := NewConsumer(meteringSvc, env.logger)

	consumerName := itestConsumerPrefix + "-sc2-" + itestRunID
	sub, err := env.bus.Subscribe(ports.SubscribeOptions{
		Subject:     subject,
		Consumer:    consumerName,
		MaxInflight: 1,
		AckWait:     itestAckWait,
		MaxDeliver:  itestMaxDeliver,
	}, consumer.HandleEvent())
	if err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}
	env.trackSub(sub, consumerName)

	// 发布 running 事件（gpu_container，1 GPU）
	env.publishInstanceEvent(subject, tenantID, instanceID, "gpu_container", "running", 1, 1)
	env.trackMeteringSvc(meteringSvc, instanceID)

	// 等待 StartCollection 生效（短暂等待，不让 ticker 第一次产出周期记录，以验证保底采集）
	time.Sleep(500 * time.Millisecond)

	// 在 ticker 第一次产出之前就发布 stopped 事件，确保 everCollected=false
	env.publishInstanceEvent(subject, tenantID, instanceID, "gpu_container", "stopped", 2, 1)

	// 等待保底采集记录写入
	waitForCondition(t, "保底采集产出计量记录", itestWaitTimeout, func() bool {
		return env.countMeteringRecords(instanceID) > 0
	})

	records := env.getMeteringRecords(instanceID)
	t.Logf("场景2: 保底采集产出 %d 条记录", len(records))

	if len(records) == 0 {
		t.Errorf("期望保底采集产出至少 1 条记录，实际 0 条")
	}

	// 保底采集应产出 GPU 维度记录（gpu_second），quantity > 0
	hasGPU := false
	for _, r := range records {
		if r.ResourceType == string(ports.MeteringResourceInstanceGPUSeconds) {
			hasGPU = true
			if r.Quantity <= 0 {
				t.Errorf("GPU 保底采集 quantity 期望 > 0，实际 %f", r.Quantity)
			}
		}
	}
	if !hasGPU {
		t.Errorf("缺少 GPU 维度保底采集记录")
	}

	// 验证 ticker 已停止：等待额外时间后不应有新的周期记录
	countAfter := env.countMeteringRecords(instanceID)
	time.Sleep(time.Duration(itestShortInterval+2) * time.Second)
	countLater := env.countMeteringRecords(instanceID)
	if countLater > countAfter {
		t.Errorf("ticker 未停止：停采后记录数从 %d 增长到 %d", countAfter, countLater)
	}
}

// =============================================================================
// 场景 3：幂等 no-op——重复发布 instance.created 同一实例 → 进程内 map 幂等，DB 无重复行
// =============================================================================

// TestIntegrationIdempotentStartCollection 覆盖 AC：
// 重复发布 instance.created 同一 instance → 进程内 map 幂等 no-op，DB 无重复行。
func TestIntegrationIdempotentStartCollection(t *testing.T) {
	env := newItestEnv(t)
	tenantID := env.ensureTestTenant("tenant-itest-sc3")
	instanceID := "inst-itest-sc3-001"
	subject := fmt.Sprintf("%s.%s", itestSubjectPrefix, instanceID)

	meteringSvc := newShortIntervalSvc(env.adminDB, env.logger)
	consumer := NewConsumer(meteringSvc, env.logger)

	consumerName := itestConsumerPrefix + "-sc3-" + itestRunID
	sub, err := env.bus.Subscribe(ports.SubscribeOptions{
		Subject:     subject,
		Consumer:    consumerName,
		MaxInflight: 1,
		AckWait:     itestAckWait,
		MaxDeliver:  itestMaxDeliver,
	}, consumer.HandleEvent())
	if err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}
	env.trackSub(sub, consumerName)

	// 发布两次相同的 running 事件（不同 seq），使用 gpu_container 确保 GPU 维度产出记录
	env.publishInstanceEvent(subject, tenantID, instanceID, "gpu_container", "running", 1, 1)
	time.Sleep(200 * time.Millisecond)
	env.publishInstanceEvent(subject, tenantID, instanceID, "gpu_container", "running", 2, 1)
	env.trackMeteringSvc(meteringSvc, instanceID)

	// 等待 ticker 产出记录
	waitForCondition(t, "ticker 产出计量记录", itestWaitTimeout, func() bool {
		return env.countMeteringRecords(instanceID) > 0
	})

	// 等待两个采集周期，确认无重复行（同一 period 同维度只有一行）
	time.Sleep(time.Duration(itestShortInterval*3) * time.Second)

	records := env.getMeteringRecords(instanceID)
	t.Logf("场景3: 产出 %d 条记录", len(records))

	// 检查是否有重复的 (period, resource_type) 组合
	seen := make(map[string]bool)
	for _, r := range records {
		key := r.Period + "|" + r.ResourceType
		if seen[key] {
			t.Errorf("发现重复记录：period=%s resource_type=%s", r.Period, r.ResourceType)
		}
		seen[key] = true
	}
}

// =============================================================================
// 场景 4：重建 + DeliverAll 回放——consumer 重启 → rebuilder 重建 ticker → DeliverAll 回放补齐
// =============================================================================

// TestIntegrationRebuildAndDeliverAll 覆盖 AC：
// consumer 进程重启 → rebuilder 查 running 实例重建 ticker → DeliverAll 回放补齐崩溃窗口消息。
func TestIntegrationRebuildAndDeliverAll(t *testing.T) {
	env := newItestEnv(t)
	tenantID := env.ensureTestTenant("tenant-itest-sc4")
	instanceID := "inst-itest-sc4-001"
	subject := fmt.Sprintf("%s.%s", itestSubjectPrefix, instanceID)

	// 1. 在 DB 中预先插入一条 running 实例（模拟崩溃前已有实例在运行）
	env.insertRunningInstance(tenantID, instanceID, "gpu_container", 1)

	// 2. 第一阶段：启动 consumer₁，订阅（DeliverAll，consumer 名固定）
	meteringSvc1 := newShortIntervalSvc(env.adminDB, env.logger)
	consumer1 := NewConsumer(meteringSvc1, env.logger)
	consumerName := itestConsumerPrefix + "-sc4-" + itestRunID

	sub1, err := env.bus.Subscribe(ports.SubscribeOptions{
		Subject:     subject,
		Consumer:    consumerName,
		MaxInflight: 1,
		AckWait:     itestAckWait,
		MaxDeliver:  itestMaxDeliver,
	}, consumer1.HandleEvent())
	if err != nil {
		t.Fatalf("Subscribe consumer1 失败: %v", err)
	}
	env.trackSub(sub1, consumerName)

	// 3. 发布 running 事件，让 consumer1 启动采集
	env.publishInstanceEvent(subject, tenantID, instanceID, "gpu_container", "running", 1, 1)
	env.trackMeteringSvc(meteringSvc1, instanceID)
	time.Sleep(500 * time.Millisecond)

	// 4. 模拟崩溃：Drain 订阅（让消息留在 stream 中，consumer 关闭）
	env.mu.Lock()
	for _, s := range env.subs {
		if s != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = s.Drain(ctx)
			cancel()
		}
	}
	time.Sleep(300 * time.Millisecond)
	env.subs = nil
	env.mu.Unlock()

	// 停止 consumer1 的 ticker（模拟进程退出）
	_ = meteringSvc1.StopCollection(context.Background(), instanceID)

	// 5. 第二阶段：启动 consumer₂（同名 durable），先运行 rebuilder
	meteringSvc2 := newShortIntervalSvc(env.adminDB, env.logger)
	// 使用过滤后的 metadataStore，使 rebuilder 只查到 inst-itest- 实例
	filteredStore := &filteredMetaStore{inner: env.metaStore}
	rebuilder := NewRebuilder(filteredStore, meteringSvc2, env.logger)
	consumer2 := NewConsumer(meteringSvc2, env.logger)

	// 运行 rebuilder，应查到 DB 中的 running 实例并重建 ticker
	ctx := context.Background()
	if err := rebuilder.Rebuild(ctx); err != nil {
		t.Fatalf("Rebuild 失败: %v", err)
	}
	env.trackMeteringSvc(meteringSvc2, instanceID)

	// 6. 订阅 consumer₂（同名 durable，DeliverAll 回放补齐崩溃窗口消息）
	sub2, err := env.bus.Subscribe(ports.SubscribeOptions{
		Subject:     subject,
		Consumer:    consumerName,
		MaxInflight: 1,
		AckWait:     itestAckWait,
		MaxDeliver:  itestMaxDeliver,
	}, consumer2.HandleEvent())
	if err != nil {
		t.Fatalf("Subscribe consumer2 失败: %v", err)
	}
	env.trackSub(sub2, consumerName)

	// 7. 在崩溃窗口期间发布 stopped 事件（模拟崩溃后到达的事件）
	env.publishInstanceEvent(subject, tenantID, instanceID, "gpu_container", "stopped", 2, 1)

	// 8. 等待 ticker 产出记录（来自 rebuilder 重建的 ticker 或 DeliverAll 回放后的处理）
	waitForCondition(t, "重建后产出计量记录", itestWaitTimeout, func() bool {
		return env.countMeteringRecords(instanceID) > 0
	})

	records := env.getMeteringRecords(instanceID)
	t.Logf("场景4: 重建 + DeliverAll 后产出 %d 条记录", len(records))
	if len(records) == 0 {
		t.Errorf("期望重建后至少 1 条记录，实际 0 条")
	}
}

// =============================================================================
// 场景 5：seenSeq 乱序过滤——先发 seq=5 再发 seq=3，seq=3 被丢弃
// =============================================================================

// TestIntegrationSeenSeqOutOfOrder 覆盖 AC：
// 先发 seq=5 再发 seq=3，seq=3 被丢弃（seenSeq 乱序过滤）。
func TestIntegrationSeenSeqOutOfOrder(t *testing.T) {
	env := newItestEnv(t)
	tenantID := env.ensureTestTenant("tenant-itest-sc5")
	instanceID := "inst-itest-sc5-001"
	subject := fmt.Sprintf("%s.%s", itestSubjectPrefix, instanceID)

	var startCallCount atomic.Int64

	// 用 wrapper 包装 meteringSvc，统计 StartCollection 调用次数
	wrappedSvc := &countingMeteringService{
		inner:      newShortIntervalSvc(env.adminDB, env.logger),
		startCount: &startCallCount,
		stopCount:  &atomic.Int64{},
	}
	consumer := NewConsumer(wrappedSvc, env.logger)

	consumerName := itestConsumerPrefix + "-sc5-" + itestRunID
	sub, err := env.bus.Subscribe(ports.SubscribeOptions{
		Subject:     subject,
		Consumer:    consumerName,
		MaxInflight: 1,
		AckWait:     itestAckWait,
		MaxDeliver:  itestMaxDeliver,
	}, consumer.HandleEvent())
	if err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}
	env.trackSub(sub, consumerName)

	// 先发 seq=5（running）
	env.publishInstanceEvent(subject, tenantID, instanceID, "container", "running", 5, 0)
	env.trackMeteringSvc(wrappedSvc, instanceID)
	waitForCondition(t, "seq=5 被处理", itestWaitTimeout, func() bool {
		return startCallCount.Load() >= 1
	})

	// 再发 seq=3（running，过期事件，应被丢弃）
	env.publishInstanceEvent(subject, tenantID, instanceID, "container", "running", 3, 0)
	time.Sleep(1 * time.Second)

	// 验证 StartCollection 只被调用 1 次（seq=3 被丢弃）
	if got := startCallCount.Load(); got != 1 {
		t.Errorf("期望 StartCollection 调用 1 次（seq=3 应被丢弃），实际 %d 次", got)
	} else {
		t.Logf("场景5: seenSeq 乱序过滤生效，seq=3 被丢弃，StartCollection 仅调用 1 次")
	}
}

// countingMeteringService 包装 MeteringCollectionService，统计 Start/Stop 调用次数。
type countingMeteringService struct {
	inner      ports.MeteringCollectionService
	startCount *atomic.Int64
	stopCount  *atomic.Int64
}

func (c *countingMeteringService) StartCollection(ctx context.Context, spec ports.CollectionSpec) error {
	c.startCount.Add(1)
	return c.inner.StartCollection(ctx, spec)
}

func (c *countingMeteringService) StopCollection(ctx context.Context, resourceRef string) error {
	c.stopCount.Add(1)
	return c.inner.StopCollection(ctx, resourceRef)
}

// =============================================================================
// 场景 6：seenSeq 失败重投——StartCollection 失败后 Nak 重投，seenSeq 未推进，重投后重新处理
// =============================================================================

// TestIntegrationSeenSeqFailureRedelivery 覆盖 AC：
// StartCollection 失败后 Nak 重投，seenSeq 未推进，重投后重新处理。
func TestIntegrationSeenSeqFailureRedelivery(t *testing.T) {
	env := newItestEnv(t)
	tenantID := env.ensureTestTenant("tenant-itest-sc6")
	instanceID := "inst-itest-sc6-001"
	subject := fmt.Sprintf("%s.%s", itestSubjectPrefix, instanceID)

	var startCallCount atomic.Int64

	// 用 failFirstSvc 包装：第一次 StartCollection 返回 error，后续返回 nil
	failFirstSvc := &failFirstMeteringService{
		inner:      newShortIntervalSvc(env.adminDB, env.logger),
		startCount: &startCallCount,
	}
	consumer := NewConsumer(failFirstSvc, env.logger)

	consumerName := itestConsumerPrefix + "-sc6-" + itestRunID
	// 用短 AckWait 加速重投
	sub, err := env.bus.Subscribe(ports.SubscribeOptions{
		Subject:     subject,
		Consumer:    consumerName,
		MaxInflight: 1,
		AckWait:     1 * time.Second, // 短 AckWait 加速重投
		MaxDeliver:  itestMaxDeliver,
	}, consumer.HandleEvent())
	if err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}
	env.trackSub(sub, consumerName)

	// 发布 running 事件
	env.publishInstanceEvent(subject, tenantID, instanceID, "container", "running", 1, 0)
	env.trackMeteringSvc(failFirstSvc, instanceID)

	// 等待重投后成功处理（StartCollection 被调用 >= 2 次）
	waitForCondition(t, "重投后重新处理", itestWaitTimeout, func() bool {
		return startCallCount.Load() >= 2
	})

	if got := startCallCount.Load(); got < 2 {
		t.Errorf("期望 StartCollection 至少调用 2 次（失败后重投），实际 %d 次", got)
	} else {
		t.Logf("场景6: seenSeq 失败重投生效，StartCollection 调用 %d 次（第一次失败 Nak，重投后成功）", got)
	}
}

// failFirstMeteringService 包装 MeteringCollectionService，第一次 StartCollection 返回 error。
type failFirstMeteringService struct {
	inner      ports.MeteringCollectionService
	startCount *atomic.Int64
}

func (c *failFirstMeteringService) StartCollection(ctx context.Context, spec ports.CollectionSpec) error {
	n := c.startCount.Add(1)
	if n == 1 {
		return fmt.Errorf("simulated failure for redelivery test")
	}
	return c.inner.StartCollection(ctx, spec)
}

func (c *failFirstMeteringService) StopCollection(ctx context.Context, resourceRef string) error {
	return c.inner.StopCollection(ctx, resourceRef)
}

// =============================================================================
// 场景 7：租户上下文不匹配 → Nak 重投
// =============================================================================

// TestIntegrationTenantMismatchNak 覆盖 AC：
// tenant-id header 与 payload tenant_id 不匹配 → Nak 重投。
func TestIntegrationTenantMismatchNak(t *testing.T) {
	env := newItestEnv(t)
	tenantID := env.ensureTestTenant("tenant-itest-sc7")
	instanceID := "inst-itest-sc7-001"
	subject := fmt.Sprintf("%s.%s", itestSubjectPrefix, instanceID)

	var handlerCallCount atomic.Int64

	meteringSvc := newShortIntervalSvc(env.adminDB, env.logger)
	consumer := NewConsumer(meteringSvc, env.logger)

	// 用 wrapper handler 统计调用次数
	wrappedHandler := func(ctx context.Context, msg ports.Message) error {
		handlerCallCount.Add(1)
		return consumer.HandleEvent()(ctx, msg)
	}

	consumerName := itestConsumerPrefix + "-sc7-" + itestRunID
	sub, err := env.bus.Subscribe(ports.SubscribeOptions{
		Subject:     subject,
		Consumer:    consumerName,
		MaxInflight: 1,
		AckWait:     1 * time.Second,
		MaxDeliver:  3, // 限制重投次数
	}, wrappedHandler)
	if err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}
	env.trackSub(sub, consumerName)

	// 构造租户不匹配的事件：header tenant-id=tenantID，payload tenant_id="other-tenant"
	event := ports.InstanceLifecycleEvent{
		InstanceID:   instanceID,
		TenantID:     "other-tenant-" + tenantID, // 与 header 不匹配
		WorkloadKind: "container",
		NewStatus:    "running",
		EventSeq:     1,
	}
	payload, _ := json.Marshal(event)
	pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := env.bus.Publish(pubCtx, ports.EventEnvelope{
		TenantID:      tenantID, // header 中的 tenant-id
		AggregateID:   instanceID,
		AggregateType: "Instance",
		EventType:     "instance.created",
		Payload:       payload,
		OccurredAt:    time.Now(),
	}, ports.PublishOptions{Subject: subject}); err != nil {
		t.Fatalf("Publish 失败: %v", err)
	}

	// 等待多次投递（Nak 重投）
	waitForCondition(t, "Nak 重投多次", itestWaitTimeout, func() bool {
		return handlerCallCount.Load() >= 2
	})

	if got := handlerCallCount.Load(); got < 2 {
		t.Errorf("期望因租户不匹配触发 Nak 重投至少 2 次，实际 %d 次", got)
	} else {
		t.Logf("场景7: 租户不匹配触发 Nak 重投 %d 次", got)
	}
}

// =============================================================================
// 场景 8：毒消息（json 畸形）→ Ack 跳过
// =============================================================================

// TestIntegrationPoisonMessageAck 覆盖 AC：
// 毒消息（json 畸形）→ Ack 跳过（不重投）。
func TestIntegrationPoisonMessageAck(t *testing.T) {
	env := newItestEnv(t)
	tenantID := env.ensureTestTenant("tenant-itest-sc8")
	instanceID := "inst-itest-sc8-001"
	subject := fmt.Sprintf("%s.%s", itestSubjectPrefix, instanceID)

	var handlerCallCount atomic.Int64

	meteringSvc := newShortIntervalSvc(env.adminDB, env.logger)
	consumer := NewConsumer(meteringSvc, env.logger)

	wrappedHandler := func(ctx context.Context, msg ports.Message) error {
		n := handlerCallCount.Add(1)
		err := consumer.HandleEvent()(ctx, msg)
		if n == 1 && err == nil {
			t.Logf("场景8: 毒消息首次处理后返回 nil（Ack 跳过）")
		}
		return err
	}

	consumerName := itestConsumerPrefix + "-sc8-" + itestRunID
	sub, err := env.bus.Subscribe(ports.SubscribeOptions{
		Subject:     subject,
		Consumer:    consumerName,
		MaxInflight: 1,
		AckWait:     1 * time.Second,
		MaxDeliver:  3,
	}, wrappedHandler)
	if err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}
	env.trackSub(sub, consumerName)

	// 发布毒消息（非法 JSON）
	pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := env.bus.Publish(pubCtx, ports.EventEnvelope{
		TenantID:      tenantID,
		AggregateID:   instanceID,
		AggregateType: "Instance",
		EventType:     "instance.created",
		Payload:       []byte("not-a-valid-json"),
		OccurredAt:    time.Now(),
	}, ports.PublishOptions{Subject: subject}); err != nil {
		t.Fatalf("Publish 毒消息失败: %v", err)
	}

	// 等待首次处理
	waitForCondition(t, "毒消息首次处理", itestWaitTimeout, func() bool {
		return handlerCallCount.Load() >= 1
	})

	// 等待足够时间确认没有重投
	time.Sleep(2 * time.Second)
	if got := handlerCallCount.Load(); got != 1 {
		t.Errorf("毒消息应 Ack 跳过（仅投递 1 次），实际投递 %d 次", got)
	} else {
		t.Logf("场景8: 毒消息 Ack 跳过生效，仅投递 1 次")
	}
}

// =============================================================================
// 场景 9：DB UNIQUE 约束兜底——同实例同维度同周期重复 INSERT 时 ON CONFLICT DO NOTHING
// =============================================================================

// TestIntegrationDBUniqueConstraint 覆盖 AC：
// 同实例同维度同周期重复 INSERT 时 ON CONFLICT DO NOTHING。
func TestIntegrationDBUniqueConstraint(t *testing.T) {
	env := newItestEnv(t)
	tenantID := env.ensureTestTenant("tenant-itest-sc9")
	instanceID := "inst-itest-sc9-001"

	ctx := context.Background()
	period := time.Now().Format("2006-01-02T15:04")
	insertSQL := `
		INSERT INTO metering_usage_records (tenant_id, resource_ref, resource_type, period, quantity, unit)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, resource_ref, resource_type, period) DO NOTHING
	`

	// 第一次插入
	_, err := env.adminDB.Exec(ctx, insertSQL,
		tenantID, instanceID, string(ports.MeteringResourceInstanceGPUSeconds), period, 100.0, "gpu_second")
	if err != nil {
		t.Fatalf("第一次 INSERT 失败: %v", err)
	}

	// 第二次插入相同 (tenant_id, resource_ref, resource_type, period)，应 ON CONFLICT DO NOTHING
	_, err = env.adminDB.Exec(ctx, insertSQL,
		tenantID, instanceID, string(ports.MeteringResourceInstanceGPUSeconds), period, 200.0, "gpu_second")
	if err != nil {
		t.Fatalf("第二次 INSERT（重复）应 ON CONFLICT DO NOTHING，但返回错误: %v", err)
	}

	// 验证只有 1 条记录
	var count int
	err = env.adminDB.QueryRow(ctx,
		`SELECT COUNT(*) FROM metering_usage_records
		 WHERE tenant_id = $1 AND resource_ref = $2 AND resource_type = $3 AND period = $4`,
		tenantID, instanceID, string(ports.MeteringResourceInstanceGPUSeconds), period).Scan(&count)
	if err != nil {
		t.Fatalf("查询记录数失败: %v", err)
	}

	if count != 1 {
		t.Errorf("期望 1 条记录（ON CONFLICT DO NOTHING），实际 %d 条", count)
	} else {
		t.Logf("场景9: DB UNIQUE 约束兜底生效，重复 INSERT 后仍只有 1 条记录")
	}

	// 验证保留的是第一次的值（quantity=100），而非第二次（200）
	var quantity float64
	err = env.adminDB.QueryRow(ctx,
		`SELECT quantity FROM metering_usage_records
		 WHERE tenant_id = $1 AND resource_ref = $2 AND resource_type = $3 AND period = $4`,
		tenantID, instanceID, string(ports.MeteringResourceInstanceGPUSeconds), period).Scan(&quantity)
	if err != nil {
		t.Fatalf("查询 quantity 失败: %v", err)
	}
	if quantity != 100.0 {
		t.Errorf("ON CONFLICT DO NOTHING 应保留首次值 100，实际 %f", quantity)
	}
}

// =============================================================================
// 辅助类型：短采集周期 MeteringCollectionService 包装器
// =============================================================================

// shortIntervalSvc 包装 MeteringCollectionService，将 StartCollection 的 IntervalSec 覆写为 itestShortInterval，
// 加速测试中的 ticker 周期（默认 60s 太慢）。
type shortIntervalSvc struct {
	inner ports.MeteringCollectionService
}

// newShortIntervalSvc 创建短采集周期包装器。
func newShortIntervalSvc(db *pgxpool.Pool, logger *slog.Logger) ports.MeteringCollectionService {
	return &shortIntervalSvc{
		inner: service.NewMeteringCollectionService(db, logger, metering.CollectAll),
	}
}

func (s *shortIntervalSvc) StartCollection(ctx context.Context, spec ports.CollectionSpec) error {
	spec.IntervalSec = itestShortInterval
	return s.inner.StartCollection(ctx, spec)
}

func (s *shortIntervalSvc) StopCollection(ctx context.Context, resourceRef string) error {
	return s.inner.StopCollection(ctx, resourceRef)
}

// fallbackCollector 先查询真实 Prometheus（primary），失败时回退到 mock Prometheus（fallback）。
// 真实 Prometheus 无测试实例指标数据时 CollectAll 会跳过维度，fallback 确保 CPU/Mem 维度仍产出记录。
type fallbackCollector struct {
	primary  metering.Collector // 真实 Prometheus collector
	fallback metering.Collector // mock Prometheus collector
}

func (f *fallbackCollector) Collect(ctx context.Context, spec ports.CollectionSpec, period string) ([]ports.MeteringUsageRecord, error) {
	records, err := f.primary.Collect(ctx, spec, period)
	if err != nil {
		return f.fallback.Collect(ctx, spec, period)
	}
	return records, nil
}

// =============================================================================
// 辅助类型：过滤非测试实例的 MetadataStore 包装器
// =============================================================================

// filteredMetaStore 包装 MetadataStore，在 WithPlatformTx 中拦截 Query 调用，
// 为包含 "workload_instances" 的查询追加 "AND instance_id LIKE 'inst-itest-%'" 过滤条件，
// 使 rebuilder 只查到测试实例，避免为非测试实例启动 ticker。
type filteredMetaStore struct {
	inner ports.MetadataStore
}

func (f *filteredMetaStore) Ping(ctx context.Context) error {
	return f.inner.Ping(ctx)
}

func (f *filteredMetaStore) WithTenantTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	return f.inner.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		return fn(ctx, &filteredMetadataTx{inner: tx})
	})
}

func (f *filteredMetaStore) WithPlatformTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	return f.inner.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		return fn(ctx, &filteredMetadataTx{inner: tx})
	})
}

// filteredMetadataTx 包装 MetadataTx，在 Query 中追加测试实例过滤条件。
type filteredMetadataTx struct {
	inner ports.MetadataTx
}

func (w *filteredMetadataTx) Exec(ctx context.Context, sql string, args ...any) (ports.CommandTag, error) {
	return w.inner.Exec(ctx, sql, args...)
}

func (w *filteredMetadataTx) QueryRow(ctx context.Context, sql string, args ...any) ports.Row {
	return w.inner.QueryRow(ctx, sql, args...)
}

func (w *filteredMetadataTx) Query(ctx context.Context, sql string, args ...any) (ports.Rows, error) {
	// 为 workload_instances 查询追加测试实例过滤
	if isWorkloadInstancesQuery(sql) {
		filteredSQL := injectItestFilter(sql)
		return w.inner.Query(ctx, filteredSQL, args...)
	}
	return w.inner.Query(ctx, sql, args...)
}

// isWorkloadInstancesQuery 判断 SQL 是否为 workload_instances 查询。
func isWorkloadInstancesQuery(sql string) bool {
	return containsFold(sql, "workload_instances") && containsFold(sql, "state") && containsFold(sql, "running")
}

// injectItestFilter 在 SQL 的 WHERE 条件后追加 AND instance_id LIKE 'inst-itest-%'。
// 简单实现：在 "ORDER BY" 之前插入过滤条件。
func injectItestFilter(sql string) string {
	orderIdx := indexFold(sql, "order by")
	if orderIdx >= 0 {
		return sql[:orderIdx] + " AND instance_id LIKE 'inst-itest-%' " + sql[orderIdx:]
	}
	return sql + " AND instance_id LIKE 'inst-itest-%'"
}

// containsFold 检查 s 是否包含 substr（大小写不敏感）。
func containsFold(s, substr string) bool {
	return indexFold(s, substr) >= 0
}

// indexFold 返回 substr 在 s 中的起始位置（大小写不敏感），不存在返回 -1。
func indexFold(s, substr string) int {
	ls := toLower(s)
	lsub := toLower(substr)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
