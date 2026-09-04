package router

import (
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestLivenessResponse(t *testing.T) {
	response := livenessResponse()
	if response.Status != "ok" || response.Version == "" {
		t.Fatalf("liveness response = %+v, want ok with version", response)
	}
}

func TestReadinessResponse(t *testing.T) {
	response := readinessResponse()
	if response.Status != "ok" {
		t.Fatalf("readiness status = %q, want ok", response.Status)
	}
	if response.Checks["process"].Status != "ok" {
		t.Fatalf("process check = %+v, want ok", response.Checks["process"])
	}
}

func TestPublicHealthRouterDoesNotExposeMetrics(t *testing.T) {
	h := server.Default()
	registerHealth(h.Group(""))
	response := ut.PerformRequest(h.Engine, http.MethodGet, "/metrics", nil).Result()
	if response.StatusCode() != http.StatusNotFound {
		t.Fatalf("GET /metrics status = %d, want 404", response.StatusCode())
	}
}
