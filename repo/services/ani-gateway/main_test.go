package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGatewayRedisConfigFromEnvParsesSentinel(t *testing.T) {
	t.Setenv("GATEWAY_REDIS_MODE", "sentinel")
	t.Setenv("GATEWAY_REDIS_ADDRS", "redis-sentinel-a:26379, redis-sentinel-b:26379")
	t.Setenv("GATEWAY_REDIS_MASTER_NAME", "ani-redis")
	t.Setenv("GATEWAY_REDIS_USERNAME", "ani")
	t.Setenv("GATEWAY_REDIS_PASSWORD", "secret")
	t.Setenv("GATEWAY_REDIS_DB", "2")

	cfg := gatewayRedisConfigFromEnv()
	if cfg.Mode != "sentinel" || cfg.MasterName != "ani-redis" {
		t.Fatalf("redis mode/master = %q/%q, want sentinel/ani-redis", cfg.Mode, cfg.MasterName)
	}
	if len(cfg.Addrs) != 2 || cfg.Addrs[0] != "redis-sentinel-a:26379" || cfg.Addrs[1] != "redis-sentinel-b:26379" {
		t.Fatalf("redis addrs = %#v, want parsed sentinel addrs", cfg.Addrs)
	}
	if cfg.Username != "ani" || cfg.Password != "secret" || cfg.DB != 2 {
		t.Fatalf("redis auth/db = %q/%q/%d, want ani/secret/2", cfg.Username, cfg.Password, cfg.DB)
	}
}

func TestGatewayPlatformServiceHealthRuntimeConfig(t *testing.T) {
	t.Setenv("PLATFORM_SERVICE_HEALTH_ENABLED", "")
	t.Setenv("PLATFORM_SERVICE_HEALTH_PROMETHEUS_URL", "")
	t.Setenv("PLATFORM_SERVICE_HEALTH_QUERY_TIMEOUT", "")
	config, err := gatewayPlatformServiceHealthRuntimeConfigFromEnv()
	if err != nil || config.Enabled {
		t.Fatalf("disabled config = %+v, err = %v", config, err)
	}

	t.Setenv("PLATFORM_SERVICE_HEALTH_ENABLED", "true")
	if _, err := gatewayPlatformServiceHealthRuntimeConfigFromEnv(); err == nil {
		t.Fatal("enabled config accepted missing Prometheus URL")
	}
	t.Setenv("PLATFORM_SERVICE_HEALTH_PROMETHEUS_URL", "http://prometheus.monitoring.svc:9090")
	t.Setenv("PLATFORM_SERVICE_HEALTH_QUERY_TIMEOUT", "4s")
	config, err = gatewayPlatformServiceHealthRuntimeConfigFromEnv()
	if err != nil {
		t.Fatalf("enabled config: %v", err)
	}
	if !config.Enabled || config.PrometheusURL != "http://prometheus.monitoring.svc:9090" || config.QueryTimeout != 4*time.Second {
		t.Fatalf("enabled config = %+v", config)
	}

	t.Setenv("PLATFORM_SERVICE_HEALTH_QUERY_TIMEOUT", "6s")
	if _, err := gatewayPlatformServiceHealthRuntimeConfigFromEnv(); err == nil {
		t.Fatal("config accepted timeout above 5s")
	}
}

func TestGatewayHealthPort(t *testing.T) {
	t.Setenv("HEALTH_PORT", "")
	if port, err := gatewayHealthPort(); err != nil || port != 9200 {
		t.Fatalf("default port = %d, err = %v; want 9200", port, err)
	}
	t.Setenv("HEALTH_PORT", "9300")
	if port, err := gatewayHealthPort(); err != nil || port != 9300 {
		t.Fatalf("configured port = %d, err = %v; want 9300", port, err)
	}
	for _, value := range []string{"invalid", "0", "65536"} {
		t.Setenv("HEALTH_PORT", value)
		if _, err := gatewayHealthPort(); err == nil {
			t.Fatalf("HEALTH_PORT=%q accepted, want error", value)
		}
	}
}

func TestGatewayRuntimeAdminPrivateContract(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("ANI_SERVICE_NAME", "")
	runtime, err := newGatewayRuntimeAdmin(nil)
	if err != nil {
		t.Fatalf("new runtime admin: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })

	ready := httptest.NewRecorder()
	runtime.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-serving status = %d, want 503", ready.Code)
	}
	runtime.SetServing(true)

	metrics := httptest.NewRecorder()
	runtime.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metrics.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", metrics.Code)
	}
	for _, want := range []string{`target_info{service_instance_id=`, `service_name="ani-gateway"`, `service_namespace="ani"`} {
		if !strings.Contains(metrics.Body.String(), want) {
			t.Fatalf("metrics missing %q:\n%s", want, metrics.Body.String())
		}
	}
}

func TestWaitForGatewayPublicListenerUsesPublicHealthContract(t *testing.T) {
	public := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" {
			http.NotFound(response, request)
			return
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer public.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForGatewayPublicListener(
		ctx,
		strings.TrimPrefix(public.URL, "http://"),
		make(chan error),
	); err != nil {
		t.Fatalf("wait for public listener: %v", err)
	}

	serverErrors := make(chan error, 1)
	serverErrors <- errors.New("bind failed")
	if err := waitForGatewayPublicListener(ctx, "127.0.0.1:1", serverErrors); err == nil || !strings.Contains(err.Error(), "bind failed") {
		t.Fatalf("server error = %v, want bind failure", err)
	}
}
