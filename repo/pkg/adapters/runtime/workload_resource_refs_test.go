package runtime

import (
	"testing"
)

func TestPrimaryWorkloadResourceRefPrefersDeploymentOverSecret(t *testing.T) {
	ref, err := primaryWorkloadResourceRef([]string{
		"kubernetes/Secret/app-01-identity",
		"kubernetes/Deployment/app-01",
	})
	if err != nil {
		t.Fatalf("primaryWorkloadResourceRef() error = %v", err)
	}
	if ref != "kubernetes/Deployment/app-01" {
		t.Fatalf("ref = %q, want deployment ref", ref)
	}
}

func TestProviderResourceRefsForLifecycleDeleteOrdersWorkloadBeforeSecret(t *testing.T) {
	refs := providerResourceRefsForLifecycleDelete([]string{
		"kubernetes/Secret/app-01-identity",
		"kubernetes/Deployment/app-01",
	})
	if len(refs) != 2 {
		t.Fatalf("refs = %#v, want 2 refs", refs)
	}
	if refs[0] != "kubernetes/Deployment/app-01" || refs[1] != "kubernetes/Secret/app-01-identity" {
		t.Fatalf("refs = %#v, want deployment then secret", refs)
	}
}
