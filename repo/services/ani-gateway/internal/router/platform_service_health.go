package router

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/kubercloud/ani/pkg/ports"
)

type platformServiceHealthResponse struct {
	Scope        string                                   `json:"scope"`
	Coverage     string                                   `json:"coverage"`
	Signal       string                                   `json:"signal"`
	ObservedAt   string                                   `json:"observed_at"`
	SourceStatus string                                   `json:"source_status"`
	Components   []platformServiceHealthComponentResponse `json:"components"`
}

type platformServiceHealthComponentResponse struct {
	ServiceName       string   `json:"service_name"`
	ScrapeStatus      string   `json:"scrape_status"`
	ObservedReplicas  int      `json:"observed_replicas"`
	ReachableReplicas int      `json:"reachable_replicas"`
	Versions          []string `json:"versions"`
	SampleAgeSeconds  *float64 `json:"sample_age_seconds"`
}

func registerPlatformServiceHealth(v1 *route.RouterGroup, reader ports.PlatformServiceHealthReader) {
	v1.GET("/platform/services/health", func(ctx context.Context, requestContext *app.RequestContext) {
		if reader == nil {
			writeInstanceError(requestContext, http.StatusServiceUnavailable, "OBSERVABILITY_NOT_CONFIGURED", "platform service observability is not configured")
			return
		}
		result, err := reader.ReadPlatformServiceHealth(ctx)
		if err != nil || validatePlatformServiceHealth(result) != nil {
			writeInstanceError(requestContext, http.StatusServiceUnavailable, "OBSERVABILITY_UNAVAILABLE", "platform service observability is unavailable")
			return
		}
		requestContext.JSON(http.StatusOK, platformServiceHealthFromResult(result))
	})
}

func validatePlatformServiceHealth(result ports.PlatformServiceHealth) error {
	if result.Scope != ports.PlatformServiceHealthScope ||
		result.Coverage != ports.PlatformServiceHealthCoverage ||
		result.Signal != ports.PlatformServiceHealthSignal ||
		result.SourceStatus != "ok" || result.ObservedAt.IsZero() {
		return errors.New("invalid platform service health envelope")
	}
	expected := map[string]struct{}{
		"ani-gateway": {}, "auth-service": {}, "model-service": {},
		"task-service": {}, "inference-service": {}, "tenant-service": {},
		"metering-service": {},
	}
	if len(result.Components) != len(expected) {
		return fmt.Errorf("platform service health must contain %d components", len(expected))
	}
	seen := make(map[string]struct{}, len(expected))
	for _, component := range result.Components {
		if _, ok := expected[component.ServiceName]; !ok {
			return fmt.Errorf("unexpected platform service %q", component.ServiceName)
		}
		if _, duplicate := seen[component.ServiceName]; duplicate {
			return fmt.Errorf("duplicate platform service %q", component.ServiceName)
		}
		seen[component.ServiceName] = struct{}{}
		if component.ObservedReplicas < 0 || component.ReachableReplicas < 0 || component.ReachableReplicas > component.ObservedReplicas {
			return fmt.Errorf("invalid replica counts for %q", component.ServiceName)
		}
		switch component.ScrapeStatus {
		case ports.PlatformServiceScrapeReachable:
			if component.ReachableReplicas == 0 {
				return fmt.Errorf("reachable service %q has no reachable replica", component.ServiceName)
			}
		case ports.PlatformServiceScrapeUnreachable:
			if component.ObservedReplicas == 0 || component.ReachableReplicas != 0 {
				return fmt.Errorf("unreachable service %q has inconsistent replicas", component.ServiceName)
			}
		case ports.PlatformServiceScrapeUnknown:
			if component.ObservedReplicas != 0 || component.ReachableReplicas != 0 {
				return fmt.Errorf("unknown service %q has observed replicas", component.ServiceName)
			}
		default:
			return fmt.Errorf("invalid scrape status for %q", component.ServiceName)
		}
		if component.ObservedReplicas == 0 {
			if component.SampleAgeSeconds != nil {
				return fmt.Errorf("service %q has sample age without fresh targets", component.ServiceName)
			}
		} else if component.SampleAgeSeconds == nil || *component.SampleAgeSeconds < 0 || math.IsNaN(*component.SampleAgeSeconds) || math.IsInf(*component.SampleAgeSeconds, 0) {
			return fmt.Errorf("service %q has invalid sample age", component.ServiceName)
		}
		seenVersions := make(map[string]struct{}, len(component.Versions))
		previous := ""
		for index, version := range component.Versions {
			if strings.TrimSpace(version) == "" {
				return fmt.Errorf("service %q has an empty version", component.ServiceName)
			}
			if _, duplicate := seenVersions[version]; duplicate || (index > 0 && version < previous) {
				return fmt.Errorf("service %q versions must be sorted and unique", component.ServiceName)
			}
			seenVersions[version] = struct{}{}
			previous = version
		}
	}
	return nil
}

func platformServiceHealthFromResult(result ports.PlatformServiceHealth) platformServiceHealthResponse {
	response := platformServiceHealthResponse{
		Scope:        result.Scope,
		Coverage:     result.Coverage,
		Signal:       result.Signal,
		ObservedAt:   result.ObservedAt.UTC().Format(time.RFC3339Nano),
		SourceStatus: result.SourceStatus,
		Components:   make([]platformServiceHealthComponentResponse, 0, len(result.Components)),
	}
	for _, component := range result.Components {
		versions := append([]string(nil), component.Versions...)
		if versions == nil {
			versions = []string{}
		}
		response.Components = append(response.Components, platformServiceHealthComponentResponse{
			ServiceName:       component.ServiceName,
			ScrapeStatus:      component.ScrapeStatus,
			ObservedReplicas:  component.ObservedReplicas,
			ReachableReplicas: component.ReachableReplicas,
			Versions:          versions,
			SampleAgeSeconds:  component.SampleAgeSeconds,
		})
	}
	return response
}
