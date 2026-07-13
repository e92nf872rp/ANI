package service

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
)

type JWTIssuer struct {
	privateKey *rsa.PrivateKey
	issuer     string
	now        func() time.Time
}

func NewJWTIssuer(cfg JWTConfig) (*JWTIssuer, error) {
	keyPEM := cfg.PrivateKeyPEM
	if keyPEM == "" && cfg.PrivateKeyFile != "" {
		data, err := os.ReadFile(cfg.PrivateKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read jwt private key file: %w", err)
		}
		keyPEM = string(data)
	}
	if keyPEM == "" {
		return nil, errJWTNotConfigured
	}
	key, err := parseRSAPrivateKey(keyPEM)
	if err != nil {
		return nil, err
	}
	return &JWTIssuer{privateKey: key, issuer: cfg.Issuer, now: time.Now}, nil
}

func (i *JWTIssuer) IssueAccessToken(principal refreshPrincipal, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = defaultAccessTokenTTL
	}
	now := i.now()
	payload := jwtPayload{
		Subject:   principal.UserID.String(),
		Issuer:    i.issuer,
		Expires:   now.Add(ttl).Unix(),
		NotBefore: now.Unix(),
		IssuedAt:  now.Unix(),
		JTI:       uuid.NewString(),
		TenantID:  principal.TenantID.String(),
		UserID:    principal.UserID.String(),
		Roles:     principal.Roles,
		Scope:     "tenant",
	}
	return i.sign(payload)
}

// IssuePlatformAccessToken signs an access token for a platform admin.
// The token carries scope=platform, no tenant_id, and roles=["platform-admin"].
func (i *JWTIssuer) IssuePlatformAccessToken(principal platformPrincipal, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = defaultAccessTokenTTL
	}
	now := i.now()
	roles := principal.Roles
	if len(roles) == 0 {
		roles = []string{"platform-admin"}
	}
	payload := jwtPayload{
		Subject:   principal.UserID.String(),
		Issuer:    i.issuer,
		Expires:   now.Add(ttl).Unix(),
		NotBefore: now.Unix(),
		IssuedAt:  now.Unix(),
		JTI:       uuid.NewString(),
		TenantID:  "",
		UserID:    principal.UserID.String(),
		Roles:     roles,
		Scope:     "platform",
	}
	return i.sign(payload)
}

func (i *JWTIssuer) sign(payload jwtPayload) (string, error) {
	header := jwtHeader{Alg: "RS256", Typ: "JWT"}
	headerSegment, err := encodeJWTJSON(header)
	if err != nil {
		return "", err
	}
	payloadSegment, err := encodeJWTJSON(payload)
	if err != nil {
		return "", err
	}
	signingInput := headerSegment + "." + payloadSegment
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, i.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseRSAPrivateKey(keyPEM string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, errInvalidJWTKey
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errInvalidJWTKey
	}
	key, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return nil, errInvalidJWTKey
	}
	return key, nil
}

func encodeJWTJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
