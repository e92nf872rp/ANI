package router

import (
	"context"
	"errors"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	registryadapter "github.com/kubercloud/ani/pkg/adapters/registry"
	"github.com/kubercloud/ani/pkg/ports"
)

type registryAPI struct {
	service ports.ImageRegistry
}

type registryProjectListResponse struct {
	Items      []registryProjectResponse `json:"items"`
	Total      int                       `json:"total"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}

type registryProjectResponse struct {
	ID         string                 `json:"id"`
	TenantID   string                 `json:"tenant_id"`
	Name       string                 `json:"name"`
	Public     bool                   `json:"public"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
	CreatedAt  string                 `json:"created_at"`
}

type createRegistryProjectRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	Public         bool   `json:"public"`
}

type registryOverviewResponse struct {
	Resources       []registryOverviewResourceSummaryResponse    `json:"resources"`
	Vulnerabilities registryOverviewVulnerabilitySummaryResponse `json:"vulnerabilities"`
	Capabilities    []registryOverviewCapabilityResponse         `json:"capabilities"`
	CreateOrder     []string                                     `json:"create_order"`
	Relationships   []registryOverviewRelationshipResponse       `json:"relationships"`
	QuickActions    []registryOverviewQuickActionResponse        `json:"quick_actions"`
	DeleteRisks     []registryOverviewDeleteRiskResponse         `json:"delete_risks"`
}

type registryOverviewResourceSummaryResponse struct {
	Kind      string `json:"kind"`
	Total     int    `json:"total"`
	Available int    `json:"available"`
	Pending   int    `json:"pending"`
	Failed    int    `json:"failed"`
	SizeBytes int64  `json:"size_bytes"`
}

type registryOverviewVulnerabilitySummaryResponse struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
}

type registryOverviewCapabilityResponse struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Status      string `json:"status"`
	Path        string `json:"path,omitempty"`
	Description string `json:"description,omitempty"`
}

type registryOverviewRelationshipResponse struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Relation string `json:"relation"`
}

type registryOverviewQuickActionResponse struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Path        string `json:"path,omitempty"`
	Description string `json:"description,omitempty"`
}

type registryOverviewDeleteRiskResponse struct {
	Kind string `json:"kind"`
	Risk string `json:"risk"`
}

type registryImageListResponse struct {
	Items      []registryImageResponse `json:"items"`
	Total      int                     `json:"total"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

type registryImageResponse struct {
	Project     string                     `json:"project"`
	Repository  string                     `json:"repository"`
	Tag         string                     `json:"tag"`
	Image       string                     `json:"image"`
	Registry    string                     `json:"registry,omitempty"`
	Digest      string                     `json:"digest"`
	MediaType   string                     `json:"media_type"`
	SizeBytes   int64                      `json:"size_bytes"`
	PullCommand string                     `json:"pull_command,omitempty"`
	PushedAt    string                     `json:"pushed_at"`
	ScanStatus  registryScanResultResponse `json:"scan_status"`
	DevProfile  coreDevProfileResponse     `json:"dev_profile"`
}

type registryRepositoryListResponse struct {
	Items      []registryRepositoryResponse `json:"items"`
	Total      int                          `json:"total"`
	NextCursor string                       `json:"next_cursor,omitempty"`
}

type registryRepositoryResponse struct {
	Project       string                      `json:"project"`
	Name          string                      `json:"name"`
	ArtifactCount int                         `json:"artifact_count"`
	PullCount     int                         `json:"pull_count"`
	Permission    *registryPermissionResponse `json:"permission,omitempty"`
	DevProfile    coreDevProfileResponse      `json:"dev_profile"`
}

type registryArtifactListResponse struct {
	Items      []registryArtifactResponse `json:"items"`
	Total      int                        `json:"total"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

type registryArtifactResponse struct {
	Project    string                     `json:"project"`
	Repository string                     `json:"repository"`
	Digest     string                     `json:"digest"`
	Tags       []string                   `json:"tags"`
	MediaType  string                     `json:"media_type"`
	SizeBytes  int64                      `json:"size_bytes"`
	PushedAt   string                     `json:"pushed_at"`
	ScanStatus registryScanResultResponse `json:"scan_status"`
	DevProfile coreDevProfileResponse     `json:"dev_profile"`
}

type setRegistryPermissionRequest struct {
	IdempotencyKey string   `json:"idempotency_key"`
	Subject        string   `json:"subject"`
	Actions        []string `json:"actions"`
}

type registryPermissionResponse struct {
	Project    string                 `json:"project"`
	Repository string                 `json:"repository"`
	Subject    string                 `json:"subject"`
	Actions    []string               `json:"actions"`
	State      string                 `json:"state"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
	UpdatedAt  string                 `json:"updated_at"`
}

type createRegistryPullSecretRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	Namespace      string `json:"namespace"`
}

type registryPullSecretResponse struct {
	Project    string                 `json:"project"`
	Name       string                 `json:"name"`
	SecretRef  string                 `json:"secret_ref"`
	Registry   string                 `json:"registry"`
	Username   string                 `json:"username"`
	Namespace  string                 `json:"namespace,omitempty"`
	State      string                 `json:"state"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
	CreatedAt  string                 `json:"created_at"`
}

type registryScanResultResponse struct {
	Image      string                 `json:"image"`
	Status     string                 `json:"status"`
	Critical   int                    `json:"critical"`
	High       int                    `json:"high"`
	Medium     int                    `json:"medium"`
	Low        int                    `json:"low"`
	ReportURL  string                 `json:"report_url"`
	ProviderID string                 `json:"provider_id"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
	ScannedAt  string                 `json:"scanned_at"`
}

type registryProjectScanReportResponse struct {
	Project          string                 `json:"project"`
	Status           string                 `json:"status"`
	Critical         int                    `json:"critical"`
	High             int                    `json:"high"`
	Medium           int                    `json:"medium"`
	Low              int                    `json:"low"`
	ArtifactsTotal   int                    `json:"artifacts_total"`
	ScannedArtifacts int                    `json:"scanned_artifacts"`
	ProviderID       string                 `json:"provider_id"`
	DevProfile       coreDevProfileResponse `json:"dev_profile"`
	ScannedAt        string                 `json:"scanned_at"`
}

type registryCommandResponse struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

type registryPushInstructionsResponse struct {
	Project           string                    `json:"project"`
	Registry          string                    `json:"registry"`
	RepositoryExample string                    `json:"repository_example"`
	Commands          []registryCommandResponse `json:"commands"`
	DevProfile        coreDevProfileResponse    `json:"dev_profile"`
}

type registryDeletedTagResponse struct {
	Project    string `json:"project"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Digest     string `json:"digest,omitempty"`
	DeletedAt  string `json:"deleted_at"`
}

type registryImageReferenceResponse struct {
	Kind       string                 `json:"kind"`
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Route      string                 `json:"route"`
	State      string                 `json:"state"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
}

type registryTagReferenceListResponse struct {
	Project       string                           `json:"project"`
	Repository    string                           `json:"repository"`
	Tag           string                           `json:"tag"`
	Image         string                           `json:"image,omitempty"`
	Items         []registryImageReferenceResponse `json:"items"`
	Total         int                              `json:"total"`
	DeleteBlocked bool                             `json:"delete_blocked"`
}

func newRegistryAPI() *registryAPI {
	return &registryAPI{service: registryadapter.NewLocalImageRegistry()}
}

func registerHarbor(v1 *route.RouterGroup) {
	api := newRegistryAPI()
	v1.GET("/registry/overview", api.getOverview)
	v1.GET("/registry/images", api.listImages)
	v1.GET("/registry/projects", api.listProjects)
	v1.POST("/registry/projects", api.createProject)
	v1.GET("/registry/projects/:project/push-instructions", api.getPushInstructions)
	v1.GET("/registry/projects/:project/repositories", api.listRepositories)
	v1.DELETE("/registry/projects/:project/repositories/:repository/tags/:tag", api.deleteTag)
	v1.GET("/registry/projects/:project/repositories/:repository/tags/:tag/references", api.listTagReferences)
	v1.GET("/registry/projects/:project/repositories/:repository/artifacts", api.listArtifacts)
	v1.POST("/registry/projects/:project/repositories/:repository/permissions", api.setPermission)
	v1.POST("/registry/projects/:project/pull-secret", api.createPullSecret)
	v1.GET("/registry/projects/:project/scan-report", api.getProjectScanReport)
	v1.GET("/registry/images/scan-result", api.getScanResult)
}

func (api *registryAPI) getOverview(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.GetOverview(ctx, ports.RegistryOverviewRequest{TenantID: demoTenantID(c)})
	if err != nil {
		writeRegistryError(c, err)
		return
	}
	c.JSON(http.StatusOK, registryOverviewFromRecord(result))
}

func (api *registryAPI) listImages(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.ListImages(ctx, ports.RegistryImageListRequest{
		TenantID:   demoTenantID(c),
		Project:    c.Query("project"),
		Repository: c.Query("repository"),
		Tag:        c.Query("tag"),
		ScanStatus: ports.RegistryScanState(c.Query("scan_status")),
		Limit:      queryInt(c, "limit", 20),
		Cursor:     c.Query("cursor"),
	})
	if err != nil {
		writeRegistryError(c, err)
		return
	}
	c.JSON(http.StatusOK, registryImagesFromResult(result))
}

func (api *registryAPI) listProjects(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.ListProjects(ctx, ports.RegistryProjectListRequest{
		TenantID: demoTenantID(c),
		Limit:    queryInt(c, "limit", 20),
		Cursor:   c.Query("cursor"),
	})
	if err != nil {
		writeRegistryError(c, err)
		return
	}
	c.JSON(http.StatusOK, registryProjectsFromResult(result))
}

func (api *registryAPI) createProject(ctx context.Context, c *app.RequestContext) {
	var req createRegistryProjectRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid registry project request")
		return
	}
	project, err := api.service.CreateProject(ctx, ports.RegistryProjectRequest{
		TenantID:       demoTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		Public:         req.Public,
	})
	if err != nil {
		writeRegistryError(c, err)
		return
	}
	c.JSON(http.StatusCreated, registryProjectFromRecord(project))
}

func (api *registryAPI) getPushInstructions(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.GetPushInstructions(ctx, ports.RegistryPushInstructionsRequest{
		TenantID:   demoTenantID(c),
		Project:    c.Param("project"),
		Repository: c.Query("repository"),
	})
	if err != nil {
		writeRegistryError(c, err)
		return
	}
	c.JSON(http.StatusOK, registryPushInstructionsFromRecord(result))
}

func (api *registryAPI) listRepositories(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.ListRepositories(ctx, ports.RegistryRepositoryListRequest{
		TenantID: demoTenantID(c),
		Project:  c.Param("project"),
		Limit:    queryInt(c, "limit", 20),
		Cursor:   c.Query("cursor"),
	})
	if err != nil {
		writeRegistryError(c, err)
		return
	}
	c.JSON(http.StatusOK, registryRepositoriesFromResult(result))
}

func (api *registryAPI) listArtifacts(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.ListArtifacts(ctx, ports.RegistryArtifactListRequest{
		TenantID:   demoTenantID(c),
		Project:    c.Param("project"),
		Repository: c.Param("repository"),
		Limit:      queryInt(c, "limit", 20),
		Cursor:     c.Query("cursor"),
	})
	if err != nil {
		writeRegistryError(c, err)
		return
	}
	c.JSON(http.StatusOK, registryArtifactsFromResult(result))
}

func (api *registryAPI) deleteTag(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.DeleteTag(ctx, ports.RegistryTagDeleteRequest{
		TenantID:   demoTenantID(c),
		Project:    c.Param("project"),
		Repository: c.Param("repository"),
		Tag:        c.Param("tag"),
	})
	if err != nil {
		writeRegistryError(c, err)
		return
	}
	c.JSON(http.StatusOK, registryDeletedTagFromRecord(result))
}

func (api *registryAPI) listTagReferences(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.ListTagReferences(ctx, ports.RegistryTagReferenceListRequest{
		TenantID:   demoTenantID(c),
		Project:    c.Param("project"),
		Repository: c.Param("repository"),
		Tag:        c.Param("tag"),
	})
	if err != nil {
		writeRegistryError(c, err)
		return
	}
	c.JSON(http.StatusOK, registryTagReferencesFromResult(result))
}

func (api *registryAPI) setPermission(ctx context.Context, c *app.RequestContext) {
	var req setRegistryPermissionRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid registry permission request")
		return
	}
	actions := make([]ports.RegistryPermissionAction, 0, len(req.Actions))
	for _, action := range req.Actions {
		actions = append(actions, ports.RegistryPermissionAction(action))
	}
	permission, err := api.service.SetRepositoryPermission(ctx, ports.RegistryPermissionRequest{
		TenantID:       demoTenantID(c),
		Project:        c.Param("project"),
		Repository:     c.Param("repository"),
		IdempotencyKey: req.IdempotencyKey,
		Subject:        req.Subject,
		Actions:        actions,
	})
	if err != nil {
		writeRegistryError(c, err)
		return
	}
	c.JSON(http.StatusOK, registryPermissionFromRecord(permission))
}

func (api *registryAPI) createPullSecret(ctx context.Context, c *app.RequestContext) {
	var req createRegistryPullSecretRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid registry pull secret request")
		return
	}
	secret, err := api.service.CreatePullSecret(ctx, ports.RegistryPullSecretRequest{
		TenantID:       demoTenantID(c),
		Project:        c.Param("project"),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		Namespace:      req.Namespace,
	})
	if err != nil {
		writeRegistryError(c, err)
		return
	}
	c.JSON(http.StatusCreated, registryPullSecretFromRecord(secret))
}

func (api *registryAPI) getProjectScanReport(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.GetProjectScanReport(ctx, ports.RegistryProjectScanReportRequest{
		TenantID: demoTenantID(c),
		Project:  c.Param("project"),
	})
	if err != nil {
		writeRegistryError(c, err)
		return
	}
	c.JSON(http.StatusOK, registryProjectScanReportFromRecord(result))
}

func (api *registryAPI) getScanResult(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.GetScanResult(ctx, ports.RegistryScanResultRequest{
		TenantID: demoTenantID(c),
		Image:    c.Query("image"),
	})
	if err != nil {
		writeRegistryError(c, err)
		return
	}
	c.JSON(http.StatusOK, registryScanResultFromRecord(result))
}

func registryOverviewFromRecord(record ports.RegistryOverview) registryOverviewResponse {
	resources := make([]registryOverviewResourceSummaryResponse, 0, len(record.Resources))
	for _, item := range record.Resources {
		resources = append(resources, registryOverviewResourceSummaryResponse{
			Kind:      item.Kind,
			Total:     item.Total,
			Available: item.Available,
			Pending:   item.Pending,
			Failed:    item.Failed,
			SizeBytes: item.SizeBytes,
		})
	}
	capabilities := make([]registryOverviewCapabilityResponse, 0, len(record.Capabilities))
	for _, item := range record.Capabilities {
		capabilities = append(capabilities, registryOverviewCapabilityResponse{
			Key:         item.Key,
			Label:       item.Label,
			Status:      item.Status,
			Path:        item.Path,
			Description: item.Description,
		})
	}
	relationships := make([]registryOverviewRelationshipResponse, 0, len(record.Relationships))
	for _, item := range record.Relationships {
		relationships = append(relationships, registryOverviewRelationshipResponse(item))
	}
	quickActions := make([]registryOverviewQuickActionResponse, 0, len(record.QuickActions))
	for _, item := range record.QuickActions {
		quickActions = append(quickActions, registryOverviewQuickActionResponse{
			Key:         item.Key,
			Label:       item.Label,
			Path:        item.Path,
			Description: item.Description,
		})
	}
	deleteRisks := make([]registryOverviewDeleteRiskResponse, 0, len(record.DeleteRisks))
	for _, item := range record.DeleteRisks {
		deleteRisks = append(deleteRisks, registryOverviewDeleteRiskResponse(item))
	}
	return registryOverviewResponse{
		Resources: resources,
		Vulnerabilities: registryOverviewVulnerabilitySummaryResponse{
			Critical: record.Vulnerabilities.Critical,
			High:     record.Vulnerabilities.High,
			Medium:   record.Vulnerabilities.Medium,
			Low:      record.Vulnerabilities.Low,
		},
		Capabilities:  capabilities,
		CreateOrder:   append([]string(nil), record.CreateOrder...),
		Relationships: relationships,
		QuickActions:  quickActions,
		DeleteRisks:   deleteRisks,
	}
}

func registryImagesFromResult(result ports.RegistryImageListResult) registryImageListResponse {
	items := make([]registryImageResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, registryImageResponse{
			Project:     item.Project,
			Repository:  item.Repository,
			Tag:         item.Tag,
			Image:       item.Image,
			Registry:    item.Registry,
			Digest:      item.Digest,
			MediaType:   item.MediaType,
			SizeBytes:   item.SizeBytes,
			PullCommand: item.PullCommand,
			PushedAt:    networkTime(item.PushedAt),
			ScanStatus:  registryScanResultFromRecord(item.ScanStatus),
			DevProfile:  devProfileFromPort(item.DevProfile),
		})
	}
	return registryImageListResponse{Items: items, Total: len(items), NextCursor: result.NextCursor}
}

func registryProjectsFromResult(result ports.RegistryProjectListResult) registryProjectListResponse {
	items := make([]registryProjectResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, registryProjectFromRecord(item))
	}
	return registryProjectListResponse{Items: items, Total: len(items), NextCursor: result.NextCursor}
}

func registryProjectFromRecord(record ports.RegistryProject) registryProjectResponse {
	return registryProjectResponse{
		ID:         record.ID,
		TenantID:   record.TenantID,
		Name:       record.Name,
		Public:     record.Public,
		DevProfile: devProfileFromPort(record.DevProfile),
		CreatedAt:  networkTime(record.CreatedAt),
	}
}

func registryRepositoriesFromResult(result ports.RegistryRepositoryListResult) registryRepositoryListResponse {
	items := make([]registryRepositoryResponse, 0, len(result.Items))
	for _, item := range result.Items {
		response := registryRepositoryResponse{
			Project:       item.Project,
			Name:          item.Name,
			ArtifactCount: item.ArtifactCount,
			PullCount:     item.PullCount,
			DevProfile:    devProfileFromPort(item.DevProfile),
		}
		if item.Permission != nil {
			permission := registryPermissionFromRecord(*item.Permission)
			response.Permission = &permission
		}
		items = append(items, response)
	}
	return registryRepositoryListResponse{Items: items, Total: len(items), NextCursor: result.NextCursor}
}

func registryArtifactsFromResult(result ports.RegistryArtifactListResult) registryArtifactListResponse {
	items := make([]registryArtifactResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, registryArtifactResponse{
			Project:    item.Project,
			Repository: item.Repository,
			Digest:     item.Digest,
			Tags:       append([]string(nil), item.Tags...),
			MediaType:  item.MediaType,
			SizeBytes:  item.SizeBytes,
			PushedAt:   networkTime(item.PushedAt),
			ScanStatus: registryScanResultFromRecord(item.ScanStatus),
			DevProfile: devProfileFromPort(item.DevProfile),
		})
	}
	return registryArtifactListResponse{Items: items, Total: len(items), NextCursor: result.NextCursor}
}

func registryPushInstructionsFromRecord(record ports.RegistryPushInstructions) registryPushInstructionsResponse {
	commands := make([]registryCommandResponse, 0, len(record.Commands))
	for _, command := range record.Commands {
		commands = append(commands, registryCommandResponse(command))
	}
	return registryPushInstructionsResponse{
		Project:           record.Project,
		Registry:          record.Registry,
		RepositoryExample: record.RepositoryExample,
		Commands:          commands,
		DevProfile:        devProfileFromPort(record.DevProfile),
	}
}

func registryDeletedTagFromRecord(record ports.RegistryDeletedTag) registryDeletedTagResponse {
	return registryDeletedTagResponse{
		Project:    record.Project,
		Repository: record.Repository,
		Tag:        record.Tag,
		Digest:     record.Digest,
		DeletedAt:  networkTime(record.DeletedAt),
	}
}

func registryTagReferencesFromResult(result ports.RegistryTagReferenceListResult) registryTagReferenceListResponse {
	items := make([]registryImageReferenceResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, registryImageReferenceResponse{
			Kind:       item.Kind,
			ID:         item.ID,
			Name:       item.Name,
			Route:      item.Route,
			State:      item.State,
			DevProfile: devProfileFromPort(item.DevProfile),
		})
	}
	return registryTagReferenceListResponse{
		Project:       result.Project,
		Repository:    result.Repository,
		Tag:           result.Tag,
		Image:         result.Image,
		Items:         items,
		Total:         len(items),
		DeleteBlocked: result.DeleteBlocked,
	}
}

func registryPermissionFromRecord(record ports.RegistryPermission) registryPermissionResponse {
	actions := make([]string, 0, len(record.Actions))
	for _, action := range record.Actions {
		actions = append(actions, string(action))
	}
	return registryPermissionResponse{
		Project:    record.Project,
		Repository: record.Repository,
		Subject:    record.Subject,
		Actions:    actions,
		State:      string(record.State),
		DevProfile: devProfileFromPort(record.DevProfile),
		UpdatedAt:  networkTime(record.UpdatedAt),
	}
}

func registryPullSecretFromRecord(record ports.RegistryPullSecret) registryPullSecretResponse {
	return registryPullSecretResponse{
		Project:    record.Project,
		Name:       record.Name,
		SecretRef:  record.SecretRef,
		Registry:   record.Registry,
		Username:   record.Username,
		Namespace:  record.Namespace,
		State:      string(record.State),
		DevProfile: devProfileFromPort(record.DevProfile),
		CreatedAt:  networkTime(record.CreatedAt),
	}
}

func registryScanResultFromRecord(record ports.RegistryScanResult) registryScanResultResponse {
	return registryScanResultResponse{
		Image:      record.Image,
		Status:     string(record.Status),
		Critical:   record.Critical,
		High:       record.High,
		Medium:     record.Medium,
		Low:        record.Low,
		ReportURL:  record.ReportURL,
		ProviderID: record.ProviderID,
		DevProfile: devProfileFromPort(record.DevProfile),
		ScannedAt:  networkTime(record.ScannedAt),
	}
}

func registryProjectScanReportFromRecord(record ports.RegistryProjectScanReport) registryProjectScanReportResponse {
	return registryProjectScanReportResponse{
		Project:          record.Project,
		Status:           string(record.Status),
		Critical:         record.Critical,
		High:             record.High,
		Medium:           record.Medium,
		Low:              record.Low,
		ArtifactsTotal:   record.ArtifactsTotal,
		ScannedArtifacts: record.ScannedArtifacts,
		ProviderID:       record.ProviderID,
		DevProfile:       devProfileFromPort(record.DevProfile),
		ScannedAt:        networkTime(record.ScannedAt),
	}
}

func writeRegistryError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	case errors.Is(err, ports.ErrNotFound):
		writeDemoError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeDemoError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrNotConfigured):
		writeDemoError(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", err.Error())
	default:
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
