package router

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ModelServiceClient is the Gateway → model-service product API.
// Handlers inject middleware.GetTenantID and must not trust JSON tenant fields.
// GetModelVersion is intentionally absent: it is an internal lookup for
// inference-service, not a tenant HTTP API.
type ModelServiceClient interface {
	ListModels(ctx context.Context, tenantID, status string, limit int32, cursor string) (*modelv1.ListModelsResponse, error)
	CreateModel(ctx context.Context, tenantID string, req *modelv1.CreateModelRequest) (*modelv1.Model, error)
	GetModel(ctx context.Context, tenantID, modelID string) (*modelv1.Model, error)
	DeleteModel(ctx context.Context, tenantID, modelID string) (*emptypb.Empty, error)
	CreateModelVersion(ctx context.Context, tenantID string, req *modelv1.CreateModelVersionRequest) (*modelv1.ModelVersion, error)
}

type modelGRPCClient struct {
	client  modelv1.ModelServiceClient
	timeout time.Duration
}

func DialModelService(ctx context.Context, addr string, timeout time.Duration) (*grpc.ClientConn, ModelServiceClient, error) {
	_ = ctx
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, nil, fmt.Errorf("model-service gRPC address is empty")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial model-service %s: %w", addr, err)
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return conn, &modelGRPCClient{client: modelv1.NewModelServiceClient(conn), timeout: timeout}, nil
}

func (c *modelGRPCClient) callCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.timeout)
}

func (c *modelGRPCClient) ListModels(ctx context.Context, tenantID, status string, limit int32, cursor string) (*modelv1.ListModelsResponse, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.ListModels(callCtx, &modelv1.ListModelsRequest{
		TenantId: tenantID,
		Status:   status,
		Page:     &commonv1.CursorPageRequest{Limit: limit, Cursor: cursor},
	})
}

func (c *modelGRPCClient) CreateModel(ctx context.Context, tenantID string, req *modelv1.CreateModelRequest) (*modelv1.Model, error) {
	if req == nil {
		req = &modelv1.CreateModelRequest{}
	}
	req.TenantId = tenantID
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.CreateModel(callCtx, req)
}

func (c *modelGRPCClient) GetModel(ctx context.Context, tenantID, modelID string) (*modelv1.Model, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.GetModel(callCtx, &modelv1.GetModelRequest{TenantId: tenantID, ModelId: modelID})
}

func (c *modelGRPCClient) DeleteModel(ctx context.Context, tenantID, modelID string) (*emptypb.Empty, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.DeleteModel(callCtx, &modelv1.DeleteModelRequest{TenantId: tenantID, ModelId: modelID})
}

func (c *modelGRPCClient) CreateModelVersion(ctx context.Context, tenantID string, req *modelv1.CreateModelVersionRequest) (*modelv1.ModelVersion, error) {
	if req == nil {
		req = &modelv1.CreateModelVersionRequest{}
	}
	req.TenantId = tenantID
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.CreateModelVersion(callCtx, req)
}

func writeModelUnavailable(c *app.RequestContext) {
	writeInstanceError(c, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "model-service gRPC client not configured")
}

func writeModelUnauthorized(c *app.RequestContext) {
	writeInstanceError(c, http.StatusUnauthorized, "UNAUTHORIZED", "tenant identity is required")
}

func writeModelInvalid(c *app.RequestContext, message string) {
	writeInstanceError(c, http.StatusBadRequest, "INVALID_ARGUMENT", message)
}

func writeModelGRPCError(c *app.RequestContext, err error) {
	if err == nil {
		return
	}
	httpStatus, code, message := mapModelGRPCError(err)
	writeInstanceError(c, httpStatus, code, message)
}

func mapModelGRPCError(err error) (int, string, string) {
	st, ok := status.FromError(err)
	if !ok {
		return http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "model dependency is temporarily unavailable"
	}
	message := strings.TrimSpace(st.Message())
	switch st.Code() {
	case codes.InvalidArgument:
		return http.StatusBadRequest, "INVALID_ARGUMENT", firstNonEmpty(message, "invalid model request")
	case codes.NotFound:
		return http.StatusNotFound, "NOT_FOUND", firstNonEmpty(message, "model not found")
	case codes.AlreadyExists:
		return http.StatusConflict, "CONFLICT", firstNonEmpty(message, "model already exists")
	case codes.Unimplemented:
		return http.StatusNotImplemented, "FEATURE_NOT_AVAILABLE", firstNonEmpty(message, "model feature is not available")
	case codes.Unauthenticated:
		return http.StatusUnauthorized, "UNAUTHORIZED", "tenant identity is required"
	case codes.Unavailable, codes.DeadlineExceeded:
		return http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "model dependency is temporarily unavailable"
	default:
		return http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "model dependency is temporarily unavailable"
	}
}
