package registry

import (
	"context"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalImageRegistryListsProjectRepositoryAndArtifacts(t *testing.T) {
	service := NewLocalImageRegistry(WithRegistryClock(func() time.Time {
		return time.Unix(2400, 0).UTC()
	}))

	if err := service.EnsureProject(context.Background(), "tenant-a"); err != nil {
		t.Fatalf("EnsureProject() error = %v", err)
	}
	projects, err := service.ListProjects(context.Background(), ports.RegistryProjectListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projects.Items) != 1 || projects.Items[0].Name != "tenant-a" {
		t.Fatalf("projects = %+v, want tenant-a project", projects.Items)
	}
	if projects.DevProfile.Provider != "local-image-registry" || projects.DevProfile.RealProvider {
		t.Fatalf("dev profile = %+v, want local non-real marker", projects.DevProfile)
	}

	repositories, err := service.ListRepositories(context.Background(), ports.RegistryRepositoryListRequest{
		TenantID: "tenant-a",
		Project:  "tenant-a",
	})
	if err != nil {
		t.Fatalf("ListRepositories() error = %v", err)
	}
	if len(repositories.Items) != 1 || repositories.Items[0].Name != "runtime" {
		t.Fatalf("repositories = %+v, want seeded runtime repository", repositories.Items)
	}

	artifacts, err := service.ListArtifacts(context.Background(), ports.RegistryArtifactListRequest{
		TenantID:   "tenant-a",
		Project:    "tenant-a",
		Repository: "runtime",
	})
	if err != nil {
		t.Fatalf("ListArtifacts() error = %v", err)
	}
	if len(artifacts.Items) != 1 || artifacts.Items[0].Tags[0] != "latest" {
		t.Fatalf("artifacts = %+v, want latest artifact", artifacts.Items)
	}
}

func TestLocalImageRegistryPermissionAndScanAreLocalProfile(t *testing.T) {
	service := NewLocalImageRegistry(WithRegistryClock(func() time.Time {
		return time.Unix(2500, 0).UTC()
	}))

	first, err := service.SetRepositoryPermission(context.Background(), ports.RegistryPermissionRequest{
		TenantID:       "tenant-a",
		Project:        "tenant-a",
		Repository:     "runtime",
		IdempotencyKey: "registry-permission-a",
		Subject:        "svc-model",
		Actions:        []ports.RegistryPermissionAction{ports.RegistryPermissionPull, ports.RegistryPermissionPush},
	})
	if err != nil {
		t.Fatalf("SetRepositoryPermission(first) error = %v", err)
	}
	second, err := service.SetRepositoryPermission(context.Background(), ports.RegistryPermissionRequest{
		TenantID:       "tenant-a",
		Project:        "tenant-a",
		Repository:     "runtime",
		IdempotencyKey: "registry-permission-a",
		Subject:        "svc-model",
		Actions:        []ports.RegistryPermissionAction{ports.RegistryPermissionPull, ports.RegistryPermissionPush},
	})
	if err != nil {
		t.Fatalf("SetRepositoryPermission(second) error = %v", err)
	}
	if first.State != ports.RegistryPermissionActive || second.State != ports.RegistryPermissionDuplicate {
		t.Fatalf("states = %q/%q, want active/duplicate", first.State, second.State)
	}

	scan, err := service.GetScanResult(context.Background(), ports.RegistryScanResultRequest{
		TenantID: "tenant-a",
		Image:    "tenant-a/runtime:latest",
	})
	if err != nil {
		t.Fatalf("GetScanResult() error = %v", err)
	}
	if scan.Status != ports.RegistryScanComplete || scan.ProviderID != "local-trivy" {
		t.Fatalf("scan = %+v, want complete local-trivy result", scan)
	}
	if scan.DevProfile.Provider != "local-image-registry" || scan.DevProfile.RealProvider {
		t.Fatalf("dev profile = %+v, want local non-real marker", scan.DevProfile)
	}
}

func TestLocalImageRegistryProjectPullSecretAndScanReport(t *testing.T) {
	service := NewLocalImageRegistry(WithRegistryClock(func() time.Time {
		return time.Unix(2600, 0).UTC()
	}))

	project, err := service.CreateProject(context.Background(), ports.RegistryProjectRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "registry-project-a",
		Name:           "tenant-a",
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if project.Name != "tenant-a" || project.DevProfile.RealProvider {
		t.Fatalf("project = %+v, want tenant-a local project", project)
	}

	secret, err := service.CreatePullSecret(context.Background(), ports.RegistryPullSecretRequest{
		TenantID:       "tenant-a",
		Project:        "tenant-a",
		IdempotencyKey: "registry-pull-secret-a",
		Name:           "ani-registry-pull",
		Namespace:      "ani-tenant-a",
	})
	if err != nil {
		t.Fatalf("CreatePullSecret() error = %v", err)
	}
	if secret.SecretRef == "" || secret.State != ports.RegistryPermissionActive {
		t.Fatalf("secret = %+v, want active local pull secret reference", secret)
	}

	report, err := service.GetProjectScanReport(context.Background(), ports.RegistryProjectScanReportRequest{
		TenantID: "tenant-a",
		Project:  "tenant-a",
	})
	if err != nil {
		t.Fatalf("GetProjectScanReport() error = %v", err)
	}
	if report.Status != ports.RegistryScanComplete || report.ArtifactsTotal != 1 || report.ScannedArtifacts != 1 {
		t.Fatalf("report = %+v, want complete one-artifact scan report", report)
	}
}

func TestLocalImageRegistryImplementsConsoleRegistryOperations(t *testing.T) {
	service := NewLocalImageRegistry(WithRegistryClock(func() time.Time {
		return time.Unix(2700, 0).UTC()
	}))

	overview, err := service.GetOverview(context.Background(), ports.RegistryOverviewRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("GetOverview() error = %v", err)
	}
	if len(overview.Resources) == 0 {
		t.Fatalf("overview resources = %v, want local registry summaries", overview.Resources)
	}

	images, err := service.ListImages(context.Background(), ports.RegistryImageListRequest{
		TenantID: "tenant-a",
		Project:  "tenant-a",
	})
	if err != nil {
		t.Fatalf("ListImages() error = %v", err)
	}
	if len(images.Items) < 4 || images.Items[0].Image == "" {
		t.Fatalf("images = %+v, want local purpose image catalog", images)
	}
}

func TestLocalImageRegistryListImagesFiltersByPurpose(t *testing.T) {
	service := NewLocalImageRegistry(WithRegistryClock(func() time.Time {
		return time.Unix(2800, 0).UTC()
	}))

	images, err := service.ListImages(context.Background(), ports.RegistryImageListRequest{
		TenantID: "tenant-a",
		Project:  "tenant-a",
		Purpose:  "gpu",
	})
	if err != nil {
		t.Fatalf("ListImages() error = %v", err)
	}
	if len(images.Items) != 1 {
		t.Fatalf("images = %+v, want one GPU image", images.Items)
	}
	if images.Items[0].Purpose != "gpu" {
		t.Fatalf("purpose = %q, want gpu", images.Items[0].Purpose)
	}
	if images.Items[0].Repository != "gpu-runtime" {
		t.Fatalf("repository = %q, want gpu-runtime", images.Items[0].Repository)
	}
}
