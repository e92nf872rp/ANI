package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestPlatformServiceHealthRouteReturnsFixedPartialCoverage(t *testing.T) {
	observedAt := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	reader := fakePlatformServiceHealthReader{result: validPlatformServiceHealth(observedAt)}
	h := server.Default()
	registerPlatformServiceHealth(h.Group("/api/v1"), reader)
	response := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/platform/services/health", nil).Result()
	if response.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.StatusCode(), response.Body())
	}
	var body platformServiceHealthResponse
	if err := json.Unmarshal(response.Body(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Scope != "ani_services" || body.Coverage != "partial" || body.Signal != "prometheus_scrape" || body.ObservedAt != observedAt.Format(time.RFC3339) {
		t.Fatalf("response envelope = %+v", body)
	}
	if len(body.Components) != 7 || body.Components[0].ServiceName != "ani-gateway" {
		t.Fatalf("components = %+v", body.Components)
	}
}

func TestPlatformServiceHealthRouteFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		reader ports.PlatformServiceHealthReader
		code   string
	}{
		{name: "disabled", reader: nil, code: "OBSERVABILITY_NOT_CONFIGURED"},
		{name: "source failure", reader: fakePlatformServiceHealthReader{err: errors.New("http://prometheus.internal?token=secret")}, code: "OBSERVABILITY_UNAVAILABLE"},
		{name: "invalid partial result", reader: fakePlatformServiceHealthReader{result: ports.PlatformServiceHealth{Scope: ports.PlatformServiceHealthScope}}, code: "OBSERVABILITY_UNAVAILABLE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := server.Default()
			registerPlatformServiceHealth(h.Group("/api/v1"), test.reader)
			response := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/platform/services/health", nil).Result()
			if response.StatusCode() != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", response.StatusCode())
			}
			if !strings.Contains(string(response.Body()), `"code":"`+test.code+`"`) {
				t.Fatalf("response missing code %q: %s", test.code, response.Body())
			}
			if strings.Contains(string(response.Body()), "prometheus.internal") || strings.Contains(string(response.Body()), "secret") {
				t.Fatalf("response leaked source error: %s", response.Body())
			}
		})
	}
}

func validPlatformServiceHealth(observedAt time.Time) ports.PlatformServiceHealth {
	age := 4.0
	names := []string{
		"ani-gateway",
		"auth-service",
		"model-service",
		"task-service",
		"inference-service",
		"tenant-service",
		"metering-service",
	}
	components := make([]ports.PlatformServiceHealthComponent, 0, len(names))
	for index, name := range names {
		component := ports.PlatformServiceHealthComponent{
			ServiceName:  name,
			ScrapeStatus: ports.PlatformServiceScrapeUnknown,
			Versions:     []string{},
		}
		if index == 0 {
			component.ScrapeStatus = ports.PlatformServiceScrapeReachable
			component.ObservedReplicas = 1
			component.ReachableReplicas = 1
			component.Versions = []string{"v0.9.0"}
			component.SampleAgeSeconds = &age
		}
		components = append(components, component)
	}
	return ports.PlatformServiceHealth{
		Scope:        ports.PlatformServiceHealthScope,
		Coverage:     ports.PlatformServiceHealthCoverage,
		Signal:       ports.PlatformServiceHealthSignal,
		ObservedAt:   observedAt,
		SourceStatus: "ok",
		Components:   components,
	}
}

type fakePlatformServiceHealthReader struct {
	result ports.PlatformServiceHealth
	err    error
}

func (reader fakePlatformServiceHealthReader) ReadPlatformServiceHealth(context.Context) (ports.PlatformServiceHealth, error) {
	return reader.result, reader.err
}
