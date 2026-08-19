package router

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	inferencecontrolv1 "github.com/kubercloud/ani/pkg/generated/pb/inference/control/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// InferenceControlClient is the Gateway → inference-service internal API.
// Handlers inject middleware.GetTenantID and must not trust JSON tenant fields.
type InferenceControlClient interface {
	ListInferenceServices(ctx context.Context, tenantID string) (*inferencecontrolv1.ListInferenceServicesResponse, error)
	CreateInferenceService(ctx context.Context, tenantID string, req *inferencecontrolv1.CreateInferenceServiceRequest) (*inferencecontrolv1.InferenceService, error)
	GetInferenceService(ctx context.Context, tenantID, serviceID string) (*inferencecontrolv1.InferenceService, error)
	ScaleInferenceService(ctx context.Context, tenantID, serviceID, idempotencyKey string, replicas int32) (*inferencecontrolv1.InferenceOperation, error)
	DeleteInferenceService(ctx context.Context, tenantID, serviceID string) (*inferencecontrolv1.InferenceOperation, error)
	ApplyInferenceServiceLifecycle(ctx context.Context, tenantID, serviceID, idempotencyKey, action string) (*inferencecontrolv1.InferenceOperation, error)
	GetInferenceOperation(ctx context.Context, tenantID, operationID string) (*inferencecontrolv1.InferenceOperation, error)
	ListInferenceServiceLogs(ctx context.Context, tenantID, serviceID string, limit int32, cursor, level string) (*inferencecontrolv1.ListInferenceServiceLogsResponse, error)
}

type inferenceGRPCClient struct {
	client  inferencecontrolv1.InferenceControlClient
	timeout time.Duration
}

// DialInferenceControl 连 inference-service 内部 gRPC。超时默认 30s。
func DialInferenceControl(ctx context.Context, addr string, timeout time.Duration) (*grpc.ClientConn, InferenceControlClient, error) {
	_ = ctx
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, nil, fmt.Errorf("inference-service gRPC address is empty")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial inference-service %s: %w", addr, err)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return conn, &inferenceGRPCClient{client: inferencecontrolv1.NewInferenceControlClient(conn), timeout: timeout}, nil
}

func (c *inferenceGRPCClient) callCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.timeout)
}

func (c *inferenceGRPCClient) ListInferenceServices(ctx context.Context, tenantID string) (*inferencecontrolv1.ListInferenceServicesResponse, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.ListInferenceServices(callCtx, &inferencecontrolv1.ListInferenceServicesRequest{TenantId: tenantID})
}

func (c *inferenceGRPCClient) CreateInferenceService(ctx context.Context, tenantID string, req *inferencecontrolv1.CreateInferenceServiceRequest) (*inferencecontrolv1.InferenceService, error) {
	if req == nil {
		req = &inferencecontrolv1.CreateInferenceServiceRequest{}
	}
	req.TenantId = tenantID
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.CreateInferenceService(callCtx, req)
}

func (c *inferenceGRPCClient) GetInferenceService(ctx context.Context, tenantID, serviceID string) (*inferencecontrolv1.InferenceService, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.GetInferenceService(callCtx, &inferencecontrolv1.GetInferenceServiceRequest{
		TenantId: tenantID, ServiceId: serviceID,
	})
}

func (c *inferenceGRPCClient) ScaleInferenceService(ctx context.Context, tenantID, serviceID, idempotencyKey string, replicas int32) (*inferencecontrolv1.InferenceOperation, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.ScaleInferenceService(callCtx, &inferencecontrolv1.ScaleInferenceServiceRequest{
		TenantId: tenantID, ServiceId: serviceID, IdempotencyKey: idempotencyKey, Replicas: replicas,
	})
}

func (c *inferenceGRPCClient) DeleteInferenceService(ctx context.Context, tenantID, serviceID string) (*inferencecontrolv1.InferenceOperation, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.DeleteInferenceService(callCtx, &inferencecontrolv1.DeleteInferenceServiceRequest{
		TenantId: tenantID, ServiceId: serviceID,
	})
}

func (c *inferenceGRPCClient) ApplyInferenceServiceLifecycle(ctx context.Context, tenantID, serviceID, idempotencyKey, action string) (*inferencecontrolv1.InferenceOperation, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.ApplyInferenceServiceLifecycle(callCtx, &inferencecontrolv1.ApplyInferenceServiceLifecycleRequest{
		TenantId: tenantID, ServiceId: serviceID, IdempotencyKey: idempotencyKey, Action: action,
	})
}

func (c *inferenceGRPCClient) GetInferenceOperation(ctx context.Context, tenantID, operationID string) (*inferencecontrolv1.InferenceOperation, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.GetInferenceOperation(callCtx, &inferencecontrolv1.GetInferenceOperationRequest{
		TenantId: tenantID, OperationId: operationID,
	})
}

func (c *inferenceGRPCClient) ListInferenceServiceLogs(ctx context.Context, tenantID, serviceID string, limit int32, cursor, level string) (*inferencecontrolv1.ListInferenceServiceLogsResponse, error) {
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	return c.client.ListInferenceServiceLogs(callCtx, &inferencecontrolv1.ListInferenceServiceLogsRequest{
		TenantId: tenantID, ServiceId: serviceID, Limit: limit, Cursor: cursor, Level: level,
	})
}

func writeInferenceUnavailable(c *app.RequestContext) {
	writeInstanceError(c, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "inference-service gRPC client not configured")
}

func writeInferenceUnauthorized(c *app.RequestContext) {
	writeInstanceError(c, http.StatusUnauthorized, "UNAUTHORIZED", "tenant identity is required")
}

func writeInferenceInvalid(c *app.RequestContext, message string) {
	writeInstanceError(c, http.StatusBadRequest, "INVALID_ARGUMENT", message)
}

func writeInferenceUnprocessable(c *app.RequestContext, code, message string) {
	writeInstanceError(c, http.StatusUnprocessableEntity, code, message)
}

func writeInferenceGRPCError(c *app.RequestContext, err error) {
	if err == nil {
		return
	}
	httpStatus, code, message := mapInferenceGRPCError(err)
	writeInstanceError(c, httpStatus, code, message)
}

func mapInferenceGRPCError(err error) (int, string, string) {
	st, ok := status.FromError(err)
	if !ok {
		return http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "inference dependency is temporarily unavailable"
	}
	code := strings.TrimSpace(st.Message())
	switch st.Code() {
	case codes.InvalidArgument:
		return http.StatusBadRequest, firstNonEmpty(code, "INVALID_ARGUMENT"), "invalid inference service request"
	case codes.NotFound:
		return http.StatusNotFound, firstNonEmpty(code, "NOT_FOUND"), "inference resource not found"
	case codes.AlreadyExists:
		if code == "IDEMPOTENCY_CONFLICT" {
			return http.StatusConflict, code, "idempotency key was reused with a different request"
		}
		return http.StatusConflict, firstNonEmpty(code, "NAME_CONFLICT"), "inference service name already exists"
	case codes.FailedPrecondition:
		switch code {
		case "OPERATION_IN_PROGRESS":
			return http.StatusConflict, code, "inference service operation is already in progress"
		case "MODEL_NOT_READY", "MODEL_INCOMPATIBLE", "INVALID_STATE_TRANSITION", "UNSUPPORTED_TOPOLOGY", "ACCELERATOR_SPEC_UNAVAILABLE", "INSUFFICIENT_CAPACITY", "IMAGE_UNAVAILABLE", "ENGINE_PROFILE_UNAPPROVED", "RESERVED_FIELD_CONFLICT":
			return http.StatusUnprocessableEntity, code, inferenceUnprocessableMessage(code)
		default:
			return http.StatusUnprocessableEntity, firstNonEmpty(code, "INVALID_STATE_TRANSITION"), "inference service precondition failed"
		}
	case codes.Unauthenticated:
		return http.StatusUnauthorized, "UNAUTHORIZED", "tenant identity is required"
	case codes.Unavailable, codes.DeadlineExceeded:
		return http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "inference dependency is temporarily unavailable"
	default:
		return http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "inference dependency is temporarily unavailable"
	}
}

func inferenceUnprocessableMessage(code string) string {
	switch code {
	case "MODEL_NOT_READY":
		return "model version is not ready for inference"
	case "MODEL_INCOMPATIBLE":
		return "model version has no compatible inference profile"
	case "UNSUPPORTED_TOPOLOGY":
		return "requested inference topology is not supported"
	case "ACCELERATOR_SPEC_UNAVAILABLE":
		return "requested accelerator spec is not available"
	case "INSUFFICIENT_CAPACITY":
		return "inference capacity is insufficient"
	case "IMAGE_UNAVAILABLE":
		return "inference runtime image is unavailable"
	case "INVALID_STATE_TRANSITION":
		return "inference service cannot accept this state transition"
	default:
		return "inference service precondition failed"
	}
}
