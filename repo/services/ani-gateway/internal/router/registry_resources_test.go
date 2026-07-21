package router

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestRegistryAPIProjectRepositoryAndArtifactResponses(t *testing.T) {
	api := newRegistryAPI()
	if err := api.service.EnsureProject(context.Background(), "tenant-a"); err != nil {
		t.Fatalf("EnsureProject error = %v", err)
	}

	projects, err := api.service.ListProjects(context.Background(), ports.RegistryProjectListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("ListProjects error = %v", err)
	}
	projectResponse := registryProjectsFromResult(projects)
	if projectResponse.Total != 1 || projectResponse.Items[0].Name != "tenant-a" {
		t.Fatalf("project response = %+v, want tenant-a project", projectResponse)
	}
	requireLocalCoreDevProfile(t, projectResponse.Items[0].DevProfile, "local-image-registry")

	repositories, err := api.service.ListRepositories(context.Background(), ports.RegistryRepositoryListRequest{
		TenantID: "tenant-a",
		Project:  "tenant-a",
	})
	if err != nil {
		t.Fatalf("ListRepositories error = %v", err)
	}
	repositoryResponse := registryRepositoriesFromResult(repositories)
	if repositoryResponse.Total != 1 || repositoryResponse.Items[0].Name != "runtime" {
		t.Fatalf("repository response = %+v, want runtime repository", repositoryResponse)
	}

	artifacts, err := api.service.ListArtifacts(context.Background(), ports.RegistryArtifactListRequest{
		TenantID:   "tenant-a",
		Project:    "tenant-a",
		Repository: "runtime",
	})
	if err != nil {
		t.Fatalf("ListArtifacts error = %v", err)
	}
	artifactResponse := registryArtifactsFromResult(artifacts)
	if artifactResponse.Total != 1 || artifactResponse.Items[0].Tags[0] != "latest" {
		t.Fatalf("artifact response = %+v, want latest artifact", artifactResponse)
	}
}

func TestRegistryAPIPermissionAndScanResponses(t *testing.T) {
	api := newRegistryAPI()

	permission, err := api.service.SetRepositoryPermission(context.Background(), ports.RegistryPermissionRequest{
		TenantID:       "tenant-a",
		Project:        "tenant-a",
		Repository:     "runtime",
		IdempotencyKey: "registry-router-permission",
		Subject:        "svc-model",
		Actions:        []ports.RegistryPermissionAction{ports.RegistryPermissionPull},
	})
	if err != nil {
		t.Fatalf("SetRepositoryPermission error = %v", err)
	}
	permissionResponse := registryPermissionFromRecord(permission)
	if permissionResponse.Subject != "svc-model" || permissionResponse.State != "active" {
		t.Fatalf("permission response = %+v, want active svc-model permission", permissionResponse)
	}
	requireLocalCoreDevProfile(t, permissionResponse.DevProfile, "local-image-registry")

	scan, err := api.service.GetScanResult(context.Background(), ports.RegistryScanResultRequest{
		TenantID: "tenant-a",
		Image:    "tenant-a/runtime:latest",
	})
	if err != nil {
		t.Fatalf("GetScanResult error = %v", err)
	}
	scanResponse := registryScanResultFromRecord(scan)
	if scanResponse.Status != "complete" || scanResponse.ProviderID != "local-trivy" {
		t.Fatalf("scan response = %+v, want complete local-trivy scan", scanResponse)
	}
	requireLocalCoreDevProfile(t, scanResponse.DevProfile, "local-image-registry")
}

func TestRegistryAPIProjectPullSecretAndScanReportResponses(t *testing.T) {
	api := newRegistryAPI()

	project, err := api.service.CreateProject(context.Background(), ports.RegistryProjectRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "registry-router-project",
		Name:           "tenant-a",
	})
	if err != nil {
		t.Fatalf("CreateProject error = %v", err)
	}
	projectResponse := registryProjectFromRecord(project)
	if projectResponse.Name != "tenant-a" {
		t.Fatalf("project response = %+v, want tenant-a", projectResponse)
	}
	requireLocalCoreDevProfile(t, projectResponse.DevProfile, "local-image-registry")

	secret, err := api.service.CreatePullSecret(context.Background(), ports.RegistryPullSecretRequest{
		TenantID:       "tenant-a",
		Project:        "tenant-a",
		IdempotencyKey: "registry-router-pull-secret",
		Name:           "ani-registry-pull",
	})
	if err != nil {
		t.Fatalf("CreatePullSecret error = %v", err)
	}
	secretResponse := registryPullSecretFromRecord(secret)
	if secretResponse.SecretRef == "" || secretResponse.State != "active" {
		t.Fatalf("secret response = %+v, want active secret reference", secretResponse)
	}
	requireLocalCoreDevProfile(t, secretResponse.DevProfile, "local-image-registry")

	report, err := api.service.GetProjectScanReport(context.Background(), ports.RegistryProjectScanReportRequest{
		TenantID: "tenant-a",
		Project:  "tenant-a",
	})
	if err != nil {
		t.Fatalf("GetProjectScanReport error = %v", err)
	}
	reportResponse := registryProjectScanReportFromRecord(report)
	if reportResponse.Status != "complete" || reportResponse.ArtifactsTotal != 1 {
		t.Fatalf("report response = %+v, want complete one-artifact report", reportResponse)
	}
	requireLocalCoreDevProfile(t, reportResponse.DevProfile, "local-image-registry")
}

func TestRegistryAPIOverviewImagesPushAndTagRiskResponses(t *testing.T) {
	api := newRegistryAPI()

	overview, err := api.service.GetOverview(context.Background(), ports.RegistryOverviewRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("GetOverview error = %v", err)
	}
	overviewResponse := registryOverviewFromRecord(overview)
	if len(overviewResponse.Resources) != 4 || overviewResponse.CreateOrder[0] != "project" {
		t.Fatalf("overview response = %+v, want resource summaries and create order", overviewResponse)
	}

	images, err := api.service.ListImages(context.Background(), ports.RegistryImageListRequest{TenantID: "tenant-a", Project: "tenant-a"})
	if err != nil {
		t.Fatalf("ListImages error = %v", err)
	}
	imageResponse := registryImagesFromResult(images)
	if imageResponse.Total != 1 || imageResponse.Items[0].Image != "registry.local/tenant-a/runtime:latest" {
		t.Fatalf("image response = %+v, want local runtime image", imageResponse)
	}
	requireLocalCoreDevProfile(t, imageResponse.Items[0].DevProfile, "local-image-registry")

	instructions, err := api.service.GetPushInstructions(context.Background(), ports.RegistryPushInstructionsRequest{
		TenantID:   "tenant-a",
		Project:    "tenant-a",
		Repository: "runtime",
	})
	if err != nil {
		t.Fatalf("GetPushInstructions error = %v", err)
	}
	instructionResponse := registryPushInstructionsFromRecord(instructions)
	if instructionResponse.Registry != "registry.local" || len(instructionResponse.Commands) != 3 {
		t.Fatalf("instruction response = %+v, want registry.local commands", instructionResponse)
	}

	references, err := api.service.ListTagReferences(context.Background(), ports.RegistryTagReferenceListRequest{
		TenantID:   "tenant-a",
		Project:    "tenant-a",
		Repository: "runtime",
		Tag:        "latest",
	})
	if err != nil {
		t.Fatalf("ListTagReferences error = %v", err)
	}
	referenceResponse := registryTagReferencesFromResult(references)
	if !referenceResponse.DeleteBlocked || referenceResponse.Total != 1 {
		t.Fatalf("reference response = %+v, want blocking reference", referenceResponse)
	}
	requireLocalCoreDevProfile(t, referenceResponse.Items[0].DevProfile, "local-image-registry")
}

func TestRegistryHTTPRoutesForConsoleImageContract(t *testing.T) {
	h := server.New()
	Register(h)

	tests := []struct {
		method string
		path   string
		status int
		assert func(t *testing.T, body []byte)
	}{
		{
			method: http.MethodGet,
			path:   "/api/v1/registry/overview",
			status: http.StatusOK,
			assert: func(t *testing.T, body []byte) {
				var response registryOverviewResponse
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("overview response is not JSON: %v; body = %s", err, body)
				}
				if len(response.Resources) != 4 || len(response.CreateOrder) != 4 || len(response.DeleteRisks) == 0 {
					t.Fatalf("overview response = %+v, want summaries, create order, and delete risks", response)
				}
			},
		},
		{
			method: http.MethodGet,
			path:   "/api/v1/registry/images?project=demo-tenant",
			status: http.StatusOK,
			assert: func(t *testing.T, body []byte) {
				var response registryImageListResponse
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("images response is not JSON: %v; body = %s", err, body)
				}
				if response.Total != 1 || response.Items[0].Image != "registry.local/demo-tenant/runtime:latest" {
					t.Fatalf("images response = %+v, want local runtime latest image", response)
				}
			},
		},
		{
			method: http.MethodGet,
			path:   "/api/v1/registry/projects/demo-tenant/push-instructions?repository=runtime",
			status: http.StatusOK,
			assert: func(t *testing.T, body []byte) {
				var response registryPushInstructionsResponse
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("push instructions response is not JSON: %v; body = %s", err, body)
				}
				if response.RepositoryExample != "registry.local/demo-tenant/runtime:latest" || len(response.Commands) != 3 {
					t.Fatalf("push instructions response = %+v, want login/tag/push commands", response)
				}
			},
		},
		{
			method: http.MethodGet,
			path:   "/api/v1/registry/projects/demo-tenant/repositories/runtime/tags/latest/references",
			status: http.StatusOK,
			assert: func(t *testing.T, body []byte) {
				var response registryTagReferenceListResponse
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("tag references response is not JSON: %v; body = %s", err, body)
				}
				if !response.DeleteBlocked || response.Total != 1 {
					t.Fatalf("tag references response = %+v, want one blocking reference", response)
				}
			},
		},
		{
			method: http.MethodDelete,
			path:   "/api/v1/registry/projects/demo-tenant/repositories/runtime/tags/latest",
			status: http.StatusConflict,
			assert: func(t *testing.T, body []byte) {
				if len(body) == 0 {
					t.Fatal("delete conflict response body is empty")
				}
			},
		},
	}

	for _, tt := range tests {
		resp := ut.PerformRequest(h.Engine, tt.method, tt.path, nil).Result()
		if resp.StatusCode() != tt.status {
			t.Fatalf("%s %s status = %d, want %d; body = %s", tt.method, tt.path, resp.StatusCode(), tt.status, resp.Body())
		}
		tt.assert(t, resp.Body())
	}
}
