package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/kubercloud/ani/pkg/adapters/metering"
	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/metering-service/internal"
	"github.com/kubercloud/ani/services/metering-service/internal/config"
	"github.com/kubercloud/ani/services/metering-service/internal/service"
)

func main() {
	cfg := config.Load()
	deps := bootstrap.MustConnect(cfg.Config)
	defer deps.Close()

	logger := deps.Logger

	// 注册全部三个 collector（GPU 无外部依赖，CPU/Mem 需要 Prometheus URL）。
	promHTTPClient := &http.Client{Timeout: 5 * time.Second}
	metering.RegisterAll(cfg.PrometheusURL, promHTTPClient)

	// 构造 meteringCollectionService，注入 CollectAll 实现（PR-M2 产物）。
	meteringSvc := service.NewMeteringCollectionService(deps.DB, logger, metering.CollectAll)

	// 构造 consumer 和 rebuilder，注入配置的采集周期。
	consumer := internal.NewConsumer(meteringSvc, logger, cfg.CollectionIntervalSeconds)
	rebuilder := internal.NewRebuilder(deps.Ports.Metadata, meteringSvc, logger, cfg.CollectionIntervalSeconds)

	// 1. 先重建（查 workload_instances WHERE state='running'）重建 ticker。
	//    重建失败不阻塞：日志告警后继续订阅（靠事件增量 + DeliverAll 兜底）。
	if err := rebuilder.Rebuild(context.Background()); err != nil {
		logger.Error("rebuild failed, continuing with subscribe (relying on event increments + DeliverAll)",
			"err", err)
	}

	// 2. 后订阅 NATS（DeliverAll 回放补齐崩溃窗口，MaxInflight=1 串行消费保证顺序）。
	//    Subscribe 失败时 os.Exit(1)，K8s 自动重启。
	sub, err := deps.Ports.MessageBus.Subscribe(ports.SubscribeOptions{
		Subject:     "ani.events.instance.>",
		Consumer:    "metering-consumer",
		MaxInflight: 1,
		AckWait:     30 * time.Second,
		MaxDeliver:  5,
		// 不设 Queue Group：单副本只有一个订阅，Queue 竞争语义无处发挥。
	}, consumer.HandleEvent())
	if err != nil {
		logger.Error("subscribe failed, exiting", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := sub.Drain(context.Background()); err != nil {
			logger.Error("subscription drain failed", "err", err)
		}
	}()

	logger.Info("metering-service started",
		"subject", "ani.events.instance.>",
		"consumer", "metering-consumer",
		"max_inflight", 1,
		"ack_wait", "30s",
		"max_deliver", 5,
	)

	// 3. 启动 health probe 服务器并阻塞等待 SIGINT/SIGTERM。
	//    health probe 监听 HEALTH_PORT（默认 9210），供 K8s readinessProbe/livenessProbe 探测。
	bootstrap.RunHealthProbe(deps)
}
