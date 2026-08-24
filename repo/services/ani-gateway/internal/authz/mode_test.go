package authz

import (
	"testing"
)

// TestValidateRejectsInvalidPilotConfig 冻结 C2 启动校验负例：
// 空 allowlist、额外 operation、拼写错误、dev+pilot、
// pilot operation 尚未 generated 均启动失败。
func TestValidateRejectsInvalidPilotConfig(t *testing.T) {
	registry := CoreRegistry()

	emptyAllow := Config{Mode: ModePilot, AuthMode: "auth_service", PilotOperations: map[string]struct{}{}}
	if err := emptyAllow.Validate(registry); err == nil {
		t.Fatal("pilot with empty allowlist: want error")
	}

	extra := Config{
		Mode: ModePilot, AuthMode: "auth_service",
		PilotOperations: map[string]struct{}{"listQuotaMeta": {}, "createInstance": {}},
	}
	if err := extra.Validate(registry); err == nil {
		t.Fatal("pilot with extra operation: want error")
	}

	typo := Config{
		Mode: ModePilot, AuthMode: "auth_service",
		PilotOperations: map[string]struct{}{"listQuotaMetas": {}},
	}
	if err := typo.Validate(registry); err == nil {
		t.Fatal("pilot with misspelled operation: want error")
	}

	devPilot := Config{Mode: ModePilot, AuthMode: "dev", PilotOperations: map[string]struct{}{"listQuotaMeta": {}}}
	if err := devPilot.Validate(registry); err == nil {
		t.Fatal("dev + pilot: want error")
	}

	// pilot operation 尚未 generated：伪造 legacy registry 验证 fail closed。
	legacyRegistry, err := NewRegistry(map[string]Policy{
		"GET /api/v1/admin/quota-meta": {
			Source:       PolicySourceLegacy,
			OperationID:  "listQuotaMeta",
			Method:       "GET",
			PathTemplate: "/api/v1/admin/quota-meta",
		},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	pilot := Config{Mode: ModePilot, AuthMode: "auth_service", PilotOperations: map[string]struct{}{"listQuotaMeta": {}}}
	if err := pilot.Validate(legacyRegistry); err == nil {
		t.Fatal("pilot operation not generated: want error")
	}
}

// TestValidateAcceptsFrozenPilotConfig 验证唯一合法 pilot 组合可以通过带 registry 的校验。
func TestValidateAcceptsFrozenPilotConfig(t *testing.T) {
	registry := CoreRegistry()
	cfg := Config{Mode: ModePilot, AuthMode: "auth_service", PilotOperations: map[string]struct{}{"listQuotaMeta": {}}}
	if err := cfg.Validate(registry); err != nil {
		t.Fatalf("frozen pilot config rejected: %v", err)
	}
}
