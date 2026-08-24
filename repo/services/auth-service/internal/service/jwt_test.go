package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestJWTValidatorValidateRS256Token(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tenantID := uuid.New()
	userID := uuid.New()
	issuedAt := time.Unix(1_700_000_000, 0)
	token := signTestJWT(t, key, map[string]any{
		"iss":               "ani-test",
		"sub":               userID.String(),
		"tid":               tenantID.String(),
		"uid":               userID.String(),
		"principal_kind":    "user",
		"credential_domain": "tenant",
		"roles":             []string{"tenant-admin"},
		"exp":               issuedAt.Add(time.Hour).Unix(),
		"iat":               issuedAt.Unix(),
		"jti":               "jwt-1",
	})

	validator, err := NewJWTValidator(JWTConfig{
		PublicKeyPEM: publicKeyPEM(t, &key.PublicKey),
		Issuer:       "ani-test",
	}, nil)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return issuedAt.Add(time.Minute) }

	claims, err := validator.Validate(context.Background(), token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if claims.Principal.TenantID != tenantID.String() {
		t.Fatalf("tenant id = %s, want %s", claims.Principal.TenantID, tenantID)
	}
	if claims.Principal.SubjectID != userID.String() {
		t.Fatalf("user id = %s, want %s", claims.Principal.SubjectID, userID)
	}
	if len(claims.Legacy.Roles) != 1 || claims.Legacy.Roles[0] != "tenant-admin" {
		t.Fatalf("roles = %v", claims.Legacy.Roles)
	}
	// V2 规范字段：user 凭证固定为 user/tenant 边界。
	if claims.Principal.Kind != "user" || claims.Principal.Domain != "tenant" {
		t.Fatalf("principal kind/domain = %q/%q", claims.Principal.Kind, claims.Principal.Domain)
	}
}

func TestJWTValidatorRejectsExpiredToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	issuedAt := time.Unix(1_700_000_000, 0)
	token := signTestJWT(t, key, map[string]any{
		"tid": uuid.NewString(),
		"uid": uuid.NewString(),
		"exp": issuedAt.Add(time.Minute).Unix(),
	})
	validator, err := NewJWTValidator(JWTConfig{PublicKeyPEM: publicKeyPEM(t, &key.PublicKey)}, nil)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return issuedAt.Add(2 * time.Minute) }

	if _, err := validator.Validate(context.Background(), token); err == nil {
		t.Fatal("expected expired token error")
	}
}

func signTestJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header := encodeJSON(t, map[string]any{"alg": "RS256", "typ": "JWT"})
	payload := encodeJSON(t, claims)
	signingInput := header + "." + payload
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func encodeJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

// decodeJWTClaims 解码 token 的 payload 段为 map，用于校验签名原始字段（如 aud）。
func decodeJWTClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("malformed token")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(data, &claims); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return claims
}

func publicKeyPEM(t *testing.T, key *rsa.PublicKey) string {
	t.Helper()
	data, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: data}))
}

func privateKeyPEM(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	data := x509.MarshalPKCS1PrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: data}))
}
