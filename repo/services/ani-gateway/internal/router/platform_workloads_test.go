package router

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

type capturingAsyncTaskStore struct {
	ctx    context.Context
	record ports.AsyncTaskRecord
}

func (s *capturingAsyncTaskStore) Create(ctx context.Context, record ports.AsyncTaskRecord) (ports.AsyncTaskRecord, bool, error) {
	s.ctx = ctx
	if record.ID == "" {
		record.ID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	}
	s.record = record
	return record, false, nil
}

func (s *capturingAsyncTaskStore) Get(context.Context, string, string) (ports.AsyncTaskRecord, error) {
	return ports.AsyncTaskRecord{}, ports.ErrNotFound
}

func (s *capturingAsyncTaskStore) Update(context.Context, ports.AsyncTaskUpdate) (ports.AsyncTaskRecord, error) {
	return ports.AsyncTaskRecord{}, ports.ErrNotFound
}

func (s *capturingAsyncTaskStore) List(context.Context, string, ports.AsyncTaskListFilter) ([]ports.AsyncTaskRecord, string, error) {
	return nil, "", ports.ErrNotFound
}

func TestPlatformWorkloadHTTPCPUCreateGetAndServiceGate(t *testing.T) {
	h := setupPlatformWorkloadTestServer(t)
	tenant := "11111111-1111-1111-1111-111111111111"
	body := `{
		"idempotency_key":"1df72d71-9d49-46c4-a48a-52bb37b082ab",
		"name":"inference-cpu-example",
		"workload_class":"inference",
		"runtime_kind":"container",
		"image_ref":"registry.ani.internal/platform/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"command":["/opt/platform-runtime/serve"],
		"replicas":1,
		"resources":{"cpu":"4","memory":"16Gi"},
		"topology":{"mode":"single_node","profile_id":"container-single-node","profile_version":"v1"},
		"scheduling":{"queue_class":"inference","gang":false},
		"network":{"exposure":"cluster_internal","ports":[{"name":"http","port":8000}]},
		"health_check":{"protocol":"http","path":"/health","port_name":"http"},
		"metadata":{"owner_ref":"05f6f46f-3db8-4551-8497-c46debb4be22"}
	}`

	denied := performPlatformWorkload(h, http.MethodPost, "/api/v1/platform-workloads", body, tenant, false)
	if denied.StatusCode() != http.StatusForbidden {
		t.Fatalf("tenant create status = %d", denied.StatusCode())
	}

	created := performPlatformWorkload(h, http.MethodPost, "/api/v1/platform-workloads", body, tenant, true)
	if created.StatusCode() != http.StatusAccepted {
		t.Fatalf("create status = %d body=%s", created.StatusCode(), created.Body())
	}
	var task map[string]any
	if err := json.Unmarshal(created.Body(), &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if task["task_type"] != "platform_workload.create" || task["resource_type"] != "platform_workload" {
		t.Fatalf("task = %#v", task)
	}
	workloadID, _ := task["resource_id"].(string)
	if workloadID == "" {
		t.Fatalf("missing resource_id: %#v", task)
	}

	got := performPlatformWorkload(h, http.MethodGet, "/api/v1/platform-workloads/"+workloadID, "", tenant, true)
	if got.StatusCode() != http.StatusOK {
		t.Fatalf("get status = %d body=%s", got.StatusCode(), got.Body())
	}
	var workload map[string]any
	if err := json.Unmarshal(got.Body(), &workload); err != nil {
		t.Fatalf("decode workload: %v", err)
	}
	if workload["state"] != "running" || workload["internal_endpoint"] == nil || workload["runtime_shape"] != "deployment" {
		t.Fatalf("workload = %#v", workload)
	}

	other := performPlatformWorkload(h, http.MethodGet, "/api/v1/platform-workloads/"+workloadID, "", "22222222-2222-2222-2222-222222222222", true)
	if other.StatusCode() != http.StatusNotFound {
		t.Fatalf("cross-tenant get status = %d", other.StatusCode())
	}
}

func TestPlatformWorkloadAcceptedTaskUsesWorkloadTenantContext(t *testing.T) {
	tasks := &capturingAsyncTaskStore{}
	h := setupPlatformWorkloadTestServerWithTasks(t, tasks)
	tenant := "11111111-1111-1111-1111-111111111111"
	body := `{
		"idempotency_key":"2df72d71-9d49-46c4-a48a-52bb37b082ab",
		"name":"inference-task-tenant-context",
		"workload_class":"inference",
		"runtime_kind":"container",
		"image_ref":"registry.ani.internal/platform/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"command":["/opt/platform-runtime/serve"],
		"replicas":1,
		"resources":{"cpu":"4","memory":"16Gi"},
		"topology":{"mode":"single_node","profile_id":"container-single-node","profile_version":"v1"},
		"scheduling":{"queue_class":"inference","gang":false},
		"network":{"exposure":"cluster_internal","ports":[{"name":"http","port":8000}]},
		"health_check":{"protocol":"http","path":"/health","port_name":"http"},
		"metadata":{"owner_ref":"05f6f46f-3db8-4551-8497-c46debb4be22"}
	}`
	created := performPlatformWorkload(h, http.MethodPost, "/api/v1/platform-workloads", body, tenant, true)
	if created.StatusCode() != http.StatusAccepted {
		t.Fatalf("create status = %d body=%s", created.StatusCode(), created.Body())
	}
	if tasks.ctx == nil {
		t.Fatal("task store did not receive a context")
	}
	tc, ok := types.TryFromContext(tasks.ctx)
	if !ok || tc == nil || tc.TenantID.String() != tenant {
		t.Fatalf("task context = %#v, want tenant %s", tc, tenant)
	}
}

func TestPlatformWorkloadSpecFromRequestMapsAcceleratorMemory(t *testing.T) {
	memory := 10240
	spec, err := platformWorkloadSpecFromRequest(platformWorkloadCreateRequest{
		Resources: platformWorkloadResourcesRequest{
			CPU: "8", Memory: "32Gi",
			Accelerator: &platformWorkloadAcceleratorRequest{SpecID: "gpu-nvidia-geforce-rtx-4090", Count: 1, Memory: &memory},
		},
	})
	if err != nil {
		t.Fatalf("spec from request: %v", err)
	}
	if spec.Resources.AcceleratorSpecID != "gpu-nvidia-geforce-rtx-4090" || spec.Resources.AcceleratorCount != 1 || spec.Resources.AcceleratorMemoryMB != 10240 {
		t.Fatalf("resources = %+v", spec.Resources)
	}
}

func TestPlatformWorkloadHTTPRejectsZeroAcceleratorMemory(t *testing.T) {
	h := setupPlatformWorkloadTestServer(t)
	tenant := "11111111-1111-1111-1111-111111111111"
	body := `{
		"idempotency_key":"5df72d71-9d49-46c4-a48a-52bb37b082ab",
		"name":"inference-gpu-zero-memory",
		"workload_class":"inference",
		"runtime_kind":"container",
		"image_ref":"registry.ani.internal/platform/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"command":["python3","-m","vllm.entrypoints.openai.api_server"],
		"replicas":1,
		"resources":{"cpu":"8","memory":"32Gi","accelerator":{"spec_id":"gpu-a100","count":1,"memory":0}},
		"topology":{"mode":"single_node","profile_id":"container-single-node","profile_version":"v1"},
		"scheduling":{"queue_class":"inference","gang":false},
		"network":{"exposure":"cluster_internal","ports":[{"name":"http","port":8000}]},
		"health_check":{"protocol":"http","path":"/health","port_name":"http"},
		"metadata":{"owner_ref":"05f6f46f-3db8-4551-8497-c46debb4be22"}
	}`
	created := performPlatformWorkload(h, http.MethodPost, "/api/v1/platform-workloads", body, tenant, true)
	if created.StatusCode() != http.StatusBadRequest {
		t.Fatalf("zero memory status = %d body=%s", created.StatusCode(), created.Body())
	}
}

func TestPlatformWorkloadHTTPGPUCreateUsesSameEntry(t *testing.T) {
	h := setupPlatformWorkloadTestServer(t)
	tenant := "11111111-1111-1111-1111-111111111111"
	body := `{
		"idempotency_key":"5df72d71-9d49-46c4-a48a-52bb37b082ab",
		"name":"inference-gpu-example",
		"workload_class":"inference",
		"runtime_kind":"container",
		"image_ref":"registry.ani.internal/platform/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"command":["python3","-m","vllm.entrypoints.openai.api_server"],
		"replicas":1,
		"resources":{"cpu":"8","memory":"32Gi","accelerator":{"spec_id":"gpu-a100","count":1}},
		"topology":{"mode":"single_node","profile_id":"container-single-node","profile_version":"v1"},
		"scheduling":{"queue_class":"inference","gang":false},
		"network":{"exposure":"cluster_internal","ports":[{"name":"http","port":8000}]},
		"health_check":{"protocol":"http","path":"/health","port_name":"http"},
		"metadata":{"owner_ref":"05f6f46f-3db8-4551-8497-c46debb4be22"}
	}`
	created := performPlatformWorkload(h, http.MethodPost, "/api/v1/platform-workloads", body, tenant, true)
	if created.StatusCode() != http.StatusAccepted {
		t.Fatalf("gpu create status = %d body=%s", created.StatusCode(), created.Body())
	}
}

func TestPlatformWorkloadHTTPLWSRejectedOnLocalProfile(t *testing.T) {
	h := setupPlatformWorkloadTestServer(t)
	body := `{
		"idempotency_key":"6df72d71-9d49-46c4-a48a-52bb37b082ab",
		"name":"inference-lws-example",
		"workload_class":"inference",
		"runtime_kind":"container",
		"image_ref":"registry.ani.internal/platform/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"command":["python3","-m","vllm.entrypoints.openai.api_server"],
		"replicas":1,
		"resources":{"cpu":"8","memory":"32Gi","accelerator":{"spec_id":"gpu-a100-full","count":2}},
		"topology":{"mode":"leader_worker","profile_id":"container-leader-worker","profile_version":"v1","leader":{"count":1,"resources":{"cpu":"8","memory":"32Gi","accelerator":{"spec_id":"gpu-a100-full","count":1}}},"workers":{"count":1,"resources":{"cpu":"8","memory":"32Gi","accelerator":{"spec_id":"gpu-a100-full","count":1}}}},
		"scheduling":{"queue_class":"inference","gang":true},
		"network":{"exposure":"cluster_internal","ports":[{"name":"http","port":8000}]},
		"health_check":{"protocol":"http","path":"/health","port_name":"http"},
		"metadata":{"owner_ref":"05f6f46f-3db8-4551-8497-c46debb4be22"}
	}`
	created := performPlatformWorkload(h, http.MethodPost, "/api/v1/platform-workloads", body, "11111111-1111-1111-1111-111111111111", true)
	if created.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("lws create status = %d body=%s", created.StatusCode(), created.Body())
	}
}

func setupPlatformWorkloadTestServer(t *testing.T) *server.Hertz {
	return setupPlatformWorkloadTestServerWithTasks(t, runtimeadapter.NewLocalAsyncTaskStore())
}

func setupPlatformWorkloadTestServerWithTasks(t *testing.T, tasks ports.AsyncTaskStore) *server.Hertz {
	t.Helper()
	h := server.Default()
	h.Use(middleware.RequestID())
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		if tenantID := string(c.GetHeader("X-Dev-Tenant-ID")); tenantID != "" {
			c.Set("tenant_id", tenantID)
		}
		if kind := string(c.GetHeader("X-Dev-Principal-Kind")); kind != "" {
			c.Set("principal_kind", kind)
		}
		if scope := string(c.GetHeader("X-Dev-Service-Scope")); scope != "" {
			c.Set("service_scope", scope)
		}
		c.Next(ctx)
	})
	registerPlatformWorkloadResources(h.Group("/api/v1"), runtimeadapter.NewLocalPlatformWorkloadService(), tasks)
	return h
}

func performPlatformWorkload(h *server.Hertz, method, path, body, tenant string, service bool) *protocol.Response {
	var bodyArg *ut.Body
	if body != "" {
		bodyArg = &ut.Body{Body: strings.NewReader(body), Len: len(body)}
	}
	headers := []ut.Header{{Key: "Content-Type", Value: "application/json"}, {Key: "X-Dev-Tenant-ID", Value: tenant}}
	if service {
		headers = append(headers,
			ut.Header{Key: "X-Dev-Principal-Kind", Value: "service"},
			ut.Header{Key: "X-Dev-Service-Scope", Value: "scope:platform-workloads:write"},
		)
	}
	return ut.PerformRequest(h.Engine, method, path, bodyArg, headers...).Result()
}
