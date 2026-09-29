package grpcapi

import (
	"context"
	"errors"

	"github.com/google/uuid"
	inferenceinternalv1 "github.com/kubercloud/ani/pkg/generated/pb/inference/resolver/v1"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	"github.com/kubercloud/ani/services/inference-service/internal/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// EndpointResolverUseCase is the narrow service boundary for internal endpoint discovery.
type EndpointResolverUseCase interface {
	ResolveInternalEndpoint(context.Context, uuid.UUID, uuid.UUID) (service.InternalEndpoint, error)
	ResolveInternalEndpointByServedModelName(context.Context, uuid.UUID, string) (service.InternalEndpoint, error)
}

// EndpointResolverServer serves only the cluster-internal endpoint resolver RPC.
type EndpointResolverServer struct {
	inferenceinternalv1.UnimplementedInferenceEndpointResolverServer
	resolver EndpointResolverUseCase
}

func NewEndpointResolverServer(resolver EndpointResolverUseCase) *EndpointResolverServer {
	return &EndpointResolverServer{resolver: resolver}
}

func (s *EndpointResolverServer) Register(grpcServer *grpc.Server) {
	inferenceinternalv1.RegisterInferenceEndpointResolverServer(grpcServer, s)
}

func (s *EndpointResolverServer) ResolveInternalEndpoint(ctx context.Context, req *inferenceinternalv1.ResolveInternalEndpointRequest) (*inferenceinternalv1.ResolveInternalEndpointResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "INVALID_ARGUMENT")
	}
	tenantID, err := parseResolverUUID(req.GetTenantId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "INVALID_ARGUMENT")
	}
	if s.resolver == nil {
		return nil, status.Error(codes.Internal, "INTERNAL")
	}
	var endpoint service.InternalEndpoint
	if servedModelName := req.GetServedModelName(); servedModelName != "" {
		endpoint, err = s.resolver.ResolveInternalEndpointByServedModelName(ctx, tenantID, servedModelName)
	} else {
		serviceID, parseErr := parseResolverUUID(req.GetServiceId())
		if parseErr != nil {
			return nil, status.Error(codes.InvalidArgument, "INVALID_ARGUMENT")
		}
		endpoint, err = s.resolver.ResolveInternalEndpoint(ctx, tenantID, serviceID)
	}
	if err != nil {
		return nil, mapEndpointResolverError(err)
	}
	return &inferenceinternalv1.ResolveInternalEndpointResponse{
		TenantId:        endpoint.TenantID.String(),
		ServiceId:       endpoint.ServiceID.String(),
		BaseUrl:         endpoint.BaseURL,
		ServedModelName: endpoint.ServedModelName,
		Task:            string(endpoint.Task),
		Status:          string(endpoint.Status),
	}, nil
}

func parseResolverUUID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errors.New("invalid UUID")
	}
	return id, nil
}

func mapEndpointResolverError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "CANCELED")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "DEADLINE_EXCEEDED")
	case errors.Is(err, repository.ErrNotFound):
		return status.Error(codes.NotFound, "NOT_FOUND")
	case errors.Is(err, service.ErrInferenceServiceNotReady):
		return status.Error(codes.FailedPrecondition, "INFERENCE_SERVICE_NOT_READY")
	case errors.Is(err, service.ErrRuntimeEndpointMissing):
		return status.Error(codes.FailedPrecondition, "RUNTIME_ENDPOINT_MISSING")
	case errors.Is(err, service.ErrRuntimeEndpointInvalid):
		return status.Error(codes.FailedPrecondition, "RUNTIME_ENDPOINT_INVALID")
	case errors.Is(err, service.ErrInvalidInput):
		return status.Error(codes.InvalidArgument, "INVALID_ARGUMENT")
	default:
		return status.Error(codes.Internal, "INTERNAL")
	}
}
