package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const healthVersion = "v0.8.0"

type probeCheck struct {
	name string
	run  func(context.Context) error
}

type probeResponse struct {
	Status  string                    `json:"status"`
	Version string                    `json:"version,omitempty"`
	Checks  map[string]probeCheckBody `json:"checks,omitempty"`
}

type probeCheckBody struct {
	Status    string `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

func newProbeHandler(serviceName string, checks []probeCheck, metricsReaders ...ports.ReconcileControllerMetricsReader) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeProbeJSON(w, http.StatusOK, probeResponse{
			Status:  "ok",
			Version: healthVersion,
		})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		result := runProbeChecks(r.Context(), checks)
		statusCode := http.StatusOK
		if result.Status != "ok" {
			statusCode = http.StatusServiceUnavailable
		}
		writeProbeJSON(w, statusCode, result)
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		reader := firstReconcileMetricsReader(metricsReaders)
		writePrometheusMetrics(w, serviceName, reader)
	})
	return mux
}

func firstReconcileMetricsReader(readers []ports.ReconcileControllerMetricsReader) ports.ReconcileControllerMetricsReader {
	for _, reader := range readers {
		if reader != nil {
			return reader
		}
	}
	return nil
}

func writePrometheusMetrics(w http.ResponseWriter, serviceName string, reader ports.ReconcileControllerMetricsReader) {
	serviceName = sanitizePrometheusLabel(firstNonEmptyString(serviceName, "unknown"))
	metrics := ports.ReconcileControllerMetrics{}
	if reader != nil {
		metrics = reader.Metrics()
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	writeCounterMetric(w, "ani_workload_reconcile_ticks_total", "Total workload reconcile controller ticks.", serviceName, metrics.Ticks)
	writeCounterMetric(w, "ani_workload_reconcile_successes_total", "Total successful workload reconcile attempts.", serviceName, metrics.Successes)
	writeCounterMetric(w, "ani_workload_reconcile_failures_total", "Total failed workload reconcile attempts.", serviceName, metrics.Failures)
	writeCounterMetric(w, "ani_workload_reconcile_backoff_skips_total", "Total workload reconcile targets skipped due to failure backoff.", serviceName, metrics.SkippedBackoff)
}

func writeCounterMetric(w http.ResponseWriter, name string, help string, serviceName string, value int64) {
	_, _ = fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	_, _ = fmt.Fprintf(w, "# TYPE %s counter\n", name)
	_, _ = fmt.Fprintf(w, "%s{service=\"%s\"} %d\n", name, serviceName, value)
}

func sanitizePrometheusLabel(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(value)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func runProbeChecks(ctx context.Context, checks []probeCheck) probeResponse {
	if len(checks) == 0 {
		checks = []probeCheck{{name: "process", run: func(context.Context) error { return nil }}}
	}
	response := probeResponse{
		Status: "ok",
		Checks: make(map[string]probeCheckBody, len(checks)),
	}
	for _, check := range checks {
		started := time.Now()
		err := check.run(ctx)
		body := probeCheckBody{
			Status:    "ok",
			LatencyMS: time.Since(started).Milliseconds(),
		}
		if err != nil {
			response.Status = "degraded"
			body.Status = "fail"
			body.Error = err.Error()
		}
		response.Checks[check.name] = body
	}
	return response
}

func writeProbeJSON(w http.ResponseWriter, statusCode int, body probeResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(body)
}

func dependencyProbeChecks(deps *Deps) []probeCheck {
	return []probeCheck{
		{
			name: "postgres",
			run: func(ctx context.Context) error {
				if deps == nil || deps.DB == nil {
					return errors.New("postgres dependency is not configured")
				}
				return deps.DB.Ping(ctx)
			},
		},
		{
			name: "nats",
			run: func(context.Context) error {
				if deps == nil || deps.NATS == nil {
					return errors.New("nats dependency is not configured")
				}
				if !deps.NATS.IsConnected() {
					return errors.New("nats is not connected")
				}
				return nil
			},
		},
		{
			name: "redis",
			run: func(ctx context.Context) error {
				if deps == nil || deps.Redis == nil {
					return errors.New("redis dependency is not configured")
				}
				return deps.Redis.Ping(ctx).Err()
			},
		},
	}
}
