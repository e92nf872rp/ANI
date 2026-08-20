package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"github.com/kubercloud/ani/pkg/types"
	"github.com/kubercloud/ani/services/model-service/internal/repo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stubRepo struct {
	model     *repo.Model
	version   *repo.ModelVersion
	err       error
	versionID uuid.UUID
}

func (s *stubRepo) GetVersionByID(_ context.Context, _ *pgxpool.Pool, versionID uuid.UUID) (*repo.Model, *repo.ModelVersion, error) {
	s.versionID = versionID
	return s.model, s.version, s.err
}

func (s *stubRepo) Create(context.Context, pgx.Tx, repo.CreateModelReq) (*repo.Model, error) {
	panic("unexpected Create")
}
func (s *stubRepo) GetByID(context.Context, *pgxpool.Pool, uuid.UUID) (*repo.Model, error) {
	panic("unexpected GetByID")
}
func (s *stubRepo) List(context.Context, *pgxpool.Pool, repo.ListFilter) ([]*repo.Model, int64, string, error) {
	panic("unexpected List")
}
func (s *stubRepo) SoftDelete(context.Context, pgx.Tx, uuid.UUID) error {
	panic("unexpected SoftDelete")
}
func (s *stubRepo) CreateVersion(context.Context, pgx.Tx, repo.CreateVersionReq) (*repo.ModelVersion, error) {
	panic("unexpected CreateVersion")
}
func (s *stubRepo) ListVersions(context.Context, *pgxpool.Pool, uuid.UUID) ([]*repo.ModelVersion, error) {
	panic("unexpected ListVersions")
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
