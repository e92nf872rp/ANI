package runtime

import (
	"context"
	"errors"
	"testing"

	registryadapter "github.com/kubercloud/ani/pkg/adapters/registry"
	"github.com/kubercloud/ani/pkg/ports"
)

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
		Data:           map[string]string{"value": "secret"},
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
