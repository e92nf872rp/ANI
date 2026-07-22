package registry

import (
	"context"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestKubernetesPullSecretWriterUsesOnlyTenantNamespace(t *testing.T) {
	client := &fakeManifestApplier{}
	writer := NewKubernetesPullSecretWriter(client)
	err := writer.ApplyRegistryPullSecret(context.Background(), ports.RegistryPullSecretWriteRequest{TenantID: "tenant_a", Namespace: "ani-tenant-tenant-a", Name: "pull", Registry: "harbor.test", DockerConfigJSON: `{"auths":{}}`})
	if err != nil {
		t.Fatalf("ApplyRegistryPullSecret() error = %v", err)
	}
	if len(client.manifests) != 1 || !strings.Contains(client.manifests[0].Content, `"kubernetes.io/dockerconfigjson"`) || !strings.Contains(client.manifests[0].Content, `"ani-tenant-tenant-a"`) {
		t.Fatalf("manifest = %+v", client.manifests)
	}
}

func TestKubernetesPullSecretWriterRejectsCrossTenantNamespace(t *testing.T) {
	writer := NewKubernetesPullSecretWriter(&fakeManifestApplier{})
	err := writer.ApplyRegistryPullSecret(context.Background(), ports.RegistryPullSecretWriteRequest{TenantID: "tenant-a", Namespace: "ani-tenant-tenant-b", Name: "pull", DockerConfigJSON: `{"auths":{}}`})
	if err == nil {
		t.Fatal("ApplyRegistryPullSecret() error = nil, want namespace rejection")
	}
}

type fakeManifestApplier struct {
	manifests []ports.WorkloadManifest
	err       error
}

func (f *fakeManifestApplier) ApplyManifests(_ context.Context, manifests []ports.WorkloadManifest) ([]string, error) {
	f.manifests = manifests
	return nil, f.err
}
