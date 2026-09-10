package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

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
	db          *pgxpool.Pool
	repo        repo.ModelRepo
	objectStore ModelObjectStore
}

func NewModelService(db *pgxpool.Pool, modelRepo repo.ModelRepo) *ModelService {
	return &ModelService{db: db, repo: modelRepo}
}

func NewModelServiceWithObjectStore(db *pgxpool.Pool, modelRepo repo.ModelRepo, objectStore ModelObjectStore) *ModelService {
	return &ModelService{db: db, repo: modelRepo, objectStore: objectStore}
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
	idempotencyKey := strings.TrimSpace(req.GetIdempotencyKey())
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "begin transaction")
	}
	defer rollback(ctx, tx)

	model, err := s.repo.Create(ctx, tx, repo.CreateModelReq{
		TenantID:       tenantID,
		Name:           req.GetName(),
		DisplayName:    req.GetDisplayName(),
		Description:    req.GetDescription(),
		Capabilities:   req.GetCapabilities(),
		Source:         "upload",
		IdempotencyKey: idempotencyKey,
		RequestHash:    repo.ModelMutationHash(tenantID, "model.create", idempotencyKey, req.GetName(), req.GetDisplayName(), req.GetDescription(), strings.Join(req.GetCapabilities(), "\x00")),
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
		TenantID:   tenantID,
		Status:     req.GetStatus(),
		Source:     req.GetSource(),
		Capability: req.GetCapability(),
		Keyword:    req.GetKeyword(),
		Limit:      limit,
		Cursor:     cursor,
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

func (s *ModelService) ListModelVersions(ctx context.Context, req *modelv1.ListModelVersionsRequest) (*modelv1.ListModelVersionsResponse, error) {
	tenantID, modelID, err := parseTenantAndID(req.GetTenantId(), req.GetModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	ctx = withTenant(ctx, tenantID)
	versions, err := s.repo.ListVersions(ctx, s.db, tenantID, modelID)
	if err != nil {
		return nil, toStatus(err)
	}
	total := int64(len(versions))
	limit := 20
	if req.GetPage() != nil && req.GetPage().GetLimit() > 0 {
		limit = int(req.GetPage().GetLimit())
	}
	if limit > 100 {
		return nil, status.Error(codes.InvalidArgument, "limit must be between 1 and 100")
	}
	if cursor := strings.TrimSpace(req.GetPage().GetCursor()); cursor != "" {
		createdAt, versionID, err := types.DecodeCursor(cursor)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid cursor")
		}
		filtered := versions[:0]
		for _, version := range versions {
			if version.CreatedAt.Before(createdAt) || (version.CreatedAt.Equal(createdAt) && version.ID.String() < versionID.String()) {
				filtered = append(filtered, version)
			}
		}
		versions = filtered
	}
	nextCursor := ""
	if len(versions) > limit {
		last := versions[limit-1]
		nextCursor = types.EncodeCursor(last.CreatedAt, last.ID)
		versions = versions[:limit]
	}
	out := &modelv1.ListModelVersionsResponse{Versions: make([]*modelv1.ModelVersion, 0, len(versions)), Meta: &commonv1.CursorPageMeta{Total: total, NextCursor: nextCursor}}
	for _, version := range versions {
		out.Versions = append(out.Versions, versionToPB(version))
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
	if strings.HasPrefix(strings.TrimSpace(req.GetStoragePath()), "object://") {
		if s.objectStore == nil {
			return nil, status.Error(codes.Unavailable, "object storage is unavailable")
		}
		ref, err := parseModelObjectPath(req.GetStoragePath())
		if err != nil || ref.TenantID != tenantID.String() || ref.ModelID != modelID.String() || ref.Version != req.GetVersion() {
			return nil, status.Error(codes.InvalidArgument, "storage_path does not belong to this tenant model version")
		}
		metadata, err := s.objectStore.StatObject(ctx, ref)
		if err != nil {
			return nil, status.Error(codes.Unavailable, "object storage is unavailable")
		}
		if normalizedSHA256(req.GetChecksumSha256()) == "" {
			return nil, status.Error(codes.InvalidArgument, "object-backed version requires checksum_sha256")
		}
		if metadata.SizeBytes <= 0 || metadata.SizeBytes != req.GetSizeBytes() || !equalSHA256(metadata.Checksum, req.GetChecksumSha256()) {
			return nil, status.Error(codes.InvalidArgument, "uploaded object metadata does not match version")
		}
		verifier, ok := s.objectStore.(types.ModelObjectStoreContentVerifier)
		if !ok {
			return nil, status.Error(codes.FailedPrecondition, "object storage content verification is unavailable")
		}
		if err := verifier.VerifyObject(ctx, ref, req.GetSizeBytes(), req.GetChecksumSha256()); err != nil {
			return nil, status.Error(codes.InvalidArgument, "uploaded object content does not match version")
		}
	}
	idempotencyKey := strings.TrimSpace(req.GetIdempotencyKey())
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
		IdempotencyKey: idempotencyKey,
		RequestHash:    repo.ModelMutationHash(tenantID, "model.version", idempotencyKey, modelID.String(), req.GetVersion(), req.GetFormat(), req.GetStoragePath(), req.GetChecksumSha256(), fmt.Sprintf("%d", req.GetSizeBytes()), fmt.Sprintf("%t", req.GetIsEncrypted()), req.GetEncryptAlgo(), req.GetEncryptHint()),
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
	tenantID, modelID, err := parseTenantAndID(req.GetTenantId(), req.GetModelId())
	if err != nil {
		return nil, toStatus(err)
	}
	if s.objectStore == nil {
		return nil, status.Error(codes.Unavailable, "object storage is unavailable")
	}
	if strings.TrimSpace(req.GetIdempotencyKey()) == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key required")
	}
	if strings.TrimSpace(req.GetVersion()) == "" {
		return nil, status.Error(codes.InvalidArgument, "version required")
	}
	if err := validateModelUploadFileName(req.GetFileName()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if req.GetSizeBytes() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "size_bytes must be positive")
	}
	// The checksum is additive for old callers, but when supplied it is
	// validated before issuing a URL and echoed as object metadata. This avoids
	// relying on an S3 ETag, which is commonly an MD5 (or multipart composite)
	// and is never a valid model SHA-256 proof.
	checksum := ""
	if raw := strings.TrimSpace(req.GetChecksumSha256()); raw != "" {
		normalized := normalizedSHA256(raw)
		if normalized == "" {
			return nil, status.Error(codes.InvalidArgument, "checksum_sha256 must be a SHA-256 digest")
		}
		checksum = "sha256:" + normalized
	}
	ctx = withTenant(ctx, tenantID)
	if _, err := s.repo.GetByID(ctx, s.db, tenantID, modelID); err != nil {
		return nil, toStatus(err)
	}
	// Include the canonical checksum (including the empty legacy value) in the
	// idempotency hash. A replay key must not make two uploads with different
	// content proofs share a reservation or storage path.
	requestHash := repo.ModelMutationHash(tenantID, "model.upload", req.GetIdempotencyKey(), modelID.String(), req.GetVersion(), req.GetFileName(), fmt.Sprintf("%d", req.GetSizeBytes()), checksum)
	documentID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(requestHash))
	if reserver, ok := s.repo.(interface {
		ReserveMutation(context.Context, *pgxpool.Pool, uuid.UUID, string, string, string, uuid.UUID) (uuid.UUID, bool, error)
	}); ok {
		reservedID, _, err := reserver.ReserveMutation(ctx, s.db, tenantID, "model.upload:"+modelID.String(), req.GetIdempotencyKey(), requestHash, documentID)
		if err != nil {
			return nil, toStatus(err)
		}
		documentID = reservedID
	}
	ref := ModelObjectRef{TenantID: tenantID.String(), ModelID: modelID.String(), BucketClass: "model", Version: req.GetVersion(), ObjectKey: modelID.String() + "/" + req.GetVersion() + "/" + documentID.String() + "/" + req.GetFileName()}
	var signed ModelSignedURL
	if checksum != "" {
		signer, ok := s.objectStore.(types.ModelObjectStoreUploadHeaders)
		if !ok {
			// Never return an unsigned checksum header: a backend that cannot
			// bind it into SigV4 would make the upload proof forgeable.
			return nil, status.Error(codes.Unavailable, "object storage does not support signed checksum uploads")
		}
		signed, err = signer.SignedUploadURLWithHeaders(ctx, ref, time.Hour, map[string]string{"x-amz-meta-sha256": checksum})
		if err == nil && !signedUploadHeaderMatches(signed.Headers, checksum) {
			err = errors.New("object storage did not sign checksum header")
		}
	} else {
		signed, err = s.objectStore.SignedUploadURL(ctx, ref, time.Hour)
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "object storage is unavailable")
	}
	expiresAt := signed.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = time.Now().UTC().Add(time.Hour)
	}
	response := &modelv1.GetUploadURLResponse{
		UploadUrl: signed.URL, StoragePath: modelObjectStoragePath(tenantID.String(), modelID.String(), req.GetVersion(), documentID.String(), req.GetFileName()),
		DocId: documentID.String(), ExpiresAt: timestamppb.New(expiresAt),
	}
	if checksum != "" {
		// x-amz-meta-sha256 is intentionally not taken from the object-store
		// adapter's arbitrary header map: only this stable, non-secret header is
		// exposed to callers. CreateModelVersion re-reads and verifies it.
		response.UploadHeaders = map[string]string{"x-amz-meta-sha256": checksum}
	}
	return response, nil
}

func signedUploadHeaderMatches(headers map[string]string, expected string) bool {
	for name, value := range headers {
		if strings.EqualFold(strings.TrimSpace(name), "x-amz-meta-sha256") {
			return strings.TrimSpace(value) == expected
		}
	}
	return false
}

func (s *ModelService) ImportModel(ctx context.Context, req *modelv1.ImportModelRequest) (*commonv1.AsyncTaskRef, error) {
	tenantID, err := parseTenant(req.GetTenantId())
	if err != nil {
		return nil, toStatus(err)
	}
	source := strings.ToLower(strings.TrimSpace(req.GetSource()))
	repoID := strings.TrimSpace(req.GetRepoId())
	revision := strings.TrimSpace(req.GetRevision())
	if revision == "" {
		revision = "main"
	}
	idempotencyKey := strings.TrimSpace(req.GetIdempotencyKey())
	webhookURL := strings.TrimSpace(req.GetWebhookUrl())
	if err := validateModelImportRequest(source, repoID, revision, idempotencyKey); err != nil {
		return nil, toStatus(err)
	}
	if s.db == nil {
		return nil, status.Error(codes.Internal, "model repository is unavailable")
	}

	ctx = withTenant(ctx, tenantID)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		log.Printf("model_import begin_transaction_failed")
		return nil, status.Error(codes.Internal, "begin transaction")
	}
	defer rollback(ctx, tx)

	requestHash := repo.ModelMutationHash(tenantID, "model.import", idempotencyKey, source, repoID, revision, webhookURL)
	task, _, err := s.repo.CreateImport(ctx, tx, repo.CreateImportRequest{
		TenantID:       tenantID,
		Source:         source,
		RepoID:         repoID,
		Revision:       revision,
		IdempotencyKey: idempotencyKey,
		WebhookURL:     webhookURL,
		RequestHash:    requestHash,
	})
	if err != nil {
		log.Printf("model_import create_import_failed")
		return nil, toStatus(err)
	}
	if task == nil || task.TaskID == uuid.Nil {
		return nil, status.Error(codes.Internal, "model repository returned an invalid import task")
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("model_import commit_failed")
		return nil, status.Error(codes.Internal, "commit transaction")
	}
	taskID := task.TaskID.String()
	return &commonv1.AsyncTaskRef{
		TaskId:      taskID,
		TaskType:    "model.import",
		Status:      task.Status,
		LocationUrl: "/api/v1/tasks/" + taskID,
	}, nil
}

func (s *ModelService) GetModelDownloadURL(ctx context.Context, req *modelv1.GetModelDownloadURLRequest) (*modelv1.GetModelDownloadURLResponse, error) {
	tenantID, versionID, err := parseTenantAndID(req.GetTenantId(), req.GetModelVersionId())
	if err != nil {
		return nil, toStatus(err)
	}
	// requester is an intentionally narrow protocol discriminator. It is not
	// an authentication credential, so do not accept aliases or whitespace.
	if req.GetRequester() != "init-container" {
		return nil, status.Error(codes.PermissionDenied, "requester is not allowed")
	}
	if s.objectStore == nil {
		return nil, status.Error(codes.Unavailable, "object storage is unavailable")
	}
	ctx = withTenant(ctx, tenantID)
	model, version, err := s.repo.GetVersionByID(ctx, s.db, tenantID, versionID)
	if err != nil {
		if errors.Is(err, types.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "model version not found")
		}
		// Repository errors can contain driver details or connection strings;
		// never expose those through the downloader boundary.
		return nil, status.Error(codes.Unavailable, "model repository is unavailable")
	}
	if model == nil || version == nil || !modelOwnedBy(model, tenantID) || version.ID == uuid.Nil || version.ID != versionID || version.ModelID == uuid.Nil || version.ModelID != model.ID {
		return nil, status.Error(codes.NotFound, "model version not found")
	}
	if model.Status != "ready" || !strings.HasPrefix(version.StoragePath, "object://") || version.SizeBytes <= 0 || normalizedSHA256(version.ChecksumSHA256) == "" {
		return nil, status.Error(codes.FailedPrecondition, "model version is not object-backed and ready")
	}
	ref, err := parseModelObjectPath(version.StoragePath)
	if err != nil || ref.TenantID != tenantID.String() || ref.ModelID != model.ID.String() || ref.Version != version.Version {
		return nil, status.Error(codes.NotFound, "model version not found")
	}
	metadata, err := s.objectStore.StatObject(ctx, ref)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "object storage is unavailable")
	}
	if metadata.SizeBytes <= 0 || metadata.SizeBytes != version.SizeBytes || !equalSHA256(metadata.Checksum, version.ChecksumSHA256) {
		return nil, status.Error(codes.FailedPrecondition, "model object metadata does not match version")
	}
	signed, err := s.objectStore.SignedDownloadURL(ctx, ref, 30*time.Minute)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "object storage is unavailable")
	}
	return &modelv1.GetModelDownloadURLResponse{DownloadUrl: signed.URL, StoragePath: version.StoragePath, IsEncrypted: version.IsEncrypted, EncryptAlgo: version.EncryptAlgo, EncryptHint: ""}, nil
}

var (
	modelNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,62}$`)
	pvcClaimPattern  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
)

// validateStoragePath accepts tenant-local PVC directories or canonical,
// tenant-bound object-store paths. HuggingFace/ModelScope sources remain
// deferred and HostPath is never a product source.
func validateStoragePath(raw string) error {
	path := strings.TrimSpace(raw)
	if path == "" {
		return types.Wrapf(types.ErrBadRequest, "storage_path required")
	}
	if strings.Contains(path, "..") {
		return types.Wrapf(types.ErrBadRequest, "storage_path must not contain ..")
	}
	if strings.HasPrefix(path, "object://") {
		if _, err := parseModelObjectPath(path); err != nil {
			return types.Wrapf(types.ErrBadRequest, "invalid object storage path")
		}
		return nil
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

func validateModelImportRequest(source, repoID, revision, idempotencyKey string) error {
	switch source {
	case "huggingface", "modelscope":
	default:
		return types.Wrapf(types.ErrBadRequest, "source must be huggingface or modelscope")
	}
	if repoID == "" {
		return types.Wrapf(types.ErrBadRequest, "repo_id required")
	}
	if len(repoID) < 3 || len(repoID) > 256 {
		return types.Wrapf(types.ErrBadRequest, "repo_id must be between 3 and 256 characters")
	}
	if revision == "" {
		return types.Wrapf(types.ErrBadRequest, "revision required")
	}
	if len(revision) > 256 {
		return types.Wrapf(types.ErrBadRequest, "revision must not exceed 256 characters")
	}
	if idempotencyKey == "" {
		return types.Wrapf(types.ErrBadRequest, "idempotency_key required")
	}
	if len(idempotencyKey) > 128 {
		return types.Wrapf(types.ErrBadRequest, "idempotency_key must not exceed 128 characters")
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

// normalizedSHA256 accepts the API's sha256:<hex> form and the bare hex form
// returned by S3/MinIO checksum headers. Invalid or non-SHA-256 values are
// represented by an empty string so callers fail closed.
func normalizedSHA256(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

func equalSHA256(left, right string) bool {
	leftDigest, rightDigest := normalizedSHA256(left), normalizedSHA256(right)
	return leftDigest != "" && leftDigest == rightDigest
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
	case errors.Is(err, repo.ErrModelInUse):
		return status.Error(codes.FailedPrecondition, "MODEL_IN_USE")
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
