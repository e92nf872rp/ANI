package middleware

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/google/uuid"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	"github.com/kubercloud/ani/pkg/security/sandboxtoken"
	"github.com/kubercloud/ani/pkg/types"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

// C3：generated（V2）认证与授权。
// 只有 resolved source 为 generated 的请求进入本文件；legacy/public 分流后
// 行为与 B0 完全一致。generated 分支 deny/error 一律终止，不回退旧 RPC。

// rawCredentialContext 保存 request-local 的原始凭证（Bearer token 或 API Key），
// 供 AuthorizePrincipal 在 CheckPermissionV2 时回传 auth-service 重验。
// 禁止日志读取；授权完成后必须 ClearRawCredentialForAuthz。
type rawCredentialContext struct {
	Value  string
	Scheme authz.CredentialScheme
}

const rawCredentialContextKey = "ani.authz.raw_credential"

func SetRawCredentialForAuthz(c *app.RequestContext, value string, scheme authz.CredentialScheme) {
	c.Set(rawCredentialContextKey, rawCredentialContext{Value: value, Scheme: scheme})
}

func GetRawCredentialForAuthz(c *app.RequestContext) (string, authz.CredentialScheme, error) {
	value, ok := c.Get(rawCredentialContextKey)
	if !ok {
		return "", "", errors.New("authorization credential missing")
	}
	credential, ok := value.(rawCredentialContext)
	if !ok || credential.Value == "" {
		return "", "", errors.New("authorization credential invalid")
	}
	return credential.Value, credential.Scheme, nil
}

func ClearRawCredentialForAuthz(c *app.RequestContext) {
	c.Set(rawCredentialContextKey, rawCredentialContext{})
}

// credentialFromRequest 按 policy 的 security alternatives 从请求头提取凭证。
// Bearer 与 X-API-Key 互斥；sandbox token 在 Bearer 位置本地识别。
func credentialFromRequest(c *app.RequestContext, policy authz.Policy) (string, authz.CredentialScheme, error) {
	authHeader := strings.TrimSpace(string(c.GetHeader("Authorization")))
	apiKey := strings.TrimSpace(string(c.GetHeader("X-API-Key")))
	bearer := ""
	if strings.HasPrefix(authHeader, "Bearer ") {
		bearer = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	}
	if bearer != "" && apiKey != "" {
		return "", "", errors.New("multiple credentials are not supported")
	}
	if bearer != "" {
		scheme := authz.CredentialBearer
		if sandboxtoken.LooksLike(bearer) {
			scheme = authz.CredentialSandboxToken
		}
		if !policyAllowsCredentialScheme(policy, scheme) {
			return "", "", errors.New("credential scheme not allowed")
		}
		return bearer, scheme, nil
	}
	if apiKey != "" {
		if !policyAllowsCredentialScheme(policy, authz.CredentialAPIKey) {
			return "", "", errors.New("credential scheme not allowed")
		}
		return apiKey, authz.CredentialAPIKey, nil
	}
	return "", "", errors.New("credential required")
}

// policyAllowsCredentialScheme 校验 credential scheme 是否命中 policy 的某个单凭证 alternative。
func policyAllowsCredentialScheme(policy authz.Policy, scheme authz.CredentialScheme) bool {
	required := authz.OpenAPISecurityBearer
	if scheme == authz.CredentialAPIKey {
		required = authz.OpenAPISecurityAPIKey
	}
	for _, alternative := range policy.SecurityAlternatives {
		if len(alternative.AllOf) == 1 && alternative.AllOf[0] == required {
			return true
		}
	}
	return false
}

// validatePrincipalAgainstPolicy 对 auth-service 返回的 Principal 做策略级复核：
// 结构合法 → 主体类型允许 → credential scheme 允许 → domain 覆盖 boundary。
func validatePrincipalAgainstPolicy(principal authz.Principal, policy authz.Policy) error {
	if err := principal.Validate(); err != nil {
		return err
	}
	if !policy.AllowsPrincipalKind(principal.Kind) {
		return errors.New("principal kind denied")
	}
	if !policyAllowsCredentialScheme(policy, principal.CredentialScheme) {
		return errors.New("credential scheme denied")
	}
	if !authz.DomainAllowsBoundary(principal, policy.Boundary) {
		return errors.New("credential domain denied")
	}
	return nil
}

// InstallGeneratedPrincipalContext 把 V2 Principal 投影到 handler 可见的上下文：
// Hertz 字段（tenant_id/user_id/scope=credential_domain）对所有主体都写入；
// Go context 的 types.TenantContext 只给 tenant/sandbox 主体安装，且不含 legacy roles。
// platform 主体不注入零 UUID tenant，跨租户访问必须显式使用 Principal。
func InstallGeneratedPrincipalContext(
	ctx context.Context, c *app.RequestContext, principal authz.Principal,
) (context.Context, error) {
	if err := principal.Validate(); err != nil {
		return ctx, err
	}

	// 兼容现有 handler 的 Hertz request context；V2 不写入 legacy roles。
	setTenantContext(c, principal.TenantID, principal.SubjectID, nil, string(principal.CredentialDomain))
	c.Set("principal_kind", string(principal.Kind))
	c.Set("credential_scheme", string(principal.CredentialScheme))

	if principal.CredentialDomain == authz.DomainPlatform {
		// platform 不属于单一租户，不向 Go context 注入伪造零 UUID tenant。
		return ctx, nil
	}
	tenantUUID, err := uuid.Parse(principal.TenantID)
	if err != nil || tenantUUID == uuid.Nil {
		return ctx, errors.New("generated tenant principal has invalid tenant id")
	}
	tenantContext := &types.TenantContext{TenantID: tenantUUID}
	// user/API Key 的 subject 是 UUID；service subject 可以是签名 service ID，
	// 此时保留 zero UserID，服务身份仍以 Principal 作为权威主体。
	if principal.SubjectID != "" {
		if subjectUUID, parseErr := uuid.Parse(principal.SubjectID); parseErr == nil {
			tenantContext.UserID = subjectUUID
		} else if principal.Kind == authz.PrincipalUser || principal.Kind == authz.PrincipalAPIKey {
			return ctx, errors.New("generated user credential has invalid subject id")
		}
	}
	return types.WithTenant(ctx, tenantContext), nil
}

// AuthenticatePrincipal 是 C 阶段认证入口：
// public 放行；legacy 完整走旧 header/parser/RPC 语义；generated 走
// credentialFromRequest → 本地 sandbox 或 ValidatePrincipal → 策略复核 →
// SetPrincipal + InstallGeneratedPrincipalContext + request-local raw credential。
func AuthenticatePrincipal(client AuthClient) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		resolved, err := GetResolvedPolicy(c)
		if err != nil {
			respond503(c, "authz policy context missing")
			return
		}
		if resolved.Source == authz.PolicySourcePublic {
			c.Next(ctx)
			return
		}
		if resolved.Source == authz.PolicySourceLegacy {
			authenticateLegacy(ctx, c, client)
			return
		}
		credential, scheme, err := credentialFromRequest(c, resolved.Policy)
		if err != nil {
			respond401(c, "invalid credential")
			return
		}
		if scheme == authz.CredentialSandboxToken {
			claims, err := sandboxtoken.Parse(credential, sandboxtoken.SigningKey(), time.Now().UTC())
			if err != nil {
				respond401(c, "invalid sandbox credential")
				return
			}
			principal := authz.Principal{
				Kind:             authz.PrincipalSandbox,
				CredentialScheme: authz.CredentialSandboxToken,
				CredentialDomain: authz.DomainSandbox,
				TenantID:         claims.TenantID,
				SubjectID:        sandboxtoken.SandboxActorUID,
				SandboxClaims: &authz.SandboxClaims{
					TenantID:   claims.TenantID,
					InstanceID: claims.InstanceID,
				},
			}
			if err := validatePrincipalAgainstPolicy(principal, resolved.Policy); err != nil {
				respond403(c, "principal not allowed by operation policy")
				return
			}
			setSandboxContext(c, claims)
			SetPrincipal(c, principal)
			ctx, err = InstallGeneratedPrincipalContext(ctx, c, principal)
			if err != nil {
				respond503(c, "generated identity context unavailable")
				return
			}
			c.Next(ctx)
			return
		}
		principalPB, err := client.ValidatePrincipal(ctx, credential, scheme)
		if err != nil {
			writeAuthRPCError(c, err)
			return
		}
		principal, err := authz.PrincipalFromProto(principalPB)
		if err != nil {
			respond503(c, "auth service returned invalid principal")
			return
		}
		if err := validatePrincipalAgainstPolicy(principal, resolved.Policy); err != nil {
			respond403(c, "principal not allowed by operation policy")
			return
		}
		SetPrincipal(c, principal)
		ctx, err = InstallGeneratedPrincipalContext(ctx, c, principal)
		if err != nil {
			respond503(c, "generated identity context unavailable")
			return
		}
		// request-local only；禁止日志读取，授权完成后清除。
		SetRawCredentialForAuthz(c, credential, scheme)
		c.Next(ctx)
	}
}

// AuthorizePrincipal 是 C 阶段授权入口：
// public 放行；legacy 只调用旧 CheckPermission；generated 的 sandbox 走本地
// capability + instance binding，其余主体走 CheckPermissionV2（无 legacy fallback）。
func AuthorizePrincipal(client AuthClient) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		resolved, err := GetResolvedPolicy(c)
		if err != nil {
			respond503(c, "authz policy context missing")
			return
		}
		if resolved.Source == authz.PolicySourcePublic {
			c.Next(ctx)
			return
		}
		if resolved.Source == authz.PolicySourceLegacy {
			authorizeLegacy(ctx, c, client)
			return
		}
		principal, err := GetPrincipal(c)
		if err != nil {
			respond503(c, "principal context missing")
			return
		}
		if principal.Kind == authz.PrincipalSandbox {
			// sandbox 由 Gateway 本地 capability + instance binding 授权，
			// 不把 sandbox credential 发送给 CheckPermissionV2。
			if !sandboxTokenAllows(c, string(c.Path())) {
				respond403(c, "sandbox capability denied")
				return
			}
			c.Next(ctx)
			return
		}
		credential, scheme, err := GetRawCredentialForAuthz(c)
		if err != nil {
			respond503(c, "authorization credential context missing")
			return
		}
		defer ClearRawCredentialForAuthz(c)
		decision, err := client.CheckPermissionV2(ctx, &authv1.AuthorizationRequest{
			Principal:        principal.WithoutLegacyRoles().Proto(),
			Resource:         resolved.Policy.Resource,
			Action:           resolved.Policy.Action,
			RequiredBoundary: string(resolved.Policy.Boundary),
			OperationId:      resolved.Policy.OperationID,
			Credential:       credential,
			CredentialScheme: string(scheme),
		})
		if err != nil {
			writeAuthRPCError(c, err)
			return
		}
		if !decision.Allowed {
			respond403(c, decision.ReasonCode)
			return
		}
		c.Next(ctx)
	}
}
