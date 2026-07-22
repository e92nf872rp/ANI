package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestHarborImageRegistryCreatesTenantProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2.0/projects" {
			t.Fatalf("request = %s %s, want POST /api/v2.0/projects", r.Method, r.URL.Path)
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "admin" || password != "secret" {
			t.Fatalf("BasicAuth() = %q/%q/%t, want admin/secret/true", username, password, ok)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	service, err := NewHarborImageRegistry(HarborImageRegistryConfig{
		Endpoint: server.URL,
		Username: "admin",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("NewHarborImageRegistry() error = %v", err)
	}

	project, err := service.CreateProject(context.Background(), ports.RegistryProjectRequest{
		TenantID:       "tenant-a",
		Name:           "tenant-a",
		IdempotencyKey: "create-tenant-a",
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if project.Name != "tenant-a" || !project.DevProfile.RealProvider {
		t.Fatalf("project = %+v, want real Harbor project", project)
	}
}

func TestHarborImageRegistryListsArtifactsWithTrivyOverview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2.0/projects/tenant-a/repositories/runtime/artifacts":
			_, _ = fmt.Fprint(w, `[{"digest":"sha256:artifact","size":1048576,"manifest_media_type":"application/vnd.oci.image.manifest.v1+json","tags":[{"name":"latest","push_time":"2026-07-21T00:00:00Z"}],"scan_overview":{"report":{"scan_status":"Success","summary":{"Critical":1,"High":2,"Medium":3,"Low":4}}}}]`)
		default:
			t.Fatalf("request path = %s", r.URL.Path)
		}
	}))
	defer server.Close()

	service, err := NewHarborImageRegistry(HarborImageRegistryConfig{Endpoint: server.URL, Username: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ListArtifacts(context.Background(), ports.RegistryArtifactListRequest{TenantID: "tenant-a", Project: "tenant-a", Repository: "runtime"})
	if err != nil {
		t.Fatalf("ListArtifacts() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Digest != "sha256:artifact" || result.Items[0].ScanStatus.Critical != 1 || !result.Items[0].DevProfile.RealProvider {
		t.Fatalf("artifacts = %+v, want mapped real Harbor artifact", result.Items)
	}
}

func TestHarborImageRegistryListsProjectsAndRepositories(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2.0/projects":
			_, _ = fmt.Fprint(w, `[{"project_id":7,"name":"tenant-a","public":false,"creation_time":"2026-07-21T00:00:00Z"}]`)
		case "/api/v2.0/projects/tenant-a/repositories":
			_, _ = fmt.Fprint(w, `[{"name":"tenant-a/runtime","artifact_count":2,"pull_count":3}]`)
		default:
			t.Fatalf("request path = %s", r.URL.Path)
		}
	}))
	defer server.Close()

	service, err := NewHarborImageRegistry(HarborImageRegistryConfig{Endpoint: server.URL, Username: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	projects, err := service.ListProjects(context.Background(), ports.RegistryProjectListRequest{TenantID: "tenant-a"})
	if err != nil || len(projects.Items) != 1 || projects.Items[0].ID != "7" {
		t.Fatalf("ListProjects() = %+v, %v", projects, err)
	}
	repositories, err := service.ListRepositories(context.Background(), ports.RegistryRepositoryListRequest{TenantID: "tenant-a", Project: "tenant-a"})
	if err != nil || len(repositories.Items) != 1 || repositories.Items[0].Name != "runtime" {
		t.Fatalf("ListRepositories() = %+v, %v", repositories, err)
	}
}

func TestHarborImageRegistryListsOnlyTheTenantProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2.0/projects" {
			t.Fatalf("request path = %s", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `[{"project_id":7,"name":"tenant-a","public":false},{"project_id":8,"name":"tenant-b","public":false}]`)
	}))
	defer server.Close()

	service, err := NewHarborImageRegistry(HarborImageRegistryConfig{Endpoint: server.URL, Username: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	projects, err := service.ListProjects(context.Background(), ports.RegistryProjectListRequest{TenantID: "tenant-a"})
	if err != nil || len(projects.Items) != 1 || projects.Items[0].Name != "tenant-a" {
		t.Fatalf("ListProjects() = %+v, %v", projects, err)
	}
}

func TestHarborImageRegistryMapsRepositoryPermissionToHarborProjectMember(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v2.0/projects/tenant-a/members":
			_, _ = fmt.Fprint(w, `[]`)
		case "POST /api/v2.0/projects/tenant-a/members":
			var payload struct {
				RoleID     int `json:"role_id"`
				MemberUser struct {
					Username string `json:"username"`
				} `json:"member_user"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.RoleID != harborProjectRoleDeveloper || payload.MemberUser.Username != "developer" {
				t.Fatalf("payload = %+v", payload)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	service, err := NewHarborImageRegistry(HarborImageRegistryConfig{Endpoint: server.URL, Username: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	permission, err := service.SetRepositoryPermission(context.Background(), ports.RegistryPermissionRequest{
		TenantID: "tenant-a", Project: "tenant-a", Repository: "runtime", IdempotencyKey: "permission-a", Subject: "developer",
		Actions: []ports.RegistryPermissionAction{ports.RegistryPermissionPull, ports.RegistryPermissionPush},
	})
	if err != nil || permission.State != ports.RegistryPermissionActive || permission.Repository != "runtime" {
		t.Fatalf("SetRepositoryPermission() = %+v, %v", permission, err)
	}
}

func TestHarborImageRegistryUpdatesExistingProjectMemberPermission(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v2.0/projects/tenant-a/members":
			_, _ = fmt.Fprint(w, `[{"id":19,"role_id":4,"member_user":{"username":"developer"}}]`)
		case "PUT /api/v2.0/projects/tenant-a/members/19":
			var payload struct {
				RoleID int `json:"role_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.RoleID != harborProjectRoleMaintainer {
				t.Fatalf("role_id = %d, want %d", payload.RoleID, harborProjectRoleMaintainer)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	service, err := NewHarborImageRegistry(HarborImageRegistryConfig{Endpoint: server.URL, Username: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	permission, err := service.SetRepositoryPermission(context.Background(), ports.RegistryPermissionRequest{
		TenantID: "tenant-a", Project: "tenant-a", Repository: "runtime", IdempotencyKey: "permission-update-a", Subject: "developer",
		Actions: []ports.RegistryPermissionAction{ports.RegistryPermissionDelete},
	})
	if err != nil || permission.State != ports.RegistryPermissionActive || permission.Repository != "runtime" {
		t.Fatalf("SetRepositoryPermission() = %+v, %v", permission, err)
	}
}

func TestHarborImageRegistryGetsImageScanFromArtifact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2.0/projects/tenant-a/repositories/runtime/artifacts" {
			t.Fatalf("request path = %s", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `[{"digest":"sha256:artifact","tags":[{"name":"latest"}],"scan_overview":{"report":{"scan_status":"Success","summary":{"High":2}}}}]`)
	}))
	defer server.Close()

	service, err := NewHarborImageRegistry(HarborImageRegistryConfig{Endpoint: server.URL, Username: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	scan, err := service.GetScanResult(context.Background(), ports.RegistryScanResultRequest{TenantID: "tenant-a", Image: "tenant-a/runtime:latest"})
	if err != nil || scan.Status != ports.RegistryScanComplete || scan.High != 2 {
		t.Fatalf("GetScanResult() = %+v, %v", scan, err)
	}
}

func TestHarborImageRegistryGetsImageScanFromFullyQualifiedImageReference(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2.0/projects/tenant-a/repositories/runtime/artifacts" {
			t.Fatalf("request path = %s", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `[{"digest":"sha256:artifact","tags":[{"name":"latest"}],"scan_overview":{"report":{"scan_status":"Success","summary":{"High":2}}}}]`)
	}))
	defer server.Close()

	service, err := NewHarborImageRegistry(HarborImageRegistryConfig{Endpoint: server.URL, Username: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	scan, err := service.GetScanResult(context.Background(), ports.RegistryScanResultRequest{TenantID: "tenant-a", Image: "harbor.example:5000/tenant-a/runtime:latest"})
	if err != nil || scan.Status != ports.RegistryScanComplete || scan.High != 2 {
		t.Fatalf("GetScanResult() = %+v, %v", scan, err)
	}
}

func TestHarborImageRegistryBuildsOverviewAndPushInstructions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2.0/projects":
			_, _ = fmt.Fprint(w, `[{"project_id":7,"name":"tenant-a","public":false}]`)
		case "/api/v2.0/projects/tenant-a/repositories":
			_, _ = fmt.Fprint(w, `[{"name":"tenant-a/runtime","artifact_count":1}]`)
		case "/api/v2.0/projects/tenant-a/repositories/runtime/artifacts":
			_, _ = fmt.Fprint(w, `[{"digest":"sha256:artifact","size":12,"tags":[{"name":"latest"}],"scan_overview":{"report":{"scan_status":"Success","summary":{"Critical":1,"High":2}}}}]`)
		default:
			t.Fatalf("request path = %s", r.URL.Path)
		}
	}))
	defer server.Close()
	service, err := NewHarborImageRegistry(HarborImageRegistryConfig{Endpoint: server.URL, Username: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	overview, err := service.GetOverview(context.Background(), ports.RegistryOverviewRequest{TenantID: "tenant-a"})
	if err != nil || overview.Resources[2].SizeBytes != 12 || overview.Vulnerabilities.Critical != 1 {
		t.Fatalf("GetOverview() = %+v, %v", overview, err)
	}
	instructions, err := service.GetPushInstructions(context.Background(), ports.RegistryPushInstructionsRequest{TenantID: "tenant-a", Project: "tenant-a", Repository: "runtime"})
	if err != nil || len(instructions.Commands) != 3 || instructions.Registry != strings.TrimPrefix(server.URL, "http://") {
		t.Fatalf("GetPushInstructions() = %+v, %v", instructions, err)
	}
}

func TestHarborImageRegistryDeletesOnlyAfterReferenceCheck(t *testing.T) {
	references := fakeRegistryImageReferenceReader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v2.0/projects/tenant-a/repositories/runtime/artifacts":
			_, _ = fmt.Fprint(w, `[{"digest":"sha256:artifact","tags":[{"name":"latest"}]}]`)
		case "DELETE /api/v2.0/projects/tenant-a/repositories/runtime/artifacts/latest":
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	service, err := NewHarborImageRegistry(HarborImageRegistryConfig{Endpoint: server.URL, Username: "admin", Password: "secret", ReferenceReader: references})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := service.DeleteTag(context.Background(), ports.RegistryTagDeleteRequest{TenantID: "tenant-a", Project: "tenant-a", Repository: "runtime", Tag: "latest"})
	if err != nil || deleted.Digest != "sha256:artifact" {
		t.Fatalf("DeleteTag() = %+v, %v", deleted, err)
	}
}

func TestHarborImageRegistryDoesNotDeleteWithoutReferenceReader(t *testing.T) {
	service, err := NewHarborImageRegistry(HarborImageRegistryConfig{Endpoint: "http://harbor.test", Username: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.DeleteTag(context.Background(), ports.RegistryTagDeleteRequest{TenantID: "tenant-a", Project: "tenant-a", Repository: "runtime", Tag: "latest"})
	if !errors.Is(err, ports.ErrNotConfigured) {
		t.Fatalf("DeleteTag() error = %v, want ErrNotConfigured", err)
	}
}

type fakeRegistryImageReferenceReader struct{}

func (fakeRegistryImageReferenceReader) ListRegistryImageReferences(context.Context, ports.RegistryImageReferenceListRequest) (ports.RegistryImageReferenceListResult, error) {
	return ports.RegistryImageReferenceListResult{}, nil
}
