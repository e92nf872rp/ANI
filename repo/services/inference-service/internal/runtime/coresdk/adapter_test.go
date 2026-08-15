package coresdk

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/runtime"
)

func TestCreateBodyUsesSamePathForCPUAndGPU(t *testing.T) {
	serviceID := uuid.MustParse("05f6f46f-3db8-4551-8497-c46debb4be22")
	cpu := createBody(runtime.EnsureRequest{
		ServiceID: serviceID, ServedModelName: "tiny-cpu", IdempotencyKey: uuid.MustParse("1df72d71-9d49-46c4-a48a-52bb37b082ab"),
		Spec: domain.Spec{Replicas: 1, CPU: "4", Memory: "16Gi", ExecutionProfile: domain.ExecutionProfile{
			ImageRef:    "registry.ani.internal/platform/vllm-openai-cpu@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			ArtifactRef: "object://models/tiny",
		}},
	})
	gpu := createBody(runtime.EnsureRequest{
		ServiceID: serviceID, ServedModelName: "tiny-gpu", IdempotencyKey: uuid.MustParse("2df72d71-9d49-46c4-a48a-52bb37b082ab"),
		Spec: domain.Spec{Replicas: 1, CPU: "8", Memory: "32Gi", Accelerator: &domain.Accelerator{SpecID: "gpu-a100", CountPerReplica: 1},
			ExecutionProfile: domain.ExecutionProfile{
				ImageRef:    "registry.ani.internal/platform/vllm-openai-gpu@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
				ArtifactRef: "object://models/tiny",
			}},
	})
	if cpu["workload_class"] != gpu["workload_class"] || cpu["topology"].(map[string]any)["mode"] != "single_node" {
		t.Fatalf("cpu/gpu create path diverged: cpu=%#v gpu=%#v", cpu, gpu)
	}
	if _, ok := cpu["resources"].(map[string]any)["accelerator"]; ok {
		t.Fatalf("CPU body included accelerator: %#v", cpu["resources"])
	}
	accelerator, _ := gpu["resources"].(map[string]any)["accelerator"].(map[string]any)
	if accelerator["spec_id"] != "gpu-a100" || accelerator["count"] != 1 {
		t.Fatalf("GPU accelerator = %#v", accelerator)
	}
	cpuArgs, _ := cpu["args"].([]string)
	gpuArgs, _ := gpu["args"].([]string)
	if !containsArg(cpuArgs, "--dtype") || containsArg(gpuArgs, "--dtype") {
		t.Fatalf("dtype args cpu=%v gpu=%v", cpuArgs, gpuArgs)
	}
	cpuCommand, _ := cpu["command"].([]string)
	if len(cpuCommand) != 1 || cpuCommand[0] != "env" {
		t.Fatalf("CPU command = %#v", cpu["command"])
	}
}

func TestCreateBodyStripsArtifactPathFragment(t *testing.T) {
	body := createBody(runtime.EnsureRequest{
		ServiceID:       uuid.MustParse("05f6f46f-3db8-4551-8497-c46debb4be22"),
		ServedModelName: "tiny-cpu",
		IdempotencyKey:  uuid.MustParse("1df72d71-9d49-46c4-a48a-52bb37b082ab"),
		Spec: domain.Spec{Replicas: 1, CPU: "4", Memory: "16Gi", ExecutionProfile: domain.ExecutionProfile{
			ImageRef:    "registry.ani.internal/platform/vllm-openai-cpu@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			ArtifactRef: "pvc://vllm-model#/models/qwen",
		}},
	})
	artifacts, _ := body["artifacts"].([]map[string]any)
	if len(artifacts) != 1 || artifacts[0]["object_ref"] != "pvc://vllm-model" || artifacts[0]["mount_path"] != "/models" {
		t.Fatalf("artifacts = %#v", body["artifacts"])
	}
}

func TestProbeHealthAndSmokeUseRuntimeEndpoint(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(server.Close)

	if err := probeHealth(t.Context(), server.Client(), server.URL); err != nil {
		t.Fatalf("probeHealth() error = %v", err)
	}
	if err := probeSmoke(t.Context(), server.Client(), server.URL, "tiny"); err != nil {
		t.Fatalf("probeSmoke() error = %v", err)
	}
	if len(paths) != 2 || paths[0] != "GET /health" || paths[1] != "POST /v1/chat/completions" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestEngineURLRejectsNonHTTP(t *testing.T) {
	if _, err := engineURL("https://example.invalid/health", "/health"); err == nil {
		t.Fatal("https endpoint must be rejected")
	}
}

func TestParseClusterServiceAndKubeProxyURL(t *testing.T) {
	namespace, name, port, err := parseClusterService("http://svc-a.ani-tenant-11111111-1111-1111-1111-111111111111.svc:8000")
	if err != nil || name != "svc-a" || port != 8000 || !strings.HasPrefix(namespace, "ani-tenant-") {
		t.Fatalf("parse = %q %q %d %v", namespace, name, port, err)
	}
	t.Setenv("KUBERNETES_API_HOST", "https://kubernetes.example.invalid")
	target, err := kubeProxyURL("http://svc-a.ani-tenant-11111111-1111-1111-1111-111111111111.svc:8000", "/health")
	if err != nil || !strings.Contains(target, "/api/v1/namespaces/ani-tenant-11111111-1111-1111-1111-111111111111/services/svc-a:8000/proxy/health") {
		t.Fatalf("proxy url = %q %v", target, err)
	}
}

func containsArg(args []string, key string) bool {
	for _, item := range args {
		if item == key {
			return true
		}
	}
	return false
}

func TestAdmitRejectsUnadvertisedAccelerator(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/platform-workload-capabilities" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"supported_topology_modes":["single_node"],"leader_worker_set_ready":false,"gang_scheduling_ready":false,"accelerator_specs":[]}`))
	}))
	t.Cleanup(server.Close)
	rt := New(server.URL, "")
	spec := domain.Spec{Accelerator: &domain.Accelerator{SpecID: "gpu-a100", CountPerReplica: 1}}
	if err := rt.Admit(t.Context(), uuid.MustParse("11111111-1111-1111-1111-111111111111"), spec); !errors.Is(err, runtime.ErrRuntimeUnsupported) {
		t.Fatalf("Admit() error = %v", err)
	}
	if err := rt.Admit(t.Context(), uuid.MustParse("11111111-1111-1111-1111-111111111111"), domain.Spec{}); err != nil {
		t.Fatalf("CPU Admit() error = %v", err)
	}
}

func TestLogPageFromPayloadProjectsPublicFields(t *testing.T) {
	page := logPageFromPayload(map[string]any{
		"items": []any{
			map[string]any{
				"timestamp": "2026-08-15T01:02:03Z",
				"level":     "info",
				"message":   "runtime accepted",
				"container": "serve",
				"stream":    "stdout",
				"replica":   "pod-a",
			},
		},
		"next_cursor": "1",
	})
	if page.NextCursor != "1" || len(page.Items) != 1 {
		t.Fatalf("page = %+v", page)
	}
	want := runtime.LogEntry{
		Timestamp: time.Date(2026, 8, 15, 1, 2, 3, 0, time.UTC),
		Level:     "info", Message: "runtime accepted", Container: "serve", Stream: "stdout",
	}
	if page.Items[0] != want {
		t.Fatalf("item = %+v", page.Items[0])
	}
}
