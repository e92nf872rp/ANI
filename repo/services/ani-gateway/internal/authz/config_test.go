package authz

import (
	"testing"
)

// TestEffectiveSourceContractSwitchMatrix 契约即开关矩阵：
// public 恒放行（auth_service 与 dev 均是）；dev 一律 legacy；
// auth_service 按 source 直通（generated→generated、legacy→legacy）。
func TestEffectiveSourceContractSwitchMatrix(t *testing.T) {
	policies := map[string]Policy{
		"public":    {Source: PolicySourcePublic, OperationID: "login"},
		"generated": {Source: PolicySourceGenerated, OperationID: "listQuotaMeta"},
		"legacy":    {Source: PolicySourceLegacy, OperationID: "listInstances"},
	}
	configs := map[string]Config{
		"auth_service": {},
		"dev":          {AuthMode: "dev"},
	}
	want := map[string]map[string]PolicySource{
		"auth_service": {"public": PolicySourcePublic, "generated": PolicySourceGenerated, "legacy": PolicySourceLegacy},
		"dev":          {"public": PolicySourcePublic, "generated": PolicySourceLegacy, "legacy": PolicySourceLegacy},
	}
	for cfgName, cfg := range configs {
		for policyName, policy := range policies {
			if got := cfg.EffectiveSource(policy); got != want[cfgName][policyName] {
				t.Fatalf("%s × %s: got %q, want %q", cfgName, policyName, got, want[cfgName][policyName])
			}
		}
	}
}

// TestConfigFromEnvParsesAuthMode ANI_AUTH_MODE 解析断言：
// 不设归一为空串，大小写与首尾空白归一为小写。
func TestConfigFromEnvParsesAuthMode(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "")
	if cfg := ConfigFromEnv(); cfg.AuthMode != "" {
		t.Fatalf("AuthMode = %q, want empty", cfg.AuthMode)
	}

	t.Setenv("ANI_AUTH_MODE", " DEV ")
	if cfg := ConfigFromEnv(); cfg.AuthMode != "dev" {
		t.Fatalf("AuthMode = %q, want dev", cfg.AuthMode)
	}
}
