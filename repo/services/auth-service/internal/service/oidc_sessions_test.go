package service

import (
	"reflect"
	"testing"
)

func TestOIDCGroupRoleMapperDefaultsToUser(t *testing.T) {
	mapper := newOIDCGroupRoleMapper("")
	got := mapper.Map([]string{"platform-admin", "tenant-admin"})
	want := []string{"user"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roles = %v, want %v", got, want)
	}
}

func TestOIDCGroupRoleMapperUsesExplicitMappings(t *testing.T) {
	mapper := newOIDCGroupRoleMapper(`{
		"/corp/ani-admins": ["tenant-admin"],
		"CN=ANI-Auditors": ["auditor"],
		"bad": ["root"]
	}`)
	got := mapper.Map([]string{"/corp/ani-admins", "cn=ani-auditors", "bad"})
	want := []string{"auditor", "tenant-admin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roles = %v, want %v", got, want)
	}
}

func TestOIDCGroupRoleMapperNormalizesConfiguredRoles(t *testing.T) {
	mapper := newOIDCGroupRoleMapper(`{
		"CN=ANI-Admins": [" Tenant-Admin ", "AUDITOR", "root"]
	}`)
	got := mapper.Map([]string{"cn=ani-admins"})
	want := []string{"auditor", "tenant-admin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roles = %v, want %v", got, want)
	}
}

func TestIsBootstrapAdminEmailDefaults(t *testing.T) {
	t.Setenv("AUTH_OIDC_BOOTSTRAP_ADMIN_EMAILS", "")
	if !isBootstrapAdminEmail("admin@ani.local") {
		t.Fatal("expected default bootstrap admin email")
	}
	if isBootstrapAdminEmail("user@ani.local") {
		t.Fatal("unexpected bootstrap admin for ordinary email")
	}
}

func TestIsBootstrapAdminEmailUsesEnvAllowlist(t *testing.T) {
	t.Setenv("AUTH_OIDC_BOOTSTRAP_ADMIN_EMAILS", "ops@ani.local, Admin@Example.com ")
	if !isBootstrapAdminEmail("ops@ani.local") || !isBootstrapAdminEmail("admin@example.com") {
		t.Fatal("expected env allowlist emails to match")
	}
	if isBootstrapAdminEmail("admin@ani.local") {
		t.Fatal("default admin should not match when env overrides")
	}
}

func TestHasPrivilegedOIDCRole(t *testing.T) {
	if !hasPrivilegedOIDCRole([]string{"user", "tenant-admin"}) {
		t.Fatal("expected tenant-admin to be privileged")
	}
	if hasPrivilegedOIDCRole([]string{"user", "auditor"}) {
		t.Fatal("user/auditor should not be privileged")
	}
}
