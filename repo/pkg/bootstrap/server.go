package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// Config holds connection strings. Load from environment in each service's config.Load().
type Config struct {
	DatabaseURL           string
	NATSURL               string
	RedisURL              string
	RedisMode             string
	RedisAddrs            []string
	RedisMasterName       string
	RedisUsername         string
	RedisPassword         string
	RedisSentinelUsername string
	RedisSentinelPassword string
	RedisDB               int
	GRPCPort              int
	HealthPort            int
	ServiceName           string

	ObjectStoreProvider        string
	ObjectStoreEndpoint        string
	ObjectStoreEndpoints       []string
	ObjectStorePublicEndpoint  string
	ObjectStoreAccessKeyID     string
	ObjectStoreSecretAccessKey string
	ObjectStoreSessionToken    string
	ObjectStoreRegion          string
	ObjectStoreSecure          bool
	ObjectStoreBucketPrefix    string

	VectorStoreProvider         string
	VectorStoreEndpoint         string
	VectorStoreEndpoints        []string
	VectorStoreToken            string
	VectorStoreDatabase         string
	VectorStoreCollectionPrefix string

	WorkloadProvider               string
	WorkloadProviderApplyEnabled   bool
	SecretService                  ports.SecretService
	GPUInventoryProvider           string
	NetworkProvider                string
	NetworkProviderApplyEnabled    bool
	NetworkProviderUserID          string
	NetworkProviderPermissionProof string
	StorageProvider                string
	StorageProviderApplyEnabled    bool
	StorageProviderUserID          string
	StorageProviderPermissionProof string
	RegistryProviderMode           string
	HarborEndpoint                 string
	HarborUsername                 string
	HarborPassword                 string
	HarborRequestTimeout           time.Duration
	RegistryTLSInsecure            bool
	// Optional shared Core services for Gateway so /networks,/volumes and
	// /instances resolve against the same in-process adapters.
	SharedNetworkService               ports.NetworkService
	SharedStorageService               ports.StorageService
	SharedImageRegistry                ports.ImageRegistry
	WorkloadLifecycleProvider          string
	WorkloadLifecycleApplyEnabled      bool
	WorkloadOpsProvider                string
	WorkloadOpsEnabled                 bool
	InstanceObservabilityProvider      string
	InstanceObservabilityPrometheusURL string
	InstanceObservabilityExecBaseURL   string
	KubernetesAPIHost                  string
	KubernetesServiceHost              string
	KubernetesServicePort              string
	KubernetesBearerToken              string
	KubernetesServiceAccountTokenFile  string
	KubernetesServiceAccountCAFile     string
	KubernetesProviderFieldManager     string

	WorkloadReconcileControllerEnabled     bool
	WorkloadReconcileNormalInterval        int
	WorkloadReconcileActiveInterval        int
	WorkloadReconcileStaleThreshold        int
	WorkloadReconcileMaxBatch              int
	WorkloadReconcileFailureBackoff        int
	WorkloadReconcileLeaderElectionEnabled bool
	WorkloadReconcileLeaderIdentity        string
	WorkloadReconcileLeaderLeaseName       string
	WorkloadReconcileLeaderLeaseTTL        int
	WorkloadReconcileLeaderRenewInterval   int

	// GPUQuotaEnabled toggles the GPU quota three-gate check (SPEC §5.1).
	// When false, TryManyTx/Confirm/Cancel/Release are all skipped and
	// quota is fully bypassed. Default false.
	GPUQuotaEnabled bool
	// ProvisioningTimeoutMin is the maximum minutes an instance may stay
	// in the provisioning state before the reconciler marks it failed and
	// Cancels the reserved quota (SPEC §5.1). Default 10.
	ProvisioningTimeoutMin int
}

// MustConnect initializes all dependencies. Exits the process if any connection fails.
func MustConnect(cfg Config) *Deps {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	cfg = cfg.withEnvironmentOverrides()

	db, err := connectDB(cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}

	nc, js, err := connectNATS(cfg.NATSURL)
	if err != nil {
		logger.Error("failed to connect to NATS", "err", err)
		os.Exit(1)
	}

	rdb, err := connectRedisWithConfig(redisConfigFromBootstrapConfig(cfg))
	if err != nil {
		logger.Error("failed to connect to Redis", "err", err)
		os.Exit(1)
	}

	ports, err := NewCapabilitiesWithConfig(db, js, rdb, cfg)
	if err != nil {
		logger.Error("failed to initialize capability adapters", "err", err)
		os.Exit(1)
	}

	return &Deps{
		DB:          db,
		NATS:        nc,
		JS:          js,
		Redis:       rdb,
		Ports:       ports,
		Logger:      logger,
		ServiceName: cfg.ServiceName,
		HealthPort:  cfg.HealthPort,

		WorkloadReconcileControllerEnabled: cfg.WorkloadReconcileControllerEnabled,
	}
}

func (c Config) withEnvironmentOverrides() Config {
	if value := os.Getenv("REDIS_MODE"); value != "" {
		c.RedisMode = value
	}
	if value := os.Getenv("REDIS_ADDRS"); value != "" {
		c.RedisAddrs = splitCSV(value)
	}
	if value := os.Getenv("REDIS_MASTER_NAME"); value != "" {
		c.RedisMasterName = value
	}
	if value := os.Getenv("REDIS_USERNAME"); value != "" {
		c.RedisUsername = value
	}
	if value := os.Getenv("REDIS_PASSWORD"); value != "" {
		c.RedisPassword = value
	}
	if value := os.Getenv("REDIS_SENTINEL_USERNAME"); value != "" {
		c.RedisSentinelUsername = value
	}
	if value := os.Getenv("REDIS_SENTINEL_PASSWORD"); value != "" {
		c.RedisSentinelPassword = value
	}
	if value := os.Getenv("REDIS_DB"); value != "" {
		c.RedisDB = parseInt(value, c.RedisDB)
	}
	if value := os.Getenv("WORKLOAD_PROVIDER"); value != "" {
		c.WorkloadProvider = value
	}
	if value := os.Getenv("HEALTH_PORT"); value != "" {
		c.HealthPort = parseInt(value, c.HealthPort)
	}
	if value := os.Getenv("WORKLOAD_PROVIDER_APPLY_ENABLED"); value != "" {
		c.WorkloadProviderApplyEnabled = parseBool(value)
	}
	if value := os.Getenv("GPU_INVENTORY_PROVIDER"); value != "" {
		c.GPUInventoryProvider = value
	}
	if value := os.Getenv("NETWORK_PROVIDER"); value != "" {
		c.NetworkProvider = value
	}
	if value := os.Getenv("NETWORK_PROVIDER_APPLY_ENABLED"); value != "" {
		c.NetworkProviderApplyEnabled = parseBool(value)
	}
	if value := os.Getenv("NETWORK_PROVIDER_USER_ID"); value != "" {
		c.NetworkProviderUserID = value
	}
	if value := os.Getenv("NETWORK_PROVIDER_PERMISSION_PROOF"); value != "" {
		c.NetworkProviderPermissionProof = value
	}
	if value := os.Getenv("STORAGE_PROVIDER"); value != "" {
		c.StorageProvider = value
	}
	if value := os.Getenv("STORAGE_PROVIDER_APPLY_ENABLED"); value != "" {
		c.StorageProviderApplyEnabled = parseBool(value)
	}
	if value := os.Getenv("STORAGE_PROVIDER_USER_ID"); value != "" {
		c.StorageProviderUserID = value
	}
	if value := os.Getenv("STORAGE_PROVIDER_PERMISSION_PROOF"); value != "" {
		c.StorageProviderPermissionProof = value
	}
	if value := os.Getenv("REGISTRY_PROVIDER_MODE"); value != "" {
		c.RegistryProviderMode = value
	}
	if value := os.Getenv("HARBOR_ENDPOINT"); value != "" {
		c.HarborEndpoint = value
	}
	if value := os.Getenv("HARBOR_USERNAME"); value != "" {
		c.HarborUsername = value
	}
	if value := os.Getenv("HARBOR_PASSWORD"); value != "" {
		c.HarborPassword = value
	}
	if value := os.Getenv("HARBOR_REQUEST_TIMEOUT"); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			c.HarborRequestTimeout = parsed
		}
	}
	if value := os.Getenv("REGISTRY_TLS_INSECURE"); value != "" {
		c.RegistryTLSInsecure = parseBool(value)
	}
	if value := os.Getenv("WORKLOAD_LIFECYCLE_PROVIDER"); value != "" {
		c.WorkloadLifecycleProvider = value
	}
	if value := os.Getenv("WORKLOAD_LIFECYCLE_APPLY_ENABLED"); value != "" {
		c.WorkloadLifecycleApplyEnabled = parseBool(value)
	}
	if value := os.Getenv("WORKLOAD_OPS_PROVIDER"); value != "" {
		c.WorkloadOpsProvider = value
	}
	if value := os.Getenv("WORKLOAD_OPS_ENABLED"); value != "" {
		c.WorkloadOpsEnabled = parseBool(value)
	}
	if value := os.Getenv("INSTANCE_OBSERVABILITY_PROVIDER"); value != "" {
		c.InstanceObservabilityProvider = value
	}
	if value := os.Getenv("INSTANCE_OBSERVABILITY_PROMETHEUS_URL"); value != "" {
		c.InstanceObservabilityPrometheusURL = value
	}
	if value := os.Getenv("INSTANCE_OBSERVABILITY_EXEC_BASE_URL"); value != "" {
		c.InstanceObservabilityExecBaseURL = value
	}
	if value := os.Getenv("KUBERNETES_API_HOST"); value != "" {
		c.KubernetesAPIHost = value
	}
	if value := os.Getenv("KUBERNETES_SERVICE_HOST"); value != "" {
		c.KubernetesServiceHost = value
	}
	if value := os.Getenv("KUBERNETES_SERVICE_PORT"); value != "" {
		c.KubernetesServicePort = value
	}
	if value := os.Getenv("KUBERNETES_BEARER_TOKEN"); value != "" {
		c.KubernetesBearerToken = value
	}
	if value := os.Getenv("KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE"); value != "" {
		c.KubernetesServiceAccountTokenFile = value
	}
	if value := os.Getenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE"); value != "" {
		c.KubernetesServiceAccountCAFile = value
	}
	if value := os.Getenv("KUBERNETES_PROVIDER_FIELD_MANAGER"); value != "" {
		c.KubernetesProviderFieldManager = value
	}
	if value := os.Getenv("OBJECT_STORE_PROVIDER"); value != "" {
		c.ObjectStoreProvider = value
	}
	if value := os.Getenv("OBJECT_STORE_ENDPOINT"); value != "" {
		c.ObjectStoreEndpoint = value
	}
	if value := os.Getenv("OBJECT_STORE_ENDPOINTS"); value != "" {
		c.ObjectStoreEndpoints = splitCSV(value)
	}
	if value := os.Getenv("OBJECT_STORE_PUBLIC_ENDPOINT"); value != "" {
		c.ObjectStorePublicEndpoint = value
	}
	if value := os.Getenv("OBJECT_STORE_ACCESS_KEY_ID"); value != "" {
		c.ObjectStoreAccessKeyID = value
	}
	if value := os.Getenv("OBJECT_STORE_SECRET_ACCESS_KEY"); value != "" {
		c.ObjectStoreSecretAccessKey = value
	}
	if value := os.Getenv("OBJECT_STORE_SESSION_TOKEN"); value != "" {
		c.ObjectStoreSessionToken = value
	}
	if value := os.Getenv("OBJECT_STORE_REGION"); value != "" {
		c.ObjectStoreRegion = value
	}
	if value := os.Getenv("OBJECT_STORE_SECURE"); value != "" {
		c.ObjectStoreSecure = parseBool(value)
	}
	if value := os.Getenv("OBJECT_STORE_BUCKET_PREFIX"); value != "" {
		c.ObjectStoreBucketPrefix = value
	}
	if value := os.Getenv("VECTOR_STORE_PROVIDER"); value != "" {
		c.VectorStoreProvider = value
	}
	if value := os.Getenv("VECTOR_STORE_ENDPOINT"); value != "" {
		c.VectorStoreEndpoint = value
	}
	if value := os.Getenv("VECTOR_STORE_ENDPOINTS"); value != "" {
		c.VectorStoreEndpoints = splitCSV(value)
	}
	if value := os.Getenv("VECTOR_STORE_TOKEN"); value != "" {
		c.VectorStoreToken = value
	}
	if value := os.Getenv("VECTOR_STORE_DATABASE"); value != "" {
		c.VectorStoreDatabase = value
	}
	if value := os.Getenv("VECTOR_STORE_COLLECTION_PREFIX"); value != "" {
		c.VectorStoreCollectionPrefix = value
	}
	if value := os.Getenv("WORKLOAD_RECONCILE_CONTROLLER_ENABLED"); value != "" {
		c.WorkloadReconcileControllerEnabled = parseBool(value)
	}
	if value := os.Getenv("WORKLOAD_RECONCILE_NORMAL_INTERVAL_SECONDS"); value != "" {
		c.WorkloadReconcileNormalInterval = parseInt(value, c.WorkloadReconcileNormalInterval)
	}
	if value := os.Getenv("WORKLOAD_RECONCILE_ACTIVE_INTERVAL_SECONDS"); value != "" {
		c.WorkloadReconcileActiveInterval = parseInt(value, c.WorkloadReconcileActiveInterval)
	}
	if value := os.Getenv("WORKLOAD_RECONCILE_STALE_THRESHOLD_SECONDS"); value != "" {
		c.WorkloadReconcileStaleThreshold = parseInt(value, c.WorkloadReconcileStaleThreshold)
	}
	if value := os.Getenv("WORKLOAD_RECONCILE_MAX_BATCH"); value != "" {
		c.WorkloadReconcileMaxBatch = parseInt(value, c.WorkloadReconcileMaxBatch)
	}
	if value := os.Getenv("WORKLOAD_RECONCILE_FAILURE_BACKOFF_SECONDS"); value != "" {
		c.WorkloadReconcileFailureBackoff = parseInt(value, c.WorkloadReconcileFailureBackoff)
	}
	if value := os.Getenv("WORKLOAD_RECONCILE_LEADER_ELECTION_ENABLED"); value != "" {
		c.WorkloadReconcileLeaderElectionEnabled = parseBool(value)
	}
	if value := os.Getenv("WORKLOAD_RECONCILE_LEADER_IDENTITY"); value != "" {
		c.WorkloadReconcileLeaderIdentity = value
	}
	if value := os.Getenv("WORKLOAD_RECONCILE_LEADER_LEASE_NAME"); value != "" {
		c.WorkloadReconcileLeaderLeaseName = value
	}
	if value := os.Getenv("WORKLOAD_RECONCILE_LEADER_LEASE_TTL_SECONDS"); value != "" {
		c.WorkloadReconcileLeaderLeaseTTL = parseInt(value, c.WorkloadReconcileLeaderLeaseTTL)
	}
	if value := os.Getenv("WORKLOAD_RECONCILE_LEADER_RENEW_INTERVAL_SECONDS"); value != "" {
		c.WorkloadReconcileLeaderRenewInterval = parseInt(value, c.WorkloadReconcileLeaderRenewInterval)
	}
	if value := os.Getenv("GPU_QUOTA_ENABLED"); value != "" {
		c.GPUQuotaEnabled = parseBool(value)
	}
	if value := os.Getenv("PROVISIONING_TIMEOUT_MIN"); value != "" {
		c.ProvisioningTimeoutMin = parseInt(value, c.ProvisioningTimeoutMin)
	}
	return c
}

func parseInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseBool(value string) bool {
	switch value {
	case "1", "true", "TRUE", "yes", "YES", "on", "ON":
		return true
	default:
		return false
	}
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

// RunGRPC starts a gRPC server on port and blocks until SIGINT/SIGTERM.
// register is called to register all service implementations.
// Performs graceful shutdown: waits for in-flight RPCs to complete.
func RunGRPC(port int, register func(*grpc.Server), deps *Deps) {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		deps.Logger.Error("failed to listen", "port", port, "err", err)
		os.Exit(1)
	}

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			loggingUnaryInterceptor(deps.Logger),
			recoveryUnaryInterceptor(deps.Logger),
		),
	)

	register(srv)
	reflection.Register(srv) // enables grpcurl and grpc-gateway reflection

	var probe *http.Server
	if deps.HealthPort > 0 {
		probe = &http.Server{
			Addr:              fmt.Sprintf(":%d", deps.HealthPort),
			Handler:           newProbeHandler(deps.ServiceName, dependencyProbeChecks(deps), reconcileControllerMetricsReader(deps)),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			deps.Logger.Info("health probe server listening", "port", deps.HealthPort)
			if err := probe.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				deps.Logger.Error("health probe serve error", "err", err)
				os.Exit(1)
			}
		}()
	}

	go func() {
		deps.Logger.Info("gRPC server listening", "port", port)
		if err := srv.Serve(lis); err != nil {
			deps.Logger.Error("gRPC serve error", "err", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startWorkloadReconcileController(ctx, deps)
	<-ctx.Done()

	deps.Logger.Info("shutting down gRPC server gracefully")
	if probe != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := probe.Shutdown(shutdownCtx); err != nil {
			deps.Logger.Error("health probe shutdown error", "err", err)
		}
	}
	srv.GracefulStop()
	deps.Logger.Info("gRPC server stopped")
}

// RunHealthProbe 仅启动 health probe HTTP 服务器，不启动 gRPC。
// 用于不需要 gRPC 的服务（如 metering-service）。
// 阻塞直到收到 SIGINT/SIGTERM，然后优雅关闭 probe 服务器。
func RunHealthProbe(deps *Deps) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var probe *http.Server
	if deps.HealthPort > 0 {
		probe = &http.Server{
			Addr:              fmt.Sprintf(":%d", deps.HealthPort),
			Handler:           newProbeHandler(deps.ServiceName, dependencyProbeChecks(deps), reconcileControllerMetricsReader(deps)),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			deps.Logger.Info("health probe server listening", "port", deps.HealthPort)
			if err := probe.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				deps.Logger.Error("health probe serve error", "err", err)
				os.Exit(1)
			}
		}()
	}

	<-ctx.Done()
	deps.Logger.Info("health probe shutting down")
	if probe != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := probe.Shutdown(shutdownCtx); err != nil {
			deps.Logger.Error("health probe shutdown error", "err", err)
		}
	}
	deps.Logger.Info("health probe stopped")
}

func RunWorkloadReconcileWorker(deps *Deps) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	runWorkloadReconcileWorker(ctx, deps)
}

func runWorkloadReconcileWorker(ctx context.Context, deps *Deps) {
	logger := slog.Default()
	if deps != nil && deps.Logger != nil {
		logger = deps.Logger
	}
	var probe *http.Server
	if deps != nil && deps.HealthPort > 0 {
		probe = &http.Server{
			Addr:              fmt.Sprintf(":%d", deps.HealthPort),
			Handler:           newProbeHandler(deps.ServiceName, dependencyProbeChecks(deps), reconcileControllerMetricsReader(deps)),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			logger.Info("reconcile worker probe server listening", "port", deps.HealthPort)
			if err := probe.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("reconcile worker probe serve error", "err", err)
				os.Exit(1)
			}
		}()
	}
	if deps != nil {
		deps.WorkloadReconcileControllerEnabled = true
	}
	startWorkloadReconcileController(ctx, deps)
	<-ctx.Done()
	if probe != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := probe.Shutdown(shutdownCtx); err != nil {
			logger.Error("reconcile worker probe shutdown error", "err", err)
		}
	}
	logger.Info("workload reconcile worker stopped")
}

func reconcileControllerMetricsReader(deps *Deps) ports.ReconcileControllerMetricsReader {
	if deps == nil || deps.Ports.WorkloadController == nil {
		return nil
	}
	reader, ok := deps.Ports.WorkloadController.(ports.ReconcileControllerMetricsReader)
	if !ok {
		return nil
	}
	return reader
}

func startWorkloadReconcileController(ctx context.Context, deps *Deps) bool {
	if deps == nil || !deps.WorkloadReconcileControllerEnabled || deps.Ports.WorkloadController == nil {
		return false
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	go func() {
		logger.InfoContext(ctx, "workload reconcile controller starting")
		if err := deps.Ports.WorkloadController.Start(ctx); err != nil {
			logger.ErrorContext(ctx, "workload reconcile controller stopped with error", "err", err)
		}
	}()
	return true
}

// loggingUnaryInterceptor logs every gRPC call with duration and status.
func loggingUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			logger.ErrorContext(ctx, "gRPC error", "method", info.FullMethod, "err", err)
		}
		return resp, err
	}
}

// recoveryUnaryInterceptor catches panics and converts them to gRPC Internal errors.
func recoveryUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorContext(ctx, "gRPC panic recovered", "method", info.FullMethod, "panic", r)
				err = fmt.Errorf("internal error")
			}
		}()
		return handler(ctx, req)
	}
}
