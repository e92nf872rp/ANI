package authz

import (
	"testing"
)

// TestConfigFromEnvRejectsRemovedPolicyModeEnv 废弃 env 残留检测：
// GATEWAY_AUTHZ_POLICY_MODE 任一非空值（含旧合法值 off/pilot/full）均启动失败。
func TestConfigFromEnvRejectsRemovedPolicyModeEnv(t *testing.T) {
	for _, value := range []string{"off", "pilot", "full", "bogus"} {
		t.Setenv("GATEWAY_AUTHZ_POLICY_MODE", value)
		t.Setenv("GATEWAY_AUTHZ_PILOT_OPERATIONS", "")
		t.Setenv("ANI_AUTH_MODE", "")
		if _, err := ConfigFromEnv(); err == nil {
			t.Fatalf("GATEWAY_AUTHZ_POLICY_MODE=%q: want error", value)
		}
	}
}

// TestConfigFromEnvRejectsRemovedPilotOperationsEnv 废弃 env 残留检测：
// GATEWAY_AUTHZ_PILOT_OPERATIONS 非空即启动失败。
func TestConfigFromEnvRejectsRemovedPilotOperationsEnv(t *testing.T) {
	t.Setenv("GATEWAY_AUTHZ_POLICY_MODE", "")
	t.Setenv("GATEWAY_AUTHZ_PILOT_OPERATIONS", "listQuotaMeta")
	t.Setenv("ANI_AUTH_MODE", "")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("GATEWAY_AUTHZ_PILOT_OPERATIONS set: want error")
	}
}

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
