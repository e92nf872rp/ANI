package authz

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
)

// CredentialDomain 表示凭证归属域：租户域、平台域、沙箱域。
type CredentialDomain string

const (
	DomainTenant   CredentialDomain = "tenant"
	DomainPlatform CredentialDomain = "platform"
	DomainSandbox  CredentialDomain = "sandbox"
)

// Principal 是规范 principal 身份，字段闭合：
// 新（generated/V2）链路认证成功后必须能构造出通过 Validate 的 Principal。
// LegacyRoles 只能由 legacy 路径读取，规范 Principal 不允许携带。
type Principal struct {
	Kind             PrincipalKind
	CredentialScheme CredentialScheme
	CredentialDomain CredentialDomain
	TenantID         string
	SubjectID        string
	CredentialID     string
	LegacyRoles      []string // 只能由 legacy 路径读取
	SandboxClaims    *SandboxClaims
}

// SandboxClaims 是沙箱凭证绑定的租户与实例。
type SandboxClaims struct {
	TenantID   string
	InstanceID string
}

// zeroUUID 是旧 platform token 的 tenant_id 占位值。
const zeroUUID = "00000000-0000-0000-0000-000000000000"

func requireNonZeroUUID(name, value string) error {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return fmt.Errorf("%s must be a non-zero UUID", name)
	}
	return nil
}

// Validate 按 kind/scheme/domain 矩阵校验规范 Principal。
func (p Principal) Validate() error {
	if len(p.LegacyRoles) != 0 {
		return errors.New("normative principal must not contain legacy roles")
	}
	switch p.Kind {
	case PrincipalUser, PrincipalService, PrincipalAPIKey, PrincipalSandbox:
	default:
		return errors.New("unknown principal kind")
	}
	switch p.CredentialDomain {
	case DomainPlatform:
		if p.TenantID != "" {
			return errors.New("platform principal tenant_id must be empty")
		}
	case DomainTenant, DomainSandbox:
		if err := requireNonZeroUUID("tenant_id", p.TenantID); err != nil {
			return err
		}
	default:
		return errors.New("unknown credential domain")
	}

	switch p.Kind {
	case PrincipalUser:
		if p.CredentialScheme != CredentialBearer {
			return errors.New("user requires bearer credential")
		}
		if p.CredentialDomain != DomainTenant && p.CredentialDomain != DomainPlatform {
			return errors.New("user requires tenant or platform domain")
		}
		if p.CredentialID != "" || p.SandboxClaims != nil {
			return errors.New("user contains unrelated credential fields")
		}
		return requireNonZeroUUID("user subject_id", p.SubjectID)
	case PrincipalService:
		if p.CredentialScheme != CredentialBearer {
			return errors.New("service requires bearer credential")
		}
		if p.CredentialDomain != DomainTenant && p.CredentialDomain != DomainPlatform {
			return errors.New("service requires tenant or platform domain")
		}
		if p.CredentialID != "" || p.SandboxClaims != nil {
			return errors.New("service contains unrelated credential fields")
		}
		if strings.TrimSpace(p.SubjectID) == "" {
			return errors.New("service subject_id required")
		}
	case PrincipalAPIKey:
		if p.CredentialScheme != CredentialAPIKey || p.CredentialDomain != DomainTenant {
			return errors.New("api key requires api_key credential and tenant domain")
		}
		if err := requireNonZeroUUID("credential_id", p.CredentialID); err != nil {
			return err
		}
		if p.SandboxClaims != nil {
			return errors.New("api key contains sandbox claims")
		}
		if p.SubjectID != "" {
			if err := requireNonZeroUUID("api key subject_id", p.SubjectID); err != nil {
				return err
			}
		}
	case PrincipalSandbox:
		if p.CredentialScheme != CredentialSandboxToken || p.CredentialDomain != DomainSandbox {
			return errors.New("sandbox requires sandbox_bearer credential and sandbox domain")
		}
		if p.CredentialID != "" {
			return errors.New("sandbox contains credential_id")
		}
		if p.SandboxClaims == nil {
			return errors.New("sandbox claims required")
		}
		if p.SandboxClaims.TenantID != p.TenantID {
			return errors.New("sandbox claim tenant mismatch")
		}
		if strings.TrimSpace(p.SandboxClaims.InstanceID) == "" {
			return errors.New("sandbox instance_id required")
		}
	}
	return nil
}

// WithoutLegacyRoles 返回去掉 legacy roles 的副本。
func (p Principal) WithoutLegacyRoles() Principal {
	p.LegacyRoles = nil
	return p
}

// PrincipalFromProto 把 auth-service 返回的 PrincipalContext 转为内部规范 Principal，
// 转换后立即执行结构校验，失败 fail closed。
func PrincipalFromProto(value *authv1.PrincipalContext) (Principal, error) {
	if value == nil {
		return Principal{}, errors.New("principal context required")
	}
	principal := Principal{
		Kind:             PrincipalKind(value.GetPrincipalKind()),
		CredentialScheme: CredentialScheme(value.GetCredentialScheme()),
		CredentialDomain: CredentialDomain(value.GetCredentialDomain()),
		TenantID:         value.GetTenantId(),
		SubjectID:        value.GetSubjectId(),
		CredentialID:     value.GetCredentialId(),
	}
	return principal, principal.Validate()
}

// Proto 把规范 Principal 序列化为 auth-service V2 RPC 使用的 PrincipalContext。
// V2 Proto 不携带 legacy roles / sandbox claims。
func (p Principal) Proto() *authv1.PrincipalContext {
	return &authv1.PrincipalContext{
		PrincipalKind:    string(p.Kind),
		CredentialScheme: string(p.CredentialScheme),
		CredentialDomain: string(p.CredentialDomain),
		TenantId:         p.TenantID,
		SubjectId:        p.SubjectID,
		CredentialId:     p.CredentialID,
	}
}

// AllowsPrincipalKind 判断 policy 是否允许该 principal kind。
func (p Policy) AllowsPrincipalKind(kind PrincipalKind) bool {
	return slices.Contains(p.PrincipalKinds, kind)
}

// DomainAllowsBoundary 校验 principal 的凭证域是否满足 policy 要求的 boundary。
func DomainAllowsBoundary(principal Principal, required Boundary) bool {
	switch required {
	case BoundaryOwn, BoundaryTenant:
		return (principal.CredentialDomain == DomainTenant && principal.TenantID != "") ||
			(principal.CredentialDomain == DomainSandbox && principal.TenantID != "")
	case BoundaryPlatform:
		return principal.CredentialDomain == DomainPlatform && principal.TenantID == ""
	default:
		return false
	}
}

// IdentityKey 返回横切（限流/幂等/审计）使用的稳定身份键。
func (p Principal) IdentityKey() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	switch p.Kind {
	case PrincipalAPIKey:
		return "tenant:" + p.TenantID + ":api_key:" + p.CredentialID, nil
	case PrincipalService:
		return string(p.CredentialDomain) + ":service:" + p.SubjectID, nil
	case PrincipalSandbox:
		return "tenant:" + p.TenantID + ":sandbox:" + p.SandboxClaims.InstanceID, nil
	case PrincipalUser:
		if p.CredentialDomain == DomainPlatform {
			return "platform:user:" + p.SubjectID, nil
		}
		return "tenant:" + p.TenantID + ":user:" + p.SubjectID, nil
	default:
		return "", errors.New("unknown principal kind")
	}
}

// LegacyPrincipalView 是旧 TenantContext 的只读投影，不是规范 Principal：
// 旧链路没有 principal kind 和 API Key credential ID，不能伪造完整身份。
type LegacyPrincipalView struct {
	CredentialScheme CredentialScheme
	TenantID         string
	SubjectID        string
	Scope            string
	Roles            []string
	SandboxClaims    *SandboxClaims
}

// LegacyViewFromTenantContext 把旧 TenantContext 转为 legacy view。
// 旧 platform 零 UUID 只在 view 中规范为空，不改变旧 RPC 返回值。
func LegacyViewFromTenantContext(
	tc *commonv1.TenantContext,
	scheme CredentialScheme,
) (LegacyPrincipalView, error) {
	if tc == nil {
		return LegacyPrincipalView{}, errors.New("tenant context required")
	}
	tenantID := strings.TrimSpace(tc.GetTenantId())
	if tc.GetScope() == "platform" {
		if tenantID == zeroUUID {
			tenantID = ""
		}
	}
	view := LegacyPrincipalView{
		CredentialScheme: scheme,
		TenantID:         tenantID,
		SubjectID:        tc.GetUserId(),
		Scope:            tc.GetScope(),
		Roles:            append([]string(nil), tc.GetRoles()...),
	}
	if view.Scope == "platform" {
		if view.TenantID != "" {
			return LegacyPrincipalView{}, errors.New("platform legacy tenant must normalize empty")
		}
	} else if err := requireNonZeroUUID("legacy tenant_id", view.TenantID); err != nil {
		return LegacyPrincipalView{}, err
	}
	return view, nil
}

// IdentityKey 返回 legacy view 的横切身份键。
// B0 没有 API Key credential ID，保留 tenant 粒度；不读取原始 API Key。
func (p LegacyPrincipalView) IdentityKey() (string, error) {
	switch p.CredentialScheme {
	case CredentialAPIKey:
		if err := requireNonZeroUUID("legacy api key tenant_id", p.TenantID); err != nil {
			return "", err
		}
		return "tenant:" + p.TenantID + ":api_key:legacy", nil
	case CredentialSandboxToken:
		if p.SandboxClaims == nil || strings.TrimSpace(p.SandboxClaims.InstanceID) == "" {
			return "", errors.New("legacy sandbox claims required")
		}
		return "tenant:" + p.TenantID + ":sandbox:" + p.SandboxClaims.InstanceID, nil
	case CredentialBearer:
		if err := requireNonZeroUUID("legacy subject_id", p.SubjectID); err != nil {
			return "", err
		}
		if p.Scope == "platform" {
			return "platform:user:" + p.SubjectID, nil
		}
		return "tenant:" + p.TenantID + ":user:" + p.SubjectID, nil
	default:
		return "", errors.New("unknown legacy credential scheme")
	}
}
