package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	registryadapter "github.com/kubercloud/ani/pkg/adapters/registry"
	"github.com/kubercloud/ani/pkg/ports"
)

type stubImageRegistry struct {
	images []ports.RegistryImage
	err    error
}

func (s *stubImageRegistry) EnsureProject(context.Context, string) error { return nil }
func (s *stubImageRegistry) ListTags(context.Context, string) ([]ports.ImageTag, error) {
	return nil, ports.ErrNotConfigured
}
func (s *stubImageRegistry) GetScanStatus(context.Context, ports.ImageRef) (ports.ImageScanStatus, error) {
	return ports.ImageScanStatus{}, ports.ErrNotConfigured
}
func (s *stubImageRegistry) CreateProject(context.Context, ports.RegistryProjectRequest) (ports.RegistryProject, error) {
	return ports.RegistryProject{}, ports.ErrNotConfigured
}
func (s *stubImageRegistry) ListProjects(context.Context, ports.RegistryProjectListRequest) (ports.RegistryProjectListResult, error) {
	return ports.RegistryProjectListResult{}, ports.ErrNotConfigured
}
func (s *stubImageRegistry) ListRepositories(context.Context, ports.RegistryRepositoryListRequest) (ports.RegistryRepositoryListResult, error) {
	return ports.RegistryRepositoryListResult{}, ports.ErrNotConfigured
}
func (s *stubImageRegistry) ListArtifacts(context.Context, ports.RegistryArtifactListRequest) (ports.RegistryArtifactListResult, error) {
	return ports.RegistryArtifactListResult{}, ports.ErrNotConfigured
}
func (s *stubImageRegistry) SetRepositoryPermission(context.Context, ports.RegistryPermissionRequest) (ports.RegistryPermission, error) {
	return ports.RegistryPermission{}, ports.ErrNotConfigured
}
func (s *stubImageRegistry) GetScanResult(context.Context, ports.RegistryScanResultRequest) (ports.RegistryScanResult, error) {
	return ports.RegistryScanResult{}, ports.ErrNotConfigured
}
func (s *stubImageRegistry) CreatePullSecret(context.Context, ports.RegistryPullSecretRequest) (ports.RegistryPullSecret, error) {
	return ports.RegistryPullSecret{}, ports.ErrNotConfigured
}
func (s *stubImageRegistry) GetProjectScanReport(context.Context, ports.RegistryProjectScanReportRequest) (ports.RegistryProjectScanReport, error) {
	return ports.RegistryProjectScanReport{}, ports.ErrNotConfigured
}
func (s *stubImageRegistry) GetOverview(context.Context, ports.RegistryOverviewRequest) (ports.RegistryOverview, error) {
	return ports.RegistryOverview{}, ports.ErrNotConfigured
}
func (s *stubImageRegistry) ListImages(context.Context, ports.RegistryImageListRequest) (ports.RegistryImageListResult, error) {
	if s.err != nil {
		return ports.RegistryImageListResult{}, s.err
	}
	return ports.RegistryImageListResult{Items: append([]ports.RegistryImage(nil), s.images...)}, nil
}
func (s *stubImageRegistry) GetPushInstructions(context.Context, ports.RegistryPushInstructionsRequest) (ports.RegistryPushInstructions, error) {
	return ports.RegistryPushInstructions{}, ports.ErrNotConfigured
}
func (s *stubImageRegistry) DeleteTag(context.Context, ports.RegistryTagDeleteRequest) (ports.RegistryDeletedTag, error) {
	return ports.RegistryDeletedTag{}, ports.ErrNotConfigured
}
func (s *stubImageRegistry) ListTagReferences(context.Context, ports.RegistryImageReferenceListRequest) (ports.RegistryImageReferenceListResult, error) {
	return ports.RegistryImageReferenceListResult{}, ports.ErrNotConfigured
}

func stubContainerImage(scan ports.RegistryScanResult) ports.RegistryImage {
	return ports.RegistryImage{
		Project:    "tenant-a",
		Repository: "runtime",
		Tag:        "latest",
		Purpose:    "container",
		Image:      "registry.local/tenant-a/runtime:latest",
		Registry:   "registry.local",
		Digest:     "sha256:stub-runtime",
		ScanStatus: scan,
	}
}

func TestLocalInstanceResourceResolverValidatesTenantAndReadyResources(t *testing.T) {
	network := NewLocalNetworkService()
	storage := NewLocalStorageService()
	vpc, err := network.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{
		TenantID: "tenant-a", IdempotencyKey: "resolver-vpc", Name: "tenant-a-vpc",
	})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	volume, err := storage.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID: "tenant-a", IdempotencyKey: "resolver-volume", Name: "data", SizeGiB: 10,
	})
	if err != nil {
		t.Fatalf("CreateVolume error = %v", err)
	}
	resolver := NewLocalInstanceResourceResolver(network, storage)
	result, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindContainer,
			Network:  ports.WorkloadNetworkPolicy{VPCID: vpc.VPCID},
			Container: &ports.ContainerInstanceSpec{
				VolumeMounts: []ports.InstanceVolumeMount{{VolumeID: volume.VolumeID, MountPath: "/data"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("ResolveCreate error = %v", err)
	}
	if len(result.ResourceRefs) != 2 || result.ResourceRefs[0] != "vpc/"+vpc.VPCID || result.ResourceRefs[1] != "volume/"+volume.VolumeID {
		t.Fatalf("resource refs = %#v, want VPC and volume refs", result.ResourceRefs)
	}
	if len(result.Spec.Storage) != 1 ||
		result.Spec.Storage[0].Kind != ports.StorageAttachmentSharedPVC ||
		result.Spec.Storage[0].SourceRef != storageProviderName("vol", volume.VolumeID) ||
		result.Spec.Storage[0].MountPath != "/data" {
		t.Fatalf("storage attachments = %#v, want shared PVC claim for resolved volume", result.Spec.Storage)
	}
}

func TestLocalInstanceResourceResolverAcceptsPendingVolumeForContainerMount(t *testing.T) {
	storage := NewLocalStorageService()
	volume, err := storage.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID: "tenant-a", IdempotencyKey: "resolver-pending-volume", Name: "pending-data", SizeGiB: 5,
	})
	if err != nil {
		t.Fatalf("CreateVolume error = %v", err)
	}
	storage.mu.Lock()
	pending := storage.volumes[volume.VolumeID]
	pending.State = ports.StorageResourcePending
	pending.Reason = "observed Kubernetes PVC phase Pending"
	storage.volumes[volume.VolumeID] = pending
	storage.mu.Unlock()
	resolver := NewLocalInstanceResourceResolver(nil, storage)
	result, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindContainer,
			Container: &ports.ContainerInstanceSpec{
				VolumeMounts: []ports.InstanceVolumeMount{{VolumeID: volume.VolumeID, MountPath: "/data"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("ResolveCreate error = %v", err)
	}
	if len(result.ResourceRefs) != 1 || result.ResourceRefs[0] != "volume/"+volume.VolumeID {
		t.Fatalf("resource refs = %#v, want pending volume ref", result.ResourceRefs)
	}
}

func TestLocalInstanceResourceResolverFailsClosedAcrossTenants(t *testing.T) {
	network := NewLocalNetworkService()
	vpc, err := network.CreateVPC(context.Background(), ports.NetworkVPCCreateRequest{
		TenantID: "tenant-a", IdempotencyKey: "resolver-cross-tenant", Name: "tenant-a-vpc",
	})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	resolver := NewLocalInstanceResourceResolver(network, nil)
	_, err = resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-b",
		Spec:     ports.WorkloadSpec{Network: ports.WorkloadNetworkPolicy{VPCID: vpc.VPCID}},
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("ResolveCreate error = %v, want ErrNotFound", err)
	}
}

func TestLocalInstanceResourceResolverResolvesAvailableGPUSpec(t *testing.T) {
	resolver := NewLocalInstanceResourceResolver(nil, nil, NewLocalGPUSpecService(NewLocalGPUInventory()))
	result, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindGPUContainer,
			GPUSpec:  &ports.InstanceGPUSpecReference{SpecID: "gpu-a100-full"},
		},
	})
	if err != nil {
		t.Fatalf("ResolveCreate error = %v", err)
	}
	if result.Spec.GPUSpec == nil || result.Spec.GPUSpec.GPUType != "A100" || result.Spec.GPUSpec.Shares != 1 || result.Spec.GPUSpec.MBPerShare != 40960 {
		t.Fatalf("gpu spec = %+v, want resolved A100 full-card values", result.Spec.GPUSpec)
	}
	if len(result.ResourceRefs) != 1 || result.ResourceRefs[0] != "gpu_spec/gpu-a100-full" {
		t.Fatalf("resource refs = %#v, want GPU spec ref", result.ResourceRefs)
	}
}

func TestParseImageReferenceSplitsHarborDigestReference(t *testing.T) {
	host, project, repository, tag, digest := parseImageReference("harbor.example/tenant-a/models/llama:7b@sha256:abc")
	if host != "harbor.example" || project != "tenant-a" || repository != "models/llama" || tag != "7b" || digest != "sha256:abc" {
		t.Fatalf("parsed image = %q/%q/%q/%q/%q", host, project, repository, tag, digest)
	}
}

func TestLocalInstanceResourceResolverResolvesImageIDAndPurpose(t *testing.T) {
	resolver := NewLocalInstanceResourceResolverWithRegistry(nil, nil, nil, registryadapter.NewLocalImageRegistry())
	result, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindContainer,
			ImageID:  "tenant-a/runtime:latest",
		},
	})
	if err != nil {
		t.Fatalf("ResolveCreate error = %v", err)
	}
	if result.Spec.ImageRef != "registry.local/tenant-a/runtime:latest" || result.Spec.ImageSummary.Digest != "sha256:local-runtime" || result.Spec.ImageSummary.Purpose != "container" {
		t.Fatalf("image summary = %+v image_ref=%q, want resolved container image", result.Spec.ImageSummary, result.Spec.ImageRef)
	}
	if len(result.ResourceRefs) != 1 || result.ResourceRefs[0] != "image/registry.local/tenant-a/runtime:latest" {
		t.Fatalf("resource refs = %#v, want resolved image ref", result.ResourceRefs)
	}
}

func TestLocalInstanceResourceResolverRejectsImagePurposeMismatch(t *testing.T) {
	resolver := NewLocalInstanceResourceResolverWithRegistry(nil, nil, nil, registryadapter.NewLocalImageRegistry())
	_, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindContainer,
			ImageID:  "tenant-a/sandbox-runtime:kata-3.8",
		},
	})
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("ResolveCreate error = %v, want ErrConflict for purpose mismatch", err)
	}
}

func TestLocalInstanceResourceResolverUsesResolvedVMImageAsBootImage(t *testing.T) {
	resolver := NewLocalInstanceResourceResolverWithRegistry(nil, nil, nil, registryadapter.NewLocalImageRegistry())
	result, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindVM,
			ImageID:  "tenant-a/system-images:ubuntu-24.04",
			VM: &ports.VMInstanceSpec{
				BootImage: "images/legacy.qcow2",
			},
		},
	})
	if err != nil {
		t.Fatalf("ResolveCreate error = %v", err)
	}
	if result.Spec.VM == nil || result.Spec.VM.BootImage != "registry.local/tenant-a/system-images:ubuntu-24.04" {
		t.Fatalf("vm boot image = %+v, want resolved system image_id", result.Spec.VM)
	}
	if result.Spec.ImageSummary.Purpose != "system" {
		t.Fatalf("image purpose = %q, want system", result.Spec.ImageSummary.Purpose)
	}
}

func TestLocalInstanceResourceResolverValidatesContainerSecretIDs(t *testing.T) {
	secrets := NewLocalSecretService()
	secret, err := secrets.CreateSecret(context.Background(), ports.SecretCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "resolver-secret",
		Name:           "app-secret",
		Data:           map[string]string{"TOKEN": "secret-value"},
	})
	if err != nil {
		t.Fatalf("CreateSecret error = %v", err)
	}
	resolver := NewLocalInstanceResourceResolverWithDependencies(nil, nil, nil, nil, secrets)
	result, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindContainer,
			Container: &ports.ContainerInstanceSpec{
				SecretIDs: []string{secret.SecretID},
			},
		},
	})
	if err != nil {
		t.Fatalf("ResolveCreate error = %v", err)
	}
	if len(result.ResourceRefs) != 1 || result.ResourceRefs[0] != "secret/"+secret.SecretID {
		t.Fatalf("resource refs = %#v, want secret ref", result.ResourceRefs)
	}

	_, err = resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-b",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-b",
			Kind:     ports.WorkloadKindContainer,
			Container: &ports.ContainerInstanceSpec{
				SecretIDs: []string{secret.SecretID},
			},
		},
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("ResolveCreate cross-tenant error = %v, want ErrNotFound", err)
	}

	deleted, err := secrets.DeleteSecret(context.Background(), ports.SecretGetRequest{TenantID: "tenant-a", SecretID: secret.SecretID})
	if err != nil {
		t.Fatalf("DeleteSecret error = %v", err)
	}
	_, err = resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindContainer,
			Container: &ports.ContainerInstanceSpec{
				SecretIDs: []string{deleted.SecretID},
			},
		},
	})
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("ResolveCreate deleted secret error = %v, want ErrConflict", err)
	}
}

func TestLocalInstanceResourceResolverValidatesContainerEnvSecretRefs(t *testing.T) {
	secrets := NewLocalSecretService()
	secret, err := secrets.CreateSecret(context.Background(), ports.SecretCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "resolver-env-secret",
		Name:           "app-secret",
		Data:           map[string]string{"TOKEN": "secret-value"},
	})
	if err != nil {
		t.Fatalf("CreateSecret error = %v", err)
	}
	resolver := NewLocalInstanceResourceResolverWithDependencies(nil, nil, nil, nil, secrets)
	result, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindContainer,
			Container: &ports.ContainerInstanceSpec{
				SecretIDs: []string{secret.SecretID},
				Env:       []ports.InstanceEnvVar{{Name: "TOKEN", SecretRef: secret.SecretID}},
			},
		},
	})
	if err != nil {
		t.Fatalf("ResolveCreate error = %v", err)
	}
	if len(result.ResourceRefs) != 1 || result.ResourceRefs[0] != "secret/"+secret.SecretID {
		t.Fatalf("resource refs = %#v, want one deduplicated secret ref", result.ResourceRefs)
	}

	_, err = resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-b",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-b",
			Kind:     ports.WorkloadKindContainer,
			Container: &ports.ContainerInstanceSpec{
				Env: []ports.InstanceEnvVar{{Name: "TOKEN", SecretRef: secret.SecretID}},
			},
		},
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("ResolveCreate cross-tenant error = %v, want ErrNotFound", err)
	}
}

func TestLocalInstanceResourceResolverValidatesVMSecretRefs(t *testing.T) {
	secrets := NewLocalSecretService()
	secret, err := secrets.CreateSecret(context.Background(), ports.SecretCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "resolver-vm-secret",
		Name:           "vm-secret",
		Data:           map[string]string{"value": "secret", "userdata": "#cloud-config\nusers:\n  - name: aniverify\n    plain_text_passwd: x\n"},
	})
	if err != nil {
		t.Fatalf("CreateSecret error = %v", err)
	}
	resolver := NewLocalInstanceResourceResolverWithDependencies(nil, nil, nil, nil, secrets)
	result, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindVM,
			VM: &ports.VMInstanceSpec{
				SSHKeySecret:    secret.SecretID,
				PasswordSecret:  secret.SecretID,
				CloudInitSecret: secret.SecretID,
			},
		},
	})
	if err != nil {
		t.Fatalf("ResolveCreate error = %v", err)
	}
	if len(result.ResourceRefs) != 1 || result.ResourceRefs[0] != "secret/"+secret.SecretID {
		t.Fatalf("resource refs = %#v, want one deduplicated VM secret ref", result.ResourceRefs)
	}

	_, err = resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-b",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-b",
			Kind:     ports.WorkloadKindVM,
			VM:       &ports.VMInstanceSpec{CloudInitSecret: secret.SecretID},
		},
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("ResolveCreate cross-tenant VM secret error = %v, want ErrNotFound", err)
	}
}

func TestLocalInstanceResourceResolverRejectsCloudInitSecretMissingUserdataKey(t *testing.T) {
	secrets := NewLocalSecretService()
	secret, err := secrets.CreateSecret(context.Background(), ports.SecretCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "resolver-vm-secret-nokey",
		Name:           "vm-secret",
		Data:           map[string]string{"value": "secret"},
	})
	if err != nil {
		t.Fatalf("CreateSecret error = %v", err)
	}
	resolver := NewLocalInstanceResourceResolverWithDependencies(nil, nil, nil, nil, secrets)
	for _, vm := range []*ports.VMInstanceSpec{
		{PasswordSecret: secret.SecretID},
		{CloudInitSecret: secret.SecretID},
	} {
		_, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
			TenantID: "tenant-a",
			Spec: ports.WorkloadSpec{
				TenantID: "tenant-a",
				Kind:     ports.WorkloadKindVM,
				VM:       vm,
			},
		})
		if !errors.Is(err, ports.ErrConflict) {
			t.Fatalf("ResolveCreate cloud-init secret missing userdata key error = %v, want ErrConflict", err)
		}
	}
}

func TestLocalInstanceResourceResolverRejectsImageScanStates(t *testing.T) {
	cases := []struct {
		name   string
		status ports.RegistryScanState
	}{
		{"not_scanned", ports.RegistryScanNotScanned},
		{"pending", ports.RegistryScanPending},
		{"running", ports.RegistryScanRunning},
		{"failed", ports.RegistryScanFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := &stubImageRegistry{images: []ports.RegistryImage{stubContainerImage(ports.RegistryScanResult{
				Status: tc.status,
			})}}
			resolver := NewLocalInstanceResourceResolverWithRegistry(nil, nil, nil, registry)
			resolver.imageVulnGate = ImageVulnGateEnforce
			_, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
				TenantID: "tenant-a",
				Spec: ports.WorkloadSpec{
					TenantID: "tenant-a",
					Kind:     ports.WorkloadKindContainer,
					ImageID:  "tenant-a/runtime:latest",
				},
			})
			if !errors.Is(err, ports.ErrFailedPrecondition) {
				t.Fatalf("ResolveCreate error = %v, want ErrFailedPrecondition", err)
			}
			if !strings.Contains(err.Error(), "ImageScanning") {
				t.Fatalf("ResolveCreate error = %v, want ImageScanning code", err)
			}
		})
	}
}

func TestLocalInstanceResourceResolverRejectsCriticalAndHighVulnerabilities(t *testing.T) {
	cases := []struct {
		name string
		scan ports.RegistryScanResult
	}{
		{"critical", ports.RegistryScanResult{Status: ports.RegistryScanComplete, Critical: 1}},
		{"high", ports.RegistryScanResult{Status: ports.RegistryScanComplete, High: 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := &stubImageRegistry{images: []ports.RegistryImage{stubContainerImage(tc.scan)}}
			resolver := NewLocalInstanceResourceResolverWithRegistry(nil, nil, nil, registry)
			resolver.imageVulnGate = ImageVulnGateEnforce
			_, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
				TenantID: "tenant-a",
				Spec: ports.WorkloadSpec{
					TenantID: "tenant-a",
					Kind:     ports.WorkloadKindContainer,
					ImageID:  "tenant-a/runtime:latest",
				},
			})
			if !errors.Is(err, ports.ErrFailedPrecondition) {
				t.Fatalf("ResolveCreate error = %v, want ErrFailedPrecondition", err)
			}
			if !strings.Contains(err.Error(), "ImageVulnerabilityBlocked") {
				t.Fatalf("ResolveCreate error = %v, want ImageVulnerabilityBlocked code", err)
			}
		})
	}
}

func TestLocalInstanceResourceResolverAllowsCleanCompleteScan(t *testing.T) {
	registry := &stubImageRegistry{images: []ports.RegistryImage{stubContainerImage(ports.RegistryScanResult{
		Status: ports.RegistryScanComplete,
		Medium: 3,
		Low:    4,
	})}}
	resolver := NewLocalInstanceResourceResolverWithRegistry(nil, nil, nil, registry)
	resolver.imageVulnGate = ImageVulnGateEnforce
	result, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindContainer,
			ImageID:  "tenant-a/runtime:latest",
		},
	})
	if err != nil {
		t.Fatalf("ResolveCreate error = %v", err)
	}
	if result.Spec.Annotations["ani.kubercloud.io/image-scan-status"] != "complete" {
		t.Fatalf("annotations = %#v, want complete scan audit markers", result.Spec.Annotations)
	}
}

func TestLocalInstanceResourceResolverObservePolicyAllowsVulnerableImageWithAudit(t *testing.T) {
	registry := &stubImageRegistry{images: []ports.RegistryImage{stubContainerImage(ports.RegistryScanResult{
		Status:   ports.RegistryScanComplete,
		Critical: 2,
		High:     5,
	})}}
	resolver := NewLocalInstanceResourceResolverWithRegistry(nil, nil, nil, registry)
	resolver.imageVulnGate = ImageVulnGateObserve
	result, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindContainer,
			ImageID:  "tenant-a/runtime:latest",
		},
	})
	if err != nil {
		t.Fatalf("ResolveCreate error = %v, want allow under observe policy", err)
	}
	if result.Spec.Annotations["ani.kubercloud.io/image-vuln-gate"] != "observe" {
		t.Fatalf("vuln gate annotation = %#v, want observe", result.Spec.Annotations)
	}
	if result.Spec.Annotations["ani.kubercloud.io/image-scan-critical"] != "2" ||
		result.Spec.Annotations["ani.kubercloud.io/image-scan-high"] != "5" {
		t.Fatalf("vuln count annotations = %#v, want critical=2 high=5", result.Spec.Annotations)
	}
}

func TestLocalInstanceResourceResolverInfersPurposeFromRepositoryWhenMetadataMissing(t *testing.T) {
	image := stubContainerImage(ports.RegistryScanResult{Status: ports.RegistryScanComplete})
	image.Purpose = ""
	image.Repository = "sandbox-runtime"
	image.Tag = "kata-3.8"
	image.Image = "registry.local/tenant-a/sandbox-runtime:kata-3.8"
	registry := &stubImageRegistry{images: []ports.RegistryImage{image}}
	resolver := NewLocalInstanceResourceResolverWithRegistry(nil, nil, nil, registry)
	resolver.imageVulnGate = ImageVulnGateEnforce
	_, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindContainer,
			ImageID:  "tenant-a/sandbox-runtime:kata-3.8",
		},
	})
	if !errors.Is(err, ports.ErrConflict) || !strings.Contains(err.Error(), "ImagePurposeMismatch") {
		t.Fatalf("ResolveCreate error = %v, want ErrConflict ImagePurposeMismatch", err)
	}
}

func TestLocalInstanceResourceResolverRejectsCreateOnVolumeHeldByActiveInstance(t *testing.T) {
	storage := NewLocalStorageService()
	volume, err := storage.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID: "tenant-a", IdempotencyKey: "resolver-rwo-held", Name: "held", SizeGiB: 10,
	})
	if err != nil {
		t.Fatalf("CreateVolume error = %v", err)
	}
	store := &fakeInstanceStore{records: []ports.WorkloadInstanceRecord{{
		TenantID:   "tenant-a",
		InstanceID: "inst-holder",
		Name:       "holder",
		Kind:       ports.WorkloadKindContainer,
		Status:     ports.WorkloadStatus{State: ports.WorkloadStateRunning},
		StorageAttachments: []ports.WorkloadStorageAttachment{{
			Name: "data", ResourceType: "volume", ResourceID: volume.VolumeID, MountPath: "/data",
		}},
	}}}
	resolver := NewLocalInstanceResourceResolver(nil, storage).WithWorkloadStore(store)
	_, err = resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindContainer,
			Container: &ports.ContainerInstanceSpec{
				VolumeMounts: []ports.InstanceVolumeMount{{VolumeID: volume.VolumeID, MountPath: "/data"}},
			},
		},
	})
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("ResolveCreate error = %v, want ErrConflict for volume held by active instance", err)
	}
	if !strings.Contains(err.Error(), "inst-holder") {
		t.Fatalf("error = %v, want occupying instance id in message", err)
	}
}

func TestLocalInstanceResourceResolverAllowsCreateOnVolumeReleasedByStoppedInstance(t *testing.T) {
	storage := NewLocalStorageService()
	volume, err := storage.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID: "tenant-a", IdempotencyKey: "resolver-rwo-stopped", Name: "released", SizeGiB: 10,
	})
	if err != nil {
		t.Fatalf("CreateVolume error = %v", err)
	}
	store := &fakeInstanceStore{records: []ports.WorkloadInstanceRecord{{
		TenantID:   "tenant-a",
		InstanceID: "inst-stopped",
		Name:       "stopped",
		Kind:       ports.WorkloadKindContainer,
		Status:     ports.WorkloadStatus{State: ports.WorkloadStateStopped},
		StorageAttachments: []ports.WorkloadStorageAttachment{{
			Name: "data", ResourceType: "volume", ResourceID: volume.VolumeID, MountPath: "/data",
		}},
	}}}
	resolver := NewLocalInstanceResourceResolver(nil, storage).WithWorkloadStore(store)
	result, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindContainer,
			Container: &ports.ContainerInstanceSpec{
				VolumeMounts: []ports.InstanceVolumeMount{{VolumeID: volume.VolumeID, MountPath: "/data"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("ResolveCreate error = %v, want stopped holder to release the volume", err)
	}
	if len(result.ResourceRefs) != 1 || result.ResourceRefs[0] != "volume/"+volume.VolumeID {
		t.Fatalf("resource refs = %#v, want volume ref", result.ResourceRefs)
	}
}

func TestLocalInstanceResourceResolverAllowsCreateOnFilesystemHeldByActiveInstance(t *testing.T) {
	storage := NewLocalStorageService()
	filesystem, err := storage.CreateFilesystem(context.Background(), ports.StorageFilesystemCreateRequest{
		TenantID: "tenant-a", IdempotencyKey: "resolver-rwx-shared", Name: "shared", Protocol: "nfs", SizeGiB: 100,
	})
	if err != nil {
		t.Fatalf("CreateFilesystem error = %v", err)
	}
	store := &fakeInstanceStore{records: []ports.WorkloadInstanceRecord{{
		TenantID:   "tenant-a",
		InstanceID: "inst-fs-holder",
		Name:       "fs-holder",
		Kind:       ports.WorkloadKindContainer,
		Status:     ports.WorkloadStatus{State: ports.WorkloadStateRunning},
		StorageAttachments: []ports.WorkloadStorageAttachment{{
			Name: "share", ResourceType: "filesystem", ResourceID: filesystem.FilesystemID, MountPath: "/share",
		}},
	}}}
	resolver := NewLocalInstanceResourceResolver(nil, storage).WithWorkloadStore(store)
	result, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindContainer,
			Container: &ports.ContainerInstanceSpec{
				FilesystemMounts: []ports.InstanceFilesystemMount{{FilesystemID: filesystem.FilesystemID, MountPath: "/share"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("ResolveCreate error = %v, want RWX filesystem sharing to stay allowed", err)
	}
	if len(result.ResourceRefs) != 1 || result.ResourceRefs[0] != "filesystem/"+filesystem.FilesystemID {
		t.Fatalf("resource refs = %#v, want filesystem ref", result.ResourceRefs)
	}
}
