package domain

import (
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestAccessPolicyValidationRejectsInvalidScope(t *testing.T) {
	p := AccessPolicy{TenantID: uuid.New(), Name: "p", Status: AccessPolicyEnabled, Priority: 1, Scope: AccessPolicyScope{Type: "bad"}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected invalid scope")
	}
}

func TestAccessPolicyValidationRejectsDisabledLegacyPolicyWithoutScope(t *testing.T) {
	p := AccessPolicy{TenantID: uuid.New(), Name: "legacy", Status: AccessPolicyDisabled, Priority: 1, Scope: AccessPolicyScope{Type: ScopeAPIKey}}
	if err := p.Validate(); err == nil {
		t.Fatal("disabled legacy policy without scope must be rejected")
	}
}

func TestAccessPolicyValidationRejectsEnabledLegacyPolicyWithoutScope(t *testing.T) {
	p := AccessPolicy{TenantID: uuid.New(), Name: "legacy", Status: AccessPolicyEnabled, Priority: 1, Scope: AccessPolicyScope{Type: ScopeAPIKey}}
	if err := p.Validate(); err == nil {
		t.Fatal("enabled legacy policy without scope must be rejected")
	}
}

func TestAccessPolicyValidationRejectsClientSuppliedRawAPIKey(t *testing.T) {
	p := AccessPolicy{TenantID: uuid.New(), Name: "p", Status: AccessPolicyEnabled, Priority: 1, Scope: AccessPolicyScope{Type: ScopeAPIKey, APIKeyIDs: []string{"ani_secret"}}}
	err := p.Validate()
	if err == nil || !strings.Contains(err.Error(), ErrRawAPIKeyRejected.Error()) {
		t.Fatalf("err=%v", err)
	}
}

func TestAccessPolicyMatchPriorityOrder(t *testing.T) {
	tenant, service, key := uuid.New(), uuid.New(), uuid.New()
	policies := []AccessPolicy{
		{TenantID: tenant, Status: AccessPolicyEnabled, Priority: 20, Scope: AccessPolicyScope{Type: ScopeTenantDefault}},
		{TenantID: tenant, Status: AccessPolicyEnabled, Priority: 1, Scope: AccessPolicyScope{Type: ScopeInferenceService, InferenceServiceIDs: []uuid.UUID{service}}},
	}
	matched := MatchPolicies(policies, service, key)
	if len(matched) != 2 || matched[0].Priority != 1 {
		t.Fatalf("matched=%v", matched)
	}
}

func TestSelectPolicyUsesSpecificityBeforePriority(t *testing.T) {
	tenant, service, key := uuid.New(), uuid.New(), uuid.New()
	selected, ok := SelectPolicy([]AccessPolicy{
		{ID: uuid.New(), TenantID: tenant, Status: AccessPolicyEnabled, Priority: 1, Scope: AccessPolicyScope{Type: ScopeTenantDefault}},
		{ID: uuid.New(), TenantID: tenant, Status: AccessPolicyEnabled, Priority: 9000, Scope: AccessPolicyScope{Type: ScopeInferenceServiceAPIKey, InferenceServiceIDs: []uuid.UUID{service}, APIKeyIDs: []string{key.String()}}},
	}, service, key)
	if !ok || selected.Scope.Type != ScopeInferenceServiceAPIKey {
		t.Fatalf("selected=%+v ok=%v", selected, ok)
	}
}

func TestSelectPolicyUsesPriorityAndIDWithinSameSpecificity(t *testing.T) {
	tenant, service, key := uuid.New(), uuid.New(), uuid.New()
	firstID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	secondID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	selected, ok := SelectPolicy([]AccessPolicy{
		{ID: secondID, TenantID: tenant, Status: AccessPolicyEnabled, Priority: 5, Scope: AccessPolicyScope{Type: ScopeInferenceService, InferenceServiceIDs: []uuid.UUID{service}}},
		{ID: firstID, TenantID: tenant, Status: AccessPolicyEnabled, Priority: 5, Scope: AccessPolicyScope{Type: ScopeInferenceService, InferenceServiceIDs: []uuid.UUID{service}}},
	}, service, key)
	if !ok || selected.ID != firstID {
		t.Fatalf("selected=%+v ok=%v", selected, ok)
	}
}

func TestAccessPolicyDefaultAllowsTenantKeyWithoutCustomPolicy(t *testing.T) {
	if !DefaultAccessAllowed(nil) {
		t.Fatal("default access should allow")
	}
}

func TestAccessPolicyAllowsOpenAPILeaseTTLMaximum(t *testing.T) {
	policy := AccessPolicy{
		TenantID: uuid.New(), Name: "max-ttl", Status: AccessPolicyEnabled, Priority: 1,
		Scope:       AccessPolicyScope{Type: ScopeTenantDefault},
		Access:      AccessPolicyAccess{AllowAllTenantKeys: true},
		Concurrency: AccessPolicyConcurrency{LeaseTTLSeconds: 3600},
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("OpenAPI maximum lease TTL must validate: %v", err)
	}
	policy.Concurrency.LeaseTTLSeconds = 3601
	if err := policy.Validate(); err == nil {
		t.Fatal("lease TTL above the OpenAPI maximum must fail")
	}
}
