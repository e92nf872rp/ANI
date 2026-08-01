package main

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/pkg/ports"
)

func gatewayInstanceRuntimeConfigFromEnv() bootstrap.Config {
	return bootstrap.Config{
		DatabaseURL:                       os.Getenv("DATABASE_URL"),
		WorkloadProvider:                  os.Getenv("WORKLOAD_PROVIDER"),
		WorkloadProviderApplyEnabled:      gatewayBoolFromEnv("WORKLOAD_PROVIDER_APPLY_ENABLED"),
		WorkloadLifecycleProvider:         os.Getenv("WORKLOAD_LIFECYCLE_PROVIDER"),
		WorkloadLifecycleApplyEnabled:     gatewayBoolFromEnv("WORKLOAD_LIFECYCLE_APPLY_ENABLED"),
		WorkloadOpsProvider:               os.Getenv("WORKLOAD_OPS_PROVIDER"),
		WorkloadOpsEnabled:                gatewayBoolFromEnv("WORKLOAD_OPS_ENABLED"),
		KubernetesAPIHost:                 os.Getenv("KUBERNETES_API_HOST"),
		KubernetesServiceHost:             os.Getenv("KUBERNETES_SERVICE_HOST"),
		KubernetesServicePort:             os.Getenv("KUBERNETES_SERVICE_PORT"),
		KubernetesBearerToken:             os.Getenv("KUBERNETES_BEARER_TOKEN"),
		KubernetesServiceAccountTokenFile: os.Getenv("KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE"),
		KubernetesServiceAccountCAFile:    os.Getenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE"),
		KubernetesProviderFieldManager:    os.Getenv("KUBERNETES_PROVIDER_FIELD_MANAGER"),
		WorkloadReconcileNormalInterval:   gatewayIntFromEnv("WORKLOAD_RECONCILE_NORMAL_INTERVAL_SECONDS"),
		WorkloadReconcileActiveInterval:   gatewayIntFromEnv("WORKLOAD_RECONCILE_ACTIVE_INTERVAL_SECONDS"),
		WorkloadReconcileStaleThreshold:   gatewayIntFromEnv("WORKLOAD_RECONCILE_STALE_THRESHOLD_SECONDS"),
		WorkloadReconcileMaxBatch:         gatewayIntFromEnv("WORKLOAD_RECONCILE_MAX_BATCH"),
		WorkloadReconcileFailureBackoff:   gatewayIntFromEnv("WORKLOAD_RECONCILE_FAILURE_BACKOFF_SECONDS"),
	}
}

func gatewayBoolFromEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

func gatewayIntFromEnv(key string) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func newGatewayInstanceRuntime(ctx context.Context, cfg bootstrap.Config, secrets ports.SecretService) (bootstrap.InstanceRuntime, func(), error) {
	cfg.SecretService = secrets
	closeRuntime := func() {}
	switch strings.TrimSpace(cfg.WorkloadProvider) {
	case "", "local":
		return bootstrap.InstanceRuntime{}, closeRuntime, nil
	default:
		return bootstrap.ConnectInstanceService(ctx, cfg)
	}
}
