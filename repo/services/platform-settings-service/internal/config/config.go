package config

import (
	"os"
	"strconv"

	"github.com/kubercloud/ani/services/pkg/bootstrap"
)

// Load reads platform-settings-service configuration from environment variables.
func Load() bootstrap.Config {
	return bootstrap.Config{
		DatabaseURL: env("DATABASE_URL", "postgres://ani_app_user:ani_dev_password@127.0.0.1:5432/ani?sslmode=disable"),
		GRPCPort:    envInt("GRPC_PORT", 9106),
		HealthPort:  envInt("HEALTH_PORT", 9206),
		ServiceName: "platform-settings-service",
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
