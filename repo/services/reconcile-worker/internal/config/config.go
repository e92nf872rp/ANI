package config

import (
	"os"
	"strconv"

	"github.com/kubercloud/ani/pkg/bootstrap"
)

func Load() bootstrap.Config {
	return bootstrap.Config{
		DatabaseURL: env("DATABASE_URL", "postgres://ani_app_user:ani_dev_password@127.0.0.1:5432/ani?sslmode=disable"),
		NATSURL:     env("NATS_URL", "nats://127.0.0.1:4222"),
		RedisURL:    env("REDIS_URL", "redis://:ani_dev_password@127.0.0.1:6379/0"),
		HealthPort:  envInt("HEALTH_PORT", 9205),
		ServiceName: "reconcile-worker",

		WorkloadReconcileControllerEnabled: true,
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

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
