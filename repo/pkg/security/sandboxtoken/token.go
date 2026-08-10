// Package sandboxtoken issues and verifies short-lived sandbox access tokens.
//
// Token format: ani.sbx.<base64url(payloadJSON)>.<base64url(hmac-sha256)>
// Claims bind tenant, instance, scopes, expiry, and jti. Signing key comes from
// SANDBOX_TOKEN_SIGNING_KEY, or a process-local secret when unset (dev/live same-pod).
package sandboxtoken

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	Prefix          = "ani.sbx."
	Typ             = "sandbox"
	EnvSigningKey   = "SANDBOX_TOKEN_SIGNING_KEY"
	SandboxActorUID = "00000000-0000-0000-0000-000000000099"
	ScopeSandbox    = "sandbox"
)

var (
	ErrInvalidToken = errors.New("invalid sandbox token")
	ErrExpiredToken = errors.New("expired sandbox token")

	processKeyOnce sync.Once
	processKey     []byte
)

// Claims are the signed sandbox token payload.
type Claims struct {
	Typ        string   `json:"typ"`
	TenantID   string   `json:"tid"`
	InstanceID string   `json:"iid"`
	Scopes     []string `json:"scp"`
	ExpiresAt  int64    `json:"exp"`
	JTI        string   `json:"jti"`
}

// LooksLike reports whether token uses the sandbox token prefix.
func LooksLike(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), Prefix)
}

// SigningKey returns the HMAC key from env, or a process-local secret.
func SigningKey() []byte {
	if key := strings.TrimSpace(os.Getenv(EnvSigningKey)); key != "" {
		return []byte(key)
	}
	processKeyOnce.Do(func() {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			// Extremely unlikely; fall back to a deterministic non-empty key so
			// local tests still function if the RNG fails.
			processKey = []byte("ani-sandbox-token-dev-fallback-key")
			return
		}
		processKey = buf
	})
	return append([]byte(nil), processKey...)
}

// Issue signs claims into a compact sandbox token string.
func Issue(claims Claims, key []byte, now time.Time) (string, error) {
	if len(key) == 0 {
		return "", fmt.Errorf("%w: signing key is required", ErrInvalidToken)
	}
	tenantID := strings.TrimSpace(claims.TenantID)
	instanceID := strings.TrimSpace(claims.InstanceID)
	jti := strings.TrimSpace(claims.JTI)
	if tenantID == "" || instanceID == "" || jti == "" {
		return "", fmt.Errorf("%w: tid, iid, and jti are required", ErrInvalidToken)
	}
	scopes := normalizeScopes(claims.Scopes)
	if len(scopes) == 0 {
		return "", fmt.Errorf("%w: at least one scope is required", ErrInvalidToken)
	}
	if claims.ExpiresAt <= 0 {
		return "", fmt.Errorf("%w: exp is required", ErrInvalidToken)
	}
	if !now.IsZero() && claims.ExpiresAt <= now.UTC().Unix() {
		return "", fmt.Errorf("%w: exp must be in the future", ErrInvalidToken)
	}
	payload := Claims{
		Typ:        Typ,
		TenantID:   tenantID,
		InstanceID: instanceID,
		Scopes:     scopes,
		ExpiresAt:  claims.ExpiresAt,
		JTI:        jti,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	sig := sign(body, key)
	return Prefix + body + "." + sig, nil
}

// Parse verifies signature and expiry, returning claims.
func Parse(token string, key []byte, now time.Time) (Claims, error) {
	token = strings.TrimSpace(token)
	if !LooksLike(token) {
		return Claims{}, ErrInvalidToken
	}
	if len(key) == 0 {
		return Claims{}, fmt.Errorf("%w: signing key is required", ErrInvalidToken)
	}
	rest := strings.TrimPrefix(token, Prefix)
	body, sig, ok := strings.Cut(rest, ".")
	if !ok || body == "" || sig == "" || strings.Contains(sig, ".") {
		return Claims{}, ErrInvalidToken
	}
	if !hmac.Equal([]byte(sig), []byte(sign(body, key))) {
		return Claims{}, ErrInvalidToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var claims Claims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return Claims{}, ErrInvalidToken
	}
	if claims.Typ != Typ || strings.TrimSpace(claims.TenantID) == "" || strings.TrimSpace(claims.InstanceID) == "" || strings.TrimSpace(claims.JTI) == "" {
		return Claims{}, ErrInvalidToken
	}
	claims.Scopes = normalizeScopes(claims.Scopes)
	if len(claims.Scopes) == 0 {
		return Claims{}, ErrInvalidToken
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	if claims.ExpiresAt <= now.Unix() {
		return Claims{}, ErrExpiredToken
	}
	return claims, nil
}

func HasScope(claims Claims, want string) bool {
	want = strings.TrimSpace(want)
	for _, scope := range claims.Scopes {
		if scope == want {
			return true
		}
	}
	return false
}

func normalizeScopes(scopes []string) []string {
	allowed := map[string]struct{}{
		"connect": {},
		"exec":    {},
		"files":   {},
		"ports":   {},
	}
	seen := make(map[string]struct{}, len(scopes))
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if _, ok := allowed[scope]; !ok {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	return out
}

func sign(body string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
