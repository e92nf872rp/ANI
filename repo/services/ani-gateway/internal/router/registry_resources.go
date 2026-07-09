package router

import (
	"context"
	"errors"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	registryadapter "github.com/kubercloud/ani/pkg/adapters/registry"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

type registryAPI struct {
	service                   ports.ImageRegistry
	pullSecretKubernetesApply ports.RegistryPullSecretKubernetesApply
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

type registryPullSecretKubernetesApplyResponse struct {
	registryPullSecretResponse
	KubernetesSecretName string   `json:"kubernetes_secret_name"`
	KubernetesNamespace  string   `json:"kubernetes_namespace"`
	KubernetesApplied    bool     `json:"kubernetes_applied"`
	ProviderRefs         []string `json:"provider_refs,omitempty"`
	AppliedAt            string   `json:"applied_at"`
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

func newRegistryAPI() *registryAPI {
	return newRegistryAPIWithService(nil, nil)
}

func newRegistryAPIWithService(service ports.ImageRegistry, pullSecretKubernetesApply ports.RegistryPullSecretKubernetesApply) *registryAPI {
	if service == nil {
		service = registryadapter.NewLocalImageRegistry()
	}
	return &registryAPI{service: service, pullSecretKubernetesApply: pullSecretKubernetesApply}
}

func registerHarbor(v1 *route.RouterGroup) {
	registerHarborWithService(v1, nil, nil)
}

func registerHarborWithService(v1 *route.RouterGroup, service ports.ImageRegistry, pullSecretKubernetesApply ports.RegistryPullSecretKubernetesApply) {
	api := newRegistryAPIWithService(service, pullSecretKubernetesApply)
	v1.GET("/registry/projects", api.listProjects)
	v1.POST("/registry/projects", api.createProject)
	v1.GET("/registry/projects/:project/repositories", api.listRepositories)
	v1.GET("/registry/projects/:project/repositories/:repository/artifacts", api.listArtifacts)
	v1.POST("/registry/projects/:project/repositories/:repository/permissions", api.setPermission)
	v1.POST("/registry/projects/:project/pull-secret", api.createPullSecret)
	v1.POST("/registry/projects/:project/pull-secret/kubernetes-apply", api.applyPullSecretToKubernetes)
	v1.GET("/registry/projects/:project/scan-report", api.getProjectScanReport)
	v1.GET("/registry/images/scan-result", api.getScanResult)
}

func (api *registryAPI) listProjects(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.ListProjects(ctx, ports.RegistryProjectListRequest{
		TenantID: middleware.GetTenantID(c),
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
		TenantID:       middleware.GetTenantID(c),
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

func (api *registryAPI) listRepositories(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.ListRepositories(ctx, ports.RegistryRepositoryListRequest{
		TenantID: middleware.GetTenantID(c),
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
		TenantID:   middleware.GetTenantID(c),
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
		TenantID:       middleware.GetTenantID(c),
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
		TenantID:       middleware.GetTenantID(c),
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

func (api *registryAPI) applyPullSecretToKubernetes(ctx context.Context, c *app.RequestContext) {
	if api.pullSecretKubernetesApply == nil {
		writeRegistryError(c, ports.ErrNotConfigured)
		return
	}
	var req createRegistryPullSecretRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid registry pull secret kubernetes apply request")
		return
	}
	result, err := api.pullSecretKubernetesApply.ApplyPullSecretToKubernetes(ctx, ports.RegistryPullSecretKubernetesApplyRequest{
		TenantID:       middleware.GetTenantID(c),
		Project:        c.Param("project"),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		Namespace:      req.Namespace,
	})
	if err != nil {
		writeRegistryError(c, err)
		return
	}
	c.JSON(http.StatusCreated, registryPullSecretKubernetesApplyFromResult(result))
}

func (api *registryAPI) getProjectScanReport(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.GetProjectScanReport(ctx, ports.RegistryProjectScanReportRequest{
		TenantID: middleware.GetTenantID(c),
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
		TenantID: middleware.GetTenantID(c),
		Image:    c.Query("image"),
	})
	if err != nil {
		writeRegistryError(c, err)
		return
	}
	c.JSON(http.StatusOK, registryScanResultFromRecord(result))
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

func registryPullSecretKubernetesApplyFromResult(result ports.RegistryPullSecretKubernetesApplyResult) registryPullSecretKubernetesApplyResponse {
	return registryPullSecretKubernetesApplyResponse{
		registryPullSecretResponse: registryPullSecretFromRecord(result.RegistryPullSecret),
		KubernetesSecretName:       result.KubernetesSecretName,
		KubernetesNamespace:        result.KubernetesNamespace,
		KubernetesApplied:          result.KubernetesApplied,
		ProviderRefs:               append([]string(nil), result.ProviderRefs...),
		AppliedAt:                  networkTime(result.AppliedAt),
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
	case errors.Is(err, ports.ErrConflict):
		writeDemoError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrNotConfigured):
		writeDemoError(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", err.Error())
	default:
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
