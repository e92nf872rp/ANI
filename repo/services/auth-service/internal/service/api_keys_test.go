package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

type retryAfterCache struct {
	count int64
	ttl   time.Duration
}

func (c *retryAfterCache) Get(context.Context, string) ([]byte, error)              { return nil, nil }
func (c *retryAfterCache) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (c *retryAfterCache) SetNX(context.Context, string, []byte, time.Duration) (bool, error) {
	return true, nil
}
func (c *retryAfterCache) Delete(context.Context, string) error { return nil }
func (c *retryAfterCache) Increment(context.Context, string, time.Duration) (int64, error) {
	return c.count, nil
}
func (c *retryAfterCache) Exists(context.Context, string) (bool, error)       { return true, nil }
func (c *retryAfterCache) TTL(context.Context, string) (time.Duration, error) { return c.ttl, nil }

func TestEnforceRateLimitCarriesRetryAfterDuration(t *testing.T) {
	store := newAPIKeyStore(nil, &retryAfterCache{count: 3, ttl: 42 * time.Second})
	err := store.enforceRateLimit(context.Background(), "hash", 2)
	if !errors.Is(err, errAPIKeyRateLimitExceeded) {
		t.Fatalf("error = %v, want rate-limit sentinel", err)
	}
	carrier, ok := err.(interface{ RetryAfter() time.Duration })
	if !ok {
		t.Fatalf("error %T does not carry RetryAfter", err)
	}
	if got := carrier.RetryAfter(); got != 42*time.Second {
		t.Fatalf("retry after = %s, want 42s", got)
	}
}

func TestEnforceRateLimitFailsClosedWhenTTLUnavailable(t *testing.T) {
	store := newAPIKeyStore(nil, &retryAfterCache{count: 3})
	err := store.enforceRateLimit(context.Background(), "hash", 2)
	if errors.Is(err, errAPIKeyRateLimitExceeded) {
		t.Fatalf("error = %v, want TTL failure rather than a retryable rate-limit error", err)
	}
}

type testRateLimitError struct{ retryAfter time.Duration }

func (e testRateLimitError) Error() string             { return errAPIKeyRateLimitExceeded.Error() }
func (e testRateLimitError) Unwrap() error             { return errAPIKeyRateLimitExceeded }
func (e testRateLimitError) RetryAfter() time.Duration { return e.retryAfter }

func TestCredentialValidationStatusCarriesRetryAfterDetails(t *testing.T) {
	err := credentialValidationStatus(testRateLimitError{retryAfter: 17 * time.Second})
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.ResourceExhausted {
		t.Fatalf("status = %v, want ResourceExhausted", err)
	}
	var found bool
	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if !ok || info.Reason != "RATE_LIMITED" {
			continue
		}
		if info.Metadata["retry_after_seconds"] != "17" {
			t.Fatalf("retry metadata = %v, want 17", info.Metadata)
		}
		found = true
	}
	if !found {
		t.Fatal("rate-limit status missing ErrorInfo retry_after_seconds")
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
