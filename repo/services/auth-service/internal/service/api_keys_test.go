package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestGenerateAPIKeyEmbedsTenantID(t *testing.T) {
	tenantID := uuid.New()
	key, err := generateAPIKey(tenantID)
	if err != nil {
		t.Fatalf("generateAPIKey: %v", err)
	}
	if !strings.HasPrefix(key, "ani_dev_"+tenantID.String()+"_") {
		t.Fatalf("key prefix = %q, want tenant embedded", key)
	}
	gotTenantID, err := parseAPIKeyTenant(key)
	if err != nil {
		t.Fatalf("parseAPIKeyTenant: %v", err)
	}
	if gotTenantID != tenantID {
		t.Fatalf("tenant id = %s, want %s", gotTenantID, tenantID)
	}
}

// TestParseAPIKeyTenantRejectsMalformedKeys 验证格式非法的 key 返回
// errInvalidAPIKeyFormat sentinel，供错误分类器映射为 401。
func TestParseAPIKeyTenantRejectsMalformedKeys(t *testing.T) {
	for _, key := range []string{
		"", "ani", "ani_dev", "ani_dev_secret",
		"prod_11111111-1111-1111-1111-111111111111_secret",
		"ani_dev_not-a-uuid_secret",
	} {
		if _, err := parseAPIKeyTenant(key); !errors.Is(err, errInvalidAPIKeyFormat) {
			t.Fatalf("parseAPIKeyTenant(%q) error = %v, want errInvalidAPIKeyFormat", key, err)
		}
	}
}

func TestHasScope(t *testing.T) {
	if !hasScope([]string{"scope:models:create"}, "models", "create") {
		t.Fatal("expected exact scope to allow")
	}
	if !hasScope([]string{"models:*"}, "models", "delete") {
		t.Fatal("expected resource wildcard scope to allow")
	}
	if hasScope([]string{"scope:tasks:get"}, "models", "get") {
		t.Fatal("unexpected scope allow")
	}
}

func TestNormalizeAPIKeyScopes(t *testing.T) {
	scopes, err := normalizeAPIKeyScopes([]string{"models:create", "scope:tasks:*", "models:create"})
	if err != nil {
		t.Fatalf("normalizeAPIKeyScopes error = %v", err)
	}
	want := []string{"scope:models:create", "scope:tasks:*"}
	if len(scopes) != len(want) {
		t.Fatalf("scopes = %v, want %v", scopes, want)
	}
	for i := range want {
		if scopes[i] != want[i] {
			t.Fatalf("scopes = %v, want %v", scopes, want)
		}
	}
}

func TestNormalizeAPIKeyScopesRejectsRolesAndEmptyScopes(t *testing.T) {
	for _, scopes := range [][]string{
		nil,
		{""},
		{"tenant-admin"},
		{"scope:models:create:extra"},
		{"Models:create"},
	} {
		if _, err := normalizeAPIKeyScopes(scopes); err == nil {
			t.Fatalf("normalizeAPIKeyScopes(%v) error = nil, want validation error", scopes)
		}
	}
}

func TestNormalizeAPIKeyName(t *testing.T) {
	name, err := normalizeAPIKeyName("  ci deploy  ")
	if err != nil {
		t.Fatalf("normalizeAPIKeyName error = %v", err)
	}
	if name != "ci deploy" {
		t.Fatalf("name = %q", name)
	}
	if _, err := normalizeAPIKeyName("   "); err == nil {
		t.Fatal("expected blank name to fail")
	}
	if _, err := normalizeAPIKeyName(strings.Repeat("a", maxAPIKeyNameLength+1)); err == nil {
		t.Fatal("expected oversized name to fail")
	}
}

func TestNormalizeAPIKeyExpiresAtRequiresFutureTime(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	future, err := normalizeAPIKeyExpiresAt(timestamppb.New(now.Add(time.Hour)), now)
	if err != nil {
		t.Fatalf("future expires_at error = %v", err)
	}
	if !future.Equal(now.Add(time.Hour)) {
		t.Fatalf("future = %s", future)
	}
	if _, err := normalizeAPIKeyExpiresAt(timestamppb.New(now), now); err == nil {
		t.Fatal("expected current expires_at to fail")
	}
	if _, err := normalizeAPIKeyExpiresAt(timestamppb.New(now.Add(-time.Second)), now); err == nil {
		t.Fatal("expected past expires_at to fail")
	}
}

func TestNormalizeAPIKeyRateLimit(t *testing.T) {
	got, err := normalizeAPIKeyRateLimit(0)
	if err != nil {
		t.Fatalf("default rate limit error = %v", err)
	}
	if got != defaultAPIKeyRateLimitRPM {
		t.Fatalf("default rate limit = %d, want %d", got, defaultAPIKeyRateLimitRPM)
	}
	got, err = normalizeAPIKeyRateLimit(120)
	if err != nil {
		t.Fatalf("explicit rate limit error = %v", err)
	}
	if got != 120 {
		t.Fatalf("explicit rate limit = %d, want 120", got)
	}
	if _, err := normalizeAPIKeyRateLimit(maxAPIKeyRateLimitRPM + 1); err == nil {
		t.Fatal("expected oversized rate limit to fail")
	}
}

func TestAPIKeyRateLimitAllowsUntilLimit(t *testing.T) {
	store := &apiKeyStore{cache: newMemoryCache()}
	keyHash := hashAPIKey("ani_dev_tenant_secret")

	if err := store.enforceRateLimit(context.Background(), keyHash, 2); err != nil {
		t.Fatalf("first enforceRateLimit error = %v", err)
	}
	if err := store.enforceRateLimit(context.Background(), keyHash, 2); err != nil {
		t.Fatalf("second enforceRateLimit error = %v", err)
	}
	if err := store.enforceRateLimit(context.Background(), keyHash, 2); !errors.Is(err, errAPIKeyRateLimitExceeded) {
		t.Fatalf("third enforceRateLimit error = %v, want rate limit", err)
	}
}

func TestAPIKeyRateLimitIsSkippedWithoutCache(t *testing.T) {
	store := &apiKeyStore{}
	if err := store.enforceRateLimit(context.Background(), "key-hash", 1); err != nil {
		t.Fatalf("enforceRateLimit without cache error = %v", err)
	}
}
