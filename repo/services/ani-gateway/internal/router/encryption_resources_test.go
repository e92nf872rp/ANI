package router

import (
	"context"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestEncryptionAPIDevProfileAndIdempotency(t *testing.T) {
	api := newEncryptionAPI()
	a, err := api.service.CreateKey(context.Background(), ports.EncryptionKeyCreateRequest{TenantID: "t1", IdempotencyKey: "idem-1", Name: "key-a", Algorithm: "SM4"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := api.service.CreateKey(context.Background(), ports.EncryptionKeyCreateRequest{TenantID: "t1", IdempotencyKey: "idem-1", Name: "key-a", Algorithm: "SM4"})
	if err != nil {
		t.Fatal(err)
	}
	if a.KeyID != b.KeyID {
		t.Fatalf("want idempotent key id, got %s != %s", a.KeyID, b.KeyID)
	}
	resp := encryptionFromRecord(a)
	requireLocalCoreDevProfile(t, resp.DevProfile, "local-encryption-service")

	sealed, err := api.service.Seal(context.Background(), ports.EncryptionSealRequest{TenantID: "t1", IdempotencyKey: "seal-1", KeyID: a.KeyID, ObjectURI: "s3://models/qwen/model.bin"})
	if err != nil {
		t.Fatal(err)
	}
	sealedAgain, err := api.service.Seal(context.Background(), ports.EncryptionSealRequest{TenantID: "t1", IdempotencyKey: "seal-1", KeyID: a.KeyID, ObjectURI: "s3://models/qwen/model.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if sealed.SealedObjectURI != sealedAgain.SealedObjectURI {
		t.Fatalf("want idempotent sealed object uri, got %s != %s", sealed.SealedObjectURI, sealedAgain.SealedObjectURI)
	}
	sealResp := encryptionSealFromRecord(sealed)
	requireLocalCoreDevProfile(t, sealResp.DevProfile, "local-encryption-service")

	token, err := api.service.CreateUnsealToken(context.Background(), ports.EncryptionUnsealTokenRequest{TenantID: "t1", KeyID: a.KeyID, SealedObjectURI: sealed.SealedObjectURI})
	if err != nil {
		t.Fatal(err)
	}
	if token.UnsealToken == "" {
		t.Fatalf("want unseal token, got %+v", token)
	}
	tokenResp := encryptionUnsealTokenFromRecord(token)
	requireLocalCoreDevProfile(t, tokenResp.DevProfile, "local-encryption-service")

	rotation, err := api.service.RotateKey(context.Background(), ports.EncryptionKeyRotateRequest{TenantID: "t1", KeyID: a.KeyID, IdempotencyKey: "rotate-1"})
	if err != nil {
		t.Fatal(err)
	}
	rotationAgain, err := api.service.RotateKey(context.Background(), ports.EncryptionKeyRotateRequest{TenantID: "t1", KeyID: a.KeyID, IdempotencyKey: "rotate-1"})
	if err != nil {
		t.Fatal(err)
	}
	if rotation.RotatedKey.KeyID != rotationAgain.RotatedKey.KeyID {
		t.Fatalf("want idempotent rotated key id, got %s != %s", rotation.RotatedKey.KeyID, rotationAgain.RotatedKey.KeyID)
	}
	if rotation.PreviousKey.State != "rotated" || rotation.RotatedKey.State != "active" {
		t.Fatalf("unexpected rotation states: %+v", rotation)
	}
	rotationResp := encryptionRotationFromRecord(rotation)
	requireLocalCoreDevProfile(t, rotationResp.DevProfile, "local-encryption-service")

	revoked, err := api.service.RevokeKey(context.Background(), ports.EncryptionKeyRevokeRequest{TenantID: "t1", KeyID: rotation.RotatedKey.KeyID, IdempotencyKey: "revoke-1", Reason: "operator requested"})
	if err != nil {
		t.Fatal(err)
	}
	revokedAgain, err := api.service.RevokeKey(context.Background(), ports.EncryptionKeyRevokeRequest{TenantID: "t1", KeyID: rotation.RotatedKey.KeyID, IdempotencyKey: "revoke-1", Reason: "operator requested"})
	if err != nil {
		t.Fatal(err)
	}
	if revoked.State != "revoked" {
		t.Fatalf("want revoked key, got %+v", revoked)
	}
	if revoked.UpdatedAt != revokedAgain.UpdatedAt {
		t.Fatalf("want idempotent revoke replay, got updated_at %d != %d", revoked.UpdatedAt, revokedAgain.UpdatedAt)
	}
	if _, err := api.service.Seal(context.Background(), ports.EncryptionSealRequest{TenantID: "t1", IdempotencyKey: "seal-revoked", KeyID: revoked.KeyID, ObjectURI: "s3://models/qwen/after-revoke.bin"}); err == nil {
		t.Fatalf("want revoked key to reject seal")
	}
}

func TestEncryptionResponseMarksRealProviderWhenKMSProviderApplied(t *testing.T) {
	resp := encryptionFromRecord(ports.EncryptionKeyRecord{
		KeyID:        "ekey-real",
		TenantID:     "tenant-a",
		Name:         "model-seal",
		Algorithm:    "SM4",
		State:        "active",
		Provider:     "kms-sm4",
		RealProvider: true,
		ProviderRefs: []string{"kms://tenant-a/ekey-real"},
		CreatedAt:    100,
		UpdatedAt:    100,
	})
	if resp.DevProfile.Mode != "real" || !resp.DevProfile.RealProvider || resp.DevProfile.Provider != "kms-sm4-provider" {
		t.Fatalf("dev_profile = %+v, want KMS SM4 provider", resp.DevProfile)
	}
}
