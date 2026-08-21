package router

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	inferencecontrolv1 "github.com/kubercloud/ani/pkg/generated/pb/inference/control/v1"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const pinnedInferenceImageRef = "registry.local/user/vllm@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func inferenceCreateBody(imageFields string) string {
	body := `{"idempotency_key":"44444444-4444-4444-4444-444444444444","name":"qwen-chat","model":"33333333-3333-3333-3333-333333333333","resources":{"cpu":"2","memory":"4Gi"}`
	if imageFields != "" {
		return body + "," + imageFields + "}"
	}
	return body + "}"
}

type fakeInferenceClient struct {
	lastTenantID string
	lastService  string
	lastKey      string
	lastAction   string
	lastReplicas int32
	lastCreate   *inferencecontrolv1.CreateInferenceServiceRequest

	listResp   *inferencecontrolv1.ListInferenceServicesResponse
	createResp *inferencecontrolv1.InferenceService
	getResp    *inferencecontrolv1.InferenceService
	scaleResp  *inferencecontrolv1.InferenceOperation
	deleteResp *inferencecontrolv1.InferenceOperation
	lifeResp   *inferencecontrolv1.InferenceOperation
	opResp     *inferencecontrolv1.InferenceOperation
	logsResp   *inferencecontrolv1.ListInferenceServiceLogsResponse
	lastLimit  int32
	lastCursor string
	lastLevel  string
	err        error
}

func (f *fakeInferenceClient) ListInferenceServices(_ context.Context, tenantID string) (*inferencecontrolv1.ListInferenceServicesResponse, error) {
	f.lastTenantID = tenantID
	return f.listResp, f.err
}
func (f *fakeInferenceClient) CreateInferenceService(_ context.Context, tenantID string, req *inferencecontrolv1.CreateInferenceServiceRequest) (*inferencecontrolv1.InferenceService, error) {
	f.lastTenantID = tenantID
	f.lastKey = req.GetIdempotencyKey()
	f.lastCreate = req
	return f.createResp, f.err
}
func (f *fakeInferenceClient) GetInferenceService(_ context.Context, tenantID, serviceID string) (*inferencecontrolv1.InferenceService, error) {
	f.lastTenantID = tenantID
	f.lastService = serviceID
	return f.getResp, f.err
}
func (f *fakeInferenceClient) ScaleInferenceService(_ context.Context, tenantID, serviceID, key string, replicas int32) (*inferencecontrolv1.InferenceOperation, error) {
	f.lastTenantID, f.lastService, f.lastKey, f.lastReplicas = tenantID, serviceID, key, replicas
	return f.scaleResp, f.err
}
func (f *fakeInferenceClient) DeleteInferenceService(_ context.Context, tenantID, serviceID string) (*inferencecontrolv1.InferenceOperation, error) {
	f.lastTenantID, f.lastService = tenantID, serviceID
	return f.deleteResp, f.err
}
func (f *fakeInferenceClient) ApplyInferenceServiceLifecycle(_ context.Context, tenantID, serviceID, key, action string) (*inferencecontrolv1.InferenceOperation, error) {
	f.lastTenantID, f.lastService, f.lastKey, f.lastAction = tenantID, serviceID, key, action
	return f.lifeResp, f.err
}
func (f *fakeInferenceClient) GetInferenceOperation(_ context.Context, tenantID, operationID string) (*inferencecontrolv1.InferenceOperation, error) {
	f.lastTenantID, f.lastService = tenantID, operationID
	return f.opResp, f.err
}
func (f *fakeInferenceClient) ListInferenceServiceLogs(_ context.Context, tenantID, serviceID string, limit int32, cursor, level string) (*inferencecontrolv1.ListInferenceServiceLogsResponse, error) {
	f.lastTenantID, f.lastService, f.lastLimit, f.lastCursor, f.lastLevel = tenantID, serviceID, limit, cursor, level
	if f.logsResp == nil {
		return &inferencecontrolv1.ListInferenceServiceLogsResponse{}, f.err
	}
	return f.logsResp, f.err
}

func setupInferenceTestServer(t *testing.T, client InferenceControlClient) *server.Hertz {
	t.Helper()
	prev := inferenceControlClient
	prevRegistry := inferenceImageRegistry
	inferenceControlClient = client
	t.Cleanup(func() {
		inferenceControlClient = prev
		inferenceImageRegistry = prevRegistry
	})
	h := server.Default()
	h.Use(middleware.RequestID())
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		if tenantID := string(c.GetHeader("X-Dev-Tenant-ID")); tenantID != "" {
			c.Set("tenant_id", tenantID)
		}
		c.Next(ctx)
	})
	registerInferenceServices(h.Group("/api/v1/svc"))
	return h
}

func performInference(h *server.Hertz, method, path, body, tenant string) *protocol.Response {
	var bodyArg *ut.Body
	if body != "" {
		bodyArg = &ut.Body{Body: strings.NewReader(body), Len: len(body)}
	}
	headers := []ut.Header{{Key: "Content-Type", Value: "application/json"}}
	if tenant != "" {
		headers = append(headers, ut.Header{Key: "X-Dev-Tenant-ID", Value: tenant})
	}
	return ut.PerformRequest(h.Engine, method, path, bodyArg, headers...).Result()
}

func sampleService() *inferencecontrolv1.InferenceService {
	return &inferencecontrolv1.InferenceService{
		Id: "22222222-2222-2222-2222-222222222222", Name: "qwen-chat", Model: "Qwen 7B / v1",
		ModelVersionId: "33333333-3333-3333-3333-333333333333", ServedModelName: "qwen-chat",
		Replicas: 1, Resources: &inferencecontrolv1.InferenceServiceResources{Cpu: "2", Memory: "4Gi"},
		PlacementMode: "auto", Status: "pending", CurrentOperationId: "55555555-5555-5555-5555-555555555555",
		ImageRef:  pinnedInferenceImageRef,
		CreatedAt: timestamppb.New(time.Date(2026, 8, 15, 1, 2, 3, 0, time.UTC)),
	}
}

func sampleOperation(taskType string) *inferencecontrolv1.InferenceOperation {
	return &inferencecontrolv1.InferenceOperation{
		Id: "55555555-5555-5555-5555-555555555555", TaskType: taskType, ResourceType: "inference_service",
		ResourceId: "22222222-2222-2222-2222-222222222222", IdempotencyKey: "44444444-4444-4444-4444-444444444444",
		Status: "pending", CreatedAt: timestamppb.New(time.Date(2026, 8, 15, 1, 2, 3, 0, time.UTC)),
	}
}

func TestInferenceRoutesRegistered(t *testing.T) {
	h := setupInferenceTestServer(t, &fakeInferenceClient{
		listResp:   &inferencecontrolv1.ListInferenceServicesResponse{},
		createResp: sampleService(), getResp: sampleService(),
		scaleResp:  sampleOperation("inference_service.scale"),
		deleteResp: sampleOperation("inference_service.delete"),
		lifeResp:   sampleOperation("inference_service.stop"),
		opResp:     sampleOperation("inference_service.create"),
	})
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/svc/inference-services", ""},
		{http.MethodPost, "/api/v1/svc/inference-services", inferenceCreateBody(`"image_ref":"` + pinnedInferenceImageRef + `"`)},
		{http.MethodGet, "/api/v1/svc/inference-services/22222222-2222-2222-2222-222222222222", ""},
		{http.MethodPatch, "/api/v1/svc/inference-services/22222222-2222-2222-2222-222222222222", `{"idempotency_key":"44444444-4444-4444-4444-444444444444","replicas":2}`},
		{http.MethodDelete, "/api/v1/svc/inference-services/22222222-2222-2222-2222-222222222222", ""},
		{http.MethodPost, "/api/v1/svc/inference-services/22222222-2222-2222-2222-222222222222/lifecycle", `{"idempotency_key":"44444444-4444-4444-4444-444444444444","action":"stop"}`},
		{http.MethodGet, "/api/v1/svc/inference-operations/55555555-5555-5555-5555-555555555555", ""},
		{http.MethodGet, "/api/v1/svc/inference-services/22222222-2222-2222-2222-222222222222/logs", ""},
		{http.MethodPut, "/api/v1/svc/inference-services/22222222-2222-2222-2222-222222222222/policies", `{"idempotency_key":"44444444-4444-4444-4444-444444444444"}`},
	}
	for _, route := range routes {
		resp := performInference(h, route.method, route.path, route.body, "11111111-1111-1111-1111-111111111111")
		if resp.StatusCode() == http.StatusNotFound {
			t.Fatalf("%s %s returned 404", route.method, route.path)
		}
	}
}

func TestInferenceCreateReturnsAcceptedPublicProjection(t *testing.T) {
	client := &fakeInferenceClient{createResp: sampleService()}
	h := setupInferenceTestServer(t, client)
	body := inferenceCreateBody(`"image_ref":"` + pinnedInferenceImageRef + `"`)
	resp := performInference(h, http.MethodPost, "/api/v1/svc/inference-services", body, "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if client.lastTenantID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("tenant = %q", client.lastTenantID)
	}
	var got map[string]any
	if err := json.Unmarshal(resp.Body(), &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != "22222222-2222-2222-2222-222222222222" || got["status"] != "pending" {
		t.Fatalf("body = %+v", got)
	}
	if got["invocation_url"] != nil || got["endpoint_url"] != nil {
		t.Fatalf("P0 URLs must be null: %+v", got)
	}
	if _, ok := got["runtime_ref"]; ok {
		t.Fatal("runtime_ref leaked")
	}
	if _, ok := got["runtime_endpoint"]; ok {
		t.Fatal("runtime_endpoint leaked")
	}
	if got["image_ref"] != pinnedInferenceImageRef {
		t.Fatalf("image_ref = %v", got["image_ref"])
	}
	if client.lastCreate == nil || client.lastCreate.GetImageRef() != pinnedInferenceImageRef || client.lastCreate.GetImageId() != "" {
		t.Fatalf("create request = %+v", client.lastCreate)
	}
}

func TestInferenceCreateForwardsEngineCommandAndEnv(t *testing.T) {
	client := &fakeInferenceClient{createResp: sampleService()}
	h := setupInferenceTestServer(t, client)
	body := inferenceCreateBody(`"image_ref":"` + pinnedInferenceImageRef + `","engine":{"env":[{"name":"VLLM_LOGGING_LEVEL","value":"DEBUG"}],"command":["python3","-m","vllm.entrypoints.openai.api_server","--model","/models/qwen"]}`)
	resp := performInference(h, http.MethodPost, "/api/v1/svc/inference-services", body, "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if client.lastCreate == nil || client.lastCreate.GetEngine() == nil {
		t.Fatal("engine was not forwarded")
	}
	engine := client.lastCreate.GetEngine()
	if len(engine.GetEnv()) != 1 || engine.GetEnv()[0].GetName() != "VLLM_LOGGING_LEVEL" || engine.GetEnv()[0].GetValue() != "DEBUG" {
		t.Fatalf("env = %#v", engine.GetEnv())
	}
	if strings.Join(engine.GetCommand(), " ") != "python3 -m vllm.entrypoints.openai.api_server --model /models/qwen" {
		t.Fatalf("command = %#v", engine.GetCommand())
	}
}

func TestInferenceCreateRejectsReservedEngineEnv(t *testing.T) {
	client := &fakeInferenceClient{createResp: sampleService()}
	h := setupInferenceTestServer(t, client)
	body := inferenceCreateBody(`"image_ref":"` + pinnedInferenceImageRef + `","engine":{"env":[{"name":"CUDA_VISIBLE_DEVICES","value":"0"}]}`)
	resp := performInference(h, http.MethodPost, "/api/v1/svc/inference-services", body, "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if client.lastCreate != nil {
		t.Fatal("reserved env must not reach gRPC")
	}
	var got map[string]any
	if err := json.Unmarshal(resp.Body(), &got); err != nil {
		t.Fatal(err)
	}
	if got["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("code = %v", got["code"])
	}
}

func TestInferenceMutationsReturnAcceptedAsyncTask(t *testing.T) {
	client := &fakeInferenceClient{
		scaleResp:  sampleOperation("inference_service.scale"),
		deleteResp: sampleOperation("inference_service.delete"),
		lifeResp:   sampleOperation("inference_service.stop"),
	}
	h := setupInferenceTestServer(t, client)
	cases := []struct {
		method, path, body, taskType string
	}{
		{http.MethodPatch, "/api/v1/svc/inference-services/22222222-2222-2222-2222-222222222222", `{"idempotency_key":"44444444-4444-4444-4444-444444444444","replicas":2}`, "inference_service.scale"},
		{http.MethodDelete, "/api/v1/svc/inference-services/22222222-2222-2222-2222-222222222222", "", "inference_service.delete"},
		{http.MethodPost, "/api/v1/svc/inference-services/22222222-2222-2222-2222-222222222222/lifecycle", `{"idempotency_key":"44444444-4444-4444-4444-444444444444","action":"stop"}`, "inference_service.stop"},
	}
	for _, tt := range cases {
		resp := performInference(h, tt.method, tt.path, tt.body, "11111111-1111-1111-1111-111111111111")
		if resp.StatusCode() != http.StatusAccepted {
			t.Fatalf("%s %s status = %d body=%s", tt.method, tt.path, resp.StatusCode(), resp.Body())
		}
		var got map[string]any
		if err := json.Unmarshal(resp.Body(), &got); err != nil {
			t.Fatal(err)
		}
		if got["task_type"] != tt.taskType || got["resource_type"] != "inference_service" {
			t.Fatalf("%s body = %+v", tt.path, got)
		}
	}
}

func TestInferenceRejectsMissingTenantAndDoesNotFallback(t *testing.T) {
	client := &fakeInferenceClient{listResp: &inferencecontrolv1.ListInferenceServicesResponse{}}
	h := setupInferenceTestServer(t, client)
	resp := performInference(h, http.MethodGet, "/api/v1/svc/inference-services", "", "")
	if resp.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode())
	}
	if client.lastTenantID != "" {
		t.Fatalf("gRPC was called with tenant %q", client.lastTenantID)
	}
}

func TestInferenceMapsStableErrorCodes(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{status.Error(codes.InvalidArgument, "INVALID_ARGUMENT"), http.StatusBadRequest, "INVALID_ARGUMENT"},
		{status.Error(codes.NotFound, "NOT_FOUND"), http.StatusNotFound, "NOT_FOUND"},
		{status.Error(codes.AlreadyExists, "NAME_CONFLICT"), http.StatusConflict, "NAME_CONFLICT"},
		{status.Error(codes.AlreadyExists, "IDEMPOTENCY_CONFLICT"), http.StatusConflict, "IDEMPOTENCY_CONFLICT"},
		{status.Error(codes.FailedPrecondition, "OPERATION_IN_PROGRESS"), http.StatusConflict, "OPERATION_IN_PROGRESS"},
		{status.Error(codes.FailedPrecondition, "MODEL_NOT_READY"), http.StatusUnprocessableEntity, "MODEL_NOT_READY"},
		{status.Error(codes.FailedPrecondition, "MODEL_INCOMPATIBLE"), http.StatusUnprocessableEntity, "MODEL_INCOMPATIBLE"},
		{status.Error(codes.FailedPrecondition, "INVALID_STATE_TRANSITION"), http.StatusUnprocessableEntity, "INVALID_STATE_TRANSITION"},
		{status.Error(codes.FailedPrecondition, "ACCELERATOR_SPEC_UNAVAILABLE"), http.StatusUnprocessableEntity, "ACCELERATOR_SPEC_UNAVAILABLE"},
		{status.Error(codes.Unavailable, "DEPENDENCY_UNAVAILABLE"), http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE"},
	}
	for _, tt := range tests {
		h := setupInferenceTestServer(t, &fakeInferenceClient{err: tt.err})
		resp := performInference(h, http.MethodGet, "/api/v1/svc/inference-services/22222222-2222-2222-2222-222222222222", "", "11111111-1111-1111-1111-111111111111")
		if resp.StatusCode() != tt.status {
			t.Fatalf("%s status = %d, want %d", tt.code, resp.StatusCode(), tt.status)
		}
		var body map[string]any
		_ = json.Unmarshal(resp.Body(), &body)
		if body["code"] != tt.code {
			t.Fatalf("code = %v, want %s", body["code"], tt.code)
		}
	}
}

func TestInferencePoliciesReturn501(t *testing.T) {
	h := setupInferenceTestServer(t, nil)
	resp := performInference(h, http.MethodPut, "/api/v1/svc/inference-services/22222222-2222-2222-2222-222222222222/policies", `{}`, "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", resp.StatusCode())
	}
	var body map[string]any
	_ = json.Unmarshal(resp.Body(), &body)
	if body["code"] != "FEATURE_NOT_AVAILABLE" {
		t.Fatalf("code = %v", body["code"])
	}
}

func TestInferenceMissingClientReturns503(t *testing.T) {
	h := setupInferenceTestServer(t, nil)
	resp := performInference(h, http.MethodGet, "/api/v1/svc/inference-services", "", "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode())
	}
	resp = performInference(h, http.MethodGet, "/api/v1/svc/inference-services/22222222-2222-2222-2222-222222222222/logs", "", "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("logs status = %d, want 503", resp.StatusCode())
	}
}

func TestInferenceLogsRequireTenantAndProjectPublicFields(t *testing.T) {
	client := &fakeInferenceClient{logsResp: &inferencecontrolv1.ListInferenceServiceLogsResponse{
		Items: []*inferencecontrolv1.InferenceServiceLogEntry{{
			Timestamp: timestamppb.New(time.Date(2026, 8, 15, 1, 2, 3, 0, time.UTC)),
			Level:     "info", Message: "runtime accepted", Container: "serve", Stream: "stdout",
		}},
		NextCursor: "1",
	}}
	h := setupInferenceTestServer(t, client)
	resp := performInference(h, http.MethodGet, "/api/v1/svc/inference-services/22222222-2222-2222-2222-222222222222/logs?limit=20&level=info&cursor=0", "", "")
	if resp.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("missing tenant status = %d", resp.StatusCode())
	}
	if client.lastTenantID != "" {
		t.Fatalf("gRPC was called with tenant %q", client.lastTenantID)
	}

	resp = performInference(h, http.MethodGet, "/api/v1/svc/inference-services/22222222-2222-2222-2222-222222222222/logs?limit=20&level=info&cursor=0", "", "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if client.lastTenantID != "11111111-1111-1111-1111-111111111111" || client.lastService != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("tenant=%q service=%q", client.lastTenantID, client.lastService)
	}
	if client.lastLimit != 20 || client.lastLevel != "info" || client.lastCursor != "0" {
		t.Fatalf("query limit=%d level=%q cursor=%q", client.lastLimit, client.lastLevel, client.lastCursor)
	}
	var got map[string]any
	if err := json.Unmarshal(resp.Body(), &got); err != nil {
		t.Fatal(err)
	}
	if got["next_cursor"] != "1" {
		t.Fatalf("body = %+v", got)
	}
	items, _ := got["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %+v", got["items"])
	}
	item, _ := items[0].(map[string]any)
	if item["message"] != "runtime accepted" || item["container"] != "serve" || item["stream"] != "stdout" {
		t.Fatalf("item = %+v", item)
	}
	if _, ok := item["replica"]; ok {
		t.Fatal("replica leaked")
	}
	if _, ok := got["runtime_ref"]; ok {
		t.Fatal("runtime_ref leaked")
	}
}

func TestInferenceLogsRejectInvalidQuery(t *testing.T) {
	client := &fakeInferenceClient{}
	h := setupInferenceTestServer(t, client)
	resp := performInference(h, http.MethodGet, "/api/v1/svc/inference-services/22222222-2222-2222-2222-222222222222/logs?level=fatal", "", "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode())
	}
	if client.lastTenantID != "" {
		t.Fatal("invalid query must not reach gRPC")
	}
}

func TestInferenceCreateRequiresImage(t *testing.T) {
	client := &fakeInferenceClient{createResp: sampleService()}
	h := setupInferenceTestServer(t, client)
	resp := performInference(h, http.MethodPost, "/api/v1/svc/inference-services", inferenceCreateBody(""), "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if client.lastCreate != nil {
		t.Fatal("missing image must not reach gRPC")
	}
}

func TestInferenceCreateRejectsUnpinnedImageRef(t *testing.T) {
	client := &fakeInferenceClient{createResp: sampleService()}
	h := setupInferenceTestServer(t, client)
	resp := performInference(h, http.MethodPost, "/api/v1/svc/inference-services", inferenceCreateBody(`"image_ref":"registry.local/user/vllm:latest"`), "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if client.lastCreate != nil {
		t.Fatal("unpinned image_ref must not reach gRPC without registry")
	}
}

type stubInferenceRegistry struct {
	ports.ImageRegistry
	items []ports.RegistryImage
}

func (s *stubInferenceRegistry) ListImages(_ context.Context, request ports.RegistryImageListRequest) (ports.RegistryImageListResult, error) {
	items := make([]ports.RegistryImage, 0, len(s.items))
	for _, item := range s.items {
		if request.Repository != "" && request.Repository != item.Repository {
			continue
		}
		if request.Tag != "" && request.Tag != item.Tag {
			continue
		}
		items = append(items, item)
	}
	return ports.RegistryImageListResult{Items: items}, nil
}

func TestInferenceCreateResolvesImageIDBeforeImageRef(t *testing.T) {
	tenant := "11111111-1111-1111-1111-111111111111"
	digest := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	prev := inferenceImageRegistry
	inferenceImageRegistry = &stubInferenceRegistry{items: []ports.RegistryImage{{
		Project: tenant, Repository: "runtime", Tag: "latest",
		Image: "registry.local/" + tenant + "/runtime:latest", Registry: "registry.local", Digest: digest,
	}}}
	t.Cleanup(func() { inferenceImageRegistry = prev })

	client := &fakeInferenceClient{createResp: sampleService()}
	h := setupInferenceTestServer(t, client)
	body := inferenceCreateBody(`"image_id":"` + tenant + `/runtime:latest","image_ref":"` + pinnedInferenceImageRef + `"`)
	resp := performInference(h, http.MethodPost, "/api/v1/svc/inference-services", body, tenant)
	if resp.StatusCode() != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	want := "registry.local/" + tenant + "/runtime@" + digest
	if client.lastCreate.GetImageId() != tenant+"/runtime:latest" || client.lastCreate.GetImageRef() != want {
		t.Fatalf("create request = %+v", client.lastCreate)
	}
}

func TestInferenceCreateUnknownImageIDReturnsUnavailable(t *testing.T) {
	prev := inferenceImageRegistry
	inferenceImageRegistry = &stubInferenceRegistry{}
	t.Cleanup(func() { inferenceImageRegistry = prev })

	client := &fakeInferenceClient{createResp: sampleService()}
	h := setupInferenceTestServer(t, client)
	resp := performInference(h, http.MethodPost, "/api/v1/svc/inference-services", inferenceCreateBody(`"image_id":"missing/runtime:latest"`), "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if client.lastCreate != nil {
		t.Fatal("unknown image_id must not reach gRPC")
	}
}

func TestProtoResourcesMapsAcceleratorMemory(t *testing.T) {
	memory := int32(10240)
	msg := protoResources(&inferenceServiceResourcesJSON{
		CPU: "8", Memory: "32Gi",
		Accelerator: &inferenceServiceAcceleratorJSON{
			SpecID: "gpu-nvidia-geforce-rtx-4090", CountPerReplica: 1, Memory: &memory,
		},
	})
	if msg.GetAccelerator().GetSpecId() != "gpu-nvidia-geforce-rtx-4090" || msg.GetAccelerator().GetCountPerReplica() != 1 || msg.GetAccelerator().GetMemory() != 10240 {
		t.Fatalf("accelerator = %+v", msg.GetAccelerator())
	}
	body := inferenceResourcesJSON(msg)
	acc, _ := body["accelerator"].(map[string]any)
	if acc["memory"] != int32(10240) {
		t.Fatalf("json accelerator = %#v", acc)
	}
}

func TestCreateInferenceServiceRejectsZeroAcceleratorMemory(t *testing.T) {
	client := &fakeInferenceClient{createResp: &inferencecontrolv1.InferenceService{Id: "svc-1"}}
	h := setupInferenceTestServer(t, client)
	body := `{"idempotency_key":"44444444-4444-4444-4444-444444444444","name":"qwen-chat","model":"33333333-3333-3333-3333-333333333333","resources":{"cpu":"2","memory":"4Gi","accelerator":{"spec_id":"gpu-nvidia-geforce-rtx-4090","count_per_replica":1,"memory":0}},"image_ref":"` + pinnedInferenceImageRef + `"}`
	resp := performInference(h, http.MethodPost, "/api/v1/svc/inference-services", body, "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if client.lastCreate != nil {
		t.Fatal("zero accelerator memory must not reach gRPC")
	}
}
