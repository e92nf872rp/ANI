package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"strings"

	"github.com/cloudwego/hertz/pkg/app/server"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"github.com/kubercloud/ani/services/ani-gateway/internal/router"
)

// Lab-only Gateway surface for the CPU PlatformWorkload live gate.
// It is not the in-cluster ani-gateway Deployment and must not be rolled out there.
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	if os.Getenv("ANI_AUTH_MODE") == "" {
		_ = os.Setenv("ANI_AUTH_MODE", "dev")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	host := os.Getenv("KUBERNETES_API_HOST")
	token := os.Getenv("KUBERNETES_BEARER_TOKEN")
	caFile := os.Getenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE")
	databaseURL := os.Getenv("DATABASE_URL")
	if host == "" || token == "" || caFile == "" || databaseURL == "" {
		logger.Error("KUBERNETES_API_HOST, KUBERNETES_BEARER_TOKEN, KUBERNETES_SERVICE_ACCOUNT_CA_FILE, and DATABASE_URL are required")
		os.Exit(1)
	}

	client, err := runtimeadapter.NewKubernetesRESTClient(runtimeadapter.KubernetesRESTClientConfig{
		Host:           host,
		BearerToken:    token,
		CAFile:         caFile,
		FieldManager:   "ani-inference-c14",
		RequestTimeout: 30 * time.Second,
	})
	if err != nil {
		logger.Error("kubernetes client", "err", err)
		os.Exit(1)
	}
	store, closeStore, err := bootstrap.ConnectMetadataStore(ctx, databaseURL)
	if err != nil {
		logger.Error("metadata store", "err", err)
		os.Exit(1)
	}
	defer closeStore()

	service := runtimeadapter.NewKubernetesPlatformWorkloadServiceWithStore(
		runtimeadapter.NewKubernetesPlatformWorkloadRuntime(client),
		runtimeadapter.NewMetadataPlatformWorkloadStore(store),
	)
	addr := os.Getenv("GATEWAY_LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:18080"
	}
	var inferenceClient router.InferenceControlClient
	if grpcAddr := strings.TrimSpace(os.Getenv("INFERENCE_SERVICE_GRPC_ADDR")); grpcAddr != "" {
		conn, client, err := router.DialInferenceControl(ctx, grpcAddr, 5*time.Second)
		if err != nil {
			logger.Error("inference control dial", "err", err)
			os.Exit(1)
		}
		defer conn.Close()
		inferenceClient = client
	}
	h := server.Default(server.WithHostPorts(addr), server.WithExitWaitTime(2))
	h.Use(middleware.RequestID())
	h.Use(middleware.Auth())
	router.RegisterWithOptions(h, router.RegisterOptions{
		PlatformWorkloadService: service,
		AsyncTaskStore:          runtimeadapter.NewLocalAsyncTaskStore(),
		InferenceServiceClient:  inferenceClient,
	})

	go func() {
		<-ctx.Done()
		_ = h.Shutdown(context.Background())
	}()
	logger.Info("platform-workload live harness listening", "addr", addr)
	h.Spin()
}
