package main

import (
	"context"
	"net"
	"testing"
	"time"

	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	"github.com/kubercloud/ani/services/envoy-authz-adapter/internal/extauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

type acceptingValidator struct{}

func (acceptingValidator) ValidateToken(context.Context, string) (*commonv1.TenantContext, error) {
	return &commonv1.TenantContext{TenantId: "tenant-1", ApiKeyId: "key-1"}, nil
}

type acceptingChecker struct{}

func (acceptingChecker) CheckInferenceAccess(context.Context, string, string, string, string, string, string, string, bool) (extauth.AccessDecision, error) {
	return extauth.AccessDecision{HTTPStatus: 200, InferenceServiceID: "service-1"}, nil
}

var _ extauth.TokenValidator = acceptingValidator{}

func TestNewGRPCServerServesHealthAndAuthorization(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := newGRPCServer(acceptingValidator{}, acceptingChecker{})
	defer server.Stop()
	go func() {
		if err := server.Serve(listener); err != nil {
			t.Errorf("serve: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close client connection: %v", err)
		}
	}()

	healthResponse, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil || healthResponse.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("health: %v %#v", err, healthResponse)
	}

	authResponse, err := authv3.NewAuthorizationClient(conn).Check(ctx, &authv3.CheckRequest{Attributes: &authv3.AttributeContext{
		Request: &authv3.AttributeContext_Request{Http: &authv3.AttributeContext_HttpRequest{
			Path:    "/v1/chat/completions",
			Headers: map[string]string{"authorization": "Bearer ani_tenant_secret", "x-ai-eg-model": "ani-qwen3"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if authResponse.GetStatus().GetCode() != int32(codes.OK) {
		t.Fatalf("authorization status: %#v", authResponse.GetStatus())
	}
	if authResponse.GetOkResponse() == nil {
		t.Fatalf("authorization response is not allowed: %#v", authResponse)
	}
}

func TestRunReturnsErrorForInvalidConfiguration(t *testing.T) {
	t.Setenv("AUTH_SERVICE_GRPC_ADDR", "")
	t.Setenv("INFERENCE_SERVICE_GRPC_ADDR", "inference:9112")

	if err := run(); err == nil {
		t.Fatal("run() accepted an invalid configuration")
	}
}

func TestRunReturnsErrorWhenInferenceServiceIsNotConfigured(t *testing.T) {
	t.Setenv("AUTH_SERVICE_GRPC_ADDR", "auth:9101")
	t.Setenv("INFERENCE_SERVICE_GRPC_ADDR", "")

	if err := run(); err == nil {
		t.Fatal("run() accepted a missing inference service address")
	}
}
