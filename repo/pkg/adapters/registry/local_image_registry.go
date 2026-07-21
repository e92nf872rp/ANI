package registry

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type LocalImageRegistry struct {
	mu          sync.RWMutex
	now         func() time.Time
	projects    map[string]ports.RegistryProject
	pullSecrets map[string]ports.RegistryPullSecret
	permissions map[string]ports.RegistryPermission
	idempotency map[string]string
}

type LocalImageRegistryOption func(*LocalImageRegistry)

func WithRegistryClock(now func() time.Time) LocalImageRegistryOption {
	return func(registry *LocalImageRegistry) {
		if now != nil {
			registry.now = now
		}
	}
}

func NewLocalImageRegistry(options ...LocalImageRegistryOption) *LocalImageRegistry {
	registry := &LocalImageRegistry{
		now:         func() time.Time { return time.Now().UTC() },
		projects:    map[string]ports.RegistryProject{},
		pullSecrets: map[string]ports.RegistryPullSecret{},
		permissions: map[string]ports.RegistryPermission{},
		idempotency: map[string]string{},
	}
	for _, option := range options {
		option(registry)
	}
	return registry
}

func (r *LocalImageRegistry) EnsureProject(_ context.Context, tenantID string) error {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureProjectLocked(tenantID)
	return nil
}

func (r *LocalImageRegistry) CreateProject(_ context.Context, request ports.RegistryProjectRequest) (ports.RegistryProject, error) {
	tenantID := strings.TrimSpace(request.TenantID)
	name := strings.TrimSpace(request.Name)
	if tenantID == "" {
		return ports.RegistryProject{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if name == "" {
		return ports.RegistryProject{}, fmt.Errorf("%w: name is required", ports.ErrInvalid)
	}
	if name != tenantID {
		return ports.RegistryProject{}, fmt.Errorf("%w: project must match tenant local profile", ports.ErrInvalid)
	}
	idemKey, err := registryIdempotencyKey(tenantID, request.IdempotencyKey)
	if err != nil {
		return ports.RegistryProject{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if projectID, ok := r.idempotency[idemKey]; ok {
		project := r.projects[projectID]
		project.DevProfile = registryDevProfile()
		return project, nil
	}
	project := r.ensureProjectLocked(tenantID)
	project.Public = request.Public
	project.DevProfile = registryDevProfile()
	r.projects[tenantID] = project
	r.idempotency[idemKey] = tenantID
	return project, nil
}

func (r *LocalImageRegistry) ListProjects(_ context.Context, request ports.RegistryProjectListRequest) (ports.RegistryProjectListResult, error) {
	tenantID := strings.TrimSpace(request.TenantID)
	if tenantID == "" {
		return ports.RegistryProjectListResult{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	project := r.ensureProjectLocked(tenantID)
	project.DevProfile = registryDevProfile()
	return ports.RegistryProjectListResult{
		Items:      []ports.RegistryProject{project},
		DevProfile: registryDevProfile(),
	}, nil
}

func (r *LocalImageRegistry) GetOverview(_ context.Context, request ports.RegistryOverviewRequest) (ports.RegistryOverview, error) {
	tenantID := strings.TrimSpace(request.TenantID)
	if tenantID == "" {
		return ports.RegistryOverview{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureProjectLocked(tenantID)
	return ports.RegistryOverview{
		Resources: []ports.RegistryOverviewResourceSummary{
			{Kind: "project", Total: 1, Available: 1},
			{Kind: "repository", Total: 1, Available: 1},
			{Kind: "artifact", Total: 1, Available: 1, SizeBytes: 1048576},
			{Kind: "tag", Total: 1, Available: 1, SizeBytes: 1048576},
		},
		Vulnerabilities: ports.RegistryOverviewVulnerabilitySummary{},
		Capabilities: []ports.RegistryOverviewCapability{
			{Key: "projects", Label: "Projects", Status: "available", Path: "/registry/projects"},
			{Key: "repositories", Label: "Repositories", Status: "available", Path: "/registry/projects/" + tenantID + "/repositories"},
			{Key: "tags", Label: "Image tags", Status: "available", Path: "/registry/images"},
			{Key: "push_instructions", Label: "Push instructions", Status: "available", Path: "/registry/projects/" + tenantID + "/push-instructions"},
			{Key: "pull_commands", Label: "Pull commands", Status: "available", Path: "/registry/images"},
			{Key: "scan_summary", Label: "Scan summary", Status: "available", Path: "/registry/projects/" + tenantID + "/scan-report"},
			{Key: "scan_policy", Label: "Scan policy", Status: "planned"},
			{Key: "quota", Label: "Quota", Status: "planned"},
			{Key: "garbage_collection", Label: "Garbage collection", Status: "planned"},
		},
		CreateOrder: []string{"project", "login", "tag", "push"},
		Relationships: []ports.RegistryOverviewRelationship{
			{Source: "project", Target: "repository", Relation: "contains"},
			{Source: "repository", Target: "tag", Relation: "publishes"},
			{Source: "tag", Target: "workload", Relation: "referenced_by"},
		},
		QuickActions: []ports.RegistryOverviewQuickAction{
			{Key: "create_project", Label: "Create project", Path: "/registry/projects"},
			{Key: "push_instructions", Label: "Push image", Path: "/registry/projects/" + tenantID + "/push-instructions"},
		},
		DeleteRisks: []ports.RegistryOverviewDeleteRisk{
			{Kind: "tag", Risk: "Deleting a tag can break workloads that still reference that image."},
			{Kind: "artifact", Risk: "Deleting the last tag may remove the underlying artifact from the provider."},
			{Kind: "repository", Risk: "Deleting a repository removes all tags and artifacts under it."},
			{Kind: "project", Risk: "Deleting a project removes repository permissions and pull-secret relationships."},
		},
	}, nil
}

func (r *LocalImageRegistry) ListImages(_ context.Context, request ports.RegistryImageListRequest) (ports.RegistryImageListResult, error) {
	tenantID := strings.TrimSpace(request.TenantID)
	project := strings.TrimSpace(request.Project)
	if project == "" {
		project = tenantID
	}
	if err := validateTenantProject(tenantID, project); err != nil {
		return ports.RegistryImageListResult{}, err
	}
	repository := strings.TrimSpace(request.Repository)
	if repository == "" {
		repository = "runtime"
	}
	tag := strings.TrimSpace(request.Tag)
	if tag == "" {
		tag = "latest"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureProjectLocked(tenantID)
	image := registryImage(project, repository, tag)
	scan := r.scanResultLocked(project + "/" + repository + ":" + tag)
	if request.ScanStatus != "" && scan.Status != request.ScanStatus {
		return ports.RegistryImageListResult{DevProfile: registryDevProfile()}, nil
	}
	return ports.RegistryImageListResult{
		Items: []ports.RegistryImage{{
			Project:     project,
			Repository:  repository,
			Tag:         tag,
			Image:       image,
			Registry:    "registry.local",
			Digest:      registryDigestForTag(tag),
			MediaType:   "application/vnd.oci.image.manifest.v1+json",
			SizeBytes:   1048576,
			PullCommand: "docker pull " + image,
			PushedAt:    r.now().UTC(),
			ScanStatus:  scan,
			DevProfile:  registryDevProfile(),
		}},
		DevProfile: registryDevProfile(),
	}, nil
}

func (r *LocalImageRegistry) ListRepositories(_ context.Context, request ports.RegistryRepositoryListRequest) (ports.RegistryRepositoryListResult, error) {
	if err := validateTenantProject(request.TenantID, request.Project); err != nil {
		return ports.RegistryRepositoryListResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureProjectLocked(strings.TrimSpace(request.TenantID))
	repository := ports.RegistryRepository{
		Project:       strings.TrimSpace(request.Project),
		Name:          "runtime",
		ArtifactCount: 1,
		PullCount:     0,
		DevProfile:    registryDevProfile(),
	}
	if permission, ok := r.permissions[permissionKey(request.Project, "runtime", "svc-model")]; ok {
		cloned := cloneRegistryPermission(permission)
		repository.Permission = &cloned
	}
	return ports.RegistryRepositoryListResult{
		Items:      []ports.RegistryRepository{repository},
		DevProfile: registryDevProfile(),
	}, nil
}

func (r *LocalImageRegistry) ListArtifacts(_ context.Context, request ports.RegistryArtifactListRequest) (ports.RegistryArtifactListResult, error) {
	if err := validateTenantProject(request.TenantID, request.Project); err != nil {
		return ports.RegistryArtifactListResult{}, err
	}
	if strings.TrimSpace(request.Repository) == "" {
		return ports.RegistryArtifactListResult{}, fmt.Errorf("%w: repository is required", ports.ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureProjectLocked(strings.TrimSpace(request.TenantID))
	scan := r.scanResultLocked(strings.TrimSpace(request.Project) + "/" + strings.TrimSpace(request.Repository) + ":latest")
	artifact := ports.RegistryArtifact{
		Project:    strings.TrimSpace(request.Project),
		Repository: strings.TrimSpace(request.Repository),
		Digest:     "sha256:local-runtime",
		Tags:       []string{"latest"},
		MediaType:  "application/vnd.oci.image.manifest.v1+json",
		SizeBytes:  1048576,
		PushedAt:   r.now().UTC(),
		ScanStatus: scan,
		DevProfile: registryDevProfile(),
	}
	return ports.RegistryArtifactListResult{
		Items:      []ports.RegistryArtifact{artifact},
		DevProfile: registryDevProfile(),
	}, nil
}

func (r *LocalImageRegistry) GetPushInstructions(_ context.Context, request ports.RegistryPushInstructionsRequest) (ports.RegistryPushInstructions, error) {
	if err := validateTenantProject(request.TenantID, request.Project); err != nil {
		return ports.RegistryPushInstructions{}, err
	}
	repository := strings.TrimSpace(request.Repository)
	if repository == "" {
		repository = "runtime"
	}
	project := strings.TrimSpace(request.Project)
	example := registryImage(project, repository, "latest")
	return ports.RegistryPushInstructions{
		Project:           project,
		Registry:          "registry.local",
		RepositoryExample: example,
		Commands: []ports.RegistryCommand{
			{Label: "Login", Command: "docker login registry.local"},
			{Label: "Tag", Command: "docker tag <local-image> " + example},
			{Label: "Push", Command: "docker push " + example},
		},
		DevProfile: registryDevProfile(),
	}, nil
}

func (r *LocalImageRegistry) DeleteTag(_ context.Context, request ports.RegistryTagDeleteRequest) (ports.RegistryDeletedTag, error) {
	if err := validateRegistryTagRequest(request.TenantID, request.Project, request.Repository, request.Tag); err != nil {
		return ports.RegistryDeletedTag{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureProjectLocked(strings.TrimSpace(request.TenantID))
	references := r.tagReferencesLocked(request.Project, request.Repository, request.Tag)
	if len(references) > 0 {
		return ports.RegistryDeletedTag{}, fmt.Errorf("%w: image tag is still referenced", ports.ErrConflict)
	}
	return ports.RegistryDeletedTag{
		Project:    strings.TrimSpace(request.Project),
		Repository: strings.TrimSpace(request.Repository),
		Tag:        strings.TrimSpace(request.Tag),
		Digest:     registryDigestForTag(request.Tag),
		DeletedAt:  r.now().UTC(),
	}, nil
}

func (r *LocalImageRegistry) ListTagReferences(_ context.Context, request ports.RegistryTagReferenceListRequest) (ports.RegistryTagReferenceListResult, error) {
	if err := validateRegistryTagRequest(request.TenantID, request.Project, request.Repository, request.Tag); err != nil {
		return ports.RegistryTagReferenceListResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureProjectLocked(strings.TrimSpace(request.TenantID))
	items := r.tagReferencesLocked(request.Project, request.Repository, request.Tag)
	return ports.RegistryTagReferenceListResult{
		Project:       strings.TrimSpace(request.Project),
		Repository:    strings.TrimSpace(request.Repository),
		Tag:           strings.TrimSpace(request.Tag),
		Image:         registryImage(request.Project, request.Repository, request.Tag),
		Items:         items,
		DeleteBlocked: len(items) > 0,
		DevProfile:    registryDevProfile(),
	}, nil
}

func (r *LocalImageRegistry) SetRepositoryPermission(_ context.Context, request ports.RegistryPermissionRequest) (ports.RegistryPermission, error) {
	if err := validateTenantProject(request.TenantID, request.Project); err != nil {
		return ports.RegistryPermission{}, err
	}
	if strings.TrimSpace(request.Repository) == "" {
		return ports.RegistryPermission{}, fmt.Errorf("%w: repository is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.Subject) == "" {
		return ports.RegistryPermission{}, fmt.Errorf("%w: subject is required", ports.ErrInvalid)
	}
	if len(request.Actions) == 0 {
		return ports.RegistryPermission{}, fmt.Errorf("%w: actions are required", ports.ErrInvalid)
	}
	idemKey, err := registryIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.RegistryPermission{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if key, ok := r.idempotency[idemKey]; ok {
		permission := cloneRegistryPermission(r.permissions[key])
		permission.State = ports.RegistryPermissionDuplicate
		permission.DevProfile = registryDevProfile()
		return permission, nil
	}
	r.ensureProjectLocked(strings.TrimSpace(request.TenantID))
	permission := ports.RegistryPermission{
		Project:    strings.TrimSpace(request.Project),
		Repository: strings.TrimSpace(request.Repository),
		Subject:    strings.TrimSpace(request.Subject),
		Actions:    append([]ports.RegistryPermissionAction(nil), request.Actions...),
		State:      ports.RegistryPermissionActive,
		DevProfile: registryDevProfile(),
		UpdatedAt:  r.now().UTC(),
	}
	key := permissionKey(permission.Project, permission.Repository, permission.Subject)
	r.permissions[key] = permission
	r.idempotency[idemKey] = key
	return permission, nil
}

func (r *LocalImageRegistry) GetScanResult(_ context.Context, request ports.RegistryScanResultRequest) (ports.RegistryScanResult, error) {
	if strings.TrimSpace(request.TenantID) == "" {
		return ports.RegistryScanResult{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.Image) == "" {
		return ports.RegistryScanResult{}, fmt.Errorf("%w: image is required", ports.ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureProjectLocked(strings.TrimSpace(request.TenantID))
	return r.scanResultLocked(strings.TrimSpace(request.Image)), nil
}

func (r *LocalImageRegistry) CreatePullSecret(_ context.Context, request ports.RegistryPullSecretRequest) (ports.RegistryPullSecret, error) {
	if err := validateTenantProject(request.TenantID, request.Project); err != nil {
		return ports.RegistryPullSecret{}, err
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = "ani-registry-pull"
	}
	idemKey, err := registryIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.RegistryPullSecret{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if key, ok := r.idempotency[idemKey]; ok {
		secret := r.pullSecrets[key]
		secret.State = ports.RegistryPermissionDuplicate
		secret.DevProfile = registryDevProfile()
		return secret, nil
	}
	r.ensureProjectLocked(strings.TrimSpace(request.TenantID))
	secret := ports.RegistryPullSecret{
		Project:    strings.TrimSpace(request.Project),
		Name:       name,
		SecretRef:  strings.TrimSpace(request.Project) + "/" + name,
		Registry:   "registry.local",
		Username:   "robot$" + strings.TrimSpace(request.Project),
		Namespace:  strings.TrimSpace(request.Namespace),
		State:      ports.RegistryPermissionActive,
		DevProfile: registryDevProfile(),
		CreatedAt:  r.now().UTC(),
	}
	key := strings.TrimSpace(request.Project) + ":" + name
	r.pullSecrets[key] = secret
	r.idempotency[idemKey] = key
	return secret, nil
}

func (r *LocalImageRegistry) GetProjectScanReport(_ context.Context, request ports.RegistryProjectScanReportRequest) (ports.RegistryProjectScanReport, error) {
	if err := validateTenantProject(request.TenantID, request.Project); err != nil {
		return ports.RegistryProjectScanReport{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureProjectLocked(strings.TrimSpace(request.TenantID))
	return ports.RegistryProjectScanReport{
		Project:          strings.TrimSpace(request.Project),
		Status:           ports.RegistryScanComplete,
		Critical:         0,
		High:             0,
		Medium:           0,
		Low:              0,
		ArtifactsTotal:   1,
		ScannedArtifacts: 1,
		ProviderID:       "local-trivy",
		DevProfile:       registryDevProfile(),
		ScannedAt:        r.now().UTC(),
	}, nil
}

func (r *LocalImageRegistry) ListTags(ctx context.Context, repository string) ([]ports.ImageTag, error) {
	artifacts, err := r.ListArtifacts(ctx, ports.RegistryArtifactListRequest{
		TenantID:   "local",
		Project:    "local",
		Repository: repository,
	})
	if err != nil {
		return nil, err
	}
	tags := make([]ports.ImageTag, 0, len(artifacts.Items))
	for _, artifact := range artifacts.Items {
		for _, tag := range artifact.Tags {
			tags = append(tags, ports.ImageTag{Name: tag, Digest: artifact.Digest})
		}
	}
	return tags, nil
}

func (r *LocalImageRegistry) GetScanStatus(ctx context.Context, ref ports.ImageRef) (ports.ImageScanStatus, error) {
	image := strings.Trim(strings.Join([]string{ref.Repository, ref.Tag}, ":"), ":")
	result, err := r.GetScanResult(ctx, ports.RegistryScanResultRequest{TenantID: "local", Image: image})
	if err != nil {
		return ports.ImageScanStatus{}, err
	}
	return ports.ImageScanStatus{
		Status:     string(result.Status),
		Critical:   result.Critical,
		High:       result.High,
		Medium:     result.Medium,
		Low:        result.Low,
		ReportURL:  result.ReportURL,
		ProviderID: result.ProviderID,
	}, nil
}

func (r *LocalImageRegistry) ensureProjectLocked(tenantID string) ports.RegistryProject {
	if project, ok := r.projects[tenantID]; ok {
		return project
	}
	project := ports.RegistryProject{
		ID:         "regproj-" + tenantID,
		TenantID:   tenantID,
		Name:       tenantID,
		Public:     false,
		DevProfile: registryDevProfile(),
		CreatedAt:  r.now().UTC(),
	}
	r.projects[tenantID] = project
	return project
}

func (r *LocalImageRegistry) scanResultLocked(image string) ports.RegistryScanResult {
	return ports.RegistryScanResult{
		Image:      image,
		Status:     ports.RegistryScanComplete,
		Critical:   0,
		High:       0,
		Medium:     0,
		Low:        0,
		ReportURL:  "local://registry-scan/" + strings.ReplaceAll(image, "/", "_"),
		ProviderID: "local-trivy",
		DevProfile: registryDevProfile(),
		ScannedAt:  r.now().UTC(),
	}
}

func validateTenantProject(tenantID, project string) error {
	tenantID = strings.TrimSpace(tenantID)
	project = strings.TrimSpace(project)
	if tenantID == "" {
		return fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if project == "" {
		return fmt.Errorf("%w: project is required", ports.ErrInvalid)
	}
	if tenantID != project {
		return fmt.Errorf("%w: project must match tenant local profile", ports.ErrInvalid)
	}
	return nil
}

func validateRegistryTagRequest(tenantID, project, repository, tag string) error {
	if err := validateTenantProject(tenantID, project); err != nil {
		return err
	}
	if strings.TrimSpace(repository) == "" {
		return fmt.Errorf("%w: repository is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(tag) == "" {
		return fmt.Errorf("%w: tag is required", ports.ErrInvalid)
	}
	return nil
}

func registryIdempotencyKey(tenantID, key string) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	key = strings.TrimSpace(key)
	if tenantID == "" {
		return "", fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if key == "" {
		return "", fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	return tenantID + ":" + key, nil
}

func permissionKey(project, repository, subject string) string {
	return strings.TrimSpace(project) + "/" + strings.TrimSpace(repository) + ":" + strings.TrimSpace(subject)
}

func registryImage(project, repository, tag string) string {
	return "registry.local/" + strings.TrimSpace(project) + "/" + strings.TrimSpace(repository) + ":" + strings.TrimSpace(tag)
}

func registryDigestForTag(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "latest" {
		return "sha256:local-runtime"
	}
	return "sha256:local-" + strings.ReplaceAll(tag, "/", "-")
}

func (r *LocalImageRegistry) tagReferencesLocked(project, repository, tag string) []ports.RegistryImageReference {
	if strings.TrimSpace(repository) != "runtime" || strings.TrimSpace(tag) != "latest" {
		return nil
	}
	return []ports.RegistryImageReference{{
		Kind:       "container_instance",
		ID:         "inst-" + strings.TrimSpace(project) + "-runtime",
		Name:       "runtime",
		Route:      "/instances/inst-" + strings.TrimSpace(project) + "-runtime",
		State:      "running",
		DevProfile: registryDevProfile(),
	}}
}

func cloneRegistryPermission(permission ports.RegistryPermission) ports.RegistryPermission {
	permission.Actions = append([]ports.RegistryPermissionAction(nil), permission.Actions...)
	return permission
}

func registryDevProfile() ports.DevProfileInfo {
	return ports.DevProfileInfo{
		Mode:         "local",
		Provider:     "local-image-registry",
		RealProvider: false,
		Reason:       "local profile returns deterministic registry metadata; it is not a Harbor or Trivy provider execution",
	}
}

var _ ports.ImageRegistry = (*LocalImageRegistry)(nil)
