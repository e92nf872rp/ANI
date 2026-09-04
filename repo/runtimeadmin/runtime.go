package runtimeadmin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

const (
	defaultCheckTimeout   = time.Second
	defaultOverallTimeout = 3 * time.Second
	servingCheckName      = "service-serving"
)

var checkNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

type Identity struct {
	Namespace  string
	Name       string
	Version    string
	InstanceID string
}

type Criticality string

const (
	Strong Criticality = "strong"
	Weak   Criticality = "weak"
)

type Check struct {
	Name        string
	Criticality Criticality
	Probe       func(context.Context) error
}

type Config struct {
	Identity       Identity
	Checks         []Check
	Collectors     []prometheus.Collector
	CheckTimeout   time.Duration
	OverallTimeout time.Duration
	Logger         *slog.Logger
}

type Summary struct {
	Status     string
	Version    string
	ObservedAt time.Time
}

type Runtime struct {
	identity       Identity
	checks         []Check
	checkTimeout   time.Duration
	overallTimeout time.Duration
	logger         *slog.Logger
	meterProvider  *sdkmetric.MeterProvider
	metricsHandler http.Handler
	serving        atomic.Bool
	summaryMu      sync.RWMutex
	summary        Summary
	shutdownOnce   sync.Once
	shutdownErr    error
}

func New(config Config) (*Runtime, error) {
	identity, err := resolveIdentity(config.Identity)
	if err != nil {
		return nil, err
	}
	checks, err := validateChecks(config.Checks)
	if err != nil {
		return nil, err
	}
	if config.CheckTimeout < 0 {
		return nil, errors.New("check timeout must not be negative")
	}
	if config.OverallTimeout < 0 {
		return nil, errors.New("overall timeout must not be negative")
	}
	if config.CheckTimeout == 0 {
		config.CheckTimeout = defaultCheckTimeout
	}
	if config.OverallTimeout == 0 {
		config.OverallTimeout = defaultOverallTimeout
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	meterProvider, handler, err := newMetrics(identity, config.Collectors)
	if err != nil {
		return nil, err
	}

	runtime := &Runtime{
		identity:       identity,
		checks:         checks,
		checkTimeout:   config.CheckTimeout,
		overallTimeout: config.OverallTimeout,
		logger:         config.Logger,
		meterProvider:  meterProvider,
		metricsHandler: handler,
		summary: Summary{
			Status:     "error",
			Version:    identity.Version,
			ObservedAt: time.Now().UTC(),
		},
	}
	return runtime, nil
}

func (runtime *Runtime) MeterProvider() metric.MeterProvider {
	return runtime.meterProvider
}

func (runtime *Runtime) SetServing(serving bool) {
	runtime.serving.Store(serving)
	if !serving {
		runtime.updateSummary("error", time.Now().UTC())
	}
}

func (runtime *Runtime) Summary() Summary {
	runtime.summaryMu.RLock()
	defer runtime.summaryMu.RUnlock()
	return runtime.summary
}

func (runtime *Runtime) Shutdown(ctx context.Context) error {
	runtime.SetServing(false)
	runtime.shutdownOnce.Do(func() {
		runtime.shutdownErr = runtime.meterProvider.Shutdown(ctx)
	})
	return runtime.shutdownErr
}

func (runtime *Runtime) updateSummary(status string, observedAt time.Time) {
	runtime.summaryMu.Lock()
	runtime.summary = Summary{Status: status, Version: runtime.identity.Version, ObservedAt: observedAt}
	runtime.summaryMu.Unlock()
}

func resolveIdentity(identity Identity) (Identity, error) {
	identity.Namespace = strings.TrimSpace(identity.Namespace)
	identity.Name = strings.TrimSpace(identity.Name)
	identity.Version = normalizeVersion(identity.Version)
	identity.InstanceID = strings.TrimSpace(identity.InstanceID)
	if identity.Namespace == "" {
		return Identity{}, errors.New("service namespace is required")
	}
	if identity.Name == "" {
		return Identity{}, errors.New("service name is required")
	}

	environmentName := strings.TrimSpace(os.Getenv("ANI_SERVICE_NAME"))
	if environmentName != "" && environmentName != identity.Name {
		return Identity{}, fmt.Errorf("ANI_SERVICE_NAME %q does not match canonical service name %q", environmentName, identity.Name)
	}
	environmentVersion := normalizeVersion(os.Getenv("ANI_SERVICE_VERSION"))
	if environmentVersion != "" {
		if identity.Version != "" && identity.Version != environmentVersion {
			return Identity{}, fmt.Errorf("ANI_SERVICE_VERSION %q does not match configured service version %q", environmentVersion, identity.Version)
		}
		identity.Version = environmentVersion
	}

	if strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST")) != "" {
		podUID := strings.TrimSpace(os.Getenv("POD_UID"))
		if podUID == "" {
			return Identity{}, errors.New("POD_UID is required in Kubernetes")
		}
		if identity.InstanceID != "" && identity.InstanceID != podUID {
			return Identity{}, errors.New("configured service instance ID does not match POD_UID")
		}
		identity.InstanceID = podUID
	} else if identity.InstanceID == "" {
		startupID, err := newStartupID()
		if err != nil {
			return Identity{}, fmt.Errorf("generate startup instance ID: %w", err)
		}
		identity.InstanceID = startupID
	}
	return identity, nil
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	switch strings.ToLower(version) {
	case "", "(devel)", "devel", "unknown":
		return ""
	default:
		return version
	}
}

func newStartupID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded), nil
}

func validateChecks(configured []Check) ([]Check, error) {
	seen := map[string]struct{}{servingCheckName: {}}
	checks := make([]Check, len(configured))
	copy(checks, configured)
	for index := range checks {
		check := &checks[index]
		check.Name = strings.TrimSpace(check.Name)
		if !checkNamePattern.MatchString(check.Name) {
			return nil, fmt.Errorf("check name %q must be a low-cardinality constant", check.Name)
		}
		if _, exists := seen[check.Name]; exists {
			return nil, fmt.Errorf("duplicate or reserved check name %q", check.Name)
		}
		seen[check.Name] = struct{}{}
		if check.Criticality != Strong && check.Criticality != Weak {
			return nil, fmt.Errorf("check %q has invalid criticality %q", check.Name, check.Criticality)
		}
		if check.Probe == nil {
			return nil, fmt.Errorf("check %q has nil probe", check.Name)
		}
	}
	return checks, nil
}
