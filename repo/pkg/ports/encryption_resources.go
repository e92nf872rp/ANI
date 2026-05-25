package ports

import (
	"context"
	"io"
	"time"
)

type EncryptionKeyCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	Name           string
	Algorithm      string
}

type EncryptionKeyGetRequest struct {
	TenantID string
	KeyID    string
}

type EncryptionKeyListRequest struct{ TenantID string }

type EncryptionSealRequest struct {
	TenantID       string
	IdempotencyKey string
	KeyID          string
	ObjectURI      string
}

type EncryptionUnsealTokenRequest struct {
	TenantID        string
	KeyID           string
	SealedObjectURI string
}

type EncryptionObjectContentSealRequest struct {
	TenantID       string
	IdempotencyKey string
	KeyID          string
	ObjectURI      string
	ChunkSize      int
}

type EncryptionObjectContentOpenRequest struct {
	TenantID         string
	KeyID            string
	ObjectURI        string
	SealedObjectURI  string
	Nonce            string
	ChunkSize        int
	ChunkCount       int
	PlaintextSHA256  string
	CiphertextSHA256 string
}

type EncryptionKeyRotateRequest struct {
	TenantID       string
	KeyID          string
	IdempotencyKey string
}

type EncryptionKeyRevokeRequest struct {
	TenantID       string
	KeyID          string
	IdempotencyKey string
	Reason         string
}

type EncryptionKeyRecord struct {
	KeyID        string
	TenantID     string
	Name         string
	Algorithm    string
	State        string
	Provider     string
	RealProvider bool
	ProviderRefs []string
	CreatedAt    int64
	UpdatedAt    int64
}

type EncryptionKeyRotationRecord struct {
	TenantID    string
	PreviousKey EncryptionKeyRecord
	RotatedKey  EncryptionKeyRecord
	RotationID  string
	RotatedAt   int64
}

type EncryptionSealRecord struct {
	KeyID           string
	TenantID        string
	ObjectURI       string
	SealedObjectURI string
	UnsealToken     string
	Provider        string
	RealProvider    bool
	ProviderRefs    []string
	ExpiresAt       int64
	CreatedAt       int64
}

type EncryptionUnsealTokenRecord struct {
	KeyID           string
	TenantID        string
	SealedObjectURI string
	UnsealToken     string
	Provider        string
	RealProvider    bool
	ProviderRefs    []string
	ExpiresAt       int64
	CreatedAt       int64
}

type EncryptionObjectContentSealRecord struct {
	KeyID               string
	TenantID            string
	ObjectURI           string
	SealedObjectURI     string
	Algorithm           string
	Nonce               string
	ChunkSize           int
	ChunkCount          int
	PlaintextSizeBytes  int64
	CiphertextSizeBytes int64
	PlaintextSHA256     string
	CiphertextSHA256    string
	Provider            string
	RealProvider        bool
	ProviderRefs        []string
	CreatedAt           int64
}

type EncryptionObjectContentOpenRecord struct {
	KeyID              string
	TenantID           string
	ObjectURI          string
	SealedObjectURI    string
	Algorithm          string
	ChunkSize          int
	ChunkCount         int
	PlaintextSizeBytes int64
	PlaintextSHA256    string
	Provider           string
	RealProvider       bool
	OpenedAt           int64
}

type EncryptionProviderCreateKeyRequest struct {
	TenantID  string
	KeyID     string
	Name      string
	Algorithm string
}

type EncryptionProviderRotateKeyRequest struct {
	TenantID      string
	PreviousKeyID string
	RotatedKeyID  string
	Name          string
	Algorithm     string
}

type EncryptionProviderRevokeKeyRequest struct {
	TenantID  string
	KeyID     string
	Reason    string
	Algorithm string
}

type EncryptionProviderDeleteKeyRequest struct {
	TenantID  string
	KeyID     string
	Algorithm string
}

type EncryptionProviderSealRequest struct {
	TenantID       string
	KeyID          string
	Algorithm      string
	ObjectURI      string
	IdempotencyKey string
}

type EncryptionProviderUnsealTokenRequest struct {
	TenantID        string
	KeyID           string
	Algorithm       string
	SealedObjectURI string
}

type EncryptionProviderKeyResult struct {
	Applied      bool
	Provider     string
	ResourceRefs []string
	Reason       string
	AppliedAt    time.Time
}

type EncryptionProviderSealResult struct {
	SealedObjectURI string
	UnsealToken     string
	ExpiresAt       time.Time
	Provider        string
	ResourceRefs    []string
}

type EncryptionProviderUnsealTokenResult struct {
	UnsealToken  string
	ExpiresAt    time.Time
	Provider     string
	ResourceRefs []string
}

type EncryptionProvider interface {
	CreateKeyMaterial(ctx context.Context, req EncryptionProviderCreateKeyRequest) (EncryptionProviderKeyResult, error)
	RotateKeyMaterial(ctx context.Context, req EncryptionProviderRotateKeyRequest) (EncryptionProviderKeyResult, error)
	RevokeKeyMaterial(ctx context.Context, req EncryptionProviderRevokeKeyRequest) (EncryptionProviderKeyResult, error)
	DeleteKeyMaterial(ctx context.Context, req EncryptionProviderDeleteKeyRequest) (EncryptionProviderKeyResult, error)
	SealObject(ctx context.Context, req EncryptionProviderSealRequest) (EncryptionProviderSealResult, error)
	CreateUnsealToken(ctx context.Context, req EncryptionProviderUnsealTokenRequest) (EncryptionProviderUnsealTokenResult, error)
}

type EncryptionService interface {
	CreateKey(ctx context.Context, req EncryptionKeyCreateRequest) (EncryptionKeyRecord, error)
	GetKey(ctx context.Context, req EncryptionKeyGetRequest) (EncryptionKeyRecord, error)
	ListKeys(ctx context.Context, req EncryptionKeyListRequest) ([]EncryptionKeyRecord, error)
	DeleteKey(ctx context.Context, req EncryptionKeyGetRequest) (EncryptionKeyRecord, error)
	RotateKey(ctx context.Context, req EncryptionKeyRotateRequest) (EncryptionKeyRotationRecord, error)
	RevokeKey(ctx context.Context, req EncryptionKeyRevokeRequest) (EncryptionKeyRecord, error)
	Seal(ctx context.Context, req EncryptionSealRequest) (EncryptionSealRecord, error)
	CreateUnsealToken(ctx context.Context, req EncryptionUnsealTokenRequest) (EncryptionUnsealTokenRecord, error)
	SealObjectContent(ctx context.Context, req EncryptionObjectContentSealRequest, plaintext io.Reader, sealed io.Writer) (EncryptionObjectContentSealRecord, error)
	OpenObjectContent(ctx context.Context, req EncryptionObjectContentOpenRequest, sealed io.Reader, plaintext io.Writer) (EncryptionObjectContentOpenRecord, error)
}
