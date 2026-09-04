package extauth

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

type TokenValidator interface {
	ValidateToken(context.Context, string) (*commonv1.TenantContext, error)
}

type AccessChecker interface {
	CheckInferenceAccess(context.Context, string, string, string, string, string, string, string, bool) (AccessDecision, error)
}

type AccessDecision struct {
	HTTPStatus         int
	InferenceServiceID string
	// LeaseID is reserved for a future request-completion channel. Standard ext_authz
	// has no upstream completion hook, so it is never injected upstream or logged.
	LeaseID           string
	RetryAfterSeconds int
}

type Server struct {
	authv3.UnimplementedAuthorizationServer
	validator TokenValidator
	checker   AccessChecker
}

func New(validator TokenValidator) *Server {
	return &Server{validator: validator}
}

func (s *Server) WithAccessChecker(checker AccessChecker) *Server { s.checker = checker; return s }

func (s *Server) Check(ctx context.Context, request *authv3.CheckRequest) (*authv3.CheckResponse, error) {
	attributes := request.GetAttributes()
	httpRequest := attributes.GetRequest().GetHttp()
	headers := normalizeHeaders(httpRequest.GetHeaders())
	token, ok := bearerAPIKey(headers)
	if !ok {
		return denied(http.StatusUnauthorized, 0), nil
	}

	principal, err := s.validator.ValidateToken(ctx, token)
	if err != nil {
		switch grpcstatus.Code(err) {
		case codes.Unauthenticated:
			return denied(http.StatusUnauthorized, 0), nil
		case codes.ResourceExhausted:
			return denied(http.StatusTooManyRequests, retryAfterSecondsFromError(err)), nil
		default:
			return denied(http.StatusServiceUnavailable, 0), nil
		}
	}
	path := normalizeOpenAIPath(httpRequest.GetPath())
	if path == "/v1/models" || (path != "/v1/chat/completions" && path != "/v1/embeddings") || strings.TrimSpace(headers["x-ai-eg-model"]) == "" {
		return denied(http.StatusNotFound, 0), nil
	}
	tenantID := strings.TrimSpace(principal.GetTenantId())
	apiKeyID := strings.TrimSpace(principal.GetApiKeyId())
	if s.checker == nil || tenantID == "" || apiKeyID == "" {
		return denied(http.StatusServiceUnavailable, 0), nil
	}
	decision, checkErr := s.checker.CheckInferenceAccess(ctx, tenantID, principal.GetUserId(), apiKeyID, keyPrefix(token), strings.TrimSpace(headers["x-ai-eg-model"]), path, httpRequest.GetId(), strings.Contains(strings.ToLower(headers["accept"]), "text/event-stream"))
	if checkErr != nil {
		return denied(http.StatusServiceUnavailable, 0), nil
	}
	if decision.HTTPStatus != http.StatusOK {
		if !isPolicyHTTPStatus(decision.HTTPStatus) {
			return denied(http.StatusServiceUnavailable, 0), nil
		}
		return denied(decision.HTTPStatus, decision.RetryAfterSeconds), nil
	}
	serviceID := strings.TrimSpace(decision.InferenceServiceID)
	if serviceID == "" {
		return denied(http.StatusServiceUnavailable, 0), nil
	}

	return &authv3.CheckResponse{
		Status: &status.Status{Code: int32(codes.OK)},
		HttpResponse: &authv3.CheckResponse_OkResponse{OkResponse: &authv3.OkHttpResponse{
			Headers: []*corev3.HeaderValueOption{
				trustedHeader("x-ani-tenant-id", tenantID),
				trustedHeader("x-ani-inference-service-id", serviceID),
			},
			HeadersToRemove: []string{"authorization", "x-api-key", "x-ani-user-id"},
		}},
	}, nil
}

func retryAfterSecondsFromError(err error) int {
	st, ok := grpcstatus.FromError(err)
	if !ok {
		return 0
	}
	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if !ok || info.GetReason() != "RATE_LIMITED" {
			continue
		}
		seconds, parseErr := strconv.Atoi(info.GetMetadata()["retry_after_seconds"])
		if parseErr != nil || seconds <= 0 {
			return 0
		}
		return seconds
	}
	return 0
}

func keyPrefix(token string) string {
	runes := []rune(token)
	if len(runes) > 12 {
		runes = runes[:12]
	}
	return string(runes)
}

func normalizeOpenAIPath(path string) string {
	path = strings.TrimSpace(path)
	if beforeQuery, _, found := strings.Cut(path, "?"); found {
		path = beforeQuery
	}
	return path
}

func isPolicyHTTPStatus(httpStatus int) bool {
	switch httpStatus {
	case http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return true
	default:
		return false
	}
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

func trustedHeader(key, value string) *corev3.HeaderValueOption {
	return &corev3.HeaderValueOption{
		Header:       &corev3.HeaderValue{Key: key, Value: value},
		AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
	}
}

func denied(httpStatus, retryAfterSeconds int) *authv3.CheckResponse {
	response := &authv3.DeniedHttpResponse{Status: &typev3.HttpStatus{Code: typev3.StatusCode(httpStatus)}}
	if httpStatus == http.StatusTooManyRequests && retryAfterSeconds > 0 {
		response.Headers = []*corev3.HeaderValueOption{trustedHeader("retry-after", strconv.Itoa(retryAfterSeconds))}
	}
	return &authv3.CheckResponse{
		Status:       &status.Status{Code: int32(codes.PermissionDenied)},
		HttpResponse: &authv3.CheckResponse_DeniedResponse{DeniedResponse: response},
	}
}
