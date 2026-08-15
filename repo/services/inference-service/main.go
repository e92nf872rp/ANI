package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/services/inference-service/internal/catalog"
	"github.com/kubercloud/ani/services/inference-service/internal/catalog/fake"
	"github.com/kubercloud/ani/services/inference-service/internal/catalog/modelsvc"
	"github.com/kubercloud/ani/services/inference-service/internal/config"
	"github.com/kubercloud/ani/services/inference-service/internal/grpcapi"
	"github.com/kubercloud/ani/services/inference-service/internal/reconcile"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	"github.com/kubercloud/ani/services/inference-service/internal/runtime"
	"github.com/kubercloud/ani/services/inference-service/internal/runtime/coresdk"
	runtimefake "github.com/kubercloud/ani/services/inference-service/internal/runtime/fake"
	"github.com/kubercloud/ani/services/inference-service/internal/service"
)

func main() {
	cfg := config.Load()
	deps := bootstrap.MustConnect(cfg.Config)
	defer deps.Close()

	store := repository.NewPostgres(deps.DB, deps.DB)
	rt := newInferenceRuntime(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go reconcile.NewWorker(store, rt, cfg.WorkerOwner, time.Now).Run(ctx)

	creator := service.NewCreator(store, newModelCatalog(cfg), time.Now)
	if admission, ok := rt.(service.RuntimeAdmission); ok {
		creator = creator.WithAdmission(admission)
	}
	server := grpcapi.NewServer(
		creator,
		service.NewController(store, time.Now),
	).WithLogs(service.NewLogReader(store, rt))
	bootstrap.RunGRPC(cfg.GRPCPort, server.Register, deps)
}

func newModelCatalog(cfg config.Config) catalog.ModelCatalog {
	if os.Getenv("INFERENCE_LAB_CATALOG") == "1" {
		return labCatalog(cfg)
	}
	if cfg.ModelServiceGRPCAddr == "" {
		return fake.New()
	}
	modelCatalog, err := modelsvc.Dial(cfg.ModelServiceGRPCAddr, modelsvc.ProfilesFromImages(cfg.CPUImageRef, cfg.GPUImageRef))
	if err != nil {
		panic(err)
	}
	return modelCatalog
}

func labCatalog(cfg config.Config) catalog.ModelCatalog {
	image := strings.TrimSpace(os.Getenv("INFERENCE_LAB_IMAGE_REF"))
	if image == "" {
		image = strings.TrimSpace(cfg.CPUImageRef)
	}
	artifact := strings.TrimSpace(os.Getenv("INFERENCE_LAB_ARTIFACT_REF"))
	if artifact == "" {
		artifact = "pvc://vllm-model#/models/qwen"
	}
	profile := catalog.EngineProfile{ID: "vllm-cpu", Version: "v1", Runtime: "vllm", ImageRef: image}
	gpuProfile := catalog.EngineProfile{ID: "vllm-gpu", Version: "v1", Runtime: "vllm", ImageRef: image}
	return fake.NewLab(catalog.ModelVersion{
		Ready:         true,
		DisplayName:   "lab-vllm-cpu",
		Format:        "safetensors",
		ArtifactRef:   artifact,
		EngineProfile: profile,
		CPUProfile:    &profile,
		GPUProfile:    &gpuProfile,
	})
}

func newInferenceRuntime(cfg config.Config) runtime.InferenceRuntime {
	if cfg.CoreAPIBaseURL == "" {
		return runtimefake.New()
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
