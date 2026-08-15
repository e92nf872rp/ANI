package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/kubercloud/ani/pkg/bootstrap"
)

type Config struct {
	bootstrap.Config
	WorkerOwner          string
	CoreAPIBaseURL       string
	CoreServiceToken     string
	ModelServiceGRPCAddr string
	CPUImageRef          string
	GPUImageRef          string
	AuthServiceGRPCAddr  string
	AuthMintSecret       string
}

func Load() Config {
	return Config{
		Config: bootstrap.Config{
			DatabaseURL: env("INFERENCE_DATABASE_URL", env("DATABASE_URL", "postgres://ani_app_user:ani_dev_password@127.0.0.1:5432/ani?sslmode=disable")),
			NATSURL:     env("NATS_URL", "nats://127.0.0.1:4222"),
			RedisURL:    env("REDIS_URL", "redis://:ani_dev_password@127.0.0.1:6379/0"),
			GRPCPort:    envInt("GRPC_PORT", 9104),
			HealthPort:  envInt("HEALTH_PORT", 9204),
			ServiceName: "inference-service",
		},
		WorkerOwner:          workerOwner(),
		CoreAPIBaseURL:       env("CORE_API_BASE_URL", ""),
		CoreServiceToken:     env("CORE_SERVICE_TOKEN", ""),
		ModelServiceGRPCAddr: env("MODEL_SERVICE_GRPC_ADDR", ""),
		CPUImageRef:          env("INFERENCE_CPU_IMAGE_REF", ""),
		GPUImageRef:          env("INFERENCE_GPU_IMAGE_REF", ""),
		AuthServiceGRPCAddr:  env("AUTH_SERVICE_GRPC_ADDR", ""),
		AuthMintSecret:       env("AUTH_SERVICE_MINT_SECRET", ""),
	}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func workerOwner() string {
	if owner := strings.TrimSpace(os.Getenv("INFERENCE_WORKER_OWNER")); owner != "" {
		return owner
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "inference-service"
	}
	return "inference-service/" + host
}
