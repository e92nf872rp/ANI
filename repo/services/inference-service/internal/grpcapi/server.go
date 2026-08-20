package grpcapi

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	inferencecontrolv1 "github.com/kubercloud/ani/pkg/generated/pb/inference/control/v1"
	"github.com/kubercloud/ani/services/inference-service/internal/catalog"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	"github.com/kubercloud/ani/services/inference-service/internal/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	errInvalidArgument = errors.New("invalid inference request")
	errUnauthenticated = errors.New("inference tenant identity is required")
)

// CreateUseCase 是 POST 创建用例，由 Creator 实现。
type CreateUseCase interface {
	Create(context.Context, uuid.UUID, service.CreateInput) (domain.Service, domain.Operation, error)
}

// ControlUseCase 覆盖 list/get/scale/lifecycle/delete 和 operation 查询。
type ControlUseCase interface {
	Get(context.Context, uuid.UUID, uuid.UUID) (service.ServiceView, error)
	List(context.Context, uuid.UUID) ([]service.ServiceView, error)
	GetOperation(context.Context, uuid.UUID, uuid.UUID) (service.OperationView, error)
	Scale(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int) (domain.Operation, error)
	Lifecycle(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, domain.Action) (domain.Operation, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) (domain.Operation, error)
}

type LogsUseCase interface {
	List(context.Context, uuid.UUID, uuid.UUID, service.LogQuery) (service.LogPage, error)
}

// Server 实现 InferenceControl gRPC。Gateway HTTP 只调这里，不直连 Core。
type Server struct {
	inferencecontrolv1.UnimplementedInferenceControlServer
	creator    CreateUseCase
	controller ControlUseCase
	logs       LogsUseCase
}

func NewServer(creator CreateUseCase, controller ControlUseCase) *Server {
	return &Server{creator: creator, controller: controller}
}

func (s *Server) WithLogs(logs LogsUseCase) *Server {
	s.logs = logs
	return s
}

// Register 挂到 bootstrap gRPC server。
func (s *Server) Register(grpcServer *grpc.Server) {
	inferencecontrolv1.RegisterInferenceControlServer(grpcServer, s)
}

func (s *Server) ListInferenceServices(ctx context.Context, req *inferencecontrolv1.ListInferenceServicesRequest) (*inferencecontrolv1.ListInferenceServicesResponse, error) {
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	views, err := s.controller.List(ctx, tenantID)
	if err != nil {
		return nil, mapError(err)
	}
	items := make([]*inferencecontrolv1.InferenceService, 0, len(views))
	for _, view := range views {
		items = append(items, protoService(view))
	}
	return &inferencecontrolv1.ListInferenceServicesResponse{Items: items}, nil
}

func (s *Server) CreateInferenceService(ctx context.Context, req *inferencecontrolv1.CreateInferenceServiceRequest) (*inferencecontrolv1.InferenceService, error) {
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	input, err := createInputFromProto(req)
	if err != nil {
		return nil, mapError(err)
	}
	resource, operation, err := s.creator.Create(ctx, tenantID, input)
	if err != nil {
		return nil, mapError(err)
	}
	return acceptedService(resource, operation), nil
}

func (s *Server) GetInferenceService(ctx context.Context, req *inferencecontrolv1.GetInferenceServiceRequest) (*inferencecontrolv1.InferenceService, error) {
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	serviceID, err := parseResourceID(req.GetServiceId(), "service_id")
	if err != nil {
		return nil, mapError(err)
	}
	view, err := s.controller.Get(ctx, tenantID, serviceID)
	if err != nil {
		return nil, mapError(err)
	}
	return protoService(view), nil
}

func (s *Server) ScaleInferenceService(ctx context.Context, req *inferencecontrolv1.ScaleInferenceServiceRequest) (*inferencecontrolv1.InferenceOperation, error) {
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	serviceID, err := parseResourceID(req.GetServiceId(), "service_id")
	if err != nil {
		return nil, mapError(err)
	}
	key, err := parseResourceID(req.GetIdempotencyKey(), "idempotency_key")
	if err != nil {
		return nil, mapError(err)
	}
	if req.GetReplicas() < 1 {
		return nil, mapError(errInvalidArgument)
	}
	operation, err := s.controller.Scale(ctx, tenantID, serviceID, key, int(req.GetReplicas()))
	if err != nil {
		return nil, mapError(err)
	}
	return protoOperationFromDomain(operation), nil
}

func (s *Server) DeleteInferenceService(ctx context.Context, req *inferencecontrolv1.DeleteInferenceServiceRequest) (*inferencecontrolv1.InferenceOperation, error) {
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	serviceID, err := parseResourceID(req.GetServiceId(), "service_id")
	if err != nil {
		return nil, mapError(err)
	}
	operation, err := s.controller.Delete(ctx, tenantID, serviceID)
	if err != nil {
		return nil, mapError(err)
	}
	return protoOperationFromDomain(operation), nil
}

func (s *Server) ApplyInferenceServiceLifecycle(ctx context.Context, req *inferencecontrolv1.ApplyInferenceServiceLifecycleRequest) (*inferencecontrolv1.InferenceOperation, error) {
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	serviceID, err := parseResourceID(req.GetServiceId(), "service_id")
	if err != nil {
		return nil, mapError(err)
	}
	key, err := parseResourceID(req.GetIdempotencyKey(), "idempotency_key")
	if err != nil {
		return nil, mapError(err)
	}
	action := domain.Action(strings.TrimSpace(req.GetAction()))
	operation, err := s.controller.Lifecycle(ctx, tenantID, serviceID, key, action)
	if err != nil {
		return nil, mapError(err)
	}
	return protoOperationFromDomain(operation), nil
}

func (s *Server) ListInferenceServiceLogs(ctx context.Context, req *inferencecontrolv1.ListInferenceServiceLogsRequest) (*inferencecontrolv1.ListInferenceServiceLogsResponse, error) {
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	serviceID, err := parseResourceID(req.GetServiceId(), "service_id")
	if err != nil {
		return nil, mapError(err)
	}
	if s.logs == nil {
		return &inferencecontrolv1.ListInferenceServiceLogsResponse{}, nil
	}
	page, err := s.logs.List(ctx, tenantID, serviceID, service.LogQuery{
		Limit: int(req.GetLimit()), Cursor: req.GetCursor(), Level: req.GetLevel(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return protoLogPage(page), nil
}

func (s *Server) GetInferenceOperation(ctx context.Context, req *inferencecontrolv1.GetInferenceOperationRequest) (*inferencecontrolv1.InferenceOperation, error) {
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	operationID, err := parseResourceID(req.GetOperationId(), "operation_id")
	if err != nil {
		return nil, mapError(err)
	}
	view, err := s.controller.GetOperation(ctx, tenantID, operationID)
	if err != nil {
		return nil, mapError(err)
	}
	return protoOperation(view), nil
}

// mapError 把领域错误翻成 gRPC code，Gateway 再映射 HTTP。
func mapError(err error) error {
	switch {
	case errors.Is(err, errUnauthenticated):
		return status.Error(codes.Unauthenticated, "UNAUTHORIZED")
	case errors.Is(err, errInvalidArgument), errors.Is(err, service.ErrInvalidInput):
		return status.Error(codes.InvalidArgument, "INVALID_ARGUMENT")
	case errors.Is(err, repository.ErrNotFound), errors.Is(err, catalog.ErrModelNotFound):
		return status.Error(codes.NotFound, "NOT_FOUND")
	case errors.Is(err, repository.ErrNameConflict):
		return status.Error(codes.AlreadyExists, "NAME_CONFLICT")
	case errors.Is(err, repository.ErrIdempotencyConflict):
		return status.Error(codes.AlreadyExists, "IDEMPOTENCY_CONFLICT")
	case errors.Is(err, domain.ErrOperationInProgress):
		return status.Error(codes.FailedPrecondition, "OPERATION_IN_PROGRESS")
	case errors.Is(err, catalog.ErrModelNotReady):
		return status.Error(codes.FailedPrecondition, "MODEL_NOT_READY")
	case errors.Is(err, catalog.ErrNoCompatibleProfile):
		return status.Error(codes.FailedPrecondition, "MODEL_INCOMPATIBLE")
	case errors.Is(err, service.ErrUnsupportedTopology):
		return status.Error(codes.FailedPrecondition, "UNSUPPORTED_TOPOLOGY")
	case errors.Is(err, service.ErrAcceleratorSpecUnavailable):
		return status.Error(codes.FailedPrecondition, "ACCELERATOR_SPEC_UNAVAILABLE")
	case errors.Is(err, service.ErrInsufficientCapacity):
		return status.Error(codes.FailedPrecondition, "INSUFFICIENT_CAPACITY")
	case errors.Is(err, service.ErrImageUnavailable):
		return status.Error(codes.FailedPrecondition, "IMAGE_UNAVAILABLE")
	case errors.Is(err, service.ErrEngineProfileUnapproved):
		return status.Error(codes.FailedPrecondition, "ENGINE_PROFILE_UNAPPROVED")
	case errors.Is(err, service.ErrReservedFieldConflict):
		return status.Error(codes.FailedPrecondition, "RESERVED_FIELD_CONFLICT")
	case errors.Is(err, service.ErrRuntimeIntentConflict):
		return status.Error(codes.AlreadyExists, "IDEMPOTENCY_CONFLICT")
	case errors.Is(err, domain.ErrInvalidTransition), errors.Is(err, domain.ErrDeleted), errors.Is(err, domain.ErrLegacyQuarantined):
		return status.Error(codes.FailedPrecondition, "INVALID_STATE_TRANSITION")
	default:
		slog.Error("unmapped inference error", "err", err)
		return status.Error(codes.Unavailable, "DEPENDENCY_UNAVAILABLE")
	}
}
