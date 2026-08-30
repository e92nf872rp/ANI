// Package authz 承载 ANI Gateway 的鉴权 policy 注册表与 lookup。
// 注册表唯一事实来源是 Core OpenAPI（api/openapi/v1.yaml）经
// scripts/generate_gateway_authz.py 生成的 zz_generated_core_policies.go，
// 本文件不提供运行时 YAML 解析。
package authz

import (
	"fmt"
	"maps"
	"regexp"

	"github.com/cloudwego/hertz/pkg/app"
)

type PolicySource string

const (
	PolicySourcePublic    PolicySource = "public"
	PolicySourceGenerated PolicySource = "generated"
	PolicySourceLegacy    PolicySource = "legacy"
)

type Boundary string

const (
	BoundaryOwn      Boundary = "own"
	BoundaryTenant   Boundary = "tenant"
	BoundaryPlatform Boundary = "platform"
)

type PrincipalKind string

const (
	PrincipalUser    PrincipalKind = "user"
	PrincipalService PrincipalKind = "service"
	PrincipalAPIKey  PrincipalKind = "api_key"
	PrincipalSandbox PrincipalKind = "sandbox"
)

type OpenAPISecurityScheme string

const (
	OpenAPISecurityBearer OpenAPISecurityScheme = "BearerAuth"
	OpenAPISecurityAPIKey OpenAPISecurityScheme = "ApiKeyAuth"
)

type CredentialScheme string

const (
	CredentialBearer       CredentialScheme = "bearer"
	CredentialAPIKey       CredentialScheme = "api_key"
	CredentialSandboxToken CredentialScheme = "sandbox_bearer"
)

// SecurityRequirement 表示一个 AND 组合；Policy.SecurityAlternatives 表示 OR。
type SecurityRequirement struct {
	AllOf []OpenAPISecurityScheme
}

type Policy struct {
	Source               PolicySource
	OperationID          string
	Method               string
	PathTemplate         string
	SecurityAlternatives []SecurityRequirement
	Version              string
	Resource             string
	Action               string
	Boundary             Boundary
	PrincipalKinds       []PrincipalKind
}

func policyKey(method, pathTemplate string) string {
	return method + " " + pathTemplate
}

type Registry struct {
	byRoute     map[string]Policy
	byOperation map[string]Policy
}

func NewRegistry(policies map[string]Policy) (Registry, error) {
	registry := Registry{byRoute: maps.Clone(policies), byOperation: map[string]Policy{}}
	for key, policy := range policies {
		if policy.OperationID == "" {
			return Registry{}, fmt.Errorf("%s missing operation id", key)
		}
		if _, exists := registry.byOperation[policy.OperationID]; exists {
			return Registry{}, fmt.Errorf("duplicate operation id %q", policy.OperationID)
		}
		registry.byOperation[policy.OperationID] = policy
	}
	return registry, nil
}

func (r Registry) Lookup(method, pathTemplate string) (Policy, bool) {
	policy, ok := r.byRoute[policyKey(method, pathTemplate)]
	return policy, ok
}

func (r Registry) LookupOperation(operationID string) (Policy, bool) {
	policy, ok := r.byOperation[operationID]
	return policy, ok
}

func CoreRegistry() Registry {
	registry, err := NewRegistry(generatedCorePolicies)
	if err != nil {
		panic("invalid generated Core authz registry: " + err.Error())
	}
	return registry
}

// LookupCorePolicy 基于统一生成注册表按 method + path template 查找 policy。
func LookupCorePolicy(method, pathTemplate string) (Policy, bool) {
	return CoreRegistry().Lookup(method, pathTemplate)
}

var hertzParam = regexp.MustCompile(`:([A-Za-z_][A-Za-z0-9_]*)`)

// NormalizeHertzFullPath 把 Hertz 路由变量（:id）规范化为 OpenAPI 模板变量（{id}），
// 使其与生成注册表中的 PathTemplate 可直接比较。
func NormalizeHertzFullPath(fullPath string) string {
	return hertzParam.ReplaceAllString(fullPath, "{$1}")
}

// LookupByRequest 从 Hertz 请求上下文解析出命中的 Core policy。
func LookupByRequest(c *app.RequestContext) (Policy, bool) {
	path := NormalizeHertzFullPath(string(c.FullPath()))
	return LookupCorePolicy(string(c.Method()), path)
}
