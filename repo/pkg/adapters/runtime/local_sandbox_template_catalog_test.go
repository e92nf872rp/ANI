package runtime

import (
	"context"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalSandboxTemplateCatalogListsBuiltinTemplates(t *testing.T) {
	catalog := NewLocalSandboxTemplateCatalog()
	result, err := catalog.ListSandboxTemplates(context.Background(), ports.SandboxTemplateListRequest{
		TenantID: "tenant-a",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListSandboxTemplates error = %v", err)
	}
	if len(result.Items) < 2 || result.Total != len(result.Items) {
		t.Fatalf("templates = %+v, want builtin templates with total", result)
	}
	if !result.Items[0].IsBuiltin || result.Items[0].Image == "" || result.Items[0].CreatedAt.IsZero() {
		t.Fatalf("first template = %+v, want builtin image and timestamp", result.Items[0])
	}
	if result.DevProfile.Mode != "local" || result.DevProfile.Provider != "local-sandbox-template-catalog" || result.DevProfile.RealProvider {
		t.Fatalf("dev profile = %+v, want local catalog marker", result.DevProfile)
	}
}
