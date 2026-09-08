package grpcapi

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
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

type AccessPolicyUseCase interface {
	CheckAccess(context.Context, service.AccessCheckInput) (service.AccessDecision, error)
	ReleaseAccessLease(context.Context, string) error
}

type AccessPolicyControlUseCase interface {
	ListPolicies(context.Context, uuid.UUID) ([]domain.AccessPolicy, error)
	GetPolicy(context.Context, uuid.UUID, uuid.UUID) (domain.AccessPolicy, error)
	CreatePolicy(context.Context, domain.AccessPolicy, uuid.UUID) (domain.AccessPolicy, error)
	UpdatePolicy(context.Context, domain.AccessPolicy, uuid.UUID, string) (domain.AccessPolicy, error)
	DeletePolicy(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
	ListServicePolicies(context.Context, uuid.UUID, uuid.UUID) ([]domain.AccessPolicy, error)
	ReplaceServicePolicies(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID, uuid.UUID) ([]domain.AccessPolicy, error)
	ListEvents(context.Context, uuid.UUID, domain.AccessPolicyEventQuery) ([]domain.AccessPolicyEvent, string, error)
}

// Server 实现 InferenceControl gRPC。Gateway HTTP 只调这里，不直连 Core。
type Server struct {
	inferencecontrolv1.UnimplementedInferenceControlServer
	creator       CreateUseCase
	controller    ControlUseCase
	logs          LogsUseCase
	policies      AccessPolicyUseCase
	policyControl AccessPolicyControlUseCase
}

func NewServer(creator CreateUseCase, controller ControlUseCase) *Server {
	return &Server{creator: creator, controller: controller}
}

func (s *Server) WithLogs(logs LogsUseCase) *Server {
	s.logs = logs
	return s
}

func (s *Server) WithAccessPolicies(policies AccessPolicyUseCase) *Server {
	s.policies = policies
	return s
}

func (s *Server) WithAccessPolicyControl(control AccessPolicyControlUseCase) *Server {
	s.policyControl = control
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

func (s *Server) CheckInferenceAccess(ctx context.Context, req *inferencecontrolv1.CheckInferenceAccessRequest) (*inferencecontrolv1.CheckInferenceAccessResponse, error) {
	if s.policies == nil {
		return nil, status.Error(codes.Unavailable, "policy service unavailable")
	}
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	keyID, err := parseResourceID(req.GetApiKeyId(), "api_key_id")
	if err != nil {
		return nil, mapError(err)
	}
	userID := uuid.Nil
	if req.GetUserId() != "" {
		userID, err = parseResourceID(req.GetUserId(), "user_id")
		if err != nil {
			return nil, mapError(err)
		}
	}
	decision, err := s.policies.CheckAccess(ctx, service.AccessCheckInput{
		TenantID: tenantID, UserID: userID, APIKeyID: keyID, KeyPrefix: req.GetKeyPrefix(),
		ServedModelName: strings.TrimSpace(req.GetServedModelName()), OpenAIPath: req.GetOpenaiPath(),
		RequestID: req.GetRequestId(), Stream: req.GetStream(),
	})
	if err != nil && decision.HTTPStatus == 0 {
		return nil, status.Error(codes.Unavailable, "policy check unavailable")
	}
	return &inferencecontrolv1.CheckInferenceAccessResponse{
		Decision: decision.Decision, HttpStatus: int32(decision.HTTPStatus), ReasonCode: decision.ReasonCode,
		PolicyId: uuidString(decision.PolicyID), LeaseId: decision.LeaseID,
		RetryAfterSeconds: int32(decision.RetryAfter.Seconds()), InferenceServiceId: uuidString(decision.InferenceServiceID),
	}, nil
}

func uuidString(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

func (s *Server) ReleaseInferenceAccessLease(ctx context.Context, req *inferencecontrolv1.ReleaseInferenceAccessLeaseRequest) (*inferencecontrolv1.ReleaseInferenceAccessLeaseResponse, error) {
	if s.policies == nil {
		return &inferencecontrolv1.ReleaseInferenceAccessLeaseResponse{}, nil
	}
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	_ = tenantID // lease ownership is enforced by the policy service/store
	if err := s.policies.ReleaseAccessLease(ctx, req.GetLeaseId()); err != nil {
		return nil, status.Error(codes.Unavailable, "policy lease release unavailable")
	}
	return &inferencecontrolv1.ReleaseInferenceAccessLeaseResponse{}, nil
}

func (s *Server) ListInferenceAccessPolicies(ctx context.Context, req *inferencecontrolv1.ListInferenceAccessPoliciesRequest) (*inferencecontrolv1.ListInferenceAccessPoliciesResponse, error) {
	if s.policyControl == nil {
		return nil, status.Error(codes.Unavailable, "policy service unavailable")
	}
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	items, err := s.policyControl.ListPolicies(ctx, tenantID)
	if err != nil {
		return nil, mapError(err)
	}
	out := &inferencecontrolv1.ListInferenceAccessPoliciesResponse{}
	for _, item := range items {
		out.Items = append(out.Items, protoAccessPolicy(item))
	}
	return out, nil
}

func (s *Server) GetInferenceAccessPolicy(ctx context.Context, req *inferencecontrolv1.GetInferenceAccessPolicyRequest) (*inferencecontrolv1.InferenceAccessPolicy, error) {
	if s.policyControl == nil {
		return nil, status.Error(codes.Unavailable, "policy service unavailable")
	}
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	policyID, err := parseResourceID(req.GetPolicyId(), "policy_id")
	if err != nil {
		return nil, mapError(err)
	}
	item, err := s.policyControl.GetPolicy(ctx, tenantID, policyID)
	if err != nil {
		return nil, mapError(err)
	}
	return protoAccessPolicy(item), nil
}

func (s *Server) CreateInferenceAccessPolicy(ctx context.Context, req *inferencecontrolv1.CreateInferenceAccessPolicyRequest) (*inferencecontrolv1.InferenceAccessPolicy, error) {
	if s.policyControl == nil {
		return nil, status.Error(codes.Unavailable, "policy service unavailable")
	}
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	key, err := parseResourceID(req.GetIdempotencyKey(), "idempotency_key")
	if err != nil {
		return nil, mapError(err)
	}
	policy, err := domainAccessPolicy(req.GetPolicy(), tenantID)
	if err != nil {
		return nil, mapError(err)
	}
	item, err := s.policyControl.CreatePolicy(ctx, policy, key)
	if err != nil {
		return nil, mapError(err)
	}
	return protoAccessPolicy(item), nil
}

func (s *Server) PatchInferenceAccessPolicy(ctx context.Context, req *inferencecontrolv1.PatchInferenceAccessPolicyRequest) (*inferencecontrolv1.InferenceAccessPolicy, error) {
	if s.policyControl == nil {
		return nil, status.Error(codes.Unavailable, "policy service unavailable")
	}
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	key, err := parseResourceID(req.GetIdempotencyKey(), "idempotency_key")
	if err != nil {
		return nil, mapError(err)
	}
	requestHash, err := parsePolicyPatchRequestHash(req.GetRequestHash())
	if err != nil {
		return nil, mapError(err)
	}
	policy, err := domainAccessPolicy(req.GetPolicy(), tenantID)
	if err != nil {
		return nil, mapError(err)
	}
	policy.ID, err = parseResourceID(req.GetPolicyId(), "policy_id")
	if err != nil {
		return nil, mapError(err)
	}
	item, err := s.policyControl.UpdatePolicy(ctx, policy, key, requestHash)
	if err != nil {
		return nil, mapError(err)
	}
	return protoAccessPolicy(item), nil
}

var policyPatchRequestHashPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func parsePolicyPatchRequestHash(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !policyPatchRequestHashPattern.MatchString(value) {
		return "", errInvalidArgument
	}
	return value, nil
}

func (s *Server) DeleteInferenceAccessPolicy(ctx context.Context, req *inferencecontrolv1.DeleteInferenceAccessPolicyRequest) (*inferencecontrolv1.DeleteInferenceAccessPolicyResponse, error) {
	if s.policyControl == nil {
		return nil, status.Error(codes.Unavailable, "policy service unavailable")
	}
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	policyID, err := parseResourceID(req.GetPolicyId(), "policy_id")
	if err != nil {
		return nil, mapError(err)
	}
	key, err := parseResourceID(req.GetIdempotencyKey(), "idempotency_key")
	if err != nil {
		return nil, mapError(err)
	}
	if err := s.policyControl.DeletePolicy(ctx, tenantID, policyID, key); err != nil {
		return nil, mapError(err)
	}
	return &inferencecontrolv1.DeleteInferenceAccessPolicyResponse{}, nil
}

func (s *Server) ListInferenceServicePolicies(ctx context.Context, req *inferencecontrolv1.ListInferenceServicePoliciesRequest) (*inferencecontrolv1.InferenceServicePolicies, error) {
	if s.policyControl == nil {
		return nil, status.Error(codes.Unavailable, "policy service unavailable")
	}
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	serviceID, err := parseResourceID(req.GetInferenceServiceId(), "inference_service_id")
	if err != nil {
		return nil, mapError(err)
	}
	items, err := s.policyControl.ListServicePolicies(ctx, tenantID, serviceID)
	if err != nil {
		return nil, mapError(err)
	}
	out := &inferencecontrolv1.InferenceServicePolicies{InferenceServiceId: serviceID.String()}
	for _, item := range items {
		out.Policies = append(out.Policies, protoAccessPolicy(item))
	}
	return out, nil
}

func (s *Server) UpdateInferenceServicePolicies(ctx context.Context, req *inferencecontrolv1.UpdateInferenceServicePoliciesRequest) (*inferencecontrolv1.InferenceServicePolicies, error) {
	if s.policyControl == nil {
		return nil, status.Error(codes.Unavailable, "policy service unavailable")
	}
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	serviceID, err := parseResourceID(req.GetInferenceServiceId(), "inference_service_id")
	if err != nil {
		return nil, mapError(err)
	}
	key, err := parseResourceID(req.GetIdempotencyKey(), "idempotency_key")
	if err != nil {
		return nil, mapError(err)
	}
	ids := make([]uuid.UUID, 0, len(req.GetPolicyIds()))
	for _, raw := range req.GetPolicyIds() {
		id, parseErr := parseResourceID(raw, "policy_id")
		if parseErr != nil {
			return nil, mapError(parseErr)
		}
		ids = append(ids, id)
	}
	items, err := s.policyControl.ReplaceServicePolicies(ctx, tenantID, serviceID, ids, key)
	if err != nil {
		return nil, mapError(err)
	}
	out := &inferencecontrolv1.InferenceServicePolicies{InferenceServiceId: serviceID.String()}
	for _, item := range items {
		out.Policies = append(out.Policies, protoAccessPolicy(item))
	}
	return out, nil
}

func (s *Server) ListInferencePolicyEvents(ctx context.Context, req *inferencecontrolv1.ListInferencePolicyEventsRequest) (*inferencecontrolv1.InferencePolicyEventListResponse, error) {
	if s.policyControl == nil {
		return nil, status.Error(codes.Unavailable, "policy service unavailable")
	}
	tenantID, err := parseTenantID(req.GetTenantId())
	if err != nil {
		return nil, mapError(err)
	}
	query := domain.AccessPolicyEventQuery{Decision: req.GetDecision(), Limit: int(req.GetLimit()), Cursor: req.GetCursor()}
	if raw := req.GetInferenceServiceId(); raw != "" {
		id, parseErr := parseResourceID(raw, "inference_service_id")
		if parseErr != nil {
			return nil, mapError(parseErr)
		}
		query.InferenceServiceID = &id
	}
	if raw := req.GetPolicyId(); raw != "" {
		id, parseErr := parseResourceID(raw, "policy_id")
		if parseErr != nil {
			return nil, mapError(parseErr)
		}
		query.PolicyID = &id
	}
	if raw := req.GetApiKeyId(); raw != "" {
		id, parseErr := parseResourceID(raw, "api_key_id")
		if parseErr != nil {
			return nil, mapError(parseErr)
		}
		query.APIKeyID = &id
	}
	items, cursor, err := s.policyControl.ListEvents(ctx, tenantID, query)
	if err != nil {
		return nil, mapError(err)
	}
	out := &inferencecontrolv1.InferencePolicyEventListResponse{NextCursor: cursor}
	for _, item := range items {
		out.Items = append(out.Items, protoAccessPolicyEvent(item))
	}
	return out, nil
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
