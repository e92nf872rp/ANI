package config

import (
	"os"
	"strconv"

	"github.com/kubercloud/ani/pkg/bootstrap"
)

type Config struct {
	bootstrap.Config
	JWTPublicKeyPEM      string
	JWTPublicKeyFile     string
	JWTPrivateKeyPEM     string
	JWTPrivateKeyFile    string
	JWTIssuer            string
	OIDCIssuerURL        string
	OIDCClientID         string
	OIDCClientSecret     string
	OIDCAuthURL          string
	OIDCTokenURL         string
	OIDCJWKSURL          string
	OIDCPublicKeyPEM     string
	OIDCPublicKeyFile    string
	OIDCGroupRoleMapJSON string
}

func Load() Config {
	return Config{
		Config: bootstrap.Config{
			DatabaseURL: env("DATABASE_URL", "postgres://ani_app_user:ani_dev_password@127.0.0.1:5432/ani?sslmode=disable"),
			NATSURL:     env("NATS_URL", "nats://127.0.0.1:4222"),
			RedisURL:    env("REDIS_URL", "redis://:ani_dev_password@127.0.0.1:6379/0"),
			GRPCPort:    envInt("GRPC_PORT", 9101),
			HealthPort:  envInt("HEALTH_PORT", 9201),
			ServiceName: "auth-service",
		},
		JWTPublicKeyPEM:      env("AUTH_JWT_PUBLIC_KEY_PEM", ""),
		JWTPublicKeyFile:     env("AUTH_JWT_PUBLIC_KEY_FILE", ""),
		JWTPrivateKeyPEM:     env("AUTH_JWT_PRIVATE_KEY_PEM", ""),
		JWTPrivateKeyFile:    env("AUTH_JWT_PRIVATE_KEY_FILE", ""),
		JWTIssuer:            env("AUTH_JWT_ISSUER", ""),
		OIDCIssuerURL:        env("AUTH_OIDC_ISSUER_URL", ""),
		OIDCClientID:         env("AUTH_OIDC_CLIENT_ID", ""),
		OIDCClientSecret:     env("AUTH_OIDC_CLIENT_SECRET", ""),
		OIDCAuthURL:          env("AUTH_OIDC_AUTH_URL", ""),
		OIDCTokenURL:         env("AUTH_OIDC_TOKEN_URL", ""),
		OIDCJWKSURL:          env("AUTH_OIDC_JWKS_URL", ""),
		OIDCPublicKeyPEM:     env("AUTH_OIDC_PUBLIC_KEY_PEM", ""),
		OIDCPublicKeyFile:    env("AUTH_OIDC_PUBLIC_KEY_FILE", ""),
		OIDCGroupRoleMapJSON: env("AUTH_OIDC_GROUP_ROLE_MAP_JSON", ""),
	}
}

// 从环境变量中获取配置项
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// 从环境变量中获取配置项（整数类型）
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
