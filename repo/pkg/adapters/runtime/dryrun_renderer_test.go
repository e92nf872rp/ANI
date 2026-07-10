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
	for _, want := range []string{"VirtualMachine", "kubevirt.io/v1", "tenant_vpc", "foundation_mesh", "management", "vm-01-root"} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered VM manifest missing %q:\n%s", want, content)
		}
	}
}

func TestRenderVMWithISOBootMediaUsesCdromAndBlankRoot(t *testing.T) {
	renderer := NewKubernetesDryRunRenderer(NewPlanningRuntime())

	manifests, err := renderer.Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "vm-iso-01",
		Kind:     ports.WorkloadKindVM,
		VM: &ports.VMInstanceSpec{
			BootMedia:        ports.VMBootMediaISO,
			BootMediaImageID: "img-abc123",
			RootDisk: ports.WorkloadStorageAttachment{
				Name:    "vm-iso-01-root",
				Kind:    ports.StorageAttachmentRootDisk,
				SizeGiB: 40,
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
	for _, want := range []string{
		`"kind": "VirtualMachine"`,
		`"cdrom"`,
		`"persistentVolumeClaim"`,
		`"img-abc123"`,
		`"bootOrder": 1`,
		`"rootdisk"`,
		// Blank root disk must be self-creating via dataVolumeTemplates, not
		// just a claimName reference to a PVC that apply never creates.
		`"dataVolumeTemplates"`,
		`"vm-iso-01-root"`,
		`"blank"`,
		`"40Gi"`,
		`"cdi.kubevirt.io/storage.bind.immediate.requested": "true"`,
		`"dataVolume"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered ISO VM manifest missing %q:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{"containerDisk", "containerdisk"} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("rendered ISO VM manifest must not contain %q:\n%s", unwanted, content)
		}
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("unmarshal rendered manifest: %v", err)
	}
	vmSpec, _ := doc["spec"].(map[string]any)
	templates, _ := vmSpec["dataVolumeTemplates"].([]any)
	if len(templates) != 1 {
		t.Fatalf("dataVolumeTemplates = %d entries, want 1:\n%s", len(templates), content)
	}
	template, _ := templates[0].(map[string]any)
	templateMeta, _ := template["metadata"].(map[string]any)
	if name, _ := templateMeta["name"].(string); name != "vm-iso-01-root" {
		t.Fatalf("dataVolumeTemplates[0].metadata.name = %q, want %q", name, "vm-iso-01-root")
	}
	templateSpec, _ := template["spec"].(map[string]any)
	source, _ := templateSpec["source"].(map[string]any)
	if _, ok := source["blank"]; !ok {
		t.Fatalf("dataVolumeTemplates[0].spec.source must be blank:\n%s", content)
	}
	storage, _ := templateSpec["storage"].(map[string]any)
	if _, ok := storage["storageClassName"]; ok {
		t.Fatalf("dataVolumeTemplates[0].spec.storage.storageClassName = %v, want omitted (cluster default)", storage["storageClassName"])
	}
	if vm, _ := storage["volumeMode"].(string); vm != "Filesystem" {
		t.Fatalf("dataVolumeTemplates[0].spec.storage.volumeMode = %q, want Filesystem", vm)
	}
	modes, _ := storage["accessModes"].([]any)
	if len(modes) != 1 || modes[0] != "ReadWriteOnce" {
		t.Fatalf("dataVolumeTemplates[0].spec.storage.accessModes = %v, want [ReadWriteOnce]", modes)
	}
	resources, _ := storage["resources"].(map[string]any)
	requests, _ := resources["requests"].(map[string]any)
	if requested, _ := requests["storage"].(string); requested != "40Gi" {
		t.Fatalf("dataVolumeTemplates[0].spec.storage.resources.requests.storage = %q, want %q", requested, "40Gi")
	}

	vmTemplate, _ := vmSpec["template"].(map[string]any)
	podSpec, _ := vmTemplate["spec"].(map[string]any)
	domain, _ := podSpec["domain"].(map[string]any)
	devices, _ := domain["devices"].(map[string]any)
	rawDisks, _ := devices["disks"].([]any)
	var rootDisk map[string]any
	var isoDisk map[string]any
	for _, d := range rawDisks {
		disk, _ := d.(map[string]any)
		switch disk["name"] {
		case "rootdisk":
			rootDisk = disk
		case "iso":
			isoDisk = disk
		}
	}
	if rootDisk == nil || isoDisk == nil {
		t.Fatalf("rootdisk/iso disk entries not found:\n%s", content)
	}
	if bootOrder, _ := rootDisk["bootOrder"].(float64); bootOrder != 1 {
		t.Fatalf("rootdisk bootOrder = %v, want 1:\n%s", rootDisk["bootOrder"], content)
	}
	if _, hasBootOrder := isoDisk["bootOrder"]; hasBootOrder {
		t.Fatalf("iso cdrom must not own bootOrder after install handoff:\n%s", content)
	}

	rawVolumes, _ := podSpec["volumes"].([]any)
	var rootVolume map[string]any
	for _, v := range rawVolumes {
		volume, _ := v.(map[string]any)
		if volume["name"] == "rootdisk" {
			rootVolume = volume
			break
		}
	}
	if rootVolume == nil {
		t.Fatalf("rootdisk volume not found:\n%s", content)
	}
	dataVolumeRef, _ := rootVolume["dataVolume"].(map[string]any)
	if name, _ := dataVolumeRef["name"].(string); name != "vm-iso-01-root" {
		t.Fatalf("rootdisk volume.dataVolume.name = %q, want %q", name, "vm-iso-01-root")
	}
	if _, hasPVC := rootVolume["persistentVolumeClaim"]; hasPVC {
		t.Fatalf("rootdisk volume must reference dataVolume, not a pre-existing persistentVolumeClaim:\n%s", content)
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

func TestKubernetesDryRunRendererInjectsKubeOVNAnnotationsForContainerNetwork(t *testing.T) {
	renderer := NewKubernetesDryRunRenderer(NewPlanningRuntime())

	manifests, err := renderer.Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "app-net-01",
		Kind:     ports.WorkloadKindContainer,
		Image:    "harbor/app:1",
		Network: ports.WorkloadNetworkPolicy{
			Attachments: []ports.WorkloadNetworkAttachment{
				{
					Plane:     ports.NetworkPlaneTenantVPC,
					NetworkID: "vpc_a",
					SubnetID:  "subnet_app",
					IPAddress: "10.72.0.10",
					Primary:   true,
					Required:  true,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	content := manifests[0].Content
	for _, want := range []string{
		`"ovn.kubernetes.io/logical_switch": "subnet-subnet-app"`,
		`"ovn.kubernetes.io/ip_address": "10.72.0.10"`,
		`"ani.kubercloud.io/subnet-id": "subnet_app"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered container manifest missing %q:\n%s", want, content)
		}
	}
}

func TestKubernetesDryRunRendererInjectsDefaultKubeOVNNetworkWhenNoSubnetSelected(t *testing.T) {
	renderer := NewKubernetesDryRunRenderer(NewPlanningRuntime())

	manifests, err := renderer.Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "app-default-net",
		Kind:     ports.WorkloadKindContainer,
		Image:    "harbor/app:1",
		Network: ports.WorkloadNetworkPolicy{
			Attachments: []ports.WorkloadNetworkAttachment{
				{
					Plane:     ports.NetworkPlaneTenantVPC,
					NetworkID: "tenant-vpc",
					Primary:   true,
					Required:  true,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	content := manifests[0].Content
	for _, want := range []string{
		`"ovn.kubernetes.io/logical_switch": "ovn-default"`,
		`"ani.kubercloud.io/network-planes": "tenant_vpc"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered container manifest missing %q:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{
		`"ani.kubercloud.io/subnet-id"`,
		`"ovn.kubernetes.io/ip_address"`,
	} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("rendered container manifest contains %q without subnet selection:\n%s", unwanted, content)
		}
	}
}

func TestKubernetesDryRunRendererInjectsKubeOVNAnnotationsForVMLauncherTemplate(t *testing.T) {
	renderer := NewKubernetesDryRunRenderer(NewPlanningRuntime())

	manifests, err := renderer.Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "vm-net-01",
		Kind:     ports.WorkloadKindVM,
		Network: ports.WorkloadNetworkPolicy{
			Attachments: []ports.WorkloadNetworkAttachment{
				{
					Plane:     ports.NetworkPlaneTenantVPC,
					NetworkID: "vpc_a",
					SubnetID:  "subnet_vm",
					IPAddress: "10.72.0.11",
					Primary:   true,
					Required:  true,
				},
			},
		},
		VM: &ports.VMInstanceSpec{
			BootImage: "harbor/base/ubuntu.qcow2",
			RootDisk: ports.WorkloadStorageAttachment{
				Name:      "root",
				Kind:      ports.StorageAttachmentRootDisk,
				SizeGiB:   80,
				SourceRef: "vm-net-01-root",
			},
		},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	content := manifests[0].Content
	for _, want := range []string{
		`"template":`,
		`"annotations":`,
		`"ovn.kubernetes.io/logical_switch": "subnet-subnet-vm"`,
		`"ovn.kubernetes.io/ip_address": "10.72.0.11"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered VM manifest missing %q:\n%s", want, content)
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
	if len(manifests) != 2 || manifests[0].Kind != "Secret" || manifests[1].Kind != "Deployment" {
		t.Fatalf("manifests = %#v, want Secret then Deployment", manifests)
	}
	secretContent := manifests[0].Content
	deploymentContent := manifests[1].Content
	if !strings.Contains(secretContent, "must-not-render") {
		t.Fatalf("secret manifest missing workload token:\n%s", secretContent)
	}
	for _, want := range []string{"ANI_WORKLOAD_TOKEN", "secretKeyRef", "ani-wi-key-1234567890", "ANI_WORKLOAD_ID", "instance-a"} {
		if !strings.Contains(deploymentContent, want) {
			t.Fatalf("rendered deployment manifest missing %q:\n%s", want, deploymentContent)
		}
	}
	if strings.Contains(deploymentContent, "must-not-render") {
		t.Fatalf("deployment manifest leaked workload identity key value:\n%s", deploymentContent)
	}
}

func TestKubernetesDryRunRendererRendersWorkloadIdentitySecretBeforeDeployment(t *testing.T) {
	renderer := NewKubernetesDryRunRenderer(NewPlanningRuntime())
	manifests, err := renderer.Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "app-01",
		Kind:     ports.WorkloadKindContainer,
		Image:    "harbor/app:1",
		Identity: &ports.WorkloadIdentityBinding{
			InstanceID: "instance-a",
			KeyID:      "ad9defcb-7e18-48c6-a7c4-5bc991de0bfc",
			KeyValue:   "workload-token-value",
			Active:     true,
		},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(manifests) != 2 {
		t.Fatalf("manifest count = %d, want secret + deployment", len(manifests))
	}
	if manifests[0].Kind != "Secret" || manifests[1].Kind != "Deployment" {
		t.Fatalf("manifest order = %#v, want Secret then Deployment", manifests)
	}
	if !strings.Contains(manifests[0].Content, "ani-wi-ad9defcb-7e18-48c6-a7c4") {
		t.Fatalf("secret manifest missing sanitized secret name:\n%s", manifests[0].Content)
	}
	if !strings.Contains(manifests[0].Content, "workload-token-value") {
		t.Fatalf("secret manifest missing workload token:\n%s", manifests[0].Content)
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
