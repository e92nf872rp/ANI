package bootstrap

import (
	"fmt"
	"log/slog"

	adapterresilience "github.com/kubercloud/ani/pkg/adapters/resilience"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/runtimeadmin"
	"github.com/prometheus/client_golang/prometheus"
)

const runtimeServiceNamespace = "ani"

func newRuntimeAdminForDeps(deps *Deps) (*runtimeadmin.Runtime, error) {
	if deps == nil {
		return nil, fmt.Errorf("bootstrap dependencies are required")
	}
	return newRuntimeAdmin(
		deps.ServiceName,
		dependencyProbeChecks(deps),
		reconcileControllerMetricsReader(deps),
		deps.Logger,
	)
}

func newRuntimeAdmin(
	serviceName string,
	checks []probeCheck,
	metricsReader ports.ReconcileControllerMetricsReader,
	logger *slog.Logger,
) (*runtimeadmin.Runtime, error) {
	runtimeChecks := make([]runtimeadmin.Check, 0, len(checks))
	for _, check := range checks {
		var criticality runtimeadmin.Criticality
		switch check.mode {
		case adapterresilience.DependencyStrong:
			criticality = runtimeadmin.Strong
		case adapterresilience.DependencyWeak:
			criticality = runtimeadmin.Weak
		default:
			return nil, fmt.Errorf("check %q requires explicit criticality", check.name)
		}
		runtimeChecks = append(runtimeChecks, runtimeadmin.Check{
			Name:        check.name,
			Criticality: criticality,
			Probe:       check.run,
		})
	}

	return runtimeadmin.New(runtimeadmin.Config{
		Identity: runtimeadmin.Identity{
			Namespace: runtimeServiceNamespace,
			Name:      serviceName,
		},
		Checks:     runtimeChecks,
		Collectors: []prometheus.Collector{newReconcileMetricsCollector(serviceName, metricsReader)},
		Logger:     logger,
	})
}

type reconcileMetricsCollector struct {
	reader         ports.ReconcileControllerMetricsReader
	service        string
	ticks          *prometheus.Desc
	successes      *prometheus.Desc
	failures       *prometheus.Desc
	backoffSkipped *prometheus.Desc
}

func newReconcileMetricsCollector(service string, reader ports.ReconcileControllerMetricsReader) prometheus.Collector {
	labels := prometheus.Labels{"service": firstNonEmptyString(service, "unknown")}
	return &reconcileMetricsCollector{
		reader:  reader,
		service: labels["service"],
		ticks: prometheus.NewDesc(
			"ani_workload_reconcile_ticks_total",
			"Total workload reconcile controller ticks.",
			nil,
			labels,
		),
		successes: prometheus.NewDesc(
			"ani_workload_reconcile_successes_total",
			"Total successful workload reconcile attempts.",
			nil,
			labels,
		),
		failures: prometheus.NewDesc(
			"ani_workload_reconcile_failures_total",
			"Total failed workload reconcile attempts.",
			nil,
			labels,
		),
		backoffSkipped: prometheus.NewDesc(
			"ani_workload_reconcile_backoff_skips_total",
			"Total workload reconcile targets skipped due to failure backoff.",
			nil,
			labels,
		),
	}
}

func (collector *reconcileMetricsCollector) Describe(descriptions chan<- *prometheus.Desc) {
	descriptions <- collector.ticks
	descriptions <- collector.successes
	descriptions <- collector.failures
	descriptions <- collector.backoffSkipped
}

func (collector *reconcileMetricsCollector) Collect(metrics chan<- prometheus.Metric) {
	values := ports.ReconcileControllerMetrics{}
	if collector.reader != nil {
		values = collector.reader.Metrics()
	}
	metrics <- prometheus.MustNewConstMetric(collector.ticks, prometheus.CounterValue, float64(values.Ticks))
	metrics <- prometheus.MustNewConstMetric(collector.successes, prometheus.CounterValue, float64(values.Successes))
	metrics <- prometheus.MustNewConstMetric(collector.failures, prometheus.CounterValue, float64(values.Failures))
	metrics <- prometheus.MustNewConstMetric(collector.backoffSkipped, prometheus.CounterValue, float64(values.SkippedBackoff))
}
