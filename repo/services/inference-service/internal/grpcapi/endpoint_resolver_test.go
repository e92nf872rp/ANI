package grpcapi

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/google/uuid"
	inferenceinternalv1 "github.com/kubercloud/ani/pkg/generated/pb/inference/resolver/v1"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	"github.com/kubercloud/ani/services/inference-service/internal/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type fakeEndpointResolver struct {
	endpoint service.InternalEndpoint
	err      error
}

func (f fakeEndpointResolver) ResolveInternalEndpoint(context.Context, uuid.UUID, uuid.UUID) (service.InternalEndpoint, error) {
	return f.endpoint, f.err
}

func (f fakeEndpointResolver) ResolveInternalEndpointByServedModelName(context.Context, uuid.UUID, string) (service.InternalEndpoint, error) {
	return f.endpoint, f.err
}

func dialEndpointResolver(t *testing.T, usecase EndpointResolverUseCase) inferenceinternalv1.InferenceEndpointResolverClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	NewEndpointResolverServer(usecase).Register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return inferenceinternalv1.NewInferenceEndpointResolverClient(conn)
}

func TestEndpointResolverBufconnReturnsPrivateEndpoint(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	serviceID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	client := dialEndpointResolver(t, fakeEndpointResolver{endpoint: service.InternalEndpoint{
		TenantID: tenantID, ServiceID: serviceID, BaseURL: "http://pw-2222.inference.svc.cluster.local:8000/v1", ServedModelName: "model", Task: domain.InferenceTaskEmbed, Status: domain.StatusRunning,
	}})
	got, err := client.ResolveInternalEndpoint(context.Background(), &inferenceinternalv1.ResolveInternalEndpointRequest{TenantId: tenantID.String(), ServiceId: serviceID.String()})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.GetTenantId() != tenantID.String() || got.GetServiceId() != serviceID.String() || got.GetBaseUrl() != "http://pw-2222.inference.svc.cluster.local:8000/v1" || got.GetServedModelName() != "model" || got.GetTask() != "embed" || got.GetStatus() != "running" {
		t.Fatalf("unexpected response: %+v", got)
	}
}

func TestEndpointResolverBufconnResolvesByServedModelName(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	serviceID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	client := dialEndpointResolver(t, fakeEndpointResolver{endpoint: service.InternalEndpoint{
		TenantID: tenantID, ServiceID: serviceID, BaseURL: "http://pw-2222.inference.svc.cluster.local:8000/v1", ServedModelName: "qwen3.5-0.8b", Task: domain.InferenceTaskEmbed, Status: domain.StatusRunning,
	}})
	got, err := client.ResolveInternalEndpoint(context.Background(), &inferenceinternalv1.ResolveInternalEndpointRequest{TenantId: tenantID.String(), ServedModelName: "qwen3.5-0.8b"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.GetServiceId() != serviceID.String() || got.GetServedModelName() != "qwen3.5-0.8b" || got.GetBaseUrl() == "" {
		t.Fatalf("unexpected response: %+v", got)
	}
}

func TestEndpointResolverBufconnMapsErrors(t *testing.T) {
	tenantID := "11111111-1111-1111-1111-111111111111"
	serviceID := "22222222-2222-2222-2222-222222222222"
	cases := []struct {
		name string
		err  error
		req  *inferenceinternalv1.ResolveInternalEndpointRequest
		code codes.Code
		msg  string
	}{
		{name: "invalid tenant", req: &inferenceinternalv1.ResolveInternalEndpointRequest{TenantId: "bad", ServiceId: serviceID}, code: codes.InvalidArgument, msg: "INVALID_ARGUMENT"},
		{name: "not found", err: repository.ErrNotFound, req: &inferenceinternalv1.ResolveInternalEndpointRequest{TenantId: tenantID, ServiceId: serviceID}, code: codes.NotFound, msg: "NOT_FOUND"},
		{name: "not ready", err: service.ErrInferenceServiceNotReady, req: &inferenceinternalv1.ResolveInternalEndpointRequest{TenantId: tenantID, ServiceId: serviceID}, code: codes.FailedPrecondition, msg: "INFERENCE_SERVICE_NOT_READY"},
		{name: "missing endpoint", err: service.ErrRuntimeEndpointMissing, req: &inferenceinternalv1.ResolveInternalEndpointRequest{TenantId: tenantID, ServiceId: serviceID}, code: codes.FailedPrecondition, msg: "RUNTIME_ENDPOINT_MISSING"},
		{name: "invalid endpoint", err: service.ErrRuntimeEndpointInvalid, req: &inferenceinternalv1.ResolveInternalEndpointRequest{TenantId: tenantID, ServiceId: serviceID}, code: codes.FailedPrecondition, msg: "RUNTIME_ENDPOINT_INVALID"},
		{name: "deadline", err: context.DeadlineExceeded, req: &inferenceinternalv1.ResolveInternalEndpointRequest{TenantId: tenantID, ServiceId: serviceID}, code: codes.DeadlineExceeded, msg: "DEADLINE_EXCEEDED"},
		{name: "internal", err: errors.New("secret storage details"), req: &inferenceinternalv1.ResolveInternalEndpointRequest{TenantId: tenantID, ServiceId: serviceID}, code: codes.Internal, msg: "INTERNAL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := dialEndpointResolver(t, fakeEndpointResolver{err: tc.err})
			_, err := client.ResolveInternalEndpoint(context.Background(), tc.req)
			if status.Code(err) != tc.code || status.Convert(err).Message() != tc.msg {
				t.Fatalf("error = %v, want code=%s message=%q", err, tc.code, tc.msg)
			}
		})
	}
}

func TestEndpointResolverBufconnPreservesContextCancellation(t *testing.T) {
	client := dialEndpointResolver(t, fakeEndpointResolver{err: context.Canceled})
	_, err := client.ResolveInternalEndpoint(context.Background(), &inferenceinternalv1.ResolveInternalEndpointRequest{TenantId: "11111111-1111-1111-1111-111111111111", ServiceId: "22222222-2222-2222-2222-222222222222"})
	if status.Code(err) != codes.Canceled {
		t.Fatalf("code = %s, want Canceled (%v)", status.Code(err), err)
	}
}
