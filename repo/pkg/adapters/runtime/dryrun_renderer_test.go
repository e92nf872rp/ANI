package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestKubernetesDryRunRendererRendersVM(t *testing.T) {
	renderer := NewKubernetesDryRunRenderer(NewPlanningRuntime())

	manifests, err := renderer.Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "vm-01",
		Kind:     ports.WorkloadKindVM,
		VM: &ports.VMInstanceSpec{
			BootImage: "harbor/base/ubuntu.qcow2",
			RootDisk: ports.WorkloadStorageAttachment{
				Name:      "root",
				Kind:      ports.StorageAttachmentRootDisk,
				SizeGiB:   80,
				SourceRef: "vm-01-root",
			},
		},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("manifests = %d, want 1", len(manifests))
	}
	content := manifests[0].Content
	for _, want := range []string{"VirtualMachine", "kubevirt.io/v1", "tenant_vpc", "foundation_mesh", "management", "vm-01-root"} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered VM manifest missing %q:\n%s", want, content)
		}
	}
}

func TestKubernetesDryRunRendererRendersGPUDeployment(t *testing.T) {
	renderer := NewKubernetesDryRunRenderer(NewPlanningRuntime(WithGPUInventory(fakeGPUInventory{})))

	manifests, err := renderer.Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "gpu-01",
		Kind:     ports.WorkloadKindGPUContainer,
		Image:    "harbor/runtime:cuda",
		Resources: ports.WorkloadResourceRequest{
			GPU: ports.GPUSchedulingRequest{RequiredCount: 1},
		},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	content := manifests[0].Content
	for _, want := range []string{"Deployment", "nvidia.com/gpu", "runtimeClassName", "nvidia", "schedulerName", "volcano", "storage"} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered GPU manifest missing %q:\n%s", want, content)
		}
	}
}

func TestKubernetesDryRunRendererInjectsWorkloadIdentityEnvFromSecret(t *testing.T) {
	renderer := NewKubernetesDryRunRenderer(NewPlanningRuntime())

	manifests, err := renderer.Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "app-01",
		Kind:     ports.WorkloadKindContainer,
		Image:    "harbor/app:1",
		Identity: &ports.WorkloadIdentityBinding{
			InstanceID: "instance-a",
			KeyID:      "key-1234567890",
			KeyValue:   "must-not-render",
			Active:     true,
		},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	content := manifests[0].Content
	for _, want := range []string{"ANI_WORKLOAD_TOKEN", "secretKeyRef", "ani-wi-key-1234567890", "ANI_WORKLOAD_ID", "instance-a"} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered identity manifest missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "must-not-render") {
		t.Fatalf("rendered manifest leaked workload identity key value:\n%s", content)
	}
}

func TestKubernetesDryRunRendererInjectsSecretBindingEnvAndFileRefs(t *testing.T) {
	renderer := NewKubernetesDryRunRenderer(NewPlanningRuntime())

	manifests, err := renderer.Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "app-01",
		Kind:     ports.WorkloadKindContainer,
		Image:    "harbor/app:1",
		SecretBindings: []ports.WorkloadSecretBinding{
			{
				SecretID:  "sec-db",
				EnvPrefix: "DB_",
				MountPath: "/etc/secrets/db",
			},
		},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	content := manifests[0].Content
	for _, want := range []string{
		`"envFrom":`,
		`"prefix": "DB_"`,
		`"secretRef":`,
		`"name": "sec-db"`,
		`"mountPath": "/etc/secrets/db"`,
		`"readOnly": true`,
		`"secretName": "sec-db"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered secret binding manifest missing %q:\n%s", want, content)
		}
	}
}

func TestKubernetesDryRunRendererInjectsVMSecretBindingsAsKubeVirtVolumes(t *testing.T) {
	renderer := NewKubernetesDryRunRenderer(NewPlanningRuntime())

	manifests, err := renderer.Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "vm-secret-01",
		Kind:     ports.WorkloadKindVM,
		VM: &ports.VMInstanceSpec{
			BootImage: "harbor/base/ubuntu.qcow2",
			RootDisk: ports.WorkloadStorageAttachment{
				Name:      "root",
				Kind:      ports.StorageAttachmentRootDisk,
				SizeGiB:   80,
				SourceRef: "vm-secret-01-root",
			},
		},
		SecretBindings: []ports.WorkloadSecretBinding{
			{
				SecretID:  "sec-bootstrap",
				MountPath: "/var/lib/ani/secrets/bootstrap",
			},
		},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	content := manifests[0].Content
	for _, want := range []string{
		`"secretName": "sec-bootstrap"`,
		`"name": "secret-sec-bootstrap-1"`,
		`"disks":`,
		`"readOnly": true`,
		`"ani.kubercloud.io/vm-secret-mounts"`,
		`"sec-bootstrap:/var/lib/ani/secrets/bootstrap"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered VM secret binding manifest missing %q:\n%s", want, content)
		}
	}
}

func TestKubernetesDryRunRendererRendersBatchJob(t *testing.T) {
	renderer := NewKubernetesDryRunRenderer(NewPlanningRuntime())

	manifests, err := renderer.Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "job-01",
		Kind:     ports.WorkloadKindBatchJob,
		Image:    "harbor/batch:1",
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	content := manifests[0].Content
	for _, want := range []string{"Job", "batch/v1", "restartPolicy", "Never"} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered Job manifest missing %q:\n%s", want, content)
		}
	}
}
