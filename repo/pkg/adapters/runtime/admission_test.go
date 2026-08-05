package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalAdmissionGuardAcceptsRenderedManifest(t *testing.T) {
	renderer := NewKubernetesDryRunRenderer(NewPlanningRuntime())
	manifests, err := renderer.Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "app-01",
		Kind:     ports.WorkloadKindContainer,
		Image:    "harbor/app:1",
		Container: &ports.ContainerInstanceSpec{
			PortSpecs: []ports.InstancePortSpec{{Name: "http", ContainerPort: 8080, Protocol: "TCP"}},
		},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	result, err := NewLocalAdmissionGuard().Review(context.Background(), manifests)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if !result.Allowed {
		t.Fatalf("Allowed = false, reason = %s", result.Reason)
	}
}

func TestLocalAdmissionGuardRejectsMissingDryRunAnnotation(t *testing.T) {
	manifest := ports.WorkloadManifest{
		Name:     "bad",
		Kind:     "Deployment",
		Provider: "kubernetes",
		Content: `{
  "apiVersion": "apps/v1",
  "kind": "Deployment",
  "metadata": {
    "name": "bad",
    "labels": {
      "ani.kubercloud.io/tenant-id": "tenant-a",
      "ani.kubercloud.io/instance": "bad"
    },
    "annotations": {
      "ani.kubercloud.io/network-planes": "tenant_vpc"
    }
  }
}`,
	}

	result, err := NewLocalAdmissionGuard().Review(context.Background(), []ports.WorkloadManifest{manifest})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Allowed {
		t.Fatalf("Allowed = true, want false")
	}
	if !strings.Contains(result.Reason, "render-mode") {
		t.Fatalf("reason = %q, want render-mode failure", result.Reason)
	}
}

func TestLocalAdmissionGuardRejectsPrivilegedManifest(t *testing.T) {
	manifest := ports.WorkloadManifest{
		Name:     "bad",
		Kind:     "Deployment",
		Provider: "kubernetes",
		Content: `{
  "apiVersion": "apps/v1",
  "kind": "Deployment",
  "metadata": {
    "name": "bad",
    "labels": {
      "ani.kubercloud.io/tenant-id": "tenant-a",
      "ani.kubercloud.io/instance": "bad"
    },
    "annotations": {
      "ani.kubercloud.io/render-mode": "dry-run",
      "ani.kubercloud.io/network-planes": "tenant_vpc"
    }
  },
  "spec": {
    "template": {
      "spec": {
        "containers": [
          {
            "name": "bad",
            "securityContext": {
              "privileged": true
            }
          }
        ]
      }
    }
  }
}`,
	}

	result, err := NewLocalAdmissionGuard().Review(context.Background(), []ports.WorkloadManifest{manifest})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Allowed {
		t.Fatalf("Allowed = true, want false")
	}
	if !strings.Contains(result.Reason, "privileged") {
		t.Fatalf("reason = %q, want privileged failure", result.Reason)
	}
}
