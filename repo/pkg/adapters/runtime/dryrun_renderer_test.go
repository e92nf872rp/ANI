package runtime

import (
	"context"
	"encoding/json"
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
	for _, want := range []string{"VirtualMachine", "kubevirt.io/v1", "tenant_vpc", "foundation_mesh", "management", "vm-01-root", "masquerade", `"name": "default"`, `"logSerialConsole": false`} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered VM manifest missing %q:\n%s", want, content)
		}
	}
	if !strings.Contains(content, `"pod"`) {
		t.Fatalf("placeholder network planes should render pod network:\n%s", content)
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
	for _, want := range []string{"Deployment", "nvidia.com/gpu", "schedulerName", "volcano", "storage"} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered GPU manifest missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "runtimeClassName") {
		t.Fatalf("rendered GPU manifest should not contain runtimeClassName when decision leaves it empty:\n%s", content)
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

func TestKubernetesDryRunRendererUsesContainerIntent(t *testing.T) {
	value := "production"
	spec := ports.WorkloadSpec{
		Name:  "api",
		Kind:  ports.WorkloadKindContainer,
		Image: "harbor.local/api:latest",
		Container: &ports.ContainerInstanceSpec{
			Replicas:     3,
			PortSpecs:    []ports.InstancePortSpec{{Name: "http", ContainerPort: 8080, Protocol: "TCP"}},
			Env:          []ports.InstanceEnvVar{{Name: "MODE", Value: &value}},
			VolumeMounts: []ports.InstanceVolumeMount{{VolumeID: "vol-1", MountPath: "/data", ReadOnly: true}},
		},
	}

	var content map[string]any
	if err := json.Unmarshal([]byte(renderDeployment(spec).Content), &content); err != nil {
		t.Fatalf("unmarshal deployment = %v", err)
	}
	deploymentSpec := content["spec"].(map[string]any)
	if deploymentSpec["replicas"] != float64(3) {
		t.Fatalf("replicas = %#v, want 3", deploymentSpec["replicas"])
	}
	pod := deploymentSpec["template"].(map[string]any)["spec"].(map[string]any)
	container := pod["containers"].([]any)[0].(map[string]any)
	if len(container["ports"].([]any)) != 1 || container["ports"].([]any)[0].(map[string]any)["name"] != "http" {
		t.Fatalf("ports = %#v, want named port spec", container["ports"])
	}
	if len(container["env"].([]any)) != 1 || container["env"].([]any)[0].(map[string]any)["name"] != "MODE" {
		t.Fatalf("env = %#v, want MODE", container["env"])
	}
	if len(container["volumeMounts"].([]any)) != 1 || container["volumeMounts"].([]any)[0].(map[string]any)["mountPath"] != "/data" {
		t.Fatalf("volume mounts = %#v, want /data", container["volumeMounts"])
	}
}

func TestKubernetesDryRunRendererRendersContainerServiceForPorts(t *testing.T) {
	renderer := NewKubernetesDryRunRenderer(NewPlanningRuntime())
	manifests, err := renderer.Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "api",
		Kind:     ports.WorkloadKindContainer,
		Image:    "harbor/app:1",
		Container: &ports.ContainerInstanceSpec{
			PortSpecs: []ports.InstancePortSpec{
				{Name: "http", ContainerPort: 8080, Protocol: "TCP"},
				{ContainerPort: 9090, Protocol: "UDP"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(manifests) != 2 {
		t.Fatalf("manifests = %d, want Deployment and Service", len(manifests))
	}
	if manifests[0].Kind != "Deployment" || manifests[1].Kind != "Service" {
		t.Fatalf("manifest kinds = %q, %q, want Deployment and Service", manifests[0].Kind, manifests[1].Kind)
	}
	var service map[string]any
	if err := json.Unmarshal([]byte(manifests[1].Content), &service); err != nil {
		t.Fatalf("unmarshal service = %v", err)
	}
	serviceSpec := service["spec"].(map[string]any)
	if serviceSpec["type"] != "ClusterIP" {
		t.Fatalf("service type = %#v, want ClusterIP", serviceSpec["type"])
	}
	selector := serviceSpec["selector"].(map[string]any)
	if selector["ani.kubercloud.io/instance"] != "api" {
		t.Fatalf("service selector = %#v, want instance api", selector)
	}
	servicePorts := serviceSpec["ports"].([]any)
	if len(servicePorts) != 2 {
		t.Fatalf("service ports = %#v, want two ports", servicePorts)
	}
	firstPort := servicePorts[0].(map[string]any)
	if firstPort["name"] != "http" || firstPort["port"] != float64(8080) || firstPort["targetPort"] != float64(8080) {
		t.Fatalf("first service port = %#v, want named http/8080", firstPort)
	}
}

func TestKubernetesDryRunRendererDoesNotRenderContainerServiceWithoutPorts(t *testing.T) {
	renderer := NewKubernetesDryRunRenderer(NewPlanningRuntime())
	manifests, err := renderer.Render(context.Background(), ports.WorkloadSpec{
		TenantID:  "tenant-a",
		Name:      "worker",
		Kind:      ports.WorkloadKindContainer,
		Image:     "harbor/app:1",
		Container: &ports.ContainerInstanceSpec{},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(manifests) != 1 || manifests[0].Kind != "Deployment" {
		t.Fatalf("manifests = %#v, want Deployment only", manifests)
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

func TestKubernetesDryRunRendererInjectsContainerSecretIDsAsEnvFrom(t *testing.T) {
	spec := ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "app-01",
		Kind:     ports.WorkloadKindContainer,
		Image:    "harbor/app:1",
		Container: &ports.ContainerInstanceSpec{
			SecretIDs: []string{"sec-db", "sec-api"},
		},
	}

	var content map[string]any
	if err := json.Unmarshal([]byte(renderDeployment(spec).Content), &content); err != nil {
		t.Fatalf("unmarshal deployment = %v", err)
	}
	pod := content["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	container := pod["containers"].([]any)[0].(map[string]any)
	envFrom := container["envFrom"].([]any)
	if len(envFrom) != 2 {
		t.Fatalf("envFrom = %#v, want two secret refs", envFrom)
	}
	for i, want := range []string{"sec-db", "sec-api"} {
		entry := envFrom[i].(map[string]any)
		secretRef := entry["secretRef"].(map[string]any)
		if secretRef["name"] != want {
			t.Fatalf("envFrom[%d] = %#v, want secret %q", i, entry, want)
		}
	}
}

func TestKubernetesDryRunRendererRendersVMDataDiskSpecs(t *testing.T) {
	manifests, err := NewKubernetesDryRunRenderer(NewPlanningRuntime()).Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "vm-01",
		Kind:     ports.WorkloadKindVM,
		VM: &ports.VMInstanceSpec{
			BootImage: "ubuntu.qcow2",
			RootDisk: ports.WorkloadStorageAttachment{
				Name:    "root",
				Kind:    ports.StorageAttachmentRootDisk,
				SizeGiB: 80,
			},
			DataDiskSpecs: []ports.InstanceDiskSpec{
				{Name: "data-01", VolumeID: "volume-data-01", SizeGiB: 100},
			},
		},
	})
	if err != nil {
		t.Fatalf("Render(VM) error = %v", err)
	}
	if len(manifests) != 1 || !strings.Contains(manifests[0].Content, `"claimName": "volume-data-01"`) {
		t.Fatalf("VM manifest = %s, want data disk PVC attachment", manifests[0].Content)
	}
}

func TestKubernetesDryRunRendererRendersVMCloudInitSecret(t *testing.T) {
	manifests, err := NewKubernetesDryRunRenderer(NewPlanningRuntime()).Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "vm-cloudinit-01",
		Kind:     ports.WorkloadKindVM,
		VM: &ports.VMInstanceSpec{
			BootImage:       "ubuntu.qcow2",
			CloudInitSecret: "secret-cloudinit",
			RootDisk: ports.WorkloadStorageAttachment{
				Name:    "root",
				Kind:    ports.StorageAttachmentRootDisk,
				SizeGiB: 80,
			},
		},
	})
	if err != nil {
		t.Fatalf("Render(VM) error = %v", err)
	}
	content := manifests[0].Content
	for _, want := range []string{
		`"name": "cloudinitdisk"`,
		`"cloudInitNoCloud"`,
		`"secretRef"`,
		`"name": "secret-cloudinit"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("VM manifest missing %q:\n%s", want, content)
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
		`"ani.kubercloud.io/vm-secret-mounts"`,
		`"sec-bootstrap:/var/lib/ani/secrets/bootstrap"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered VM secret binding manifest missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, `"readOnly": true`) {
		t.Fatalf("rendered VM secret binding disk contains KubeVirt-invalid readOnly field:\n%s", content)
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

func TestKubernetesDryRunRendererContainerResourcesSetsLimitsAndRequests(t *testing.T) {
	renderer := NewKubernetesDryRunRenderer(NewPlanningRuntime())

	manifests, err := renderer.Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "app-01",
		Kind:     ports.WorkloadKindContainer,
		Image:    "harbor/app:1",
		Resources: ports.WorkloadResourceRequest{
			CPU:    "500m",
			Memory: "512Mi",
		},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	content := manifests[0].Content
	// CPU/Memory 应同时出现在 limits 和 requests 中，这样 Prometheus
	// container_spec_memory_limit_bytes 才能采集到 memory_total。
	for _, want := range []string{
		`"limits": {`,
		`"cpu": "500m"`,
		`"memory": "512Mi"`,
		`"requests": {`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered manifest missing %q:\n%s", want, content)
		}
	}
	// limits 里应有两个 key（cpu + memory），不含空 limits。
	if strings.Contains(content, `"limits": {}`) {
		t.Fatalf("rendered manifest has empty limits:\n%s", content)
	}
}
