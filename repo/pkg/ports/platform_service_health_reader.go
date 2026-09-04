package ports

import (
	"context"
	"time"
)

const (
	PlatformServiceHealthScope    = "ani_services"
	PlatformServiceHealthCoverage = "partial"
	PlatformServiceHealthSignal   = "prometheus_scrape"

	PlatformServiceScrapeReachable   = "reachable"
	PlatformServiceScrapeUnreachable = "unreachable"
	PlatformServiceScrapeUnknown     = "unknown"
)

type PlatformServiceHealth struct {
	Scope        string
	Coverage     string
	Signal       string
	ObservedAt   time.Time
	SourceStatus string
	Components   []PlatformServiceHealthComponent
}

type PlatformServiceHealthComponent struct {
	ServiceName       string
	ScrapeStatus      string
	ObservedReplicas  int
	ReachableReplicas int
	Versions          []string
	SampleAgeSeconds  *float64
}

type PlatformServiceHealthReader interface {
	ReadPlatformServiceHealth(context.Context) (PlatformServiceHealth, error)
}
