package registry

import (
	"context"
	"errors"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

// PersistingImageRegistry wraps a provider ImageRegistry and write-through registry metadata to PostgreSQL.
// ListProjects reads from the metadata store so Gateway restarts can recover project records for local and Harbor profiles.
type PersistingImageRegistry struct {
	inner        ports.ImageRegistry
	store        ports.RegistryResourceStore
	providerMode string
}

func NewPersistingImageRegistry(inner ports.ImageRegistry, store ports.RegistryResourceStore, providerMode string) *PersistingImageRegistry {
	mode := strings.TrimSpace(providerMode)
	if mode == "" {
		mode = "local"
	}
	return &PersistingImageRegistry{
		inner:        inner,
		store:        store,
		providerMode: mode,
	}
}

var _ ports.ImageRegistry = (*PersistingImageRegistry)(nil)

func (r *PersistingImageRegistry) EnsureProject(ctx context.Context, tenantID string) error {
	if err := r.inner.EnsureProject(ctx, tenantID); err != nil {
		return err
	}
	if r.store == nil {
		return nil
	}
	result, err := r.inner.ListProjects(ctx, ports.RegistryProjectListRequest{TenantID: tenantID})
	if err != nil || len(result.Items) == 0 {
		return err
	}
	project := result.Items[0]
	return r.store.UpsertProject(ctx, ports.RegistryProjectRecord{
		TenantID:     tenantID,
		ProjectID:    project.ID,
		Name:         project.Name,
		Public:       project.Public,
		ProviderMode: r.providerMode,
		CreatedAt:    project.CreatedAt,
	}, "")
}

func (r *PersistingImageRegistry) CreateProject(ctx context.Context, request ports.RegistryProjectRequest) (ports.RegistryProject, error) {
	if r.store != nil && strings.TrimSpace(request.IdempotencyKey) != "" {
		if stored, err := r.store.GetProjectByIdempotency(ctx, request.TenantID, request.IdempotencyKey); err == nil {
			return registryProjectFromRecord(stored, r.devProfile()), nil
		} else if !errors.Is(err, ports.ErrNotFound) {
			return ports.RegistryProject{}, err
		}
	}
	project, err := r.inner.CreateProject(ctx, request)
	if err != nil {
		return ports.RegistryProject{}, err
	}
	if r.store != nil {
		if err := r.store.UpsertProject(ctx, ports.RegistryProjectRecord{
			TenantID:     request.TenantID,
			ProjectID:    project.ID,
			Name:         project.Name,
			Public:       project.Public,
			ProviderMode: r.providerMode,
			CreatedAt:    project.CreatedAt,
		}, request.IdempotencyKey); err != nil {
			return ports.RegistryProject{}, err
		}
	}
	return project, nil
}

func (r *PersistingImageRegistry) ListProjects(ctx context.Context, request ports.RegistryProjectListRequest) (ports.RegistryProjectListResult, error) {
	if r.store == nil {
		return r.inner.ListProjects(ctx, request)
	}
	records, err := r.store.ListProjects(ctx, request.TenantID)
	if err != nil {
		return ports.RegistryProjectListResult{}, err
	}
	items := make([]ports.RegistryProject, 0, len(records))
	profile := r.devProfile()
	for _, record := range records {
		items = append(items, registryProjectFromRecord(record, profile))
	}
	return ports.RegistryProjectListResult{
		Items:      items,
		NextCursor: "",
		DevProfile: profile,
	}, nil
}

func (r *PersistingImageRegistry) ListRepositories(ctx context.Context, request ports.RegistryRepositoryListRequest) (ports.RegistryRepositoryListResult, error) {
	result, err := r.inner.ListRepositories(ctx, request)
	if err != nil || r.store == nil {
		return result, err
	}
	for i := range result.Items {
		if result.Items[i].Permission != nil {
			continue
		}
		stored, err := r.store.GetRepositoryPermission(ctx, request.TenantID, request.Project, result.Items[i].Name, "svc-model")
		if err != nil {
			continue
		}
		permission := registryPermissionFromRecord(stored, result.Items[i].DevProfile)
		result.Items[i].Permission = &permission
	}
	return result, nil
}

func (r *PersistingImageRegistry) SetRepositoryPermission(ctx context.Context, request ports.RegistryPermissionRequest) (ports.RegistryPermission, error) {
	if r.store != nil && strings.TrimSpace(request.IdempotencyKey) != "" {
		if stored, err := r.store.GetPermissionByIdempotency(ctx, request.TenantID, request.IdempotencyKey); err == nil {
			permission := registryPermissionFromRecord(stored, r.devProfile())
			permission.State = ports.RegistryPermissionDuplicate
			return permission, nil
		} else if !errors.Is(err, ports.ErrNotFound) {
			return ports.RegistryPermission{}, err
		}
	}
	permission, err := r.inner.SetRepositoryPermission(ctx, request)
	if err != nil {
		return ports.RegistryPermission{}, err
	}
	if r.store != nil {
		record := ports.RegistryPermissionRecord{
			TenantID:   request.TenantID,
			Project:    permission.Project,
			Repository: permission.Repository,
			Subject:    permission.Subject,
			Actions:    append([]ports.RegistryPermissionAction(nil), permission.Actions...),
			State:      permission.State,
			UpdatedAt:  permission.UpdatedAt,
		}
		if err := r.store.UpsertRepositoryPermission(ctx, record, request.IdempotencyKey); err != nil {
			return ports.RegistryPermission{}, err
		}
	}
	return permission, nil
}

func (r *PersistingImageRegistry) CreatePullSecret(ctx context.Context, request ports.RegistryPullSecretRequest) (ports.RegistryPullSecret, error) {
	if r.store != nil && strings.TrimSpace(request.IdempotencyKey) != "" {
		if stored, err := r.store.GetPullSecretByIdempotency(ctx, request.TenantID, request.IdempotencyKey); err == nil {
			secret := registryPullSecretFromRecord(stored, r.devProfile())
			secret.State = ports.RegistryPermissionDuplicate
			return secret, nil
		} else if !errors.Is(err, ports.ErrNotFound) {
			return ports.RegistryPullSecret{}, err
		}
	}
	secret, err := r.inner.CreatePullSecret(ctx, request)
	if err != nil {
		return ports.RegistryPullSecret{}, err
	}
	if r.store != nil {
		if err := r.store.UpsertPullSecret(ctx, ports.RegistryPullSecretRecord{
			TenantID:  request.TenantID,
			Project:   secret.Project,
			Name:      secret.Name,
			SecretRef: secret.SecretRef,
			Registry:  secret.Registry,
			Username:  secret.Username,
			Namespace: secret.Namespace,
			State:     secret.State,
			CreatedAt: secret.CreatedAt,
		}, request.IdempotencyKey); err != nil {
			return ports.RegistryPullSecret{}, err
		}
	}
	return secret, nil
}

func (r *PersistingImageRegistry) ListArtifacts(ctx context.Context, request ports.RegistryArtifactListRequest) (ports.RegistryArtifactListResult, error) {
	return r.inner.ListArtifacts(ctx, request)
}

func (r *PersistingImageRegistry) GetScanResult(ctx context.Context, request ports.RegistryScanResultRequest) (ports.RegistryScanResult, error) {
	return r.inner.GetScanResult(ctx, request)
}

func (r *PersistingImageRegistry) GetProjectScanReport(ctx context.Context, request ports.RegistryProjectScanReportRequest) (ports.RegistryProjectScanReport, error) {
	return r.inner.GetProjectScanReport(ctx, request)
}

func (r *PersistingImageRegistry) ListTags(ctx context.Context, repository string) ([]ports.ImageTag, error) {
	return r.inner.ListTags(ctx, repository)
}

func (r *PersistingImageRegistry) GetScanStatus(ctx context.Context, ref ports.ImageRef) (ports.ImageScanStatus, error) {
	return r.inner.GetScanStatus(ctx, ref)
}

func (r *PersistingImageRegistry) devProfile() ports.DevProfileInfo {
	if r.providerMode == "harbor" {
		return harborDevProfile()
	}
	return registryDevProfile()
}

func registryProjectFromRecord(record ports.RegistryProjectRecord, profile ports.DevProfileInfo) ports.RegistryProject {
	return ports.RegistryProject{
		ID:         record.ProjectID,
		TenantID:   record.TenantID,
		Name:       record.Name,
		Public:     record.Public,
		DevProfile: profile,
		CreatedAt:  record.CreatedAt,
	}
}

func registryPermissionFromRecord(record ports.RegistryPermissionRecord, profile ports.DevProfileInfo) ports.RegistryPermission {
	return ports.RegistryPermission{
		Project:    record.Project,
		Repository: record.Repository,
		Subject:    record.Subject,
		Actions:    append([]ports.RegistryPermissionAction(nil), record.Actions...),
		State:      record.State,
		DevProfile: profile,
		UpdatedAt:  record.UpdatedAt,
	}
}

func registryPullSecretFromRecord(record ports.RegistryPullSecretRecord, profile ports.DevProfileInfo) ports.RegistryPullSecret {
	return ports.RegistryPullSecret{
		Project:    record.Project,
		Name:       record.Name,
		SecretRef:  record.SecretRef,
		Registry:   record.Registry,
		Username:   record.Username,
		Namespace:  record.Namespace,
		State:      record.State,
		DevProfile: profile,
		CreatedAt:  record.CreatedAt,
	}
}
