package extauth

import (
	"context"
	"net/http"
	"strings"

	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

const (
	targetTenantKey  = "ani.target_tenant_id"
	targetServiceKey = "ani.inference_service_id"
)

type TokenValidator interface {
	ValidateToken(context.Context, string) (*commonv1.TenantContext, error)
}

type Server struct {
	authv3.UnimplementedAuthorizationServer
	validator TokenValidator
}

func New(validator TokenValidator) *Server {
	return &Server{validator: validator}
}

func (s *Server) Check(ctx context.Context, request *authv3.CheckRequest) (*authv3.CheckResponse, error) {
	attributes := request.GetAttributes()
	targetTenant := attributes.GetContextExtensions()[targetTenantKey]
	if targetTenant == "" || attributes.GetContextExtensions()[targetServiceKey] == "" {
		return denied(http.StatusServiceUnavailable), nil
	}

	token, ok := bearerAPIKey(normalizeHeaders(attributes.GetRequest().GetHttp().GetHeaders()))
	if !ok {
		return denied(http.StatusUnauthorized), nil
	}

	principal, err := s.validator.ValidateToken(ctx, token)
	if err != nil {
		switch grpcstatus.Code(err) {
		case codes.Unauthenticated:
			return denied(http.StatusUnauthorized), nil
		case codes.ResourceExhausted:
			return denied(http.StatusTooManyRequests), nil
		default:
			return denied(http.StatusServiceUnavailable), nil
		}
	}
	if principal.GetTenantId() != targetTenant {
		return denied(http.StatusNotFound), nil
	}

	return &authv3.CheckResponse{
		Status: &status.Status{Code: int32(codes.OK)},
		HttpResponse: &authv3.CheckResponse_OkResponse{OkResponse: &authv3.OkHttpResponse{
			HeadersToRemove: []string{"authorization", "x-api-key", "x-ani-tenant-id", "x-ani-user-id"},
		}},
	}, nil
}

func bearerAPIKey(headers map[string]string) (string, bool) {
	raw := strings.TrimSpace(headers["authorization"])
	parts := strings.SplitN(raw, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	token := strings.TrimSpace(parts[1])
	return token, strings.HasPrefix(token, "ani_")
}

func normalizeHeaders(headers map[string]string) map[string]string {
	normalized := make(map[string]string, len(headers))
	for name, value := range headers {
		normalized[strings.ToLower(name)] = value
	}
	return normalized
}

func denied(httpStatus int) *authv3.CheckResponse {
	return &authv3.CheckResponse{
		Status: &status.Status{Code: int32(codes.PermissionDenied)},
		HttpResponse: &authv3.CheckResponse_DeniedResponse{DeniedResponse: &authv3.DeniedHttpResponse{
			Status: &typev3.HttpStatus{Code: typev3.StatusCode(httpStatus)},
		}},
	}
}
