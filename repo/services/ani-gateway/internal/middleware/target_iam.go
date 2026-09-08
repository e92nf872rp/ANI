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
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

const (
	targetIAMHandledContextKey       = "ani.target_iam.handled"
	targetIAMPasswordLoginContextKey = "ani.target_iam.password_login"
)

// TargetIAMAuthorization selects only the DP2-05 listInstances tracer route.
// Once selected, every error fails closed and never falls back to the legacy
// AuthService chain. A nil client is the disabled compatibility mode, including
// an unset IAM_TARGET_MODE; target mode never falls back per request.
func TargetIAMAuthorization(client ports.TargetIAM, registry authz.TargetOperationRegistry) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		method := string(c.Method())
		path := authz.NormalizeHertzFullPath(string(c.FullPath()))
		if client != nil && method == http.MethodPost && path == "/api/v1/auth/password/login" {
			c.Set(targetIAMPasswordLoginContextKey, true)
			c.Next(ctx)
			return
		}
		if client == nil || method != http.MethodGet || path != "/api/v1/instances" {
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
		decision, err := client.CheckPermission(ctx, ports.TargetIAMCheckPermissionRequest{
			Credential:     credential,
			OperationID:    policy.OperationID,
			PolicyRevision: registry.Revision(),
			TenantID:       tenantID,
		})
		if err != nil {
			reason, retryAfter := targetIAMReason(err)
			if retryAfter != "" {
				c.Header("Retry-After", retryAfter)
			}
			writeTargetIAMError(c, reason)
			return
		}
		if !decision.Present {
			writeTargetIAMError(c, "IAM_UNAVAILABLE")
			return
		}
		if decision.PolicyRevision != registry.Revision() {
			writeTargetIAMError(c, "AUTHZ_POLICY_MISMATCH")
			return
		}
		if !decision.Allowed {
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

func isTargetIAMPasswordLogin(c *app.RequestContext) bool {
	value, ok := c.Get(targetIAMPasswordLoginContextKey)
	targetLogin, valid := value.(bool)
	return ok && valid && targetLogin
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

func targetPrincipalContext(decision ports.TargetIAMAuthorizationDecision, targetTenantID string) (authz.Principal, map[string]string, error) {
	value := decision.Principal
	if value.Status != ports.TargetIAMPrincipalActive {
		return authz.Principal{}, nil, errors.New("active principal required")
	}
	if value.Boundary.Type != ports.TargetIAMBoundaryTenant || value.Boundary.TenantID != targetTenantID {
		return authz.Principal{}, nil, errors.New("principal tenant mismatch")
	}
	for _, id := range []string{decision.DecisionID, value.ID, value.SessionID, value.GrantID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return authz.Principal{}, nil, errors.New("invalid target IAM identifier")
		}
	}

	var kind authz.PrincipalKind
	var kindHeader string
	switch value.Type {
	case ports.TargetIAMPrincipalHuman:
		kind = authz.PrincipalUser
		kindHeader = "human"
	case ports.TargetIAMPrincipalService:
		kind = authz.PrincipalService
		kindHeader = "service"
	default:
		return authz.Principal{}, nil, errors.New("unsupported target principal type")
	}
	methods, err := targetAuthnMethods(value.AuthnMethods)
	if err != nil {
		return authz.Principal{}, nil, err
	}
	principal := authz.Principal{
		Kind:             kind,
		CredentialScheme: authz.CredentialBearer,
		CredentialDomain: authz.DomainTenant,
		TenantID:         targetTenantID,
		SubjectID:        value.ID,
	}
	if err := principal.Validate(); err != nil {
		return authz.Principal{}, nil, err
	}
	return principal, map[string]string{
		"x-ani-authn-method":   methods,
		"x-ani-boundary":       "tenant",
		"x-ani-decision-id":    decision.DecisionID,
		"x-ani-grant-id":       value.GrantID,
		"x-ani-principal-id":   value.ID,
		"x-ani-principal-type": kindHeader,
		"x-ani-session-id":     value.SessionID,
		"x-ani-tenant-id":      targetTenantID,
	}, nil
}

func targetAuthnMethods(values []ports.TargetIAMAuthnMethod) (string, error) {
	if len(values) == 0 {
		return "", errors.New("authentication method required")
	}
	methods := make([]string, 0, len(values))
	for _, value := range values {
		switch value {
		case ports.TargetIAMAuthnPassword:
			methods = append(methods, "password")
		case ports.TargetIAMAuthnOIDC:
			methods = append(methods, "oidc")
		case ports.TargetIAMAuthnAPIKey:
			methods = append(methods, "api_key")
		case ports.TargetIAMAuthnServiceToken:
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

func targetIAMReason(err error) (string, string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return "IAM_TIMEOUT", ""
	}
	var failure *ports.TargetIAMError
	if errors.As(err, &failure) {
		if _, known := authz.TargetStableErrorForReason(failure.Reason); known {
			if failure.Reason != "AUTH_RATE_LIMITED" {
				return failure.Reason, ""
			}
			if retryAfter, valid := authz.TargetRetryAfter(failure.Metadata); valid {
				return failure.Reason, retryAfter
			}
			return "IAM_UNAVAILABLE", ""
		}
		switch failure.Kind {
		case ports.TargetIAMFailureUnauthenticated:
			return "CREDENTIAL_INVALID", ""
		case ports.TargetIAMFailurePermissionDenied:
			return "PERMISSION_DENIED", ""
		case ports.TargetIAMFailureDeadlineExceeded:
			return "IAM_TIMEOUT", ""
		}
	}
	return "IAM_UNAVAILABLE", ""
}

func writeTargetIAMError(c *app.RequestContext, reason string) {
	stable, ok := authz.TargetStableErrorForReason(reason)
	if !ok {
		stable, _ = authz.TargetStableErrorForReason("IAM_UNAVAILABLE")
	}
	message := map[string]string{
		"CREDENTIAL_INVALID":           "credential is invalid",
		"PERMISSION_DENIED":            "permission denied",
		"AUTH_RATE_LIMITED":            "authentication rate limit exceeded",
		"AUTHZ_POLICY_MISMATCH":        "authorization policy revision mismatch",
		"AUTHZ_OPERATION_UNREGISTERED": "authorization operation is unregistered",
		"IAM_TIMEOUT":                  "IAM operation timed out",
		"IAM_UNAVAILABLE":              "IAM dependency is unavailable",
	}[stable.Code]
	respondError(c, stable.HTTPStatus, stable.Code, message)
}
