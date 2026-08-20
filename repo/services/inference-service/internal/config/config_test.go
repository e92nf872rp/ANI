package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadReadsServiceLocalSettings(t *testing.T) {
	t.Setenv("INFERENCE_DATABASE_URL", "postgres://tenant/db")
	t.Setenv("NATS_URL", "nats://nats:4222")
	t.Setenv("REDIS_URL", "redis://redis:6379/0")
	t.Setenv("GRPC_PORT", "9104")
	t.Setenv("HEALTH_PORT", "9204")
	t.Setenv("INFERENCE_WORKER_OWNER", "inference-test")

	cfg := Load()
	if cfg.DatabaseURL != "postgres://tenant/db" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.NATSURL != "nats://nats:4222" || cfg.RedisURL != "redis://redis:6379/0" {
		t.Fatalf("bus urls = %+v", cfg.Config)
	}
	if cfg.GRPCPort != 9104 || cfg.HealthPort != 9204 || cfg.ServiceName != "inference-service" {
		t.Fatalf("service settings = %+v", cfg.Config)
	}
	if cfg.WorkerOwner != "inference-test" {
		t.Fatalf("WorkerOwner = %q", cfg.WorkerOwner)
	}
}

func TestLoadReadsWorkerLimits(t *testing.T) {
	t.Setenv("INFERENCE_MAX_ATTEMPTS", "90")
	t.Setenv("INFERENCE_DEPLOY_TIMEOUT_SECONDS", "20")
	t.Setenv("INFERENCE_RETRY_DELAY_SECONDS", "1")

	cfg := Load()
	if cfg.MaxAttempts != 90 {
		t.Fatalf("MaxAttempts = %d", cfg.MaxAttempts)
	}
	if cfg.DeployTimeout != 20*time.Second {
		t.Fatalf("DeployTimeout = %s", cfg.DeployTimeout)
	}
	if cfg.RetryDelay != time.Second {
		t.Fatalf("RetryDelay = %s", cfg.RetryDelay)
	}
}

func TestLoadFallsBackSharedDatabaseAndDefaults(t *testing.T) {
	t.Setenv("INFERENCE_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "postgres://shared/db")
	t.Setenv("NATS_URL", "")
	t.Setenv("REDIS_URL", "")
	t.Setenv("GRPC_PORT", "")
	t.Setenv("HEALTH_PORT", "")
	t.Setenv("INFERENCE_WORKER_OWNER", "")

	cfg := Load()
	if cfg.DatabaseURL != "postgres://shared/db" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.NATSURL != "nats://127.0.0.1:4222" || !strings.Contains(cfg.RedisURL, "6379") {
		t.Fatalf("default bus urls = %+v", cfg.Config)
	}
	if cfg.GRPCPort != 9104 || cfg.HealthPort != 9204 {
		t.Fatalf("default ports = %d/%d", cfg.GRPCPort, cfg.HealthPort)
	}
	if !strings.HasPrefix(cfg.WorkerOwner, "inference-service") {
		t.Fatalf("WorkerOwner = %q", cfg.WorkerOwner)
	}
}

func TestLoadIgnoresRemovedEngineImageEnv(t *testing.T) {
	t.Setenv("INFERENCE_CPU_IMAGE_REF", "registry.example/vllm:latest")
	t.Setenv("INFERENCE_GPU_IMAGE_REF", "registry.example/vllm-gpu:latest")
	t.Setenv("INFERENCE_SGLANG_CPU_IMAGE_REF", "registry.example/sglang:latest")
	t.Setenv("INFERENCE_SGLANG_GPU_IMAGE_REF", "registry.example/sglang-gpu:latest")

	cfg := Load()
	if cfg.ServiceName != "inference-service" {
		t.Fatalf("ServiceName = %q", cfg.ServiceName)
	}
}
