package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestKubernetesSecretProviderAdapterRendersAndAppliesSecretManifest(t *testing.T) {
	client := &fakeSecretProviderClient{}
	adapter := NewKubernetesSecretProviderAdapter(client)

	result, err := adapter.ApplySecret(context.Background(), ports.SecretProviderApplyRequest{
		TenantID: "tenant-a",
		SecretID: "sec-abc",
		Name:     "db-password",
		Type:     "opaque",
		Data:     map[string]string{"password": "secret-value"},
	})
	if err != nil {
		t.Fatalf("ApplySecret() error = %v", err)
	}
	if !result.Applied || result.Provider != "kubernetes" {
		t.Fatalf("result = %+v, want applied kubernetes", result)
	}
	if len(client.manifests) != 1 {
		t.Fatalf("manifest count = %d, want 1", len(client.manifests))
	}
	manifest := client.manifests[0]
	if manifest.Provider != "kubernetes" || manifest.Kind != "Secret" || manifest.Name != "sec-abc" {
		t.Fatalf("manifest metadata = %+v, want Kubernetes Secret sec-abc", manifest)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(manifest.Content), &doc); err != nil {
		t.Fatalf("manifest content is not JSON: %v", err)
	}
	metadata := doc["metadata"].(map[string]any)
	if metadata["namespace"] != "ani-tenant-tenant-a" {
		t.Fatalf("namespace = %v, want tenant namespace", metadata["namespace"])
	}
	if !strings.Contains(manifest.Content, `"stringData"`) || strings.Contains(manifest.Content, `"data"`) {
		t.Fatalf("manifest content = %s, want stringData and no base64 data", manifest.Content)
	}
}

type fakeSecretProviderClient struct {
	manifests []ports.WorkloadManifest
}

func (c *fakeSecretProviderClient) ApplyManifests(_ context.Context, manifests []ports.WorkloadManifest) ([]string, error) {
	c.manifests = append([]ports.WorkloadManifest(nil), manifests...)
	return []string{"kubernetes/Secret/" + manifests[0].Name}, nil
}
