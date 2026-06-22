package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"strconv"
	"testing"
	"time"

	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestRevokeTokenAddsJTIToBlocklist(t *testing.T) {
	cache := newMemoryCache()
	svc := NewAuthService(nil, cache, JWTConfig{})

	if _, err := svc.RevokeToken(context.Background(), &authv1.RevokeTokenRequest{Jti: "jwt-123"}); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	ok, err := cache.Exists(context.Background(), jwtBlocklistKey("jwt-123"))
	if err != nil {
		t.Fatalf("cache exists: %v", err)
	}
	if !ok {
		t.Fatal("expected revoked jti in cache")
	}
}

func TestJWTValidatorRejectsBlockedJTI(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	cache := newMemoryCache()
	if err := cache.Set(context.Background(), jwtBlocklistKey("jwt-blocked"), []byte("revoked"), time.Hour); err != nil {
		t.Fatalf("cache set: %v", err)
	}
	blocklist := newTokenBlocklist(nil, cache)
	issuedAt := time.Unix(1_700_000_000, 0)
	token := signTestJWT(t, key, map[string]any{
		"tid": "1c3dcf4a-4b36-4cdb-954b-a34897d4dd00",
		"uid": "2a6f7511-6768-4fe2-a74e-699ff7b87df5",
		"exp": issuedAt.Add(time.Hour).Unix(),
		"jti": "jwt-blocked",
	})
	validator, err := NewJWTValidator(JWTConfig{PublicKeyPEM: publicKeyPEM(t, &key.PublicKey)}, blocklist)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return issuedAt.Add(time.Minute) }

	if _, err := validator.Validate(context.Background(), token); err == nil {
		t.Fatal("expected blocked token to be rejected")
	}
}

type memoryCache struct {
	values map[string][]byte
}

func newMemoryCache() *memoryCache {
	return &memoryCache{values: map[string][]byte{}}
}

var _ ports.CacheStore = (*memoryCache)(nil)

func (m *memoryCache) Get(_ context.Context, key string) ([]byte, error) {
	value, ok := m.values[key]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return value, nil
}

func (m *memoryCache) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	m.values[key] = value
	return nil
}

func (m *memoryCache) SetNX(_ context.Context, key string, value []byte, _ time.Duration) (bool, error) {
	if _, ok := m.values[key]; ok {
		return false, nil
	}
	m.values[key] = value
	return true, nil
}

func (m *memoryCache) Delete(_ context.Context, key string) error {
	delete(m.values, key)
	return nil
}

func (m *memoryCache) Increment(_ context.Context, key string, _ time.Duration) (int64, error) {
	var current int64
	if raw, ok := m.values[key]; ok {
		current, _ = strconv.ParseInt(string(raw), 10, 64)
	}
	current++
	m.values[key] = []byte(strconv.FormatInt(current, 10))
	return current, nil
}

func (m *memoryCache) Exists(_ context.Context, key string) (bool, error) {
	_, ok := m.values[key]
	return ok, nil
}
