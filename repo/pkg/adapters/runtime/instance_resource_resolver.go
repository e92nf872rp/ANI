package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

// LocalInstanceResourceResolver keeps instance creation provider-neutral while
// enforcing that referenced Core resources belong to the tenant and are ready.
// The concrete Network/Storage services decide whether the lookup is local or
// backed by a real provider adapter.
type LocalInstanceResourceResolver struct {
	network  ports.NetworkService
	storage  ports.StorageService
	gpuSpecs ports.GPUSpecService
	registry ports.ImageRegistry
	secrets  ports.SecretService
}

func NewLocalInstanceResourceResolver(network ports.NetworkService, storage ports.StorageService, gpuSpecServices ...ports.GPUSpecService) *LocalInstanceResourceResolver {
	var gpuSpecs ports.GPUSpecService
	if len(gpuSpecServices) > 0 {
		gpuSpecs = gpuSpecServices[0]
	}
	return &LocalInstanceResourceResolver{network: network, storage: storage, gpuSpecs: gpuSpecs}
}

func NewLocalInstanceResourceResolverWithRegistry(network ports.NetworkService, storage ports.StorageService, gpuSpecs ports.GPUSpecService, registry ports.ImageRegistry) *LocalInstanceResourceResolver {
	return &LocalInstanceResourceResolver{network: network, storage: storage, gpuSpecs: gpuSpecs, registry: registry}
}

func NewLocalInstanceResourceResolverWithDependencies(network ports.NetworkService, storage ports.StorageService, gpuSpecs ports.GPUSpecService, registry ports.ImageRegistry, secrets ports.SecretService) *LocalInstanceResourceResolver {
	return &LocalInstanceResourceResolver{network: network, storage: storage, gpuSpecs: gpuSpecs, registry: registry, secrets: secrets}
}

func (r *LocalInstanceResourceResolver) ResolveCreate(ctx context.Context, request ports.WorkloadResourceResolveRequest) (ports.WorkloadResourceResolveResult, error) {
	spec := request.Spec
	refs := make([]string, 0)
	if r.network != nil {
		resolvedRefs, err := r.resolveNetwork(ctx, request.TenantID, &spec)
		if err != nil {
			return ports.WorkloadResourceResolveResult{}, err
		}
		refs = append(refs, resolvedRefs...)
	}
	if r.storage != nil {
		resolvedRefs, err := r.resolveStorage(ctx, request.TenantID, &spec)
		if err != nil {
			return ports.WorkloadResourceResolveResult{}, err
		}
		refs = append(refs, resolvedRefs...)
	}
	if r.gpuSpecs != nil && spec.GPUSpec != nil {
		resolved, err := r.gpuSpecs.GetGPUSpec(ctx, spec.GPUSpec.SpecID)
		if err != nil {
			return ports.WorkloadResourceResolveResult{}, fmt.Errorf("resolve instance gpu spec %q: %w", spec.GPUSpec.SpecID, err)
		}
		if !resolved.Available {
			return ports.WorkloadResourceResolveResult{}, fmt.Errorf("%w: gpu spec %q is unavailable", ports.ErrConflict, resolved.ID)
		}
		spec.GPUSpec.GPUType = resolved.GPUType
		spec.GPUSpec.Shares = resolved.Shares
		spec.GPUSpec.MBPerShare = resolved.MBPerShare
		refs = append(refs, "gpu_spec/"+resolved.ID)
	}
	if r.registry != nil && (strings.TrimSpace(spec.ImageID) != "" || strings.TrimSpace(spec.ImageRef) != "") {
		imageID := strings.TrimSpace(spec.ImageID)
		imageRef := strings.TrimSpace(spec.ImageRef)
		if imageID == "" {
			imageID = imageRef
		}
		resolved, err := r.resolveImage(ctx, request.TenantID, imageID)
		if err != nil {
			return ports.WorkloadResourceResolveResult{}, err
		}
		if err := validateImagePurposeForInstanceKind(spec.Kind, resolved); err != nil {
			return ports.WorkloadResourceResolveResult{}, err
		}
		spec.Image = resolved.Image
		spec.ImageRef = resolved.Image
		spec.ImageID = imageID
		spec.ImageSummary = ports.InstanceImageSummary{ID: imageID, Ref: resolved.Image, Digest: resolved.Digest, Name: resolved.Repository, Tag: resolved.Tag, Purpose: resolved.Purpose}
		if spec.Kind == ports.WorkloadKindVM && spec.VM != nil {
			spec.VM.BootImage = resolved.Image
		}
		refs = append(refs, "image/"+resolved.Image)
	}
	if r.secrets != nil && spec.Container != nil {
		secretIDs := append([]string(nil), spec.Container.SecretIDs...)
		for _, env := range spec.Container.Env {
			if secretRef := strings.TrimSpace(env.SecretRef); secretRef != "" {
				secretIDs = append(secretIDs, secretRef)
			}
		}
		resolvedRefs, err := r.resolveSecrets(ctx, request.TenantID, secretIDs)
		if err != nil {
			return ports.WorkloadResourceResolveResult{}, err
		}
		refs = append(refs, resolvedRefs...)
	}
	if r.secrets != nil && spec.VM != nil {
		secretIDs := []string{
			spec.VM.SSHKeySecret,
			spec.VM.PasswordSecret,
			spec.VM.CloudInitSecret,
		}
		resolvedRefs, err := r.resolveSecrets(ctx, request.TenantID, secretIDs)
		if err != nil {
			return ports.WorkloadResourceResolveResult{}, err
		}
		refs = append(refs, resolvedRefs...)
	}
	return ports.WorkloadResourceResolveResult{Spec: spec, ResourceRefs: refs}, nil
}

func validateImagePurposeForInstanceKind(kind ports.WorkloadKind, image ports.RegistryImage) error {
	expected := ""
	switch kind {
	case ports.WorkloadKindVM:
		expected = "system"
	case ports.WorkloadKindContainer:
		expected = "container"
	case ports.WorkloadKindGPUContainer:
		expected = "gpu"
	case ports.WorkloadKindSandbox:
		expected = "sandbox"
	}
	if expected == "" || strings.TrimSpace(image.Purpose) == "" || image.Purpose == expected {
		return nil
	}
	return fmt.Errorf("%w: image purpose %q is not valid for %s instance", ports.ErrConflict, image.Purpose, kind)
}

func (r *LocalInstanceResourceResolver) resolveImage(ctx context.Context, tenantID, imageRef string) (ports.RegistryImage, error) {
	registryHost, project, repository, tag, digest := parseImageReference(imageRef)
	if project != "" && project != tenantID {
		return ports.RegistryImage{}, fmt.Errorf("%w: image project %q does not belong to tenant %q", ports.ErrConflict, project, tenantID)
	}
	result, err := r.registry.ListImages(ctx, ports.RegistryImageListRequest{TenantID: tenantID, Project: tenantID, Repository: repository, Tag: tag})
	if err != nil {
		return ports.RegistryImage{}, fmt.Errorf("resolve instance image %q: %w", imageRef, err)
	}
	for _, item := range result.Items {
		if digest != "" && item.Digest != digest {
			continue
		}
		if registryHost == "" || item.Registry == "" || strings.EqualFold(registryHost, item.Registry) {
			return item, nil
		}
	}
	return ports.RegistryImage{}, fmt.Errorf("%w: image %q was not found for tenant %q", ports.ErrNotFound, imageRef, tenantID)
}

func parseImageReference(value string) (registryHost, project, repository, tag, digest string) {
	value = strings.TrimSpace(value)
	if at := strings.Index(value, "@"); at >= 0 {
		digest = value[at+1:]
		value = value[:at]
	}
	parts := strings.Split(value, "/")
	if len(parts) > 0 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") || parts[0] == "localhost") {
		registryHost = parts[0]
		parts = parts[1:]
	}
	if len(parts) > 1 {
		project = parts[0]
		repository = strings.Join(parts[1:], "/")
	} else if len(parts) == 1 {
		repository = parts[0]
	}
	if colon := strings.LastIndex(repository, ":"); colon >= 0 {
		tag = repository[colon+1:]
		repository = repository[:colon]
	}
	return registryHost, project, repository, tag, digest
}

func (r *LocalInstanceResourceResolver) resolveNetwork(ctx context.Context, tenantID string, spec *ports.WorkloadSpec) ([]string, error) {
	refs := make([]string, 0)
	if strings.TrimSpace(spec.Network.VPCID) != "" {
		vpc, err := r.network.GetVPC(ctx, ports.NetworkResourceGetRequest{TenantID: tenantID, ResourceID: spec.Network.VPCID})
		if err != nil {
			return nil, fmt.Errorf("resolve instance vpc %q: %w", spec.Network.VPCID, err)
		}
		if vpc.State != ports.NetworkResourceAvailable {
			return nil, fmt.Errorf("%w: instance vpc %q is %s", ports.ErrConflict, spec.Network.VPCID, vpc.State)
		}
		refs = append(refs, "vpc/"+vpc.VPCID)
	}
	if strings.TrimSpace(spec.Network.SubnetID) != "" {
		subnet, err := r.network.GetSubnet(ctx, ports.NetworkResourceGetRequest{TenantID: tenantID, ResourceID: spec.Network.SubnetID})
		if err != nil {
			return nil, fmt.Errorf("resolve instance subnet %q: %w", spec.Network.SubnetID, err)
		}
		if subnet.State != ports.NetworkResourceAvailable {
			return nil, fmt.Errorf("%w: instance subnet %q is %s", ports.ErrConflict, spec.Network.SubnetID, subnet.State)
		}
		if spec.Network.VPCID != "" && subnet.VPCID != spec.Network.VPCID {
			return nil, fmt.Errorf("%w: instance subnet %q does not belong to vpc %q", ports.ErrConflict, subnet.SubnetID, spec.Network.VPCID)
		}
		refs = append(refs, "subnet/"+subnet.SubnetID)
	}
	for _, securityGroupID := range spec.Network.SecurityGroupIDs {
		securityGroupID = strings.TrimSpace(securityGroupID)
		if securityGroupID == "" {
			continue
		}
		group, err := r.network.GetSecurityGroup(ctx, ports.NetworkResourceGetRequest{TenantID: tenantID, ResourceID: securityGroupID})
		if err != nil {
			return nil, fmt.Errorf("resolve instance security group %q: %w", securityGroupID, err)
		}
		if group.State != ports.NetworkResourceAvailable {
			return nil, fmt.Errorf("%w: instance security group %q is %s", ports.ErrConflict, securityGroupID, group.State)
		}
		refs = append(refs, "security_group/"+group.SecurityGroupID)
	}
	return refs, nil
}

func (r *LocalInstanceResourceResolver) resolveStorage(ctx context.Context, tenantID string, spec *ports.WorkloadSpec) ([]string, error) {
	refs := make([]string, 0)
	seenVolumes := map[string]struct{}{}
	seenFilesystems := map[string]struct{}{}
	checkVolume := func(volumeID string) error {
		volumeID = strings.TrimSpace(volumeID)
		if volumeID == "" {
			return nil
		}
		if _, ok := seenVolumes[volumeID]; ok {
			return nil
		}
		volume, err := r.storage.GetVolume(ctx, ports.StorageResourceGetRequest{TenantID: tenantID, ResourceID: volumeID})
		if err != nil {
			return fmt.Errorf("resolve instance volume %q: %w", volumeID, err)
		}
		if volume.State != ports.StorageResourceAvailable {
			return fmt.Errorf("%w: instance volume %q is %s", ports.ErrConflict, volumeID, volume.State)
		}
		seenVolumes[volumeID] = struct{}{}
		refs = append(refs, "volume/"+volume.VolumeID)
		return nil
	}
	checkFilesystem := func(filesystemID string) error {
		filesystemID = strings.TrimSpace(filesystemID)
		if filesystemID == "" {
			return nil
		}
		if _, ok := seenFilesystems[filesystemID]; ok {
			return nil
		}
		filesystem, err := r.storage.GetFilesystem(ctx, ports.StorageResourceGetRequest{TenantID: tenantID, ResourceID: filesystemID})
		if err != nil {
			return fmt.Errorf("resolve instance filesystem %q: %w", filesystemID, err)
		}
		if filesystem.State != ports.StorageResourceAvailable {
			return fmt.Errorf("%w: instance filesystem %q is %s", ports.ErrConflict, filesystemID, filesystem.State)
		}
		seenFilesystems[filesystemID] = struct{}{}
		refs = append(refs, "filesystem/"+filesystem.FilesystemID)
		return nil
	}
	for _, attachment := range spec.Storage {
		if attachment.ResourceType == "filesystem" {
			if err := checkFilesystem(attachment.ResourceID); err != nil {
				return nil, err
			}
			continue
		}
		if err := checkVolume(attachment.ResourceID); err != nil {
			return nil, err
		}
	}
	if spec.VM != nil {
		if spec.VM.SystemDisk != nil {
			if err := checkVolume(spec.VM.SystemDisk.VolumeID); err != nil {
				return nil, err
			}
		}
		for _, disk := range spec.VM.DataDiskSpecs {
			if err := checkVolume(disk.VolumeID); err != nil {
				return nil, err
			}
		}
		for _, mount := range spec.VM.FilesystemMounts {
			if err := checkFilesystem(mount.FilesystemID); err != nil {
				return nil, err
			}
		}
	}
	if spec.Container != nil {
		for _, mount := range spec.Container.VolumeMounts {
			if err := checkVolume(mount.VolumeID); err != nil {
				return nil, err
			}
		}
		for _, mount := range spec.Container.FilesystemMounts {
			if err := checkFilesystem(mount.FilesystemID); err != nil {
				return nil, err
			}
		}
	}
	return refs, nil
}

func (r *LocalInstanceResourceResolver) resolveSecrets(ctx context.Context, tenantID string, secretIDs []string) ([]string, error) {
	refs := make([]string, 0)
	seen := map[string]struct{}{}
	for _, secretID := range secretIDs {
		secretID = strings.TrimSpace(secretID)
		if secretID == "" {
			continue
		}
		if _, ok := seen[secretID]; ok {
			continue
		}
		secret, err := r.secrets.GetSecret(ctx, ports.SecretGetRequest{TenantID: tenantID, SecretID: secretID})
		if err != nil {
			return nil, fmt.Errorf("resolve instance secret %q: %w", secretID, err)
		}
		if secret.State != "active" {
			return nil, fmt.Errorf("%w: instance secret %q is %s", ports.ErrConflict, secretID, secret.State)
		}
		seen[secretID] = struct{}{}
		refs = append(refs, "secret/"+secret.SecretID)
	}
	return refs, nil
}

var _ ports.WorkloadInstanceResourceResolver = (*LocalInstanceResourceResolver)(nil)
