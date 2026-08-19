package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/services/inference-service/internal/catalog"
	"github.com/kubercloud/ani/services/inference-service/internal/catalog/modelsvc"
	"github.com/kubercloud/ani/services/inference-service/internal/config"
	"github.com/kubercloud/ani/services/inference-service/internal/grpcapi"
	"github.com/kubercloud/ani/services/inference-service/internal/reconcile"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	"github.com/kubercloud/ani/services/inference-service/internal/runtime"
	"github.com/kubercloud/ani/services/inference-service/internal/runtime/coresdk"
	"github.com/kubercloud/ani/services/inference-service/internal/service"
)

// main 组装 catalog + Core runtime + 对账 worker + InferenceControl gRPC。
func main() {
	cfg := config.Load()
	deps := bootstrap.MustConnect(cfg.Config)
	defer deps.Close()

	store := repository.NewPostgres(deps.DB, deps.DB)
	rt := newInferenceRuntime(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	slog.Info("inference worker limits",
		"max_attempts", cfg.MaxAttempts,
		"deploy_timeout_seconds", int(cfg.DeployTimeout/time.Second),
		"retry_delay_seconds", int(cfg.RetryDelay/time.Second),
	)
	go reconcile.NewWorker(store, rt, cfg.WorkerOwner, time.Now).
		WithLimits(cfg.MaxAttempts, cfg.DeployTimeout, cfg.RetryDelay).
		Run(ctx)

	creator := service.NewCreator(store, newModelCatalog(cfg), time.Now)
	if admission, ok := rt.(service.RuntimeAdmission); ok {
		creator = creator.WithAdmission(admission)
	}
	server := grpcapi.NewServer(
		creator.WithRuntime(rt),
		service.NewController(store, time.Now).WithRuntime(rt),
	).WithLogs(service.NewLogReader(store, rt))
	bootstrap.RunGRPC(cfg.GRPCPort, server.Register, deps)
}

// newModelCatalog 只连真实 model-service。假 catalog 只允许出现在 *_test.go。
func newModelCatalog(cfg config.Config) catalog.ModelCatalog {
	if os.Getenv("INFERENCE_LAB_CATALOG") == "1" {
		panic("INFERENCE_LAB_CATALOG is removed; configure MODEL_SERVICE_GRPC_ADDR")
	}
	if strings.TrimSpace(cfg.ModelServiceGRPCAddr) == "" {
		panic("MODEL_SERVICE_GRPC_ADDR is required")
	}
	modelCatalog, err := modelsvc.Dial(cfg.ModelServiceGRPCAddr)
	if err != nil {
		panic(err)
	}
	return modelCatalog
}

// newInferenceRuntime 只走 Core SDK。假 runtime 只允许出现在 *_test.go。
func newInferenceRuntime(cfg config.Config) runtime.InferenceRuntime {
	if strings.TrimSpace(cfg.CoreAPIBaseURL) == "" {
		panic("CORE_API_BASE_URL is required")
	}
	rt := coresdk.New(cfg.CoreAPIBaseURL, cfg.CoreServiceToken)
	if cfg.AuthServiceGRPCAddr != "" && cfg.AuthMintSecret != "" {
		minter, err := coresdk.DialMinter(cfg.AuthServiceGRPCAddr, cfg.AuthMintSecret)
		if err != nil {
			panic(err)
		}
		return rt.WithMinter(minter)
	}
	return rt
}
