package runtimeadmin

import (
	"encoding/json"
	"net/http"
)

type probeResponse struct {
	Status  string                         `json:"status"`
	Version string                         `json:"version,omitempty"`
	Checks  map[string]readinessCheckState `json:"checks,omitempty"`
}

func (runtime *Runtime) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/healthz", "/readyz", "/metrics":
	default:
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	switch request.URL.Path {
	case "/healthz":
		writeJSON(writer, http.StatusOK, probeResponse{Status: "ok", Version: runtime.identity.Version})
	case "/readyz":
		result := runtime.evaluateReadiness(request.Context())
		statusCode := http.StatusOK
		if result.Status == "error" {
			statusCode = http.StatusServiceUnavailable
		}
		writeJSON(writer, statusCode, probeResponse{
			Status:  result.Status,
			Version: runtime.identity.Version,
			Checks:  result.Checks,
		})
	case "/metrics":
		runtime.metricsHandler.ServeHTTP(writer, request)
	}
}

func writeJSON(writer http.ResponseWriter, statusCode int, body probeResponse) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(body)
}
