package runtime

import (
	"bytes"
	"context"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalEncryptionServiceDelegatesLifecycleAndSealToProvider(t *testing.T) {
	provider := &fakeEncryptionProvider{}
	service := NewLocalEncryptionService(
		WithEncryptionProvider(provider),
	)

	key, err := service.CreateKey(context.Background(), ports.EncryptionKeyCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-key",
		Name:           "model-seal",
		Algorithm:      "SM4",
	})
	if err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}
	if provider.createCalls != 1 {
		t.Fatalf("provider create calls = %d, want 1", provider.createCalls)
	}
	if provider.created.KeyID != key.KeyID || provider.created.Algorithm != "SM4" {
		t.Fatalf("provider create request = %+v, want generated key id and SM4", provider.created)
	}
	if !key.RealProvider || key.Provider != "kms-sm4" || len(key.ProviderRefs) != 1 {
		t.Fatalf("key provider evidence = %+v, want kms-sm4 provider ref", key)
	}

	keyToDelete, err := service.CreateKey(context.Background(), ports.EncryptionKeyCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "delete-key",
		Name:           "delete-me",
		Algorithm:      "SM4",
	})
	if err != nil {
		t.Fatalf("CreateKey(delete) error = %v", err)
	}
	deleted, err := service.DeleteKey(context.Background(), ports.EncryptionKeyGetRequest{
		TenantID: "tenant-a",
		KeyID:    keyToDelete.KeyID,
	})
	if err != nil {
		t.Fatalf("DeleteKey() error = %v", err)
	}
	if provider.deleteCalls != 1 || provider.deleted.KeyID != keyToDelete.KeyID {
		t.Fatalf("provider delete request = %+v calls = %d, want deleted key id", provider.deleted, provider.deleteCalls)
	}
	if deleted.State != "deleted" || !deleted.RealProvider || deleted.Provider != "kms-sm4" {
		t.Fatalf("deleted key = %+v, want deleted kms-sm4 key", deleted)
	}

	sealed, err := service.Seal(context.Background(), ports.EncryptionSealRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "seal-object",
		KeyID:          key.KeyID,
		ObjectURI:      "s3://models/qwen/model.bin",
	})
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if provider.sealCalls != 1 {
		t.Fatalf("provider seal calls = %d, want 1", provider.sealCalls)
	}
	if sealed.SealedObjectURI != "kms+sm4://tenant-a/"+key.KeyID+"/model.bin" || sealed.UnsealToken != "kms-token" {
		t.Fatalf("sealed record = %+v, want provider seal result", sealed)
	}
	if !sealed.RealProvider || sealed.Provider != "kms-sm4" {
		t.Fatalf("seal provider evidence = %+v, want kms-sm4", sealed)
	}

	token, err := service.CreateUnsealToken(context.Background(), ports.EncryptionUnsealTokenRequest{
		TenantID:        "tenant-a",
		IdempotencyKey:  "unseal-object",
		KeyID:           key.KeyID,
		SealedObjectURI: sealed.SealedObjectURI,
	})
	if err != nil {
		t.Fatalf("CreateUnsealToken() error = %v", err)
	}
	if provider.tokenCalls != 1 || token.UnsealToken != "kms-unseal-token" {
		t.Fatalf("provider token calls = %d token = %+v, want provider token", provider.tokenCalls, token)
	}
	if !token.RealProvider || token.Provider != "kms-sm4" {
		t.Fatalf("token provider evidence = %+v, want kms-sm4", token)
	}
	tokenAgain, err := service.CreateUnsealToken(context.Background(), ports.EncryptionUnsealTokenRequest{
		TenantID:        "tenant-a",
		IdempotencyKey:  "unseal-object",
		KeyID:           key.KeyID,
		SealedObjectURI: sealed.SealedObjectURI,
	})
	if err != nil {
		t.Fatalf("CreateUnsealToken() replay error = %v", err)
	}
	if tokenAgain.UnsealToken != token.UnsealToken || provider.tokenCalls != 1 {
		t.Fatalf("token replay = %+v provider token calls = %d, want same token without provider replay", tokenAgain, provider.tokenCalls)
	}

	rotation, err := service.RotateKey(context.Background(), ports.EncryptionKeyRotateRequest{
		TenantID:       "tenant-a",
		KeyID:          key.KeyID,
		IdempotencyKey: "rotate-key",
	})
	if err != nil {
		t.Fatalf("RotateKey() error = %v", err)
	}
	if provider.rotateCalls != 1 || provider.rotated.PreviousKeyID != key.KeyID || provider.rotated.RotatedKeyID != rotation.RotatedKey.KeyID {
		t.Fatalf("provider rotate request = %+v calls = %d, want previous and rotated key ids", provider.rotated, provider.rotateCalls)
	}
	if !rotation.RotatedKey.RealProvider || rotation.RotatedKey.Provider != "kms-sm4" {
		t.Fatalf("rotated key provider evidence = %+v, want kms-sm4", rotation.RotatedKey)
	}

	revoked, err := service.RevokeKey(context.Background(), ports.EncryptionKeyRevokeRequest{
		TenantID:       "tenant-a",
		KeyID:          rotation.RotatedKey.KeyID,
		IdempotencyKey: "revoke-key",
		Reason:         "operator requested",
	})
	if err != nil {
		t.Fatalf("RevokeKey() error = %v", err)
	}
	if provider.revokeCalls != 1 || provider.revoked.KeyID != rotation.RotatedKey.KeyID {
		t.Fatalf("provider revoke request = %+v calls = %d, want rotated key id", provider.revoked, provider.revokeCalls)
	}
	if revoked.State != "revoked" || !revoked.RealProvider || revoked.Provider != "kms-sm4" {
		t.Fatalf("revoked key = %+v, want revoked kms-sm4 key", revoked)
	}
}

func TestSM4BlockCipherMatchesStandardVector(t *testing.T) {
	key, err := hex.DecodeString("0123456789abcdeffedcba9876543210")
	if err != nil {
		t.Fatalf("DecodeString(key) error = %v", err)
	}
	plain, err := hex.DecodeString("0123456789abcdeffedcba9876543210")
	if err != nil {
		t.Fatalf("DecodeString(plain) error = %v", err)
	}
	want, err := hex.DecodeString("681edf34d206965e86b3e94f536e4246")
	if err != nil {
		t.Fatalf("DecodeString(cipher) error = %v", err)
	}
	block, err := newSM4BlockCipher(key)
	if err != nil {
		t.Fatalf("newSM4BlockCipher() error = %v", err)
	}
	got := make([]byte, len(plain))
	block.Encrypt(got, plain)
	if !bytes.Equal(got, want) {
		t.Fatalf("SM4 ciphertext = %x, want %x", got, want)
	}
	decrypted := make([]byte, len(got))
	block.Decrypt(decrypted, got)
	if !bytes.Equal(decrypted, plain) {
		t.Fatalf("SM4 decrypted = %x, want %x", decrypted, plain)
	}
}

func TestLocalEncryptionServiceSealsObjectContentWithSM4GCMChunks(t *testing.T) {
	service := NewLocalEncryptionService()
	key, err := service.CreateKey(context.Background(), ports.EncryptionKeyCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "content-key",
		Name:           "model-content",
		Algorithm:      "SM4",
	})
	if err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}
	plaintext := strings.Repeat("model-weight-block-", 40)
	var sealed bytes.Buffer
	sealRecord, err := service.SealObjectContent(context.Background(), ports.EncryptionObjectContentSealRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "seal-content",
		KeyID:          key.KeyID,
		ObjectURI:      "s3://models/qwen/model.bin",
		ChunkSize:      32,
	}, strings.NewReader(plaintext), &sealed)
	if err != nil {
		t.Fatalf("SealObjectContent() error = %v", err)
	}
	if sealRecord.Algorithm != "SM4-GCM" || sealRecord.ChunkSize != 32 || sealRecord.ChunkCount <= 1 {
		t.Fatalf("seal record = %+v, want SM4-GCM multi-chunk metadata", sealRecord)
	}
	if sealRecord.SealedObjectURI == "" || sealRecord.Nonce == "" || sealRecord.PlaintextSHA256 == "" || sealRecord.CiphertextSHA256 == "" {
		t.Fatalf("seal record missing metadata: %+v", sealRecord)
	}
	if bytes.Contains(sealed.Bytes(), []byte("model-weight-block")) {
		t.Fatalf("sealed content contains plaintext marker")
	}
	var opened bytes.Buffer
	openRecord, err := service.OpenObjectContent(context.Background(), ports.EncryptionObjectContentOpenRequest{
		TenantID:         "tenant-a",
		KeyID:            key.KeyID,
		ObjectURI:        sealRecord.ObjectURI,
		SealedObjectURI:  sealRecord.SealedObjectURI,
		Nonce:            sealRecord.Nonce,
		ChunkSize:        sealRecord.ChunkSize,
		ChunkCount:       sealRecord.ChunkCount,
		PlaintextSHA256:  sealRecord.PlaintextSHA256,
		CiphertextSHA256: sealRecord.CiphertextSHA256,
	}, bytes.NewReader(sealed.Bytes()), &opened)
	if err != nil {
		t.Fatalf("OpenObjectContent() error = %v", err)
	}
	if opened.String() != plaintext {
		t.Fatalf("opened plaintext = %q, want original plaintext", opened.String())
	}
	if openRecord.PlaintextSHA256 != sealRecord.PlaintextSHA256 || openRecord.ChunkCount != sealRecord.ChunkCount {
		t.Fatalf("open record = %+v, want seal metadata", openRecord)
	}
}

type fakeEncryptionProvider struct {
	createCalls int
	rotateCalls int
	revokeCalls int
	deleteCalls int
	sealCalls   int
	tokenCalls  int
	created     ports.EncryptionProviderCreateKeyRequest
	rotated     ports.EncryptionProviderRotateKeyRequest
	revoked     ports.EncryptionProviderRevokeKeyRequest
	deleted     ports.EncryptionProviderDeleteKeyRequest
}

func (p *fakeEncryptionProvider) CreateKeyMaterial(_ context.Context, req ports.EncryptionProviderCreateKeyRequest) (ports.EncryptionProviderKeyResult, error) {
	p.createCalls++
	p.created = req
	return ports.EncryptionProviderKeyResult{
		Applied:      true,
		Provider:     "kms-sm4",
		ResourceRefs: []string{"kms://tenant-a/" + req.KeyID},
	}, nil
}

func (p *fakeEncryptionProvider) RotateKeyMaterial(_ context.Context, req ports.EncryptionProviderRotateKeyRequest) (ports.EncryptionProviderKeyResult, error) {
	p.rotateCalls++
	p.rotated = req
	return ports.EncryptionProviderKeyResult{
		Applied:      true,
		Provider:     "kms-sm4",
		ResourceRefs: []string{"kms://tenant-a/" + req.RotatedKeyID},
	}, nil
}

func (p *fakeEncryptionProvider) RevokeKeyMaterial(_ context.Context, req ports.EncryptionProviderRevokeKeyRequest) (ports.EncryptionProviderKeyResult, error) {
	p.revokeCalls++
	p.revoked = req
	return ports.EncryptionProviderKeyResult{
		Applied:      true,
		Provider:     "kms-sm4",
		ResourceRefs: []string{"kms://tenant-a/" + req.KeyID},
	}, nil
}

func (p *fakeEncryptionProvider) DeleteKeyMaterial(_ context.Context, req ports.EncryptionProviderDeleteKeyRequest) (ports.EncryptionProviderKeyResult, error) {
	p.deleteCalls++
	p.deleted = req
	return ports.EncryptionProviderKeyResult{
		Applied:      true,
		Provider:     "kms-sm4",
		ResourceRefs: []string{"kms://tenant-a/" + req.KeyID},
	}, nil
}

func (p *fakeEncryptionProvider) SealObject(_ context.Context, req ports.EncryptionProviderSealRequest) (ports.EncryptionProviderSealResult, error) {
	p.sealCalls++
	return ports.EncryptionProviderSealResult{
		SealedObjectURI: "kms+sm4://tenant-a/" + req.KeyID + "/model.bin",
		UnsealToken:     "kms-token",
		ExpiresAt:       time.Unix(2000, 0).UTC(),
		Provider:        "kms-sm4",
		ResourceRefs:    []string{"kms://tenant-a/" + req.KeyID},
	}, nil
}

func (p *fakeEncryptionProvider) CreateUnsealToken(_ context.Context, req ports.EncryptionProviderUnsealTokenRequest) (ports.EncryptionProviderUnsealTokenResult, error) {
	p.tokenCalls++
	return ports.EncryptionProviderUnsealTokenResult{
		UnsealToken:  "kms-unseal-token",
		ExpiresAt:    time.Unix(3000, 0).UTC(),
		Provider:     "kms-sm4",
		ResourceRefs: []string{"kms://tenant-a/" + req.KeyID},
	}, nil
}

var _ ports.EncryptionProvider = (*fakeEncryptionProvider)(nil)
