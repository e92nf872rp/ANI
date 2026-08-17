package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

const healthVersion = "services-bootstrap-v1"

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

func newProbeHandler(serviceName string, checks []probeCheck) http.Handler {
	_ = serviceName
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
		if result.Status == "fail" {
			statusCode = http.StatusServiceUnavailable
		}
		writeProbeJSON(w, statusCode, result)
	})
	return mux
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
			response.Status = "fail"
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
	}
}
