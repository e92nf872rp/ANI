package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"github.com/kubercloud/ani/pkg/types"
	"github.com/kubercloud/ani/services/model-service/internal/repo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stubRepo struct {
	model           *repo.Model
	models          []*repo.Model
	version         *repo.ModelVersion
	versions        []*repo.ModelVersion
	err             error
	getTenantID     uuid.UUID
	getModelID      uuid.UUID
	versionTenantID uuid.UUID
	versionID       uuid.UUID
	listFilter      repo.ListFilter
	total           int64
	nextCursor      string
}

func (s *stubRepo) GetVersionByID(_ context.Context, _ *pgxpool.Pool, tenantID, versionID uuid.UUID) (*repo.Model, *repo.ModelVersion, error) {
	s.versionTenantID = tenantID
	s.versionID = versionID
	return s.model, s.version, s.err
}

func (s *stubRepo) Create(context.Context, pgx.Tx, repo.CreateModelReq) (*repo.Model, error) {
	panic("unexpected Create")
}
func (s *stubRepo) GetByID(_ context.Context, _ *pgxpool.Pool, tenantID, modelID uuid.UUID) (*repo.Model, error) {
	s.getTenantID = tenantID
	s.getModelID = modelID
	return s.model, s.err
}
func (s *stubRepo) List(_ context.Context, _ *pgxpool.Pool, filter repo.ListFilter) ([]*repo.Model, int64, string, error) {
	s.listFilter = filter
	return s.models, s.total, s.nextCursor, s.err
}
func (s *stubRepo) SoftDelete(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	panic("unexpected SoftDelete")
}
func (s *stubRepo) CreateVersion(context.Context, pgx.Tx, repo.CreateVersionReq) (*repo.ModelVersion, error) {
	panic("unexpected CreateVersion")
}
func (s *stubRepo) CreateImport(context.Context, pgx.Tx, repo.CreateImportRequest) (*repo.ImportTask, bool, error) {
	panic("unexpected CreateImport")
}
func (s *stubRepo) ListVersions(context.Context, *pgxpool.Pool, uuid.UUID, uuid.UUID) ([]*repo.ModelVersion, error) {
	return s.versions, s.err
}

func TestGetModelRejectsForeignTenantResult(t *testing.T) {
	tenantA := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tenantB := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	modelID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	stub := &stubRepo{model: &repo.Model{TenantID: tenantB, ID: modelID}}
	svc := NewModelService(nil, stub)

	_, err := svc.GetModel(context.Background(), &modelv1.GetModelRequest{
		TenantId: tenantA.String(), ModelId: modelID.String(),
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, want NotFound", status.Code(err))
	}
	if stub.getTenantID != tenantA || stub.getModelID != modelID {
		t.Fatalf("repo GetByID args = (%s, %s), want (%s, %s)", stub.getTenantID, stub.getModelID, tenantA, modelID)
	}
}

func TestListModelsFailsClosedOnForeignTenantResult(t *testing.T) {
	tenantA := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tenantB := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	stub := &stubRepo{
		models:     []*repo.Model{{TenantID: tenantB, ID: uuid.New()}},
		total:      99,
		nextCursor: "foreign-derived-cursor",
	}
	svc := NewModelService(nil, stub)

	got, err := svc.ListModels(context.Background(), &modelv1.ListModelsRequest{TenantId: tenantA.String()})
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal fail-closed response", status.Code(err))
	}
	if got != nil {
		t.Fatalf("response = %+v, want nil so total/cursor cannot escape", got)
	}
	if stub.listFilter.TenantID != tenantA {
		t.Fatalf("repo List tenant = %s, want %s", stub.listFilter.TenantID, tenantA)
	}
}

func TestListModelVersionsAppliesCursorAndPreservesTotal(t *testing.T) {
	tenant := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	firstAt := time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC)
	secondAt := firstAt.Add(-time.Minute)
	thirdAt := secondAt.Add(-time.Minute)
	first := &repo.ModelVersion{ID: uuid.MustParse("33333333-3333-3333-3333-333333333333"), ModelID: modelID, CreatedAt: firstAt}
	second := &repo.ModelVersion{ID: uuid.MustParse("44444444-4444-4444-4444-444444444444"), ModelID: modelID, CreatedAt: secondAt}
	third := &repo.ModelVersion{ID: uuid.MustParse("55555555-5555-5555-5555-555555555555"), ModelID: modelID, CreatedAt: thirdAt}
	stub := &stubRepo{versions: []*repo.ModelVersion{first, second, third}}
	svc := NewModelService(nil, stub)
	resp, err := svc.ListModelVersions(context.Background(), &modelv1.ListModelVersionsRequest{
		TenantId: tenant.String(), ModelId: modelID.String(),
		Page: &commonv1.CursorPageRequest{Limit: 1, Cursor: types.EncodeCursor(firstAt, first.ID)},
	})
	if err != nil {
		t.Fatalf("ListModelVersions error = %v", err)
	}
	if got := len(resp.GetVersions()); got != 1 || resp.GetVersions()[0].GetId() != second.ID.String() {
		t.Fatalf("versions = %#v, want second only", resp.GetVersions())
	}
	if resp.GetMeta().GetTotal() != 3 || resp.GetMeta().GetNextCursor() == "" {
		t.Fatalf("meta = %+v, want total=3 and next cursor", resp.GetMeta())
	}
}

func TestGetModelVersionReturnsParentAndVersion(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	stub := &stubRepo{
		model: &repo.Model{
			TenantID:     tenantID,
			ID:           modelID,
			Name:         "qwen",
			DisplayName:  "Qwen 7B",
			Capabilities: []string{"text-generation"},
			Status:       "ready",
		},
		version: &repo.ModelVersion{
			ID:             versionID,
			ModelID:        modelID,
			Version:        "v1",
			Format:         "safetensors",
			IsEncrypted:    true,
			EncryptAlgo:    "sm4",
			EncryptHint:    "do-not-leak",
			SizeBytes:      12,
			ChecksumSHA256: "abc",
			StoragePath:    "object://models/qwen/v1",
		},
	}
	svc := NewModelService(nil, stub)

	got, err := svc.GetModelVersion(context.Background(), &modelv1.GetModelVersionRequest{
		TenantId:       tenantID.String(),
		ModelVersionId: versionID.String(),
	})
	if err != nil {
		t.Fatalf("GetModelVersion: %v", err)
	}
	if stub.versionID != versionID {
		t.Fatalf("repo version id = %s", stub.versionID)
	}
	if stub.versionTenantID != tenantID {
		t.Fatalf("repo version tenant = %s, want %s", stub.versionTenantID, tenantID)
	}
	if got.GetModel().GetId() != modelID.String() || got.GetModel().GetStatus() != "ready" {
		t.Fatalf("model = %+v", got.GetModel())
	}
	if got.GetVersion().GetId() != versionID.String() || got.GetVersion().GetEncryptAlgo() != "sm4" {
		t.Fatalf("version = %+v", got.GetVersion())
	}
	if got.GetVersion().GetEncryptHint() != "" {
		t.Fatalf("encrypt_hint leaked: %q", got.GetVersion().GetEncryptHint())
	}
}

func TestGetModelVersionRejectsForeignTenantResult(t *testing.T) {
	tenantA := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tenantB := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	stub := &stubRepo{model: &repo.Model{TenantID: tenantB, ID: uuid.New()}}
	svc := NewModelService(nil, stub)

	got, err := svc.GetModelVersion(context.Background(), &modelv1.GetModelVersionRequest{
		TenantId: tenantA.String(), ModelVersionId: versionID.String(),
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, want NotFound", status.Code(err))
	}
	if got != nil {
		t.Fatalf("response = %+v, want nil", got)
	}
	if stub.versionTenantID != tenantA || stub.versionID != versionID {
		t.Fatalf("repo GetVersionByID args = (%s, %s), want (%s, %s)", stub.versionTenantID, stub.versionID, tenantA, versionID)
	}
}

func TestGetModelVersionMapsNotFound(t *testing.T) {
	stub := &stubRepo{err: types.Wrapf(types.ErrNotFound, "missing")}
	svc := NewModelService(nil, stub)
	_, err := svc.GetModelVersion(context.Background(), &modelv1.GetModelVersionRequest{
		TenantId:       "11111111-1111-1111-1111-111111111111",
		ModelVersionId: "33333333-3333-3333-3333-333333333333",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v err = %v", status.Code(err), err)
	}
}

func TestGetModelVersionRejectsInvalidIDs(t *testing.T) {
	svc := NewModelService(nil, &stubRepo{})
	_, err := svc.GetModelVersion(context.Background(), &modelv1.GetModelVersionRequest{
		TenantId:       "not-a-uuid",
		ModelVersionId: "33333333-3333-3333-3333-333333333333",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v err = %v", status.Code(err), err)
	}
}

func TestValidateModelVersionReqAcceptsTenantPVC(t *testing.T) {
	req := &modelv1.CreateModelVersionRequest{
		Version: "v1", Format: "safetensors", StoragePath: "pvc://vllm-model#/models/qwen", SizeBytes: 1,
	}
	if err := validateModelVersionReq(req); err != nil {
		t.Fatalf("valid pvc://: %v", err)
	}
	req.StoragePath = "pvc://vllm-model"
	if err := validateModelVersionReq(req); err != nil {
		t.Fatalf("claim-only pvc://: %v", err)
	}
}

func TestValidateModelVersionReqRejectsNonLocalSources(t *testing.T) {
	req := &modelv1.CreateModelVersionRequest{
		Version: "v1", Format: "safetensors", SizeBytes: 1,
	}
	for _, path := range []string{
		"",
		"object://models/qwen/v1",
		"file:///models/qwen",
		"dir:///data/qwen",
		"pvc://",
		"pvc://VLLM",
		"pvc://vllm_model",
		"pvc://vllm-model#../etc",
		"hostPath:/data/models",
	} {
		req.StoragePath = path
		if err := validateModelVersionReq(req); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
}

func TestGetModelVersionMapsUnexpectedRepoError(t *testing.T) {
	stub := &stubRepo{err: errors.New("db down")}
	svc := NewModelService(nil, stub)
	_, err := svc.GetModelVersion(context.Background(), &modelv1.GetModelVersionRequest{
		TenantId:       "11111111-1111-1111-1111-111111111111",
		ModelVersionId: "33333333-3333-3333-3333-333333333333",
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v err = %v", status.Code(err), err)
	}
}

func TestImportModelRejectsInvalidInputBeforeOpeningDatabase(t *testing.T) {
	tenantID := "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		name string
		req  *modelv1.ImportModelRequest
	}{
		{
			name: "source",
			req:  &modelv1.ImportModelRequest{TenantId: tenantID, RepoId: "Qwen/Qwen3-0.6B", IdempotencyKey: "k"},
		},
		{
			name: "repo_id",
			req:  &modelv1.ImportModelRequest{TenantId: tenantID, Source: "huggingface", IdempotencyKey: "k"},
		},
		{
			name: "idempotency_key",
			req:  &modelv1.ImportModelRequest{TenantId: tenantID, Source: "huggingface", RepoId: "Qwen/Qwen3-0.6B"},
		},
		{
			name: "unsupported source",
			req:  &modelv1.ImportModelRequest{TenantId: tenantID, Source: "github", RepoId: "Qwen/Qwen3-0.6B", IdempotencyKey: "k"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewModelService(nil, &stubRepo{})
			_, err := svc.ImportModel(context.Background(), tc.req)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
			}
		})
	}
}

func TestValidateModelImportAllowsModelScopeRevisionResolver(t *testing.T) {
	if err := validateModelImportRequest("modelscope", "Qwen/Qwen3-0.6B", "master", "k"); err != nil {
		t.Fatalf("mutable ModelScope revision rejected before resolver: %v", err)
	}
	if err := validateModelImportRequest("modelscope", "Qwen/Qwen3-0.6B", "0123456789abcdef0123456789abcdef01234567", "k"); err != nil {
		t.Fatalf("immutable ModelScope revision rejected: %v", err)
	}
	if err := validateModelImportRequest("huggingface", "Qwen/Qwen3-0.6B", "main", "k"); err != nil {
		t.Fatalf("Hugging Face mutable revision should remain resolver-bound: %v", err)
	}
}
