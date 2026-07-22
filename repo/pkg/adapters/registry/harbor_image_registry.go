package registry

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const defaultHarborRequestTimeout = 10 * time.Second

type HarborImageRegistryConfig struct {
	Endpoint         string
	Username         string
	Password         string
	HTTPClient       *http.Client
	RequestTimeout   time.Duration
	PullSecretWriter ports.RegistryPullSecretWriter
	ReferenceReader  ports.RegistryImageReferenceReader
}

type HarborImageRegistry struct {
	endpoint         string
	username         string
	password         string
	httpClient       *http.Client
	pullSecretWriter ports.RegistryPullSecretWriter
	referenceReader  ports.RegistryImageReferenceReader
	now              func() time.Time
}

func NewHarborImageRegistry(config HarborImageRegistryConfig) (*HarborImageRegistry, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(config.Endpoint), "/")
	if endpoint == "" {
		return nil, fmt.Errorf("%w: Harbor endpoint is required", ports.ErrNotConfigured)
	}
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("%w: invalid Harbor endpoint", ports.ErrInvalid)
	}
	username := strings.TrimSpace(config.Username)
	if username == "" || config.Password == "" {
		return nil, fmt.Errorf("%w: Harbor credentials are required", ports.ErrNotConfigured)
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	clientCopy := *client
	if config.RequestTimeout > 0 {
		clientCopy.Timeout = config.RequestTimeout
	} else if clientCopy.Timeout == 0 {
		clientCopy.Timeout = defaultHarborRequestTimeout
	}
	return &HarborImageRegistry{
		endpoint:         endpoint,
		username:         username,
		password:         config.Password,
		httpClient:       &clientCopy,
		pullSecretWriter: config.PullSecretWriter,
		referenceReader:  config.ReferenceReader,
		now:              time.Now,
	}, nil
}

func (r *HarborImageRegistry) CreateProject(ctx context.Context, request ports.RegistryProjectRequest) (ports.RegistryProject, error) {
	tenantID := strings.TrimSpace(request.TenantID)
	name := strings.TrimSpace(request.Name)
	if tenantID == "" || name == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.RegistryProject{}, fmt.Errorf("%w: tenant_id, name, and idempotency_key are required", ports.ErrInvalid)
	}
	if tenantID != name {
		return ports.RegistryProject{}, fmt.Errorf("%w: project must match tenant id", ports.ErrInvalid)
	}
	body, err := json.Marshal(struct {
		ProjectName string `json:"project_name"`
		Public      bool   `json:"public"`
	}{ProjectName: name, Public: request.Public})
	if err != nil {
		return ports.RegistryProject{}, fmt.Errorf("%w: encode Harbor project request: %v", ports.ErrInvalid, err)
	}
	if err := r.do(ctx, http.MethodPost, "/api/v2.0/projects", body, http.StatusCreated); err != nil {
		return ports.RegistryProject{}, err
	}
	return ports.RegistryProject{
		ID:         name,
		TenantID:   tenantID,
		Name:       name,
		Public:     request.Public,
		DevProfile: harborDevProfile(),
		CreatedAt:  r.now().UTC(),
	}, nil
}

func (r *HarborImageRegistry) EnsureProject(ctx context.Context, tenantID string) error {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	projects, err := r.ListProjects(ctx, ports.RegistryProjectListRequest{TenantID: tenantID})
	if err != nil {
		return err
	}
	for _, project := range projects.Items {
		if project.Name == tenantID {
			return nil
		}
	}
	_, err = r.CreateProject(ctx, ports.RegistryProjectRequest{TenantID: tenantID, Name: tenantID, IdempotencyKey: "ensure-" + tenantID})
	if err == nil || !isConflict(err) {
		return err
	}
	// A concurrent ANI request created the project. Verify it exists instead of
	// treating an idempotent ensure as a failed operation.
	projects, err = r.ListProjects(ctx, ports.RegistryProjectListRequest{TenantID: tenantID})
	if err != nil {
		return err
	}
	for _, project := range projects.Items {
		if project.Name == tenantID {
			return nil
		}
	}
	return ports.ErrConflict
}

func (r *HarborImageRegistry) ListProjects(ctx context.Context, request ports.RegistryProjectListRequest) (ports.RegistryProjectListResult, error) {
	tenantID := strings.TrimSpace(request.TenantID)
	if tenantID == "" {
		return ports.RegistryProjectListResult{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	var projects []harborProject
	if err := r.getJSON(ctx, "/api/v2.0/projects?name="+url.QueryEscape(tenantID), &projects); err != nil {
		return ports.RegistryProjectListResult{}, err
	}
	items := make([]ports.RegistryProject, 0, len(projects))
	for _, project := range projects {
		if project.Name != tenantID {
			continue
		}
		createdAt, _ := time.Parse(time.RFC3339, project.CreatedAt)
		items = append(items, ports.RegistryProject{ID: fmt.Sprint(project.ID), TenantID: tenantID, Name: project.Name, Public: project.Public, DevProfile: harborDevProfile(), CreatedAt: createdAt.UTC()})
	}
	return ports.RegistryProjectListResult{Items: items, DevProfile: harborDevProfile()}, nil
}

func (r *HarborImageRegistry) ListRepositories(ctx context.Context, request ports.RegistryRepositoryListRequest) (ports.RegistryRepositoryListResult, error) {
	if err := validateTenantProject(request.TenantID, request.Project); err != nil {
		return ports.RegistryRepositoryListResult{}, err
	}
	project := strings.TrimSpace(request.Project)
	var repositories []harborRepository
	path := "/api/v2.0/projects/" + url.PathEscape(project) + "/repositories"
	if err := r.getJSON(ctx, path, &repositories); err != nil {
		return ports.RegistryRepositoryListResult{}, err
	}
	items := make([]ports.RegistryRepository, 0, len(repositories))
	for _, repository := range repositories {
		name := strings.TrimPrefix(repository.Name, project+"/")
		items = append(items, ports.RegistryRepository{Project: project, Name: name, ArtifactCount: repository.ArtifactCount, PullCount: repository.PullCount, DevProfile: harborDevProfile()})
	}
	return ports.RegistryRepositoryListResult{Items: items, DevProfile: harborDevProfile()}, nil
}

func (r *HarborImageRegistry) SetRepositoryPermission(ctx context.Context, request ports.RegistryPermissionRequest) (ports.RegistryPermission, error) {
	if err := validateTenantProject(request.TenantID, request.Project); err != nil {
		return ports.RegistryPermission{}, err
	}
	if strings.TrimSpace(request.Repository) == "" || strings.TrimSpace(request.Subject) == "" || strings.TrimSpace(request.IdempotencyKey) == "" || len(request.Actions) == 0 {
		return ports.RegistryPermission{}, fmt.Errorf("%w: repository, subject, idempotency_key, and actions are required", ports.ErrInvalid)
	}
	roleID, err := harborProjectRoleForActions(request.Actions)
	if err != nil {
		return ports.RegistryPermission{}, err
	}
	project := strings.TrimSpace(request.Project)
	subject := strings.TrimSpace(request.Subject)
	path := "/api/v2.0/projects/" + url.PathEscape(project) + "/members"
	var members []harborProjectMember
	if err := r.getJSON(ctx, path, &members); err != nil {
		return ports.RegistryPermission{}, err
	}
	for _, member := range members {
		if strings.TrimSpace(member.MemberUser.Username) != subject {
			continue
		}
		body, err := json.Marshal(struct {
			RoleID int `json:"role_id"`
		}{RoleID: roleID})
		if err != nil {
			return ports.RegistryPermission{}, fmt.Errorf("%w: encode Harbor project member update", ports.ErrInvalid)
		}
		if err := r.do(ctx, http.MethodPut, path+"/"+url.PathEscape(fmt.Sprint(member.ID)), body, http.StatusOK); err != nil {
			return ports.RegistryPermission{}, err
		}
		return registryPermissionFromRequest(request, r.now()), nil
	}
	body, err := json.Marshal(struct {
		RoleID     int `json:"role_id"`
		MemberUser struct {
			Username string `json:"username"`
		} `json:"member_user"`
	}{RoleID: roleID, MemberUser: struct {
		Username string `json:"username"`
	}{Username: subject}})
	if err != nil {
		return ports.RegistryPermission{}, fmt.Errorf("%w: encode Harbor project member request", ports.ErrInvalid)
	}
	if err := r.do(ctx, http.MethodPost, path, body, http.StatusCreated); err != nil {
		return ports.RegistryPermission{}, err
	}
	return registryPermissionFromRequest(request, r.now()), nil
}

func registryPermissionFromRequest(request ports.RegistryPermissionRequest, updatedAt time.Time) ports.RegistryPermission {
	return ports.RegistryPermission{Project: request.Project, Repository: request.Repository, Subject: request.Subject, Actions: append([]ports.RegistryPermissionAction(nil), request.Actions...), State: ports.RegistryPermissionActive, DevProfile: harborDevProfile(), UpdatedAt: updatedAt.UTC()}
}

const (
	harborProjectRoleMaintainer = 2
	harborProjectRoleDeveloper  = 3
	harborProjectRoleGuest      = 4
)

func harborProjectRoleForActions(actions []ports.RegistryPermissionAction) (int, error) {
	roleID := harborProjectRoleGuest
	for _, action := range actions {
		switch action {
		case ports.RegistryPermissionPull, ports.RegistryPermissionScan:
		case ports.RegistryPermissionPush:
			if roleID > harborProjectRoleDeveloper {
				roleID = harborProjectRoleDeveloper
			}
		case ports.RegistryPermissionDelete:
			roleID = harborProjectRoleMaintainer
		default:
			return 0, fmt.Errorf("%w: unsupported registry permission action %q", ports.ErrInvalid, action)
		}
	}
	return roleID, nil
}

func (r *HarborImageRegistry) GetScanResult(ctx context.Context, request ports.RegistryScanResultRequest) (ports.RegistryScanResult, error) {
	tenantID := strings.TrimSpace(request.TenantID)
	project, repository, tag, err := parseRegistryImageReference(request.Image)
	if err != nil {
		return ports.RegistryScanResult{}, err
	}
	if tenantID == "" || project != tenantID {
		return ports.RegistryScanResult{}, fmt.Errorf("%w: image project must match tenant_id", ports.ErrInvalid)
	}
	artifacts, err := r.ListArtifacts(ctx, ports.RegistryArtifactListRequest{TenantID: tenantID, Project: project, Repository: repository})
	if err != nil {
		return ports.RegistryScanResult{}, err
	}
	for _, artifact := range artifacts.Items {
		for _, artifactTag := range artifact.Tags {
			if artifactTag == tag {
				result := artifact.ScanStatus
				result.Image = project + "/" + repository + ":" + tag
				return result, nil
			}
		}
	}
	return ports.RegistryScanResult{}, ports.ErrNotFound
}

func (r *HarborImageRegistry) GetProjectScanReport(ctx context.Context, request ports.RegistryProjectScanReportRequest) (ports.RegistryProjectScanReport, error) {
	if err := validateTenantProject(request.TenantID, request.Project); err != nil {
		return ports.RegistryProjectScanReport{}, err
	}
	repositories, err := r.ListRepositories(ctx, ports.RegistryRepositoryListRequest{TenantID: request.TenantID, Project: request.Project})
	if err != nil {
		return ports.RegistryProjectScanReport{}, err
	}
	report := ports.RegistryProjectScanReport{Project: request.Project, Status: ports.RegistryScanNotScanned, ProviderID: "harbor-trivy", DevProfile: harborDevProfile()}
	for _, repository := range repositories.Items {
		artifacts, err := r.ListArtifacts(ctx, ports.RegistryArtifactListRequest{TenantID: request.TenantID, Project: request.Project, Repository: repository.Name})
		if err != nil {
			return ports.RegistryProjectScanReport{}, err
		}
		for _, artifact := range artifacts.Items {
			report.ArtifactsTotal++
			scan := artifact.ScanStatus
			report.Critical += scan.Critical
			report.High += scan.High
			report.Medium += scan.Medium
			report.Low += scan.Low
			if scan.Status == ports.RegistryScanComplete {
				report.ScannedArtifacts++
			}
			report.Status = aggregateScanState(report.Status, scan.Status)
			if scan.ScannedAt.After(report.ScannedAt) {
				report.ScannedAt = scan.ScannedAt
			}
		}
	}
	return report, nil
}

func (r *HarborImageRegistry) GetOverview(ctx context.Context, request ports.RegistryOverviewRequest) (ports.RegistryOverview, error) {
	tenantID := strings.TrimSpace(request.TenantID)
	if tenantID == "" {
		return ports.RegistryOverview{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	projects, err := r.ListProjects(ctx, ports.RegistryProjectListRequest{TenantID: tenantID})
	if err != nil {
		return ports.RegistryOverview{}, err
	}
	images, err := r.ListImages(ctx, ports.RegistryImageListRequest{TenantID: tenantID, Project: tenantID})
	if err != nil {
		return ports.RegistryOverview{}, err
	}
	repositories := map[string]struct{}{}
	vulnerabilities := ports.RegistryOverviewVulnerabilitySummary{}
	for _, image := range images.Items {
		repositories[image.Repository] = struct{}{}
		vulnerabilities.Critical += image.ScanStatus.Critical
		vulnerabilities.High += image.ScanStatus.High
		vulnerabilities.Medium += image.ScanStatus.Medium
		vulnerabilities.Low += image.ScanStatus.Low
	}
	var sizeBytes int64
	for _, image := range images.Items {
		sizeBytes += image.SizeBytes
	}
	return ports.RegistryOverview{
		Resources: []ports.RegistryOverviewResourceSummary{
			{Kind: "project", Total: len(projects.Items), Available: len(projects.Items)},
			{Kind: "repository", Total: len(repositories), Available: len(repositories)},
			{Kind: "artifact", Total: len(images.Items), Available: len(images.Items), SizeBytes: sizeBytes},
			{Kind: "tag", Total: len(images.Items), Available: len(images.Items)},
		},
		Vulnerabilities: vulnerabilities,
		Capabilities: []ports.RegistryOverviewCapability{
			{Key: "projects", Label: "Projects", Status: "available", Path: "/registry/projects"},
			{Key: "repositories", Label: "Repositories", Status: "available", Path: "/registry/projects/" + tenantID + "/repositories"},
			{Key: "tags", Label: "Tags", Status: "available"},
			{Key: "push_instructions", Label: "Push instructions", Status: "available"},
			{Key: "pull_commands", Label: "Pull commands", Status: "available"},
			{Key: "scan_summary", Label: "Vulnerability scan", Status: "available", Path: "/registry/projects/" + tenantID + "/scan-report"},
			{Key: "scan_policy", Label: "Scan policy", Status: "planned"},
			{Key: "quota", Label: "Quota", Status: "planned"},
			{Key: "garbage_collection", Label: "Garbage collection", Status: "planned"},
		},
		CreateOrder:   []string{"project", "login", "tag", "push"},
		Relationships: []ports.RegistryOverviewRelationship{{Source: "project", Target: "repository", Relation: "contains"}, {Source: "repository", Target: "tag", Relation: "publishes"}},
		QuickActions:  []ports.RegistryOverviewQuickAction{{Key: "create_project", Label: "Create project", Path: "/registry/projects"}},
		DeleteRisks:   []ports.RegistryOverviewDeleteRisk{{Kind: "tag", Risk: "Deleting a tag can break workloads that still reference the image."}},
	}, nil
}

func (r *HarborImageRegistry) ListImages(ctx context.Context, request ports.RegistryImageListRequest) (ports.RegistryImageListResult, error) {
	project := strings.TrimSpace(request.Project)
	if project == "" {
		project = strings.TrimSpace(request.TenantID)
	}
	if err := validateTenantProject(request.TenantID, project); err != nil {
		return ports.RegistryImageListResult{}, err
	}
	repositories := []ports.RegistryRepository{}
	if repository := strings.TrimSpace(request.Repository); repository != "" {
		repositories = append(repositories, ports.RegistryRepository{Name: repository})
	} else {
		result, err := r.ListRepositories(ctx, ports.RegistryRepositoryListRequest{TenantID: request.TenantID, Project: project})
		if err != nil {
			return ports.RegistryImageListResult{}, err
		}
		repositories = result.Items
	}
	registryHost := harborRegistryHost(r.endpoint)
	items := []ports.RegistryImage{}
	for _, repository := range repositories {
		artifacts, err := r.ListArtifacts(ctx, ports.RegistryArtifactListRequest{TenantID: request.TenantID, Project: project, Repository: repository.Name})
		if err != nil {
			return ports.RegistryImageListResult{}, err
		}
		for _, artifact := range artifacts.Items {
			for _, tag := range artifact.Tags {
				if requestedTag := strings.TrimSpace(request.Tag); requestedTag != "" && requestedTag != tag {
					continue
				}
				if request.ScanStatus != "" && request.ScanStatus != artifact.ScanStatus.Status {
					continue
				}
				image := registryHost + "/" + project + "/" + repository.Name + ":" + tag
				items = append(items, ports.RegistryImage{Project: project, Repository: repository.Name, Tag: tag, Image: image, Registry: registryHost, Digest: artifact.Digest, MediaType: artifact.MediaType, SizeBytes: artifact.SizeBytes, PullCommand: "docker pull " + image, PushedAt: artifact.PushedAt, ScanStatus: artifact.ScanStatus, DevProfile: harborDevProfile()})
			}
		}
	}
	return ports.RegistryImageListResult{Items: items, DevProfile: harborDevProfile()}, nil
}

func (r *HarborImageRegistry) GetPushInstructions(_ context.Context, request ports.RegistryPushInstructionsRequest) (ports.RegistryPushInstructions, error) {
	if err := validateTenantProject(request.TenantID, request.Project); err != nil {
		return ports.RegistryPushInstructions{}, err
	}
	repository := strings.TrimSpace(request.Repository)
	if repository == "" {
		repository = "<repository>"
	}
	registryHost := harborRegistryHost(r.endpoint)
	example := registryHost + "/" + request.Project + "/" + repository + ":<tag>"
	return ports.RegistryPushInstructions{Project: request.Project, Registry: registryHost, RepositoryExample: example, Commands: []ports.RegistryCommand{{Label: "Login", Command: "docker login " + registryHost}, {Label: "Tag", Command: "docker tag <local-image> " + example}, {Label: "Push", Command: "docker push " + example}}, DevProfile: harborDevProfile()}, nil
}

func (r *HarborImageRegistry) ListTagReferences(ctx context.Context, request ports.RegistryImageReferenceListRequest) (ports.RegistryImageReferenceListResult, error) {
	if err := validateRegistryTagRequest(request.TenantID, request.Project, request.Repository, request.Tag); err != nil {
		return ports.RegistryImageReferenceListResult{}, err
	}
	if r.referenceReader == nil {
		return ports.RegistryImageReferenceListResult{}, ports.ErrNotConfigured
	}
	return r.referenceReader.ListRegistryImageReferences(ctx, request)
}

func (r *HarborImageRegistry) DeleteTag(ctx context.Context, request ports.RegistryTagDeleteRequest) (ports.RegistryDeletedTag, error) {
	references, err := r.ListTagReferences(ctx, ports.RegistryImageReferenceListRequest(request))
	if err != nil {
		return ports.RegistryDeletedTag{}, err
	}
	if references.DeleteBlocked || len(references.Items) > 0 {
		return ports.RegistryDeletedTag{}, fmt.Errorf("%w: image tag is still referenced", ports.ErrConflict)
	}
	artifacts, err := r.ListArtifacts(ctx, ports.RegistryArtifactListRequest{TenantID: request.TenantID, Project: request.Project, Repository: request.Repository})
	if err != nil {
		return ports.RegistryDeletedTag{}, err
	}
	digest := ""
	for _, artifact := range artifacts.Items {
		for _, tag := range artifact.Tags {
			if tag == request.Tag {
				digest = artifact.Digest
				break
			}
		}
	}
	if digest == "" {
		return ports.RegistryDeletedTag{}, ports.ErrNotFound
	}
	path := "/api/v2.0/projects/" + url.PathEscape(request.Project) + "/repositories/" + url.PathEscape(request.Repository) + "/artifacts/" + url.PathEscape(request.Tag)
	if err := r.do(ctx, http.MethodDelete, path, nil, http.StatusOK); err != nil {
		return ports.RegistryDeletedTag{}, err
	}
	return ports.RegistryDeletedTag{Project: request.Project, Repository: request.Repository, Tag: request.Tag, Digest: digest, DeletedAt: r.now().UTC()}, nil
}

func (r *HarborImageRegistry) ListTags(ctx context.Context, repository string) ([]ports.ImageTag, error) {
	project, repository, found := strings.Cut(strings.TrimSpace(repository), "/")
	if !found || project == "" || repository == "" {
		return nil, fmt.Errorf("%w: repository must be project/repository", ports.ErrInvalid)
	}
	artifacts, err := r.ListArtifacts(ctx, ports.RegistryArtifactListRequest{TenantID: project, Project: project, Repository: repository})
	if err != nil {
		return nil, err
	}
	tags := []ports.ImageTag{}
	for _, artifact := range artifacts.Items {
		for _, tag := range artifact.Tags {
			tags = append(tags, ports.ImageTag{Name: tag, Digest: artifact.Digest})
		}
	}
	return tags, nil
}

func (r *HarborImageRegistry) GetScanStatus(ctx context.Context, ref ports.ImageRef) (ports.ImageScanStatus, error) {
	project, repository, found := strings.Cut(strings.TrimSpace(ref.Repository), "/")
	if !found || project == "" || repository == "" {
		return ports.ImageScanStatus{}, fmt.Errorf("%w: repository must be project/repository", ports.ErrInvalid)
	}
	scan, err := r.GetScanResult(ctx, ports.RegistryScanResultRequest{TenantID: project, Image: project + "/" + repository + ":" + strings.TrimSpace(ref.Tag)})
	if err != nil {
		return ports.ImageScanStatus{}, err
	}
	return ports.ImageScanStatus{Status: string(scan.Status), Critical: scan.Critical, High: scan.High, Medium: scan.Medium, Low: scan.Low, ReportURL: scan.ReportURL, ProviderID: scan.ProviderID}, nil
}

func (r *HarborImageRegistry) CreatePullSecret(ctx context.Context, request ports.RegistryPullSecretRequest) (ports.RegistryPullSecret, error) {
	if err := validateTenantProject(request.TenantID, request.Project); err != nil {
		return ports.RegistryPullSecret{}, err
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.RegistryPullSecret{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.Namespace) == "" {
		return ports.RegistryPullSecret{}, fmt.Errorf("%w: namespace is required", ports.ErrInvalid)
	}
	if r.pullSecretWriter == nil {
		return ports.RegistryPullSecret{}, ports.ErrNotConfigured
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = "ani-registry-pull"
	}
	if err := r.EnsureProject(ctx, request.Project); err != nil {
		return ports.RegistryPullSecret{}, err
	}
	robotName := harborRobotName(name)
	payload, err := json.Marshal(struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Duration    int    `json:"duration"`
		Disable     bool   `json:"disable"`
		Permissions []struct {
			Kind      string `json:"kind"`
			Namespace string `json:"namespace"`
			Access    []struct {
				Resource string `json:"resource"`
				Action   string `json:"action"`
			} `json:"access"`
		} `json:"permissions"`
	}{Name: robotName, Description: "ANI pull-only workload credential", Duration: -1, Permissions: []struct {
		Kind      string `json:"kind"`
		Namespace string `json:"namespace"`
		Access    []struct {
			Resource string `json:"resource"`
			Action   string `json:"action"`
		} `json:"access"`
	}{{Kind: "project", Namespace: request.Project, Access: []struct {
		Resource string `json:"resource"`
		Action   string `json:"action"`
	}{{Resource: "repository", Action: "pull"}}}}})
	if err != nil {
		return ports.RegistryPullSecret{}, fmt.Errorf("%w: encode Harbor robot request", ports.ErrInvalid)
	}
	var robot harborRobot
	path := "/api/v2.0/projects/" + url.PathEscape(request.Project) + "/robots"
	if err := r.doJSON(ctx, http.MethodPost, path, payload, http.StatusCreated, &robot); err != nil {
		return ports.RegistryPullSecret{}, err
	}
	if strings.TrimSpace(robot.Token) == "" || strings.TrimSpace(robot.Name) == "" {
		return ports.RegistryPullSecret{}, fmt.Errorf("%w: Harbor did not return robot credentials", ports.ErrInvalid)
	}
	registryHost := harborRegistryHost(r.endpoint)
	dockerConfigJSON, err := dockerConfigJSON(registryHost, robot.Name, robot.Token)
	if err != nil {
		return ports.RegistryPullSecret{}, err
	}
	if err := r.pullSecretWriter.ApplyRegistryPullSecret(ctx, ports.RegistryPullSecretWriteRequest{TenantID: request.TenantID, Namespace: request.Namespace, Name: name, Registry: registryHost, Username: robot.Name, DockerConfigJSON: dockerConfigJSON}); err != nil {
		return ports.RegistryPullSecret{}, err
	}
	return ports.RegistryPullSecret{Project: request.Project, Name: name, SecretRef: request.Namespace + "/" + name, Registry: registryHost, Username: robot.Name, Namespace: request.Namespace, State: ports.RegistryPermissionActive, DevProfile: harborDevProfile(), CreatedAt: r.now().UTC()}, nil
}

type harborRobot struct {
	Name  string `json:"name"`
	Token string `json:"token"`
}

func harborRobotName(secretName string) string {
	value := strings.ToLower(strings.TrimSpace(secretName))
	value = strings.NewReplacer("_", "-", ".", "-", "/", "-").Replace(value)
	if value == "" {
		return "ani-registry-pull"
	}
	return "ani-" + value
}

func dockerConfigJSON(registry, username, token string) (string, error) {
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + token))
	payload, err := json.Marshal(map[string]any{"auths": map[string]any{registry: map[string]string{"username": username, "password": token, "auth": auth}}})
	if err != nil {
		return "", fmt.Errorf("%w: encode pull secret", ports.ErrInvalid)
	}
	return string(payload), nil
}

func parseRegistryImageReference(image string) (project, repository, tag string, err error) {
	value := strings.TrimSpace(image)
	lastSlash := strings.LastIndex(value, "/")
	tagSeparator := strings.LastIndex(value, ":")
	if tagSeparator <= lastSlash || tagSeparator == len(value)-1 {
		return "", "", "", fmt.Errorf("%w: image tag is required", ports.ErrInvalid)
	}
	tag = strings.TrimSpace(value[tagSeparator+1:])
	parts := strings.Split(value[:tagSeparator], "/")
	if len(parts) >= 3 && isRegistryHost(parts[0]) {
		parts = parts[1:]
	}
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" {
		return "", "", "", fmt.Errorf("%w: image must include project and repository", ports.ErrInvalid)
	}
	project = strings.TrimSpace(parts[0])
	repository = strings.TrimSpace(strings.Join(parts[1:], "/"))
	if repository == "" {
		return "", "", "", fmt.Errorf("%w: image must include project and repository", ports.ErrInvalid)
	}
	return project, repository, tag, nil
}

func isRegistryHost(value string) bool {
	value = strings.TrimSpace(value)
	return value == "localhost" || strings.Contains(value, ".") || strings.Contains(value, ":")
}

type harborProject struct {
	ID        int64  `json:"project_id"`
	Name      string `json:"name"`
	Public    bool   `json:"public"`
	CreatedAt string `json:"creation_time"`
}

type harborProjectMember struct {
	ID         int64 `json:"id"`
	MemberUser struct {
		Username string `json:"username"`
	} `json:"member_user"`
}

type harborRepository struct {
	Name          string `json:"name"`
	ArtifactCount int    `json:"artifact_count"`
	PullCount     int    `json:"pull_count"`
}

func (r *HarborImageRegistry) ListArtifacts(ctx context.Context, request ports.RegistryArtifactListRequest) (ports.RegistryArtifactListResult, error) {
	if err := validateTenantProject(request.TenantID, request.Project); err != nil {
		return ports.RegistryArtifactListResult{}, err
	}
	repository := strings.TrimSpace(request.Repository)
	if repository == "" {
		return ports.RegistryArtifactListResult{}, fmt.Errorf("%w: repository is required", ports.ErrInvalid)
	}
	var artifacts []harborArtifact
	path := "/api/v2.0/projects/" + url.PathEscape(strings.TrimSpace(request.Project)) + "/repositories/" + url.PathEscape(repository) + "/artifacts"
	if err := r.getJSON(ctx, path, &artifacts); err != nil {
		return ports.RegistryArtifactListResult{}, err
	}
	items := make([]ports.RegistryArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		item := ports.RegistryArtifact{
			Project:    strings.TrimSpace(request.Project),
			Repository: repository,
			Digest:     artifact.Digest,
			MediaType:  artifact.MediaType,
			SizeBytes:  artifact.SizeBytes,
			Tags:       artifact.tagNames(),
			PushedAt:   artifact.pushedAt(),
			ScanStatus: artifact.scanResult(strings.TrimSpace(request.Project) + "/" + repository),
			DevProfile: harborDevProfile(),
		}
		items = append(items, item)
	}
	return ports.RegistryArtifactListResult{Items: items, DevProfile: harborDevProfile()}, nil
}

type harborArtifact struct {
	Digest       string                     `json:"digest"`
	SizeBytes    int64                      `json:"size"`
	MediaType    string                     `json:"manifest_media_type"`
	Tags         []harborArtifactTag        `json:"tags"`
	ScanOverview map[string]harborScanEntry `json:"scan_overview"`
}

type harborArtifactTag struct {
	Name     string `json:"name"`
	PushTime string `json:"push_time"`
}

type harborScanEntry struct {
	ScanStatus string         `json:"scan_status"`
	Summary    map[string]int `json:"summary"`
}

func (a harborArtifact) tagNames() []string {
	names := make([]string, 0, len(a.Tags))
	for _, tag := range a.Tags {
		if name := strings.TrimSpace(tag.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func (a harborArtifact) pushedAt() time.Time {
	for _, tag := range a.Tags {
		if value, err := time.Parse(time.RFC3339, tag.PushTime); err == nil {
			return value.UTC()
		}
	}
	return time.Time{}
}

func (a harborArtifact) scanResult(image string) ports.RegistryScanResult {
	result := ports.RegistryScanResult{Image: image, Status: ports.RegistryScanNotScanned, ProviderID: "harbor-trivy", DevProfile: harborDevProfile()}
	for _, scan := range a.ScanOverview {
		switch strings.ToLower(strings.TrimSpace(scan.ScanStatus)) {
		case "success", "complete", "finished":
			result.Status = ports.RegistryScanComplete
		case "pending":
			result.Status = ports.RegistryScanPending
		case "running":
			result.Status = ports.RegistryScanRunning
		case "error", "failed":
			result.Status = ports.RegistryScanFailed
		}
		result.Critical = scan.Summary["Critical"]
		result.High = scan.Summary["High"]
		result.Medium = scan.Summary["Medium"]
		result.Low = scan.Summary["Low"]
		break
	}
	return result
}

func (r *HarborImageRegistry) getJSON(ctx context.Context, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint+path, nil)
	if err != nil {
		return fmt.Errorf("%w: create Harbor request: %v", ports.ErrInvalid, err)
	}
	req.SetBasicAuth(r.username, r.password)
	response, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: Harbor request failed", ports.ErrNotConfigured)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return harborStatusError(response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("%w: decode Harbor response", ports.ErrInvalid)
	}
	return nil
}

func (r *HarborImageRegistry) do(ctx context.Context, method, path string, body []byte, expectedStatus int) error {
	return r.doJSON(ctx, method, path, body, expectedStatus, nil)
}

func (r *HarborImageRegistry) doJSON(ctx context.Context, method, path string, body []byte, expectedStatus int, target any) error {
	req, err := http.NewRequestWithContext(ctx, method, r.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: create Harbor request: %v", ports.ErrInvalid, err)
	}
	req.SetBasicAuth(r.username, r.password)
	req.Header.Set("Content-Type", "application/json")
	response, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: Harbor request failed", ports.ErrNotConfigured)
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		return harborStatusError(response.StatusCode)
	}
	if target == nil || response.ContentLength == 0 {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("%w: decode Harbor response", ports.ErrInvalid)
	}
	return nil
}

func harborStatusError(statusCode int) error {
	switch statusCode {
	case http.StatusNotFound:
		return ports.ErrNotFound
	case http.StatusConflict:
		return ports.ErrConflict
	case http.StatusUnauthorized, http.StatusForbidden:
		return ports.ErrInvalidCredentials
	default:
		return fmt.Errorf("%w: Harbor returned HTTP %d", ports.ErrNotConfigured, statusCode)
	}
}

func isConflict(err error) bool { return errors.Is(err, ports.ErrConflict) }

func harborRegistryHost(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return endpoint
	}
	return parsed.Host
}

func aggregateScanState(current, next ports.RegistryScanState) ports.RegistryScanState {
	if next == ports.RegistryScanFailed || current == ports.RegistryScanFailed {
		return ports.RegistryScanFailed
	}
	if next == ports.RegistryScanRunning || current == ports.RegistryScanRunning {
		return ports.RegistryScanRunning
	}
	if next == ports.RegistryScanPending || current == ports.RegistryScanPending {
		return ports.RegistryScanPending
	}
	if next == ports.RegistryScanComplete || current == ports.RegistryScanComplete {
		return ports.RegistryScanComplete
	}
	return ports.RegistryScanNotScanned
}

func harborDevProfile() ports.DevProfileInfo {
	return ports.DevProfileInfo{Mode: "provider", Provider: "harbor", RealProvider: true}
}

var _ ports.ImageRegistry = (*HarborImageRegistry)(nil)
