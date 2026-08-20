package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/bootstrap"
)

// Config 是 inference-service 进程启动配置。运行镜像来自创建请求，不读进程默认镜像 env。
type Config struct {
	bootstrap.Config
	WorkerOwner          string // 对账 lease 持有者，默认 inference-service/<hostname>
	CoreAPIBaseURL       string // Core OpenAPI 基址，空则用 fake runtime
	CoreServiceToken     string // 访问 platform-workloads 的静态 service token（无 minter 时）
	ModelServiceGRPCAddr string // model-service 内部 gRPC，空则用 fake catalog
	AuthServiceGRPCAddr  string // 用于按租户 mint Core service JWT
	AuthMintSecret       string // mint 调用凭据
	MaxAttempts          int    // worker 对同一 operation 的最大尝试次数
	DeployTimeout        time.Duration
	RetryDelay           time.Duration
}

// Load 从环境变量组装配置。
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
		AuthServiceGRPCAddr:  env("AUTH_SERVICE_GRPC_ADDR", ""),
		AuthMintSecret:       env("AUTH_SERVICE_MINT_SECRET", ""),
		MaxAttempts:          envInt("INFERENCE_MAX_ATTEMPTS", 180),
		DeployTimeout:        envDurationSeconds("INFERENCE_DEPLOY_TIMEOUT_SECONDS", 15*time.Minute),
		RetryDelay:           envDurationSeconds("INFERENCE_RETRY_DELAY_SECONDS", 5*time.Second),
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

func envDurationSeconds(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return time.Duration(parsed) * time.Second
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
