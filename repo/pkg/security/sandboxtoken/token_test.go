package sandboxtoken

import (
	"strings"
	"testing"
	"time"
)

func TestIssueAndParseRoundTrip(t *testing.T) {
	key := []byte("test-signing-key")
	now := time.Unix(1_700_000_000, 0).UTC()
	token, err := Issue(Claims{
		TenantID:   "11111111-1111-1111-1111-111111111111",
		InstanceID: "sandbox_1",
		Scopes:     []string{"files", "ports", "files"},
		ExpiresAt:  now.Add(15 * time.Minute).Unix(),
		JTI:        "jti-1",
	}, key, now)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if !LooksLike(token) || !strings.HasPrefix(token, Prefix) {
		t.Fatalf("token = %q, want prefix %q", token, Prefix)
	}

	claims, err := Parse(token, key, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if claims.TenantID != "11111111-1111-1111-1111-111111111111" || claims.InstanceID != "sandbox_1" {
		t.Fatalf("claims identity = %+v", claims)
	}
	if len(claims.Scopes) != 2 || claims.Scopes[0] != "files" || claims.Scopes[1] != "ports" {
		t.Fatalf("scopes = %v, want [files ports]", claims.Scopes)
	}
	if !HasScope(claims, "files") || HasScope(claims, "exec") {
		t.Fatalf("HasScope mismatch: %+v", claims.Scopes)
	}
}

func TestParseRejectsTamperAndExpiry(t *testing.T) {
	key := []byte("test-signing-key")
	now := time.Unix(1_700_000_000, 0).UTC()
	token, err := Issue(Claims{
		TenantID:   "tenant-a",
		InstanceID: "sandbox_1",
		Scopes:     []string{"exec"},
		ExpiresAt:  now.Add(time.Minute).Unix(),
		JTI:        "jti-2",
	}, key, now)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	tampered := token[:len(token)-1] + "x"
	if _, err := Parse(tampered, key, now); err == nil {
		t.Fatal("Parse(tampered) error = nil, want error")
	}
	if _, err := Parse(token, []byte("other-key"), now); err == nil {
		t.Fatal("Parse(wrong key) error = nil, want error")
	}
	if _, err := Parse(token, key, now.Add(2*time.Minute)); err != ErrExpiredToken {
		t.Fatalf("Parse(expired) error = %v, want %v", err, ErrExpiredToken)
	}
}
