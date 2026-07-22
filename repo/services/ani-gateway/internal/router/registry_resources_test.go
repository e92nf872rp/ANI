package router

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	registryadapter "github.com/kubercloud/ani/pkg/adapters/registry"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

func TestRegistryProjectCreateReplaysIdempotencyKeyBeforeProvider(t *testing.T) {
	store := &registryIdempotencyStore{values: map[string][]byte{}}
	provider := &countingRegistry{ImageRegistry: registryadapter.NewLocalImageRegistry()}
	h := server.New()
	h.Use(
		middleware.RequestID(),
		func(ctx context.Context, c *app.RequestContext) { c.Set("tenant_id", "demo-tenant"); c.Next(ctx) },
		middleware.Idempotency(store),
	)
	registerHarbor(h.Group("/api/v1"), provider)

	body := `{"idempotency_key":"registry-project-a","name":"demo-tenant","public":false}`
	first := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/registry/projects", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "application/json"}).Result()
	second := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/registry/projects", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "application/json"}).Result()
	if first.StatusCode() != http.StatusCreated || second.StatusCode() != http.StatusCreated {
		t.Fatalf("statuses = %d, %d, want 201", first.StatusCode(), second.StatusCode())
	}
	if string(first.Body()) != string(second.Body()) {
		t.Fatalf("second response must replay first response: first=%q second=%q", first.Body(), second.Body())
	}
	if provider.createProjectCalls != 1 {
		t.Fatalf("CreateProject calls = %d, want 1", provider.createProjectCalls)
	}
}

type countingRegistry struct {
	ports.ImageRegistry
	createProjectCalls int
}

func (r *countingRegistry) CreateProject(ctx context.Context, request ports.RegistryProjectRequest) (ports.RegistryProject, error) {
	r.createProjectCalls++
	return r.ImageRegistry.CreateProject(ctx, request)
}

type registryIdempotencyStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (s *registryIdempotencyStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[key]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}
func (s *registryIdempotencyStore) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = append([]byte(nil), value...)
	return nil
}
func (s *registryIdempotencyStore) SetNX(_ context.Context, key string, value []byte, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.values[key]; ok {
		return false, nil
	}
	s.values[key] = append([]byte(nil), value...)
	return true, nil
}
func (s *registryIdempotencyStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	return nil
}
func (s *registryIdempotencyStore) Increment(context.Context, string, time.Duration) (int64, error) {
	return 0, nil
}
func (s *registryIdempotencyStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.values[key]
	return ok, nil
}

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
