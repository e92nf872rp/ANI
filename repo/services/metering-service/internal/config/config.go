package config

import (
	"os"
	"strconv"

	"github.com/kubercloud/ani/pkg/bootstrap"
)

// Config 是 metering-service 的配置，嵌入 bootstrap.Config 获得公共字段。
type Config struct {
	bootstrap.Config
	PrometheusURL             string
	CollectionIntervalSeconds int
}

// Load 从环境变量加载配置，填充 bootstrap.Config 公共字段和 metering 专有字段。
func Load() Config {
	return Config{
		Config: bootstrap.Config{
			DatabaseURL: env("DATABASE_URL", "postgres://ani_app_user:ani_dev_password@127.0.0.1:5432/ani?sslmode=disable"),
			NATSURL:     env("NATS_URL", "nats://127.0.0.1:4222"),
			RedisURL:    env("REDIS_URL", "redis://127.0.0.1:6379"),
			GRPCPort:    envInt("GRPC_PORT", 9104),
			HealthPort:  envInt("HEALTH_PORT", 9210),
			ServiceName: "metering-service",
		},
		PrometheusURL:             env("METERING_PROMETHEUS_URL", "http://127.0.0.1:9090"),
		CollectionIntervalSeconds: envInt("METERING_COLLECTION_INTERVAL_SECONDS", 60),
	}
}

// env 从环境变量中获取字符串配置项，缺失时返回 fallback。
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envInt 从环境变量中获取整数配置项，缺失或解析失败时返回 fallback。
func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
