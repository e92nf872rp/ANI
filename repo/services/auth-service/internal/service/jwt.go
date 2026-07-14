package service

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

type JWTConfig struct {
	PublicKeyPEM         string
	PublicKeyFile        string
	PrivateKeyPEM        string
	PrivateKeyFile       string
	Issuer               string
	OIDCIssuerURL        string
	OIDCClientID         string
	OIDCClientSecret     string
	OIDCAuthURL          string
	OIDCTokenURL         string
	OIDCJWKSURL          string
	OIDCPublicKeyPEM     string
	OIDCPublicKeyFile    string
	OIDCGroupRoleMapJSON string
}

type JWTValidator struct {
	publicKey *rsa.PublicKey
	issuer    string
	blocklist tokenBlocklist
	now       func() time.Time
}

type Claims struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
	Roles    []string
	JTI      string
	Scope    string
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

type jwtPayload struct {
	Subject   string   `json:"sub"`
	Issuer    string   `json:"iss"`
	Expires   int64    `json:"exp"`
	NotBefore int64    `json:"nbf"`
	IssuedAt  int64    `json:"iat"`
	JTI       string   `json:"jti"`
	TenantID  string   `json:"tid"`
	UserID    string   `json:"uid"`
	Roles     []string `json:"roles"`
	Scope     string   `json:"scope,omitempty"`
}

func NewJWTValidator(cfg JWTConfig, blocklist tokenBlocklist) (*JWTValidator, error) {
	keyPEM := cfg.PublicKeyPEM
	if keyPEM == "" && cfg.PublicKeyFile != "" {
		data, err := os.ReadFile(cfg.PublicKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read jwt public key file: %w", err)
		}
		keyPEM = string(data)
	}
	if keyPEM == "" {
		return nil, errJWTNotConfigured
	}
	key, err := parseRSAPublicKey(keyPEM)
	if err != nil {
		return nil, err
	}
	return &JWTValidator{
		publicKey: key,
		issuer:    cfg.Issuer,
		blocklist: blocklist,
		now:       time.Now,
	}, nil
}

func (v *JWTValidator) Validate(ctx context.Context, token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errInvalidJWT
	}

	var header jwtHeader
	if err := decodeSegment(parts[0], &header); err != nil {
		return nil, errInvalidJWT
	}
	if header.Alg != "RS256" {
		return nil, errInvalidJWT
	}

	signingInput := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errInvalidJWT
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(v.publicKey, crypto.SHA256, digest[:], signature); err != nil {
		return nil, errInvalidJWT
	}

	var payload jwtPayload
	if err := decodeSegment(parts[1], &payload); err != nil {
		return nil, errInvalidJWT
	}
	if err := v.validatePayload(ctx, payload); err != nil {
		return nil, err
	}

	tenantID, err := uuid.Parse(payload.TenantID)
	if err != nil || tenantID == uuid.Nil {
		// Platform tokens carry no tenant_id (scope=platform). Tenant tokens must have a valid tenant_id.
		if payload.Scope != "platform" {
			return nil, errInvalidJWT
		}
		tenantID = uuid.Nil
	}
	userID, err := uuid.Parse(payload.UserID)
	if err != nil || userID == uuid.Nil {
		return nil, errInvalidJWT
	}
	return &Claims{
		TenantID: tenantID,
		UserID:   userID,
		Roles:    payload.Roles,
		JTI:      payload.JTI,
		Scope:    payload.Scope,
	}, nil
}

func (v *JWTValidator) validatePayload(ctx context.Context, payload jwtPayload) error {
	now := v.now().Unix()
	if payload.Expires <= 0 || now >= payload.Expires {
		return errInvalidJWT
	}
	if payload.NotBefore > 0 && now < payload.NotBefore {
		return errInvalidJWT
	}
	if v.issuer != "" && payload.Issuer != v.issuer {
		return errInvalidJWT
	}
	if payload.JTI != "" && v.blocklist != nil {
		blocked, err := v.blocklist.IsRevoked(ctx, payload.JTI)
		if err != nil {
			return fmt.Errorf("check jwt blocklist: %w", err)
		}
		if blocked {
			return errInvalidJWT
		}
	}
	return nil
}

func parseRSAPublicKey(keyPEM string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, errInvalidJWTKey
	}
	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaKey, ok := pub.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
	}
	if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
		if rsaKey, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
	}
	return nil, errInvalidJWTKey
}

func decodeSegment(segment string, out any) error {
	data, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

var (
	errJWTNotConfigured = errors.New("jwt validator is not configured")
	errInvalidJWT       = errors.New("invalid jwt")
	errInvalidJWTKey    = errors.New("invalid jwt public key")
)
