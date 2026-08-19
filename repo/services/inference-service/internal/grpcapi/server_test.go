package grpcapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	inferencecontrolv1 "github.com/kubercloud/ani/pkg/generated/pb/inference/control/v1"
	"github.com/kubercloud/ani/services/inference-service/internal/catalog"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	"github.com/kubercloud/ani/services/inference-service/internal/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	testTenant  = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	testService = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	testModel   = uuid.MustParse("33333333-3333-3333-3333-333333333333")
	testKey     = uuid.MustParse("44444444-4444-4444-4444-444444444444")
	testOp      = uuid.MustParse("55555555-5555-5555-5555-555555555555")
	pinnedImage = "registry.local/user/vllm@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type fakeCreator struct {
	input  service.CreateInput
	tenant uuid.UUID
	result domain.Service
	op     domain.Operation
	err    error
	calls  int
}

func (f *fakeCreator) Create(_ context.Context, tenantID uuid.UUID, input service.CreateInput) (domain.Service, domain.Operation, error) {
	f.calls++
	f.tenant = tenantID
	f.input = input
	return f.result, f.op, f.err
}

type fakeController struct {
	tenant uuid.UUID
	id     uuid.UUID
	view   service.ServiceView
	list   []service.ServiceView
	opView service.OperationView
	op     domain.Operation
	err    error
}

func (f *fakeController) Get(_ context.Context, tenantID, serviceID uuid.UUID) (service.ServiceView, error) {
	f.tenant, f.id = tenantID, serviceID
	return f.view, f.err
}
func (f *fakeController) List(_ context.Context, tenantID uuid.UUID) ([]service.ServiceView, error) {
	f.tenant = tenantID
	return f.list, f.err
}
func (f *fakeController) GetOperation(_ context.Context, tenantID, operationID uuid.UUID) (service.OperationView, error) {
	f.tenant, f.id = tenantID, operationID
	return f.opView, f.err
}
func (f *fakeController) Scale(_ context.Context, tenantID, serviceID, _ uuid.UUID, _ int) (domain.Operation, error) {
	f.tenant, f.id = tenantID, serviceID
	return f.op, f.err
}
func (f *fakeController) Lifecycle(_ context.Context, tenantID, serviceID, _ uuid.UUID, _ domain.Action) (domain.Operation, error) {
	f.tenant, f.id = tenantID, serviceID
	return f.op, f.err
}
func (f *fakeController) Delete(_ context.Context, tenantID, serviceID uuid.UUID) (domain.Operation, error) {
	f.tenant, f.id = tenantID, serviceID
	return f.op, f.err
}

func pendingResource() (domain.Service, domain.Operation) {
	now := time.Date(2026, 8, 15, 1, 2, 3, 0, time.UTC)
	operation := domain.Operation{
		ID: testOp, TenantID: testTenant, ServiceID: testService, Type: domain.ActionCreate,
		State: domain.OperationPending, IdempotencyKey: testKey, CreatedAt: now,
	}
	resource := domain.Service{
		ID: testService, TenantID: testTenant, Name: "qwen-chat", ModelVersionID: testModel,
		ServedModelName: "qwen-chat", Status: domain.StatusPending, CurrentOperationID: testOp,
		DesiredSpec: domain.Spec{
			Replicas: 1, CPU: "2", Memory: "4Gi", PlacementMode: "auto",
			ExecutionProfile: domain.ExecutionProfile{ImageRef: pinnedImage},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	return resource, operation
}

func TestCreateRejectsMissingTenant(t *testing.T) {
	server := NewServer(&fakeCreator{}, &fakeController{})
	_, err := server.CreateInferenceService(context.Background(), &inferencecontrolv1.CreateInferenceServiceRequest{
		IdempotencyKey: testKey.String(), Name: "qwen-chat", Model: testModel.String(),
		Resources: &inferencecontrolv1.InferenceServiceResources{Cpu: "2", Memory: "4Gi"},
	})
	assertStatus(t, err, codes.Unauthenticated, "UNAUTHORIZED")
}

func TestCreateRejectsGPUCountWithoutGPUType(t *testing.T) {
	creator := &fakeCreator{}
	resource, operation := pendingResource()
	creator.result, creator.op = resource, operation
	server := NewServer(creator, &fakeController{})
	_, err := server.CreateInferenceService(context.Background(), &inferencecontrolv1.CreateInferenceServiceRequest{
		TenantId: testTenant.String(), IdempotencyKey: testKey.String(), Name: "qwen-chat",
		Model: testModel.String(), GpuCountPerPod: 2, ImageRef: pinnedImage,
		Resources: &inferencecontrolv1.InferenceServiceResources{Cpu: "2", Memory: "4Gi"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if creator.input.Spec.Accelerator != nil {
		t.Fatalf("gpu_count_per_pod alone inferred accelerator: %+v", creator.input.Spec.Accelerator)
	}
}

func TestCreateMapsCatalogAndConflictErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code codes.Code
		msg  string
	}{
		{name: "not found", err: catalog.ErrModelNotFound, code: codes.NotFound, msg: "NOT_FOUND"},
		{name: "not ready", err: catalog.ErrModelNotReady, code: codes.FailedPrecondition, msg: "MODEL_NOT_READY"},
		{name: "incompatible", err: catalog.ErrNoCompatibleProfile, code: codes.FailedPrecondition, msg: "MODEL_INCOMPATIBLE"},
		{name: "name", err: repository.ErrNameConflict, code: codes.AlreadyExists, msg: "NAME_CONFLICT"},
		{name: "idempotency", err: repository.ErrIdempotencyConflict, code: codes.AlreadyExists, msg: "IDEMPOTENCY_CONFLICT"},
		{name: "invalid", err: service.ErrInvalidInput, code: codes.InvalidArgument, msg: "INVALID_ARGUMENT"},
		{name: "topology", err: service.ErrUnsupportedTopology, code: codes.FailedPrecondition, msg: "UNSUPPORTED_TOPOLOGY"},
		{name: "accelerator", err: service.ErrAcceleratorSpecUnavailable, code: codes.FailedPrecondition, msg: "ACCELERATOR_SPEC_UNAVAILABLE"},
		{name: "capacity", err: service.ErrInsufficientCapacity, code: codes.FailedPrecondition, msg: "INSUFFICIENT_CAPACITY"},
		{name: "image", err: service.ErrImageUnavailable, code: codes.FailedPrecondition, msg: "IMAGE_UNAVAILABLE"},
		{name: "unknown", err: errors.New("sql: connection refused"), code: codes.Unavailable, msg: "DEPENDENCY_UNAVAILABLE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewServer(&fakeCreator{err: tt.err}, &fakeController{})
			_, err := server.CreateInferenceService(context.Background(), &inferencecontrolv1.CreateInferenceServiceRequest{
				TenantId: testTenant.String(), IdempotencyKey: testKey.String(), Name: "qwen-chat",
				Model: testModel.String(), ImageRef: pinnedImage,
				Resources: &inferencecontrolv1.InferenceServiceResources{Cpu: "2", Memory: "4Gi"},
			})
			assertStatus(t, err, tt.code, tt.msg)
		})
	}
}

func TestCreateReturnsPublicProjection(t *testing.T) {
	resource, operation := pendingResource()
	creator := &fakeCreator{result: resource, op: operation}
	server := NewServer(creator, &fakeController{})
	got, err := server.CreateInferenceService(context.Background(), &inferencecontrolv1.CreateInferenceServiceRequest{
		TenantId: testTenant.String(), IdempotencyKey: testKey.String(), Name: "qwen-chat",
		Model: testModel.String(), ImageId: "tenant/runtime:latest", ImageRef: pinnedImage,
		Resources: &inferencecontrolv1.InferenceServiceResources{Cpu: "2", Memory: "4Gi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if creator.input.ImageID != "tenant/runtime:latest" || creator.input.ImageRef != pinnedImage {
		t.Fatalf("create input image = id=%q ref=%q", creator.input.ImageID, creator.input.ImageRef)
	}
	if got.GetId() != testService.String() || got.GetStatus() != "pending" {
		t.Fatalf("service = %+v", got)
	}
	if got.GetImageRef() != pinnedImage {
		t.Fatalf("image_ref = %q", got.GetImageRef())
	}
	if got.GetCurrentOperationId() != testOp.String() {
		t.Fatalf("current_operation_id = %q", got.GetCurrentOperationId())
	}
	if got.GetGpuType() != "" {
		t.Fatalf("gpu_type should stay empty for CPU create, got %q", got.GetGpuType())
	}
}

func TestCreateForwardsFrozenEngine(t *testing.T) {
	resource, operation := pendingResource()
	resource.DesiredSpec.Engine = &domain.Engine{
		Env:     []domain.EngineEnvVar{{Name: "VLLM_LOGGING_LEVEL", Value: "DEBUG"}},
		Command: []string{"python3", "-m", "vllm.entrypoints.openai.api_server"},
	}
	creator := &fakeCreator{result: resource, op: operation}
	server := NewServer(creator, &fakeController{})
	got, err := server.CreateInferenceService(context.Background(), &inferencecontrolv1.CreateInferenceServiceRequest{
		TenantId: testTenant.String(), IdempotencyKey: testKey.String(), Name: "qwen-chat",
		Model: testModel.String(), ImageRef: pinnedImage,
		Resources: &inferencecontrolv1.InferenceServiceResources{Cpu: "2", Memory: "4Gi"},
		Engine: &inferencecontrolv1.InferenceServiceEngine{
			Env:     []*inferencecontrolv1.InferenceServiceEngineEnvVar{{Name: "VLLM_LOGGING_LEVEL", Value: "DEBUG"}},
			Command: []string{"python3", "-m", "vllm.entrypoints.openai.api_server"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if creator.input.Spec.Engine == nil || strings.Join(creator.input.Spec.Engine.Command, " ") != "python3 -m vllm.entrypoints.openai.api_server" {
		t.Fatalf("create input engine = %+v", creator.input.Spec.Engine)
	}
	if got.GetEngine() == nil || strings.Join(got.GetEngine().GetCommand(), " ") != "python3 -m vllm.entrypoints.openai.api_server" {
		t.Fatalf("response engine = %+v", got.GetEngine())
	}
}

func TestCreateRejectsReservedEngineEnv(t *testing.T) {
	creator := &fakeCreator{}
	server := NewServer(creator, &fakeController{})
	_, err := server.CreateInferenceService(context.Background(), &inferencecontrolv1.CreateInferenceServiceRequest{
		TenantId: testTenant.String(), IdempotencyKey: testKey.String(), Name: "qwen-chat",
		Model: testModel.String(), ImageRef: pinnedImage,
		Resources: &inferencecontrolv1.InferenceServiceResources{Cpu: "2", Memory: "4Gi"},
		Engine: &inferencecontrolv1.InferenceServiceEngine{
			Env: []*inferencecontrolv1.InferenceServiceEngineEnvVar{{Name: "CUDA_VISIBLE_DEVICES", Value: "0"}},
		},
	})
	assertStatus(t, err, codes.InvalidArgument, "INVALID_ARGUMENT")
	if creator.calls != 0 {
		t.Fatalf("creator calls = %d, want 0", creator.calls)
	}
}

func TestCreateRejectsMissingAndUnpinnedImage(t *testing.T) {
	creator := &fakeCreator{}
	server := NewServer(creator, &fakeController{})
	tests := []struct {
		name     string
		imageID  string
		imageRef string
		code     codes.Code
		msg      string
	}{
		{name: "missing", code: codes.InvalidArgument, msg: "INVALID_ARGUMENT"},
		{name: "tag only", imageRef: "registry.local/user/vllm:latest", code: codes.FailedPrecondition, msg: "IMAGE_UNAVAILABLE"},
		{name: "image_id without digest", imageID: "tenant/runtime:latest", code: codes.FailedPrecondition, msg: "IMAGE_UNAVAILABLE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creator.calls = 0
			_, err := server.CreateInferenceService(context.Background(), &inferencecontrolv1.CreateInferenceServiceRequest{
				TenantId: testTenant.String(), IdempotencyKey: testKey.String(), Name: "qwen-chat",
				Model: testModel.String(), ImageId: tt.imageID, ImageRef: tt.imageRef,
				Resources: &inferencecontrolv1.InferenceServiceResources{Cpu: "2", Memory: "4Gi"},
			})
			assertStatus(t, err, tt.code, tt.msg)
			if creator.calls != 0 {
				t.Fatalf("creator calls = %d, want 0", creator.calls)
			}
		})
	}
}

func TestGetAndLifecycleMapControlErrors(t *testing.T) {
	server := NewServer(&fakeCreator{}, &fakeController{err: repository.ErrNotFound})
	_, err := server.GetInferenceService(context.Background(), &inferencecontrolv1.GetInferenceServiceRequest{
		TenantId: testTenant.String(), ServiceId: testService.String(),
	})
	assertStatus(t, err, codes.NotFound, "NOT_FOUND")

	server = NewServer(&fakeCreator{}, &fakeController{err: domain.ErrOperationInProgress})
	_, err = server.ApplyInferenceServiceLifecycle(context.Background(), &inferencecontrolv1.ApplyInferenceServiceLifecycleRequest{
		TenantId: testTenant.String(), ServiceId: testService.String(),
		IdempotencyKey: testKey.String(), Action: "stop",
	})
	assertStatus(t, err, codes.FailedPrecondition, "OPERATION_IN_PROGRESS")
}

type fakeLogs struct {
	tenant uuid.UUID
	id     uuid.UUID
	query  service.LogQuery
	page   service.LogPage
	err    error
}

func (f *fakeLogs) List(_ context.Context, tenantID, serviceID uuid.UUID, query service.LogQuery) (service.LogPage, error) {
	f.tenant, f.id, f.query = tenantID, serviceID, query
	return f.page, f.err
}

func TestListLogsMapsNotFoundAndProjectsPublicFields(t *testing.T) {
	server := NewServer(&fakeCreator{}, &fakeController{}).WithLogs(&fakeLogs{err: repository.ErrNotFound})
	_, err := server.ListInferenceServiceLogs(context.Background(), &inferencecontrolv1.ListInferenceServiceLogsRequest{
		TenantId: testTenant.String(), ServiceId: testService.String(), Limit: 20, Level: "info",
	})
	assertStatus(t, err, codes.NotFound, "NOT_FOUND")

	logs := &fakeLogs{page: service.LogPage{
		Items: []service.LogEntry{{
			Timestamp: time.Date(2026, 8, 15, 1, 2, 3, 0, time.UTC),
			Level:     "info", Message: "runtime accepted", Container: "serve", Stream: "stdout",
		}},
		NextCursor: "1",
	}}
	server = NewServer(&fakeCreator{}, &fakeController{}).WithLogs(logs)
	resp, err := server.ListInferenceServiceLogs(context.Background(), &inferencecontrolv1.ListInferenceServiceLogsRequest{
		TenantId: testTenant.String(), ServiceId: testService.String(), Limit: 20, Cursor: "0", Level: "info",
	})
	if err != nil {
		t.Fatal(err)
	}
	if logs.tenant != testTenant || logs.id != testService || logs.query.Limit != 20 || logs.query.Level != "info" {
		t.Fatalf("forwarded query = tenant=%s id=%s %+v", logs.tenant, logs.id, logs.query)
	}
	if len(resp.GetItems()) != 1 || resp.GetNextCursor() != "1" || resp.GetItems()[0].GetMessage() != "runtime accepted" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestListUsesTenantFromRequest(t *testing.T) {
	controller := &fakeController{list: []service.ServiceView{{
		ID: testService, Name: "qwen-chat", Model: "Qwen 7B / v1", ModelVersionID: testModel,
		Status: domain.StatusPending, CreatedAt: time.Now().UTC(),
	}}}
	server := NewServer(&fakeCreator{}, controller)
	resp, err := server.ListInferenceServices(context.Background(), &inferencecontrolv1.ListInferenceServicesRequest{
		TenantId: testTenant.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if controller.tenant != testTenant || len(resp.GetItems()) != 1 {
		t.Fatalf("tenant=%s items=%d", controller.tenant, len(resp.GetItems()))
	}
}

func assertStatus(t *testing.T, err error, code codes.Code, message string) {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error %v is not a gRPC status", err)
	}
	if st.Code() != code || st.Message() != message {
		t.Fatalf("status = %s %q, want %s %q", st.Code(), st.Message(), code, message)
	}
}
