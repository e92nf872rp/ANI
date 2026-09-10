package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"github.com/kubercloud/ani/services/model-service/internal/repo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const testModelChecksum = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

func TestParseModelObjectPathEnforcesTenantAndModel(t *testing.T) {
	ref, err := parseModelObjectPath("object://models/11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222/v1/doc-1/doc.bin")
	if err != nil {
		t.Fatalf("parse object path: %v", err)
	}
	if ref.BucketClass != "model" || ref.ObjectKey == "" || ref.TenantID == "" {
		t.Fatalf("reference = %+v", ref)
	}
	for _, path := range []string{
		"object://models/tenant-a/model/v1/doc-1/doc.bin",
		"object://models/11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222/v1/../secret/file.bin",
		"object://datasets/11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222/v1/doc-1/doc.bin",
	} {
		if _, err := parseModelObjectPath(path); err == nil {
			t.Fatalf("accepted unsafe object path %q", path)
		}
	}
}

func TestModelObjectStoreContractUsesAuthoritativeMetadata(t *testing.T) {
	store := &fakeModelObjectStore{metadata: ModelObjectMetadata{SizeBytes: 8, Checksum: "sha"}}
	if store.metadata.SizeBytes != 8 || store.metadata.Checksum != "sha" {
		t.Fatal("fake metadata setup failed")
	}
	var _ ModelObjectStore = store
	_ = context.Background()
	_ = time.Minute
}

func TestGetUploadURLIsDeterministicForAnIdempotencyRequest(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	store := &fakeModelObjectStore{metadata: ModelObjectMetadata{SizeBytes: 8, Checksum: "sha"}}
	svc := NewModelServiceWithObjectStore(nil, &stubRepo{model: &repo.Model{TenantID: tenantID, ID: modelID}}, store)
	req := &modelv1.GetUploadURLRequest{TenantId: tenantID.String(), ModelId: modelID.String(), Version: "v1", FileName: "model.bin", SizeBytes: 8, IdempotencyKey: "upload-1"}
	one, err := svc.GetUploadURL(context.Background(), req)
	if err != nil {
		t.Fatalf("first URL: %v", err)
	}
	two, err := svc.GetUploadURL(context.Background(), req)
	if err != nil {
		t.Fatalf("replayed URL: %v", err)
	}
	if one.GetDocId() == "" || one.GetDocId() != two.GetDocId() || one.GetStoragePath() != two.GetStoragePath() {
		t.Fatalf("URLs are not deterministic: one=%+v two=%+v", one, two)
	}
	if store.lastRef.TenantID != tenantID.String() || store.lastRef.ModelID != modelID.String() || store.lastRef.BucketClass != "model" {
		t.Fatalf("object ref = %+v", store.lastRef)
	}
}

func TestGetUploadURLReturnsRequiredSHA256MetadataHeader(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	store := &signedHeaderModelObjectStore{fakeModelObjectStore: &fakeModelObjectStore{}}
	svc := NewModelServiceWithObjectStore(nil, &stubRepo{model: &repo.Model{TenantID: tenantID, ID: modelID}}, store)
	got, err := svc.GetUploadURL(context.Background(), &modelv1.GetUploadURLRequest{
		TenantId: tenantID.String(), ModelId: modelID.String(), Version: "v1", FileName: "model.bin", SizeBytes: 8,
		ChecksumSha256: "SHA256:DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD", IdempotencyKey: "upload-sha",
	})
	if err != nil {
		t.Fatalf("GetUploadURL() error = %v", err)
	}
	if got.GetUploadHeaders()["x-amz-meta-sha256"] != testModelChecksum {
		t.Fatalf("upload_headers = %#v, want x-amz-meta-sha256=%q", got.GetUploadHeaders(), testModelChecksum)
	}
	if store.signedHeaderCalls != 1 || store.unsignedCalls != 0 {
		t.Fatalf("signed/unsigned calls = %d/%d, want 1/0", store.signedHeaderCalls, store.unsignedCalls)
	}
}

func TestGetUploadURLIdempotencyHashIncludesChecksum(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	repoStub := &captureMutationRepo{stubRepo: &stubRepo{model: &repo.Model{TenantID: tenantID, ID: modelID}}}
	svc := NewModelServiceWithObjectStore(nil, repoStub, &signedHeaderModelObjectStore{fakeModelObjectStore: &fakeModelObjectStore{}})
	base := func(checksum string) *modelv1.GetUploadURLRequest {
		return &modelv1.GetUploadURLRequest{TenantId: tenantID.String(), ModelId: modelID.String(), Version: "v1", FileName: "model.bin", SizeBytes: 8, ChecksumSha256: checksum, IdempotencyKey: "upload-hash"}
	}
	if _, err := svc.GetUploadURL(context.Background(), base("")); err != nil {
		t.Fatalf("legacy upload URL: %v", err)
	}
	if _, err := svc.GetUploadURL(context.Background(), base(testModelChecksum)); err != nil {
		t.Fatalf("checksummed upload URL: %v", err)
	}
	if len(repoStub.hashes) != 2 || repoStub.hashes[0] == repoStub.hashes[1] {
		t.Fatalf("mutation hashes = %#v, want distinct empty/checksum hashes", repoStub.hashes)
	}
}

func TestGetUploadURLFailsClosedWhenChecksumSignerIsUnavailable(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	svc := NewModelServiceWithObjectStore(nil, &stubRepo{model: &repo.Model{TenantID: tenantID, ID: modelID}}, &fakeModelObjectStore{})
	_, err := svc.GetUploadURL(context.Background(), &modelv1.GetUploadURLRequest{
		TenantId: tenantID.String(), ModelId: modelID.String(), Version: "v1", FileName: "model.bin", SizeBytes: 8,
		ChecksumSha256: testModelChecksum, IdempotencyKey: "upload-no-signer",
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %v, want Unavailable (err=%v)", status.Code(err), err)
	}
}

func TestGetUploadURLRejectsInvalidChecksumButKeepsLegacyShapeCompatible(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	store := &fakeModelObjectStore{}
	svc := NewModelServiceWithObjectStore(nil, &stubRepo{model: &repo.Model{TenantID: tenantID, ID: modelID}}, store)
	base := func(checksum string) *modelv1.GetUploadURLRequest {
		return &modelv1.GetUploadURLRequest{TenantId: tenantID.String(), ModelId: modelID.String(), Version: "v1", FileName: "model.bin", SizeBytes: 8, ChecksumSha256: checksum, IdempotencyKey: "upload-compat"}
	}
	if _, err := svc.GetUploadURL(context.Background(), base("etag-md5")); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid checksum code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}
	legacy, err := svc.GetUploadURL(context.Background(), base(""))
	if err != nil {
		t.Fatalf("legacy GetUploadURL() error = %v", err)
	}
	if legacy.GetUploadHeaders() != nil {
		t.Fatalf("legacy upload_headers = %#v, want nil; old clients must not be given a false checksum", legacy.GetUploadHeaders())
	}
}

func TestCreateModelVersionRejectsMD5ETagAsChecksum(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	store := &fakeModelObjectStore{metadata: ModelObjectMetadata{SizeBytes: 8, Checksum: "0123456789abcdef0123456789abcdef"}}
	svc := NewModelServiceWithObjectStore(nil, &stubRepo{model: &repo.Model{TenantID: tenantID, ID: modelID}}, store)
	_, err := svc.CreateModelVersion(context.Background(), &modelv1.CreateModelVersionRequest{
		TenantId: tenantID.String(), ModelId: modelID.String(), Version: "v1", Format: "safetensors",
		StoragePath:    "object://models/" + tenantID.String() + "/" + modelID.String() + "/v1/doc-1/model.bin",
		ChecksumSha256: testModelChecksum, SizeBytes: 8, IdempotencyKey: "version-md5",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("MD5 ETag code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}
}

func TestCreateModelVersionFailsClosedWithoutContentVerifier(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	store := &fakeModelObjectStore{metadata: ModelObjectMetadata{SizeBytes: 8, Checksum: testModelChecksum}}
	svc := NewModelServiceWithObjectStore(nil, &stubRepo{model: &repo.Model{TenantID: tenantID, ID: modelID}}, store)
	_, err := svc.CreateModelVersion(context.Background(), &modelv1.CreateModelVersionRequest{
		TenantId: tenantID.String(), ModelId: modelID.String(), Version: "v1", Format: "safetensors",
		StoragePath:    "object://models/" + tenantID.String() + "/" + modelID.String() + "/v1/doc-1/model.bin",
		ChecksumSha256: testModelChecksum, SizeBytes: 8, IdempotencyKey: "version-no-verifier",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("missing verifier code = %v, want FailedPrecondition (err=%v)", status.Code(err), err)
	}
}

func TestCreateModelVersionRejectsContentMismatchAfterMetadataMatches(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	store := &verifyingFakeModelObjectStore{
		fakeModelObjectStore: &fakeModelObjectStore{metadata: ModelObjectMetadata{SizeBytes: 8, Checksum: testModelChecksum}},
		verifyErr:            errors.New("content mismatch"),
	}
	svc := NewModelServiceWithObjectStore(nil, &stubRepo{model: &repo.Model{TenantID: tenantID, ID: modelID}}, store)
	_, err := svc.CreateModelVersion(context.Background(), &modelv1.CreateModelVersionRequest{
		TenantId: tenantID.String(), ModelId: modelID.String(), Version: "v1", Format: "safetensors",
		StoragePath:    "object://models/" + tenantID.String() + "/" + modelID.String() + "/v1/doc-1/model.bin",
		ChecksumSha256: testModelChecksum, SizeBytes: 8, IdempotencyKey: "version-content-mismatch",
	})
	if status.Code(err) != codes.InvalidArgument || store.verifyCalls != 1 {
		t.Fatalf("code=%v verifier calls=%d err=%v, want InvalidArgument/1", status.Code(err), store.verifyCalls, err)
	}
}

func TestGetUploadURLFailsClosedWithoutObjectStore(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	svc := NewModelService(nil, &stubRepo{model: &repo.Model{TenantID: tenantID, ID: modelID}})
	_, err := svc.GetUploadURL(context.Background(), &modelv1.GetUploadURLRequest{TenantId: tenantID.String(), ModelId: modelID.String(), Version: "v1", FileName: "model.bin", SizeBytes: 8, IdempotencyKey: "upload-1"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %v, want Unavailable", status.Code(err))
	}
}

func TestGetModelDownloadURLRequiresExactInitContainerRequester(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	versionPath := "object://models/" + tenantID.String() + "/" + modelID.String() + "/v1/doc-1/model.bin"
	store := &fakeModelObjectStore{metadata: ModelObjectMetadata{SizeBytes: 8, Checksum: testModelChecksum}}
	stub := &stubRepo{
		model:   &repo.Model{TenantID: tenantID, ID: modelID, Status: "ready"},
		version: &repo.ModelVersion{ID: versionID, ModelID: modelID, Version: "v1", StoragePath: versionPath, SizeBytes: 8, ChecksumSHA256: testModelChecksum},
	}
	svc := NewModelServiceWithObjectStore(nil, stub, store)

	got, err := svc.GetModelDownloadURL(context.Background(), &modelv1.GetModelDownloadURLRequest{
		TenantId: tenantID.String(), ModelVersionId: versionID.String(), Requester: "init-container",
	})
	if err != nil {
		t.Fatalf("GetModelDownloadURL() error = %v", err)
	}
	if got.GetDownloadUrl() == "" || store.downloadRef.TenantID != tenantID.String() || store.downloadRef.ModelID != modelID.String() || store.downloadRef.Version != "v1" {
		t.Fatalf("response/ref = %+v / %+v", got, store.downloadRef)
	}

	for _, requester := range []string{"", "init-container ", " init-container", "INIT-CONTAINER", "gateway"} {
		store.downloadCalls = 0
		_, err := svc.GetModelDownloadURL(context.Background(), &modelv1.GetModelDownloadURLRequest{
			TenantId: tenantID.String(), ModelVersionId: versionID.String(), Requester: requester,
		})
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("requester %q code = %v, want PermissionDenied", requester, status.Code(err))
		}
		if store.downloadCalls != 0 {
			t.Errorf("requester %q invoked object store", requester)
		}
	}
}

func TestGetModelDownloadURLFailsClosedOnObjectOwnershipReadinessAndMetadata(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	otherTenantID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	validPath := "object://models/" + tenantID.String() + "/" + modelID.String() + "/v1/doc-1/model.bin"
	baseVersion := &repo.ModelVersion{ID: versionID, ModelID: modelID, Version: "v1", StoragePath: validPath, SizeBytes: 8, ChecksumSHA256: testModelChecksum}

	cases := []struct {
		name       string
		model      *repo.Model
		version    *repo.ModelVersion
		objectMeta ModelObjectMetadata
		want       codes.Code
	}{
		{name: "nil model", model: nil, version: baseVersion, objectMeta: ModelObjectMetadata{SizeBytes: 8, Checksum: testModelChecksum}, want: codes.NotFound},
		{name: "nil version", model: &repo.Model{TenantID: tenantID, ID: modelID, Status: "ready"}, version: nil, objectMeta: ModelObjectMetadata{SizeBytes: 8, Checksum: testModelChecksum}, want: codes.NotFound},
		{name: "not ready", model: &repo.Model{TenantID: tenantID, ID: modelID, Status: "pending"}, version: baseVersion, objectMeta: ModelObjectMetadata{SizeBytes: 8, Checksum: testModelChecksum}, want: codes.FailedPrecondition},
		{name: "pvc backed", model: &repo.Model{TenantID: tenantID, ID: modelID, Status: "ready"}, version: &repo.ModelVersion{ID: versionID, ModelID: modelID, Version: "v1", StoragePath: "pvc://vllm-model#/qwen3", SizeBytes: 8, ChecksumSHA256: testModelChecksum}, objectMeta: ModelObjectMetadata{SizeBytes: 8, Checksum: testModelChecksum}, want: codes.FailedPrecondition},
		{name: "foreign object tenant", model: &repo.Model{TenantID: tenantID, ID: modelID, Status: "ready"}, version: &repo.ModelVersion{ID: versionID, ModelID: modelID, Version: "v1", StoragePath: "object://models/" + otherTenantID.String() + "/" + modelID.String() + "/v1/doc-1/model.bin", SizeBytes: 8, ChecksumSHA256: testModelChecksum}, objectMeta: ModelObjectMetadata{SizeBytes: 8, Checksum: testModelChecksum}, want: codes.NotFound},
		{name: "version id mismatch", model: &repo.Model{TenantID: tenantID, ID: modelID, Status: "ready"}, version: &repo.ModelVersion{ID: uuid.MustParse("55555555-5555-5555-5555-555555555555"), ModelID: modelID, Version: "v1", StoragePath: validPath, SizeBytes: 8, ChecksumSHA256: testModelChecksum}, objectMeta: ModelObjectMetadata{SizeBytes: 8, Checksum: testModelChecksum}, want: codes.NotFound},
		{name: "version mismatch", model: &repo.Model{TenantID: tenantID, ID: modelID, Status: "ready"}, version: &repo.ModelVersion{ID: versionID, ModelID: modelID, Version: "v2", StoragePath: validPath, SizeBytes: 8, ChecksumSHA256: testModelChecksum}, objectMeta: ModelObjectMetadata{SizeBytes: 8, Checksum: testModelChecksum}, want: codes.NotFound},
		{name: "size mismatch", model: &repo.Model{TenantID: tenantID, ID: modelID, Status: "ready"}, version: baseVersion, objectMeta: ModelObjectMetadata{SizeBytes: 9, Checksum: testModelChecksum}, want: codes.FailedPrecondition},
		{name: "checksum mismatch", model: &repo.Model{TenantID: tenantID, ID: modelID, Status: "ready"}, version: baseVersion, objectMeta: ModelObjectMetadata{SizeBytes: 8, Checksum: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}, want: codes.FailedPrecondition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeModelObjectStore{metadata: tc.objectMeta}
			svc := NewModelServiceWithObjectStore(nil, &stubRepo{model: tc.model, version: tc.version}, store)
			_, err := svc.GetModelDownloadURL(context.Background(), &modelv1.GetModelDownloadURLRequest{TenantId: tenantID.String(), ModelVersionId: versionID.String(), Requester: "init-container"})
			if status.Code(err) != tc.want {
				t.Fatalf("code = %v, want %v (err=%v)", status.Code(err), tc.want, err)
			}
			if store.downloadCalls != 0 {
				t.Fatalf("signed URL called on rejected descriptor")
			}
		})
	}
}

func TestGetModelDownloadURLRejectsObjectUserinfoAndDoesNotLeakSignedURL(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	version := &repo.ModelVersion{ID: versionID, ModelID: modelID, Version: "v1", StoragePath: "object://secret:credential@models/" + tenantID.String() + "/" + modelID.String() + "/v1/doc-1/model.bin", SizeBytes: 8, ChecksumSHA256: testModelChecksum}
	store := &fakeModelObjectStore{metadata: ModelObjectMetadata{SizeBytes: 8, Checksum: testModelChecksum}}
	svc := NewModelServiceWithObjectStore(nil, &stubRepo{model: &repo.Model{TenantID: tenantID, ID: modelID, Status: "ready"}, version: version}, store)
	_, err := svc.GetModelDownloadURL(context.Background(), &modelv1.GetModelDownloadURLRequest{TenantId: tenantID.String(), ModelVersionId: versionID.String(), Requester: "init-container"})
	if status.Code(err) != codes.NotFound || strings.Contains(err.Error(), "credential") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("code/error = %v / %v, want sanitized NotFound", status.Code(err), err)
	}
}

func TestGetModelDownloadURLRedactsObjectStoreErrors(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	path := "object://models/" + tenantID.String() + "/" + modelID.String() + "/v1/doc-1/model.bin"
	version := &repo.ModelVersion{ID: versionID, ModelID: modelID, Version: "v1", StoragePath: path, SizeBytes: 8, ChecksumSHA256: testModelChecksum}
	store := &fakeModelObjectStore{metadata: ModelObjectMetadata{SizeBytes: 8, Checksum: testModelChecksum}, statErr: errors.New("minio access_key=secret")}
	svc := NewModelServiceWithObjectStore(nil, &stubRepo{model: &repo.Model{TenantID: tenantID, ID: modelID, Status: "ready"}, version: version}, store)
	_, err := svc.GetModelDownloadURL(context.Background(), &modelv1.GetModelDownloadURLRequest{TenantId: tenantID.String(), ModelVersionId: versionID.String(), Requester: "init-container"})
	if status.Code(err) != codes.Unavailable || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "access_key") {
		t.Fatalf("code/error = %v / %v, want sanitized Unavailable", status.Code(err), err)
	}
}

func TestGetModelDownloadURLRedactsRepositoryErrors(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	store := &fakeModelObjectStore{}
	svc := NewModelServiceWithObjectStore(nil, &stubRepo{err: errors.New("postgres password=secret")}, store)
	_, err := svc.GetModelDownloadURL(context.Background(), &modelv1.GetModelDownloadURLRequest{TenantId: tenantID.String(), ModelVersionId: versionID.String(), Requester: "init-container"})
	if status.Code(err) != codes.Unavailable || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "password") {
		t.Fatalf("code/error = %v / %v, want sanitized Unavailable", status.Code(err), err)
	}
}

type fakeModelObjectStore struct {
	metadata      ModelObjectMetadata
	lastRef       ModelObjectRef
	downloadRef   ModelObjectRef
	downloadCalls int
	statErr       error
	downloadErr   error
}

type signedHeaderModelObjectStore struct {
	*fakeModelObjectStore
	signedHeaderCalls int
	unsignedCalls     int
}

type verifyingFakeModelObjectStore struct {
	*fakeModelObjectStore
	verifyErr   error
	verifyCalls int
}

func (f *verifyingFakeModelObjectStore) VerifyObject(context.Context, ModelObjectRef, int64, string) error {
	f.verifyCalls++
	return f.verifyErr
}

func (f *signedHeaderModelObjectStore) SignedUploadURL(_ context.Context, ref ModelObjectRef, _ time.Duration) (ModelSignedURL, error) {
	f.unsignedCalls++
	f.fakeModelObjectStore.lastRef = ref
	return ModelSignedURL{URL: "https://object.invalid/unsigned"}, nil
}

func (f *signedHeaderModelObjectStore) SignedUploadURLWithHeaders(_ context.Context, ref ModelObjectRef, _ time.Duration, headers map[string]string) (ModelSignedURL, error) {
	f.signedHeaderCalls++
	f.fakeModelObjectStore.lastRef = ref
	return ModelSignedURL{URL: "https://object.invalid/signed?X-Amz-SignedHeaders=host%3Bx-amz-meta-sha256", Headers: headers}, nil
}

type captureMutationRepo struct {
	*stubRepo
	hashes []string
}

func (r *captureMutationRepo) ReserveMutation(_ context.Context, _ *pgxpool.Pool, _ uuid.UUID, _ string, _ string, requestHash string, documentID uuid.UUID) (uuid.UUID, bool, error) {
	r.hashes = append(r.hashes, requestHash)
	return documentID, false, nil
}

func (f *fakeModelObjectStore) SignedUploadURL(_ context.Context, ref ModelObjectRef, _ time.Duration) (ModelSignedURL, error) {
	f.lastRef = ref
	return ModelSignedURL{URL: "https://object.invalid/upload"}, nil
}
func (f *fakeModelObjectStore) SignedDownloadURL(_ context.Context, ref ModelObjectRef, _ time.Duration) (ModelSignedURL, error) {
	f.downloadRef = ref
	f.downloadCalls++
	if f.downloadErr != nil {
		return ModelSignedURL{}, f.downloadErr
	}
	return ModelSignedURL{URL: "https://object.invalid/download"}, nil
}
func (f *fakeModelObjectStore) StatObject(context.Context, ModelObjectRef) (ModelObjectMetadata, error) {
	return f.metadata, f.statErr
}
