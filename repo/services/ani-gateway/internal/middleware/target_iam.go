package middleware

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
	"github.com/kubercloud/ani/services/ani-gateway/internal/targetiam"
	iamv1 "github.com/kubercloud/ani/services/ani-gateway/internal/targetiam/gen"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const targetIAMHandledContextKey = "ani.target_iam.handled"

// TargetIAMAuthorization selects only the DP2-05 listInstances tracer route.
// Once selected, every error fails closed and never falls back to the legacy
// AuthService chain. A nil client is valid only when startup explicitly selects
// IAM_TARGET_MODE=disabled; target mode never falls back per request.
func TargetIAMAuthorization(client targetiam.Client, registry authz.TargetOperationRegistry) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if client == nil || string(c.Method()) != http.MethodGet || authz.NormalizeHertzFullPath(string(c.FullPath())) != "/api/v1/instances" {
			c.Next(ctx)
			return
		}

		stripClientTargetHeaders(c)
		policy, err := registry.Lookup(http.MethodGet, "/api/v1/instances", authz.TargetPolicyRevision)
		if err != nil {
			writeTargetIAMError(c, targetRegistryReason(err))
			return
		}
		if policy.OperationID != "listInstances" ||
			policy.AuthClassification != authz.TargetAuthClassificationAuthorized ||
			policy.IAMDecision != authz.TargetIAMDecisionCheckPermission ||
			policy.IAMDecisionCalls != 1 || len(policy.Obligations) != 0 {
			writeTargetIAMError(c, authz.TargetReasonOperationUnregistered)
			return
		}

		credential, err := targetBearerCredential(c)
		if err != nil {
			writeTargetIAMError(c, "CREDENTIAL_INVALID")
			return
		}
		tenantID, err := untrustedTargetTenantHint(credential)
		if err != nil {
			writeTargetIAMError(c, "CREDENTIAL_INVALID")
			return
		}
		response, err := client.CheckPermission(ctx, &iamv1.CheckPermissionRequest{
			Credential:     &iamv1.BearerCredential{Value: credential},
			OperationId:    policy.OperationID,
			PolicyRevision: registry.Revision(),
			Target:         &iamv1.AuthorizationTarget{TenantId: tenantID},
		})
		if err != nil {
			writeTargetIAMError(c, targetIAMReason(err))
			return
		}
		decision := response.GetDecision()
		if decision == nil {
			writeTargetIAMError(c, "IAM_UNAVAILABLE")
			return
		}
		if decision.GetPolicyRevision() != registry.Revision() {
			writeTargetIAMError(c, "AUTHZ_POLICY_MISMATCH")
			return
		}
		if !decision.GetAllowed() {
			writeTargetIAMError(c, "PERMISSION_DENIED")
			return
		}
		principal, trusted, err := targetPrincipalContext(decision, tenantID)
		if err != nil {
			writeTargetIAMError(c, "IAM_UNAVAILABLE")
			return
		}

		SetPrincipal(c, principal)
		ctx, err = InstallGeneratedPrincipalContext(ctx, c, principal)
		if err != nil {
			writeTargetIAMError(c, "IAM_UNAVAILABLE")
			return
		}
		if err := installTargetHeaders(c, trusted); err != nil {
			writeTargetIAMError(c, "IAM_UNAVAILABLE")
			return
		}
		c.Set(targetIAMHandledContextKey, true)
		c.Next(ctx)
	}
}

func isTargetIAMHandled(c *app.RequestContext) bool {
	value, ok := c.Get(targetIAMHandledContextKey)
	handled, valid := value.(bool)
	return ok && valid && handled
}

func stripClientTargetHeaders(c *app.RequestContext) {
	keys := make([]string, 0, 8)
	c.Request.Header.VisitAll(func(key, _ []byte) {
		if strings.HasPrefix(strings.ToLower(string(key)), "x-ani-") {
			keys = append(keys, string(key))
		}
	})
	for _, key := range keys {
		c.Request.Header.Del(key)
	}
}

func targetBearerCredential(c *app.RequestContext) (string, error) {
	value := strings.TrimSpace(string(c.GetHeader("Authorization")))
	if !strings.HasPrefix(value, "Bearer ") {
		return "", errors.New("bearer credential required")
	}
	credential := strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
	if credential == "" {
		return "", errors.New("bearer credential required")
	}
	return credential, nil
}

// untrustedTargetTenantHint extracts only the tenant selector from the JWT
// payload. It is never treated as authenticated: IAM verifies the signature
// and compares the resulting claim with AuthorizationTarget before allowing.
func untrustedTargetTenantHint(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("malformed access token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("malformed access token")
	}
	var claims struct {
		TenantID string `json:"tenant_id"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", errors.New("malformed access token")
	}
	tenantID, err := uuid.Parse(strings.TrimSpace(claims.TenantID))
	if err != nil || tenantID == uuid.Nil {
		return "", errors.New("access token tenant is invalid")
	}
	return tenantID.String(), nil
}

func targetPrincipalContext(decision *iamv1.AuthorizationDecision, targetTenantID string) (authz.Principal, map[string]string, error) {
	value := decision.GetPrincipal()
	if value == nil || value.GetPrincipalStatus() != iamv1.PrincipalStatus_PRINCIPAL_STATUS_ACTIVE {
		return authz.Principal{}, nil, errors.New("active principal required")
	}
	tenant := value.GetBoundary().GetTenant()
	if tenant == nil || tenant.GetTenantId() != targetTenantID {
		return authz.Principal{}, nil, errors.New("principal tenant mismatch")
	}
	for _, id := range []string{decision.GetDecisionId(), value.GetPrincipalId(), value.GetSessionId(), value.GetGrantId()} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return authz.Principal{}, nil, errors.New("invalid target IAM identifier")
		}
	}

	var kind authz.PrincipalKind
	var kindHeader string
	switch value.GetPrincipalType() {
	case iamv1.PrincipalType_PRINCIPAL_TYPE_HUMAN:
		kind = authz.PrincipalUser
		kindHeader = "human"
	case iamv1.PrincipalType_PRINCIPAL_TYPE_SERVICE:
		kind = authz.PrincipalService
		kindHeader = "service"
	default:
		return authz.Principal{}, nil, errors.New("unsupported target principal type")
	}
	methods, err := targetAuthnMethods(value.GetAuthnMethods())
	if err != nil {
		return authz.Principal{}, nil, err
	}
	principal := authz.Principal{
		Kind:             kind,
		CredentialScheme: authz.CredentialBearer,
		CredentialDomain: authz.DomainTenant,
		TenantID:         targetTenantID,
		SubjectID:        value.GetPrincipalId(),
	}
	if err := principal.Validate(); err != nil {
		return authz.Principal{}, nil, err
	}
	return principal, map[string]string{
		"x-ani-authn-method":   methods,
		"x-ani-boundary":       "tenant",
		"x-ani-decision-id":    decision.GetDecisionId(),
		"x-ani-grant-id":       value.GetGrantId(),
		"x-ani-principal-id":   value.GetPrincipalId(),
		"x-ani-principal-type": kindHeader,
		"x-ani-session-id":     value.GetSessionId(),
		"x-ani-tenant-id":      targetTenantID,
	}, nil
}

func targetAuthnMethods(values []iamv1.AuthnMethod) (string, error) {
	if len(values) == 0 {
		return "", errors.New("authentication method required")
	}
	methods := make([]string, 0, len(values))
	for _, value := range values {
		switch value {
		case iamv1.AuthnMethod_AUTHN_METHOD_PASSWORD:
			methods = append(methods, "password")
		case iamv1.AuthnMethod_AUTHN_METHOD_OIDC:
			methods = append(methods, "oidc")
		case iamv1.AuthnMethod_AUTHN_METHOD_API_KEY:
			methods = append(methods, "api_key")
		case iamv1.AuthnMethod_AUTHN_METHOD_SERVICE_TOKEN:
			methods = append(methods, "service_token")
		default:
			return "", errors.New("unknown authentication method")
		}
	}
	return strings.Join(methods, ","), nil
}

func installTargetHeaders(c *app.RequestContext, trusted map[string]string) error {
	validated, err := authz.BuildTargetForwardHeaders(make(http.Header), trusted)
	if err != nil {
		return err
	}
	for key, values := range validated {
		if len(values) != 1 {
			return errors.New("trusted target header must have one value")
		}
		c.Request.Header.Set(key, values[0])
	}
	return nil
}

func targetRegistryReason(err error) string {
	if authz.IsTargetRegistryFailure(err, authz.TargetReasonPolicyMismatch) {
		return authz.TargetReasonPolicyMismatch
	}
	return authz.TargetReasonOperationUnregistered
}

func targetIAMReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		return "IAM_TIMEOUT"
	}
	grpcStatus := status.Convert(err)
	for _, detail := range grpcStatus.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if !ok || info.GetDomain() != "iam.ani.internal" {
			continue
		}
		if _, known := authz.TargetStableErrorForReason(info.GetReason()); known {
			return info.GetReason()
		}
	}
	switch grpcStatus.Code() {
	case codes.Unauthenticated:
		return "CREDENTIAL_INVALID"
	case codes.PermissionDenied:
		return "PERMISSION_DENIED"
	case codes.DeadlineExceeded:
		return "IAM_TIMEOUT"
	default:
		return "IAM_UNAVAILABLE"
	}
}

func writeTargetIAMError(c *app.RequestContext, reason string) {
	stable, ok := authz.TargetStableErrorForReason(reason)
	if !ok {
		stable, _ = authz.TargetStableErrorForReason("IAM_UNAVAILABLE")
	}
	message := map[string]string{
		"CREDENTIAL_INVALID":           "credential is invalid",
		"PERMISSION_DENIED":            "permission denied",
		"AUTHZ_POLICY_MISMATCH":        "authorization policy revision mismatch",
		"AUTHZ_OPERATION_UNREGISTERED": "authorization operation is unregistered",
		"IAM_TIMEOUT":                  "IAM operation timed out",
		"IAM_UNAVAILABLE":              "IAM dependency is unavailable",
	}[stable.Code]
	respondError(c, stable.HTTPStatus, stable.Code, message)
}
