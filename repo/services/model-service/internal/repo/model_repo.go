package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kubercloud/ani/pkg/types"
)

type ModelRepo interface {
	Create(ctx context.Context, tx pgx.Tx, req CreateModelReq) (*Model, error)
	GetByID(ctx context.Context, pool *pgxpool.Pool, tenantID, modelID uuid.UUID) (*Model, error)
	GetVersionByID(ctx context.Context, pool *pgxpool.Pool, tenantID, versionID uuid.UUID) (*Model, *ModelVersion, error)
	List(ctx context.Context, pool *pgxpool.Pool, filter ListFilter) ([]*Model, int64, string, error)
	SoftDelete(ctx context.Context, tx pgx.Tx, tenantID, modelID uuid.UUID) error
	CreateVersion(ctx context.Context, tx pgx.Tx, req CreateVersionReq) (*ModelVersion, error)
	ListVersions(ctx context.Context, pool *pgxpool.Pool, tenantID, modelID uuid.UUID) ([]*ModelVersion, error)
}

type PostgresModelRepo struct{}

func NewPostgresModelRepo() *PostgresModelRepo {
	return &PostgresModelRepo{}
}

type CreateModelReq struct {
	TenantID     uuid.UUID
	Name         string
	DisplayName  string
	Description  string
	Capabilities []string
	Source       string
	SourceRepoID string
}

type CreateVersionReq struct {
	TenantID       uuid.UUID
	ModelID        uuid.UUID
	Version        string
	Format         string
	StoragePath    string
	ChecksumSHA256 string
	SizeBytes      int64
	IsEncrypted    bool
	EncryptAlgo    string
	EncryptHint    string
}

type ListFilter struct {
	TenantID uuid.UUID
	Status   string
	Cursor   string
	Limit    int
}

type Model struct {
	TenantID       uuid.UUID
	ID             uuid.UUID
	Name           string
	DisplayName    string
	Description    string
	Source         string
	SourceRepoID   string
	Capabilities   []string
	Status         string
	ErrorMessage   string
	TotalSizeBytes int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Versions       []*ModelVersion
}

type ModelVersion struct {
	ID             uuid.UUID
	ModelID        uuid.UUID
	Version        string
	Format         string
	IsEncrypted    bool
	EncryptAlgo    string
	EncryptHint    string
	SizeBytes      int64
	ChecksumSHA256 string
	StoragePath    string
	CreatedAt      time.Time
}

func (r *PostgresModelRepo) Create(ctx context.Context, tx pgx.Tx, req CreateModelReq) (*Model, error) {
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return nil, fmt.Errorf("modelRepo.Create set tenant: %w", err)
	}
	if req.Source == "" {
		req.Source = "upload"
	}

	model := &Model{}
	err := tx.QueryRow(ctx, `
		INSERT INTO models (
			tenant_id, name, display_name, description, source, source_repo_id, capabilities
		)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, NULLIF($6, ''), $7)
		RETURNING tenant_id, id, name, display_name, COALESCE(description, ''),
			source, COALESCE(source_repo_id, ''), capabilities, status,
			COALESCE(error_message, ''), COALESCE(total_size_bytes, 0),
			created_at, updated_at
	`, req.TenantID, req.Name, req.DisplayName, req.Description, req.Source, req.SourceRepoID, req.Capabilities).
		Scan(modelScanDest(model)...)
	if err != nil {
		return nil, fmt.Errorf("modelRepo.Create insert: %w", err)
	}
	return model, nil
}

func (r *PostgresModelRepo) GetByID(ctx context.Context, pool *pgxpool.Pool, tenantID, modelID uuid.UUID) (*Model, error) {
	tx, err := beginTenantTx(ctx, pool)
	if err != nil {
		return nil, err
	}
	defer rollback(ctx, tx)

	model, err := getModelByQuery(ctx, tx, getModelByIDSQL, modelID, tenantID)
	if err != nil {
		return nil, err
	}
	versions, err := listVersionsByModel(ctx, tx, tenantID, model.ID)
	if err != nil {
		return nil, err
	}
	model.Versions = versions
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("modelRepo.GetByID commit: %w", err)
	}
	return model, nil
}

func (r *PostgresModelRepo) GetVersionByID(ctx context.Context, pool *pgxpool.Pool, tenantID, versionID uuid.UUID) (*Model, *ModelVersion, error) {
	tx, err := beginTenantTx(ctx, pool)
	if err != nil {
		return nil, nil, err
	}
	defer rollback(ctx, tx)

	model := &Model{}
	version := &ModelVersion{}
	err = tx.QueryRow(ctx, getModelVersionByIDSQL, versionID, tenantID).
		Scan(append(modelScanDest(model), versionScanDest(version)...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, types.Wrapf(types.ErrNotFound, "modelRepo.GetVersionByID id=%s", versionID)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("modelRepo.GetVersionByID query: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("modelRepo.GetVersionByID commit: %w", err)
	}
	return model, version, nil
}

const getModelVersionByIDSQL = `
		SELECT
			m.tenant_id, m.id, m.name, m.display_name, COALESCE(m.description, ''),
			m.source, COALESCE(m.source_repo_id, ''), m.capabilities, m.status,
			COALESCE(m.error_message, ''), COALESCE(m.total_size_bytes, 0),
			m.created_at, m.updated_at,
			v.id, v.model_id, v.version, v.format, v.is_encrypted, COALESCE(v.encrypt_algo, ''),
			COALESCE(v.encrypt_hint, ''), COALESCE(v.size_bytes, 0), COALESCE(v.checksum_sha256, ''),
			v.storage_path, v.created_at
		FROM model_versions v
		JOIN models m ON m.id = v.model_id
		WHERE v.id=$1 AND m.tenant_id=$2 AND m.status <> 'deleted'
	`

func (r *PostgresModelRepo) List(ctx context.Context, pool *pgxpool.Pool, filter ListFilter) ([]*Model, int64, string, error) {
	req := types.ListRequest{Limit: filter.Limit, Cursor: filter.Cursor}
	req.Normalize()

	tx, err := beginTenantTx(ctx, pool)
	if err != nil {
		return nil, 0, "", err
	}
	defer rollback(ctx, tx)

	where, args, err := buildListModelsFilter(filter)
	if err != nil {
		return nil, 0, "", err
	}

	var total int64
	countSQL := buildCountModelsSQL(where)
	if err := tx.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, "", fmt.Errorf("modelRepo.List count: %w", err)
	}

	args = append(args, req.Limit+1)
	rows, err := tx.Query(ctx, buildListModelsSQL(where, len(args)), args...)
	if err != nil {
		return nil, 0, "", fmt.Errorf("modelRepo.List query: %w", err)
	}
	defer rows.Close()

	var models []*Model
	for rows.Next() {
		model := &Model{}
		if err := rows.Scan(modelScanDest(model)...); err != nil {
			return nil, 0, "", fmt.Errorf("modelRepo.List scan: %w", err)
		}
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, "", fmt.Errorf("modelRepo.List rows: %w", err)
	}

	nextCursor := ""
	if len(models) > req.Limit {
		last := models[req.Limit-1]
		nextCursor = types.EncodeCursor(last.CreatedAt, last.ID)
		models = models[:req.Limit]
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, "", fmt.Errorf("modelRepo.List commit: %w", err)
	}
	return models, total, nextCursor, nil
}

func (r *PostgresModelRepo) SoftDelete(ctx context.Context, tx pgx.Tx, tenantID, modelID uuid.UUID) error {
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return fmt.Errorf("modelRepo.SoftDelete set tenant: %w", err)
	}
	tag, err := tx.Exec(ctx, softDeleteModelSQL, modelID, tenantID)
	if err != nil {
		return fmt.Errorf("modelRepo.SoftDelete update: %w", err)
	}
	if err := requireRowsAffected(tag.RowsAffected(), "modelRepo.SoftDelete", modelID); err != nil {
		return err
	}
	return nil
}

func (r *PostgresModelRepo) CreateVersion(ctx context.Context, tx pgx.Tx, req CreateVersionReq) (*ModelVersion, error) {
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return nil, fmt.Errorf("modelRepo.CreateVersion set tenant: %w", err)
	}
	version := &ModelVersion{}
	err := tx.QueryRow(ctx, createModelVersionSQL, req.ModelID, req.TenantID, req.ModelID, req.Version,
		req.Format, req.IsEncrypted, req.EncryptAlgo, req.EncryptHint, req.SizeBytes,
		req.ChecksumSHA256, req.StoragePath).Scan(versionScanDest(version)...)
	err = mapQueryNoRows(err, "modelRepo.CreateVersion", req.ModelID)
	if errors.Is(err, types.ErrNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("modelRepo.CreateVersion insert: %w", err)
	}
	tag, err := tx.Exec(ctx, updateModelAfterVersionSQL, req.ModelID, req.TenantID, req.SizeBytes)
	if err != nil {
		return nil, fmt.Errorf("modelRepo.CreateVersion update model: %w", err)
	}
	if err := requireRowsAffected(tag.RowsAffected(), "modelRepo.CreateVersion", req.ModelID); err != nil {
		return nil, err
	}
	return version, nil
}

func (r *PostgresModelRepo) ListVersions(ctx context.Context, pool *pgxpool.Pool, tenantID, modelID uuid.UUID) ([]*ModelVersion, error) {
	tx, err := beginTenantTx(ctx, pool)
	if err != nil {
		return nil, err
	}
	defer rollback(ctx, tx)

	if err := requireModelOwned(ctx, tx, tenantID, modelID); err != nil {
		return nil, err
	}
	versions, err := listVersionsByModel(ctx, tx, tenantID, modelID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("modelRepo.ListVersions commit: %w", err)
	}
	return versions, nil
}

const modelSelectSQL = `
	SELECT tenant_id, id, name, display_name, COALESCE(description, ''),
		source, COALESCE(source_repo_id, ''), capabilities, status,
		COALESCE(error_message, ''), COALESCE(total_size_bytes, 0),
		created_at, updated_at
	FROM models
`

const getModelByIDSQL = `WHERE id=$1 AND tenant_id=$2 AND status <> 'deleted'`

const listModelsBaseWhere = `WHERE tenant_id=$1 AND status <> 'deleted'`

const softDeleteModelSQL = `
	UPDATE models
	SET status='deleted', updated_at=NOW()
	WHERE id=$1 AND tenant_id=$2 AND status <> 'deleted'
`

const createModelVersionSQL = `
	INSERT INTO model_versions (
		model_id, version, format, is_encrypted, encrypt_algo, encrypt_hint,
		size_bytes, checksum_sha256, storage_path
	)
	SELECT $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9,
		NULLIF($10, ''), $11
	FROM models
	WHERE id=$1 AND tenant_id=$2 AND status <> 'deleted'
	RETURNING id, model_id, version, format, is_encrypted, COALESCE(encrypt_algo, ''),
		COALESCE(encrypt_hint, ''), COALESCE(size_bytes, 0), COALESCE(checksum_sha256, ''),
		storage_path, created_at
`

const updateModelAfterVersionSQL = `
	UPDATE models
	SET status='ready', total_size_bytes=COALESCE(total_size_bytes, 0)+$3, updated_at=NOW()
	WHERE id=$1 AND tenant_id=$2 AND status <> 'deleted'
`

const listModelVersionsParentSQL = `
	SELECT id
	FROM models
	WHERE id=$1 AND tenant_id=$2 AND status <> 'deleted'
`

const listModelVersionsSQL = `
	SELECT v.id, v.model_id, v.version, v.format, v.is_encrypted, COALESCE(v.encrypt_algo, ''),
		COALESCE(v.encrypt_hint, ''), COALESCE(v.size_bytes, 0), COALESCE(v.checksum_sha256, ''),
		v.storage_path, v.created_at
	FROM model_versions v
	JOIN models m ON m.id=v.model_id
	WHERE v.model_id=$1 AND m.tenant_id=$2 AND m.status <> 'deleted'
	ORDER BY v.created_at DESC, v.id DESC
`

func buildListModelsFilter(filter ListFilter) (string, []any, error) {
	args := []any{filter.TenantID}
	where := listModelsBaseWhere
	if filter.Status != "" {
		args = append(args, filter.Status)
		where += fmt.Sprintf(" AND status=$%d", len(args))
	}
	if filter.Cursor != "" {
		createdAt, id, err := types.DecodeCursor(filter.Cursor)
		if err != nil {
			return "", nil, types.Wrapf(types.ErrBadRequest, "modelRepo.List cursor: %v", err)
		}
		args = append(args, createdAt, id)
		where += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", len(args)-1, len(args))
	}
	return where, args, nil
}

func buildCountModelsSQL(where string) string {
	return "SELECT COUNT(*) FROM models " + where
}

func buildListModelsSQL(where string, limitArg int) string {
	return modelSelectSQL + " " + where + fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", limitArg)
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func requireModelOwned(ctx context.Context, queryer queryRower, tenantID, modelID uuid.UUID) error {
	var ownedID uuid.UUID
	err := queryer.QueryRow(ctx, listModelVersionsParentSQL, modelID, tenantID).Scan(&ownedID)
	err = mapQueryNoRows(err, "modelRepo.ListVersions", modelID)
	if errors.Is(err, types.ErrNotFound) {
		return err
	}
	if err != nil {
		return fmt.Errorf("modelRepo.ListVersions verify parent: %w", err)
	}
	return nil
}

func mapQueryNoRows(err error, operation string, resourceID uuid.UUID) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return types.Wrapf(types.ErrNotFound, "%s id=%s", operation, resourceID)
	}
	return err
}

func requireRowsAffected(rows int64, operation string, resourceID uuid.UUID) error {
	if rows == 0 {
		return types.Wrapf(types.ErrNotFound, "%s id=%s", operation, resourceID)
	}
	return nil
}

func getModelByQuery(ctx context.Context, tx pgx.Tx, where string, args ...any) (*Model, error) {
	model := &Model{}
	err := tx.QueryRow(ctx, modelSelectSQL+" "+where, args...).Scan(modelScanDest(model)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, types.Wrapf(types.ErrNotFound, "modelRepo.Get")
	}
	if err != nil {
		return nil, fmt.Errorf("modelRepo.Get query: %w", err)
	}
	return model, nil
}

func listVersionsByModel(ctx context.Context, tx pgx.Tx, tenantID, modelID uuid.UUID) ([]*ModelVersion, error) {
	rows, err := tx.Query(ctx, listModelVersionsSQL, modelID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("modelRepo.ListVersions query: %w", err)
	}
	defer rows.Close()

	var versions []*ModelVersion
	for rows.Next() {
		version := &ModelVersion{}
		if err := rows.Scan(versionScanDest(version)...); err != nil {
			return nil, fmt.Errorf("modelRepo.ListVersions scan: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("modelRepo.ListVersions rows: %w", err)
	}
	return versions, nil
}

func modelScanDest(m *Model) []any {
	return []any{
		&m.TenantID, &m.ID, &m.Name, &m.DisplayName, &m.Description,
		&m.Source, &m.SourceRepoID, &m.Capabilities, &m.Status,
		&m.ErrorMessage, &m.TotalSizeBytes, &m.CreatedAt, &m.UpdatedAt,
	}
}

func versionScanDest(v *ModelVersion) []any {
	return []any{
		&v.ID, &v.ModelID, &v.Version, &v.Format, &v.IsEncrypted, &v.EncryptAlgo,
		&v.EncryptHint, &v.SizeBytes, &v.ChecksumSHA256, &v.StoragePath, &v.CreatedAt,
	}
}

func beginTenantTx(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tenant tx: %w", err)
	}
	if err := types.SetDBTenant(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("set tenant: %w", err)
	}
	return tx, nil
}

func rollback(ctx context.Context, tx pgx.Tx) {
	_ = tx.Rollback(ctx)
}
