package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"github.com/kubercloud/ani/pkg/types"
	"github.com/kubercloud/ani/services/model-service/internal/repo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ModelService struct {
	modelv1.UnimplementedModelServiceServer
	db   *pgxpool.Pool
	repo repo.ModelRepo
}

func NewModelService(db *pgxpool.Pool, modelRepo repo.ModelRepo) *ModelService {
	return &ModelService{db: db, repo: modelRepo}
}

func (s *ModelService) Register(server *grpc.Server) {
	modelv1.RegisterModelServiceServer(server, s)
}

func (s *ModelService) CreateModel(ctx context.Context, req *modelv1.CreateModelRequest) (*modelv1.Model, error) {
	tenantID, err := parseTenant(req.GetTenantId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := validateModelName(req.GetName()); err != nil {
		return nil, toStatus(err)
	}
	if req.GetDisplayName() == "" {
		return nil, toStatus(types.Wrapf(types.ErrBadRequest, "display_name required"))
	}

	ctx = withTenant(ctx, tenantID)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "begin transaction")
	}
	defer rollback(ctx, tx)

	model, err := s.repo.Create(ctx, tx, repo.CreateModelReq{
		TenantID:     tenantID,
		Name:         req.GetName(),
		DisplayName:  req.GetDisplayName(),
		Description:  req.GetDescription(),
		Capabilities: req.GetCapabilities(),
		Source:       "upload",
	})
	if err != nil {
		return nil, toStatus(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "commit transaction")
	}
	return modelToPB(model), nil
}

func (s *ModelService) GetModel(ctx context.Context, req *modelv1.GetModelRequest) (*modelv1.Model, error) {
	tenantID, id, err := parseTenantAndID(req.GetTenantId(), req.GetModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	ctx = withTenant(ctx, tenantID)
	model, err := s.repo.GetByID(ctx, s.db, tenantID, id)
	if err != nil {
		return nil, toStatus(err)
	}
	if !modelOwnedBy(model, tenantID) {
		return nil, toStatus(types.Wrapf(types.ErrNotFound, "model not found"))
	}
	return modelToPB(model), nil
}

func (s *ModelService) GetModelVersion(ctx context.Context, req *modelv1.GetModelVersionRequest) (*modelv1.GetModelVersionResponse, error) {
	tenantID, versionID, err := parseTenantAndID(req.GetTenantId(), req.GetModelVersionId())
	if err != nil {
		return nil, toStatus(err)
	}
	ctx = withTenant(ctx, tenantID)
	model, version, err := s.repo.GetVersionByID(ctx, s.db, tenantID, versionID)
	if err != nil {
		return nil, toStatus(err)
	}
	if !modelOwnedBy(model, tenantID) {
		return nil, toStatus(types.Wrapf(types.ErrNotFound, "model not found"))
	}
	versionPB := versionToPB(version)
	versionPB.EncryptHint = ""
	return &modelv1.GetModelVersionResponse{
		Model:   modelToPB(model),
		Version: versionPB,
	}, nil
}

func (s *ModelService) ListModels(ctx context.Context, req *modelv1.ListModelsRequest) (*modelv1.ListModelsResponse, error) {
	tenantID, err := parseTenant(req.GetTenantId())
	if err != nil {
		return nil, toStatus(err)
	}
	ctx = withTenant(ctx, tenantID)

	limit := 20
	cursor := ""
	if req.GetPage() != nil {
		limit = int(req.GetPage().GetLimit())
		cursor = req.GetPage().GetCursor()
	}
	models, total, nextCursor, err := s.repo.List(ctx, s.db, repo.ListFilter{
		TenantID: tenantID,
		Status:   req.GetStatus(),
		Limit:    limit,
		Cursor:   cursor,
	})
	if err != nil {
		return nil, toStatus(err)
	}
	for _, model := range models {
		if !modelOwnedBy(model, tenantID) {
			return nil, status.Error(codes.Internal, "internal error")
		}
	}
	out := &modelv1.ListModelsResponse{
		Models: make([]*modelv1.Model, 0, len(models)),
		Meta: &commonv1.CursorPageMeta{
			Total:      total,
			NextCursor: nextCursor,
		},
	}
	for _, model := range models {
		out.Models = append(out.Models, modelToPB(model))
	}
	return out, nil
}

func (s *ModelService) DeleteModel(ctx context.Context, req *modelv1.DeleteModelRequest) (*emptypb.Empty, error) {
	tenantID, id, err := parseTenantAndID(req.GetTenantId(), req.GetModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	ctx = withTenant(ctx, tenantID)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "begin transaction")
	}
	defer rollback(ctx, tx)
	if err := s.repo.SoftDelete(ctx, tx, tenantID, id); err != nil {
		return nil, toStatus(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "commit transaction")
	}
	return &emptypb.Empty{}, nil
}

func (s *ModelService) CreateModelVersion(ctx context.Context, req *modelv1.CreateModelVersionRequest) (*modelv1.ModelVersion, error) {
	tenantID, modelID, err := parseTenantAndID(req.GetTenantId(), req.GetModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	if err := validateModelVersionReq(req); err != nil {
		return nil, toStatus(err)
	}
	ctx = withTenant(ctx, tenantID)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "begin transaction")
	}
	defer rollback(ctx, tx)

	version, err := s.repo.CreateVersion(ctx, tx, repo.CreateVersionReq{
		TenantID:       tenantID,
		ModelID:        modelID,
		Version:        req.GetVersion(),
		Format:         req.GetFormat(),
		StoragePath:    req.GetStoragePath(),
		ChecksumSHA256: req.GetChecksumSha256(),
		SizeBytes:      req.GetSizeBytes(),
		IsEncrypted:    req.GetIsEncrypted(),
		EncryptAlgo:    req.GetEncryptAlgo(),
		EncryptHint:    req.GetEncryptHint(),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "commit transaction")
	}
	return versionToPB(version), nil
}

func (s *ModelService) GetUploadURL(ctx context.Context, req *modelv1.GetUploadURLRequest) (*modelv1.GetUploadURLResponse, error) {
	return nil, status.Error(codes.Unimplemented, "model upload presigned URL requires MinIO client wiring")
}

func (s *ModelService) ImportModel(ctx context.Context, req *modelv1.ImportModelRequest) (*commonv1.AsyncTaskRef, error) {
	return nil, status.Error(codes.Unimplemented, "model import requires outbox publisher and downloader worker")
}

func (s *ModelService) GetModelDownloadURL(ctx context.Context, req *modelv1.GetModelDownloadURLRequest) (*modelv1.GetModelDownloadURLResponse, error) {
	return nil, status.Error(codes.Unimplemented, "model download presigned URL requires MinIO client wiring")
}

var (
	modelNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,62}$`)
	pvcClaimPattern  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
)

// validateStoragePath accepts tenant-local model directories only:
// pvc://<dns-1123-claim>[#/absolute-path]. HuggingFace / ModelScope / MinIO
// object:// paths are the next source slice, not this one. HostPath is never a
// product source.
func validateStoragePath(raw string) error {
	path := strings.TrimSpace(raw)
	if path == "" {
		return types.Wrapf(types.ErrBadRequest, "storage_path required")
	}
	if strings.Contains(path, "..") {
		return types.Wrapf(types.ErrBadRequest, "storage_path must not contain ..")
	}
	rest, ok := strings.CutPrefix(path, "pvc://")
	if !ok {
		return types.Wrapf(types.ErrBadRequest, "storage_path must be pvc://<claim>[#/path] for a tenant-local model directory")
	}
	claim, subpath, found := strings.Cut(rest, "#")
	claim = strings.TrimSpace(claim)
	if !pvcClaimPattern.MatchString(claim) {
		return types.Wrapf(types.ErrBadRequest, "invalid pvc claim name")
	}
	if found {
		subpath = strings.TrimSpace(subpath)
		if subpath == "" || !strings.HasPrefix(subpath, "/") {
			return types.Wrapf(types.ErrBadRequest, "pvc subpath must be an absolute path")
		}
	}
	return nil
}

func validateModelName(name string) error {
	if !modelNamePattern.MatchString(name) {
		return types.Wrapf(types.ErrBadRequest, "invalid model name")
	}
	return nil
}

func validateModelVersionReq(req *modelv1.CreateModelVersionRequest) error {
	if req.GetVersion() == "" {
		return types.Wrapf(types.ErrBadRequest, "version required")
	}
	switch req.GetFormat() {
	case "safetensors", "gguf", "pytorch":
	default:
		return types.Wrapf(types.ErrBadRequest, "invalid model format")
	}
	if err := validateStoragePath(req.GetStoragePath()); err != nil {
		return err
	}
	if req.GetSizeBytes() < 0 {
		return types.Wrapf(types.ErrBadRequest, "size_bytes must be non-negative")
	}
	if req.GetIsEncrypted() {
		switch req.GetEncryptAlgo() {
		case "sm4", "zuc", "aes256gcm":
		default:
			return types.Wrapf(types.ErrBadRequest, "invalid encrypt_algo")
		}
	}
	return nil
}

func parseTenant(tenantID string) (uuid.UUID, error) {
	id, err := uuid.Parse(tenantID)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, types.Wrapf(types.ErrBadRequest, "invalid tenant_id")
	}
	return id, nil
}

func parseTenantAndID(tenantID, id string) (uuid.UUID, uuid.UUID, error) {
	tid, err := parseTenant(tenantID)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	parsedID, err := uuid.Parse(id)
	if err != nil || parsedID == uuid.Nil {
		return uuid.Nil, uuid.Nil, types.Wrapf(types.ErrBadRequest, "invalid id")
	}
	return tid, parsedID, nil
}

func withTenant(ctx context.Context, tenantID uuid.UUID) context.Context {
	return types.WithTenant(ctx, &types.TenantContext{TenantID: tenantID})
}

func modelOwnedBy(model *repo.Model, tenantID uuid.UUID) bool {
	return model != nil && tenantID != uuid.Nil && model.TenantID == tenantID
}

func modelToPB(model *repo.Model) *modelv1.Model {
	out := &modelv1.Model{
		TenantId:       model.TenantID.String(),
		Id:             model.ID.String(),
		Name:           model.Name,
		DisplayName:    model.DisplayName,
		Description:    model.Description,
		Source:         model.Source,
		SourceRepoId:   model.SourceRepoID,
		Capabilities:   model.Capabilities,
		Status:         model.Status,
		ErrorMessage:   model.ErrorMessage,
		TotalSizeBytes: model.TotalSizeBytes,
		CreatedAt:      timestamppb.New(model.CreatedAt),
		UpdatedAt:      timestamppb.New(model.UpdatedAt),
	}
	for _, version := range model.Versions {
		out.Versions = append(out.Versions, versionToPB(version))
	}
	return out
}

func versionToPB(version *repo.ModelVersion) *modelv1.ModelVersion {
	return &modelv1.ModelVersion{
		Id:             version.ID.String(),
		ModelId:        version.ModelID.String(),
		Version:        version.Version,
		Format:         version.Format,
		IsEncrypted:    version.IsEncrypted,
		EncryptAlgo:    version.EncryptAlgo,
		EncryptHint:    version.EncryptHint,
		SizeBytes:      version.SizeBytes,
		ChecksumSha256: version.ChecksumSHA256,
		StoragePath:    version.StoragePath,
		CreatedAt:      timestamppb.New(version.CreatedAt),
	}
}

func toStatus(err error) error {
	switch {
	case errors.Is(err, types.ErrNotFound):
		return status.Error(codes.NotFound, "not found")
	case errors.Is(err, types.ErrConflict):
		return status.Error(codes.AlreadyExists, "already exists")
	case errors.Is(err, types.ErrBadRequest), errors.Is(err, types.ErrInvalidState):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, types.ErrForbidden):
		return status.Error(codes.PermissionDenied, "forbidden")
	case errors.Is(err, types.ErrUnauthorized):
		return status.Error(codes.Unauthenticated, "unauthorized")
	default:
		return status.Error(codes.Internal, fmt.Sprintf("internal error: %v", err))
	}
}

func rollback(ctx context.Context, tx interface{ Rollback(context.Context) error }) {
	_ = tx.Rollback(ctx)
}
