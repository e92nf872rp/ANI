package coresdk

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	anisdk "github.com/kubercloud/ani-sdks/core-go/anisdk"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/runtime"
)

func TestCreateBodyUsesSamePathForCPUAndGPU(t *testing.T) {
	serviceID := uuid.MustParse("05f6f46f-3db8-4551-8497-c46debb4be22")
	single := runtime.TopologyPlan{Mode: "single_node", ProfileID: "container-single-node", ProfileVersion: "v1"}
	cpu := createBody(runtime.EnsureRequest{
		ServiceID: serviceID, ServedModelName: "tiny-cpu", IdempotencyKey: uuid.MustParse("1df72d71-9d49-46c4-a48a-52bb37b082ab"),
		Spec: domain.Spec{Replicas: 1, CPU: "4", Memory: "16Gi", ExecutionProfile: domain.ExecutionProfile{
			ImageRef:    "registry.ani.internal/platform/vllm-openai-cpu@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			ArtifactRef: "object://models/tiny",
		}},
	}, single)
	gpu := createBody(runtime.EnsureRequest{
		ServiceID: serviceID, ServedModelName: "tiny-gpu", IdempotencyKey: uuid.MustParse("2df72d71-9d49-46c4-a48a-52bb37b082ab"),
		Spec: domain.Spec{Replicas: 1, CPU: "8", Memory: "32Gi", Accelerator: &domain.Accelerator{SpecID: "gpu-a100", CountPerReplica: 1},
			ExecutionProfile: domain.ExecutionProfile{
				ImageRef:    "registry.ani.internal/platform/vllm-openai-gpu@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
				ArtifactRef: "object://models/tiny",
			}},
	}, single)
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
	if !containsArg(gpuArgs, "--enforce-eager") {
		t.Fatalf("GPU args missing --enforce-eager: %v", gpuArgs)
	}
	cpuCommand, _ := cpu["command"].([]string)
	if len(cpuCommand) != 1 || cpuCommand[0] != "env" {
		t.Fatalf("CPU command = %#v", cpu["command"])
	}
}

func TestCreateBodyUsesFrozenTenantCommandAndEnv(t *testing.T) {
	body := createBody(runtime.EnsureRequest{
		ServiceID:       uuid.MustParse("05f6f46f-3db8-4551-8497-c46debb4be22"),
		ServedModelName: "tiny-gpu",
		IdempotencyKey:  uuid.MustParse("1df72d71-9d49-46c4-a48a-52bb37b082ab"),
		Spec: domain.Spec{
			Replicas: 1, CPU: "8", Memory: "32Gi",
			Accelerator: &domain.Accelerator{SpecID: "gpu-nvidia-geforce-rtx-4090-full", CountPerReplica: 1},
			Engine: &domain.Engine{
				Env:     []domain.EngineEnvVar{{Name: "VLLM_LOGGING_LEVEL", Value: "DEBUG"}},
				Command: []string{"python3", "-m", "vllm.entrypoints.openai.api_server", "--model", "/models/qwen"},
			},
			ExecutionProfile: domain.ExecutionProfile{
				ImageRef:    "registry.ani.internal/platform/vllm-openai-gpu@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
				ArtifactRef: "pvc://vllm-model#/models/qwen",
			},
		},
	}, runtime.TopologyPlan{Mode: "single_node", ProfileID: "container-single-node", ProfileVersion: "v1"})
	command, _ := body["command"].([]string)
	if strings.Join(command, " ") != "python3 -m vllm.entrypoints.openai.api_server --model /models/qwen" {
		t.Fatalf("command = %#v", body["command"])
	}
	args, _ := body["args"].([]string)
	if len(args) != 0 {
		t.Fatalf("args = %#v, want empty", args)
	}
	env, _ := body["env"].([]map[string]string)
	if len(env) != 1 || env[0]["name"] != "VLLM_LOGGING_LEVEL" || env[0]["value"] != "DEBUG" {
		t.Fatalf("env = %#v", body["env"])
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
	}, runtime.TopologyPlan{Mode: "single_node", ProfileID: "container-single-node", ProfileVersion: "v1"})
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

func TestCreateBodyLeaderWorkerUsesInternalRoles(t *testing.T) {
	body := createBody(runtime.EnsureRequest{
		ServiceID:       uuid.MustParse("05f6f46f-3db8-4551-8497-c46debb4be22"),
		ServedModelName: "tiny-gpu",
		IdempotencyKey:  uuid.MustParse("2df72d71-9d49-46c4-a48a-52bb37b082ab"),
		Spec: domain.Spec{Replicas: 1, CPU: "8", Memory: "32Gi", PlacementMode: "multi_node",
			Accelerator: &domain.Accelerator{SpecID: "gpu-a100-full", CountPerReplica: 2},
			ExecutionProfile: domain.ExecutionProfile{
				ImageRef: "registry.ani.internal/platform/vllm-openai-gpu@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			}},
	}, runtime.TopologyPlan{
		Mode: "leader_worker", ProfileID: "container-leader-worker", ProfileVersion: "v1",
		Gang: true, LeaderCount: 1, WorkerCount: 1, LeaderGPUs: 1, WorkerGPUs: 1,
	})
	topology, _ := body["topology"].(map[string]any)
	if topology["mode"] != "leader_worker" || body["scheduling"].(map[string]any)["gang"] != true {
		t.Fatalf("lws body = %#v", body)
	}
	if _, ok := topology["leader"]; !ok || topology["workers"] == nil {
		t.Fatalf("missing roles: %#v", topology)
	}
	command, _ := body["command"].([]string)
	args, _ := body["args"].([]string)
	joined := strings.Join(args, " ")
	if len(command) != 2 || command[0] != "sh" || !strings.Contains(joined, "ray start --head") {
		t.Fatalf("leader launch = %#v %#v", command, args)
	}
	if !strings.Contains(joined, "--num-gpus=1") || !strings.Contains(joined, "RAY_EXPERIMENTAL_NOSET_CUDA_VISIBLE_DEVICES=1") || !strings.Contains(joined, "sitecustomize.py") {
		t.Fatalf("leader launch missing Ray GPU env: %q", joined)
	}
	if !strings.Contains(joined, "VLLM_USE_RAY_COMPILED_DAG=0") {
		t.Fatalf("leader launch missing compiled DAG disable: %q", joined)
	}
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

func TestMapCoreErrorClassifiesPermanentCodes(t *testing.T) {
	cases := []struct {
		code string
		want error
	}{
		{"PRECONDITION_FAILED", runtime.ErrRuntimeUnsupported},
		{"UNSUPPORTED_TOPOLOGY", runtime.ErrUnsupportedTopology},
		{"INSUFFICIENT_CAPACITY", runtime.ErrInsufficientCapacity},
		{"ACCELERATOR_SPEC_UNAVAILABLE", runtime.ErrRuntimeUnsupported},
		{"IMAGE_UNAVAILABLE", runtime.ErrImageUnavailable},
		{"IMAGE_NOT_FOUND", runtime.ErrImageUnavailable},
		{"ENGINE_PROFILE_UNAPPROVED", runtime.ErrEngineProfileUnapproved},
		{"RESERVED_FIELD_CONFLICT", runtime.ErrReservedFieldConflict},
		{"NOT_FOUND", runtime.ErrRuntimeNotFound},
	}
	for _, tc := range cases {
		if err := mapCoreError(anisdk.APIError{Code: tc.code}); !errors.Is(err, tc.want) {
			t.Fatalf("mapCoreError(%s) = %v, want %v", tc.code, err, tc.want)
		}
	}
}

func TestEnsureRetriesIdempotencyInProgress(t *testing.T) {
	workloadID := "80000000-0000-0000-0000-000000000008"
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":"` + workloadID + `","state":"provisioning","ready_replicas":0,"internal_endpoint":""}`))
			return
		}
		if r.URL.Path != "/api/v1/platform-workloads" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Fatal("missing Idempotency-Key header")
		}
		posts++
		if posts == 1 {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"IDEMPOTENCY_IN_PROGRESS","message":"idempotent request is already in progress"}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"resource_id":"` + workloadID + `","state":"provisioning"}`))
	}))
	t.Cleanup(server.Close)
	observed, err := New(server.URL, "dev-token").Ensure(t.Context(), runtime.EnsureRequest{
		TenantID:        uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ServiceID:       uuid.MustParse("05f6f46f-3db8-4551-8497-c46debb4be22"),
		IdempotencyKey:  uuid.MustParse("1df72d71-9d49-46c4-a48a-52bb37b082ab"),
		ServedModelName: "tiny-cpu",
		Spec: domain.Spec{Replicas: 1, CPU: "4", Memory: "16Gi", ExecutionProfile: domain.ExecutionProfile{
			ImageRef: "registry.ani.internal/platform/vllm-openai-cpu@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		}},
	})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if posts != 2 {
		t.Fatalf("POST count = %d, want 2", posts)
	}
	if observed.RuntimeRef.String() != workloadID {
		t.Fatalf("runtime ref = %s", observed.RuntimeRef)
	}
}
