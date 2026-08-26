package coresdk

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	if _, ok := accelerator["memory"]; ok {
		t.Fatalf("whole-card accelerator leaked memory: %#v", accelerator)
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
	if err := probeSmoke(t.Context(), server.Client(), server.URL, "tiny", domain.InferenceTaskGenerate); err != nil {
		t.Fatalf("probeSmoke() error = %v", err)
	}
	if len(paths) != 2 || paths[0] != "GET /health" || paths[1] != "POST /v1/chat/completions" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestProbeSmokeUsesTaskEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		response string
		task     domain.InferenceTask
	}{
		{name: "generate", path: "/v1/chat/completions", response: `{"choices":[{}]}`, task: domain.InferenceTaskGenerate},
		{name: "legacy-empty", path: "/v1/chat/completions", response: `{"choices":[{}]}`},
		{name: "legacy-unknown", path: "/v1/chat/completions", response: `{"choices":[{}]}`, task: domain.InferenceTask("unknown")},
		{name: "embed", path: "/v1/embeddings", response: `{"data":[{"embedding":[0.1]}]}`, task: domain.InferenceTaskEmbed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			var gotBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.response))
			}))
			t.Cleanup(server.Close)

			if err := probeSmoke(t.Context(), server.Client(), server.URL, " tiny ", tc.task); err != nil {
				t.Fatalf("probeSmoke() error = %v", err)
			}
			if gotPath != tc.path {
				t.Fatalf("path = %q, want %q", gotPath, tc.path)
			}
			if gotBody["model"] != "tiny" {
				t.Fatalf("model = %#v, want tiny", gotBody["model"])
			}
			if tc.task == domain.InferenceTaskEmbed {
				input, ok := gotBody["input"].([]any)
				if !ok || len(input) != 1 || input[0] != "ping" {
					t.Fatalf("embedding input = %#v", gotBody["input"])
				}
			}
		})
	}
}

func TestProbeSmokeAcceptsLargeEmbeddingResponse(t *testing.T) {
	response := `{"data":[{"embedding":[` + strings.Repeat("0.1,", 5000) + `0.1]}]}`
	if len(response) <= 4096 {
		t.Fatalf("test response length = %d, want > 4096", len(response))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)

	if err := probeSmoke(t.Context(), server.Client(), server.URL, "tiny", domain.InferenceTaskEmbed); err != nil {
		t.Fatalf("probeSmoke() error = %v", err)
	}
}

func TestProbeSmokeRejectsInvalidEmbeddingResponse(t *testing.T) {
	tests := []struct {
		name, response string
	}{
		{name: "missing-data", response: `{}`},
		{name: "empty-data", response: `{"data":[]}`},
		{name: "non-json", response: `not-json`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.response))
			}))
			t.Cleanup(server.Close)

			if err := probeSmoke(t.Context(), server.Client(), server.URL, "tiny", domain.InferenceTaskEmbed); err == nil {
				t.Fatal("probeSmoke() error = nil, want invalid embedding response")
			}
		})
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

func TestCreateBodyMapsAcceleratorMemory(t *testing.T) {
	body := createBody(runtime.EnsureRequest{
		ServiceID:       uuid.MustParse("05f6f46f-3db8-4551-8497-c46debb4be22"),
		ServedModelName: "tiny-gpu",
		IdempotencyKey:  uuid.MustParse("2df72d71-9d49-46c4-a48a-52bb37b082ab"),
		Spec: domain.Spec{
			Replicas: 1, CPU: "8", Memory: "32Gi",
			Accelerator: &domain.Accelerator{SpecID: "gpu-nvidia-geforce-rtx-4090", CountPerReplica: 1, MemoryMB: 10240},
			ExecutionProfile: domain.ExecutionProfile{
				ImageRef: "registry.ani.internal/platform/vllm-openai-gpu@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			},
		},
	}, runtime.TopologyPlan{Mode: "single_node", ProfileID: "container-single-node", ProfileVersion: "v1"})
	accelerator, _ := body["resources"].(map[string]any)["accelerator"].(map[string]any)
	if accelerator["spec_id"] != "gpu-nvidia-geforce-rtx-4090" || accelerator["count"] != 1 || accelerator["memory"] != 10240 {
		t.Fatalf("accelerator = %#v", accelerator)
	}
}

func TestHealthAndSmokePassCallerTenantID(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(engine.Close)

	tenantA := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tenantB := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	refA := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	refB := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	var mu sync.Mutex
	seen := map[string]string{}
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/platform-workloads/")
		mu.Lock()
		seen[id] = r.Header.Get("X-Dev-Tenant-ID")
		mu.Unlock()
		payload, _ := json.Marshal(map[string]any{
			"id":                id,
			"state":             "running",
			"ready_replicas":    1,
			"internal_endpoint": engine.URL,
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(core.Close)

	rt := New(core.URL, "dev-token")
	errCh := make(chan error, 4)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		errCh <- rt.Health(t.Context(), tenantA, refA)
	}()
	go func() {
		defer wg.Done()
		errCh <- rt.Health(t.Context(), tenantB, refB)
	}()
	wg.Wait()
	if err := rt.Smoke(t.Context(), tenantA, refA, "tiny", domain.InferenceTaskGenerate); err != nil {
		t.Fatalf("Smoke() error = %v", err)
	}
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("Health() error = %v", err)
		}
	}
	if seen[refA.String()] != tenantA.String() || seen[refB.String()] != tenantB.String() {
		t.Fatalf("tenant headers = %#v", seen)
	}
}
