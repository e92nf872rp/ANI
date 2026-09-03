package runtime

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

// Image vulnerability gate mode for instance create.
// enforce (default): block non-complete scans and critical/high findings.
// observe: allow create but keep scan audit annotations on the resolved spec.
type ImageVulnGateMode string

const (
	ImageVulnGateEnforce ImageVulnGateMode = "enforce"
	ImageVulnGateObserve ImageVulnGateMode = "observe"

	imageScanStatusAnnotation   = "ani.kubercloud.io/image-scan-status"
	imageScanCriticalAnnotation = "ani.kubercloud.io/image-scan-critical"
	imageScanHighAnnotation     = "ani.kubercloud.io/image-scan-high"
	imageScanMediumAnnotation   = "ani.kubercloud.io/image-scan-medium"
	imageScanLowAnnotation      = "ani.kubercloud.io/image-scan-low"
	imageVulnGateAnnotation     = "ani.kubercloud.io/image-vuln-gate"
)

// LocalInstanceResourceResolver keeps instance creation provider-neutral while
// enforcing that referenced Core resources belong to the tenant and are ready.
// The concrete Network/Storage services decide whether the lookup is local or
// backed by a real provider adapter.
type LocalInstanceResourceResolver struct {
	network       ports.NetworkService
	storage       ports.StorageService
	gpuSpecs      ports.GPUSpecService
	registry      ports.ImageRegistry
	secrets       ports.SecretService
	imageVulnGate ImageVulnGateMode
}

func NewLocalInstanceResourceResolver(network ports.NetworkService, storage ports.StorageService, gpuSpecServices ...ports.GPUSpecService) *LocalInstanceResourceResolver {
	var gpuSpecs ports.GPUSpecService
	if len(gpuSpecServices) > 0 {
		gpuSpecs = gpuSpecServices[0]
	}
	return &LocalInstanceResourceResolver{network: network, storage: storage, gpuSpecs: gpuSpecs, imageVulnGate: imageVulnGateFromEnv()}
}

func NewLocalInstanceResourceResolverWithRegistry(network ports.NetworkService, storage ports.StorageService, gpuSpecs ports.GPUSpecService, registry ports.ImageRegistry) *LocalInstanceResourceResolver {
	return &LocalInstanceResourceResolver{network: network, storage: storage, gpuSpecs: gpuSpecs, registry: registry, imageVulnGate: imageVulnGateFromEnv()}
}

func NewLocalInstanceResourceResolverWithDependencies(network ports.NetworkService, storage ports.StorageService, gpuSpecs ports.GPUSpecService, registry ports.ImageRegistry, secrets ports.SecretService) *LocalInstanceResourceResolver {
	return &LocalInstanceResourceResolver{network: network, storage: storage, gpuSpecs: gpuSpecs, registry: registry, secrets: secrets, imageVulnGate: imageVulnGateFromEnv()}
}

func imageVulnGateFromEnv() ImageVulnGateMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ANI_INSTANCE_IMAGE_VULN_GATE"))) {
	case "observe", "off", "allow", "0", "false":
		return ImageVulnGateObserve
	default:
		return ImageVulnGateEnforce
	}
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
		if strings.TrimSpace(resolved.Purpose) == "" {
			resolved.Purpose = inferImagePurpose(resolved.Repository, resolved.Tag)
		}
		if err := validateImagePurposeForInstanceKind(spec.Kind, resolved); err != nil {
			return ports.WorkloadResourceResolveResult{}, err
		}
		if err := r.validateImageScanForCreate(resolved); err != nil {
			return ports.WorkloadResourceResolveResult{}, err
		}
		spec.Image = resolved.Image
		spec.ImageRef = resolved.Image
		spec.ImageID = imageID
		spec.ImageSummary = ports.InstanceImageSummary{ID: imageID, Ref: resolved.Image, Digest: resolved.Digest, Name: resolved.Repository, Tag: resolved.Tag, Purpose: resolved.Purpose}
		spec.Annotations = withImageScanAuditAnnotations(spec.Annotations, resolved.ScanStatus, r.imageVulnGate)
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
	return fmt.Errorf("%w: ImagePurposeMismatch: image purpose %q is not valid for %s instance", ports.ErrConflict, image.Purpose, kind)
}

func (r *LocalInstanceResourceResolver) validateImageScanForCreate(image ports.RegistryImage) error {
	scan := image.ScanStatus
	switch scan.Status {
	case ports.RegistryScanComplete:
		// continue to vulnerability checks
	case ports.RegistryScanNotScanned, ports.RegistryScanPending, ports.RegistryScanRunning, ports.RegistryScanFailed, "":
		if r.imageVulnGate == ImageVulnGateObserve {
			return nil
		}
		status := scan.Status
		if status == "" {
			status = ports.RegistryScanNotScanned
		}
		return fmt.Errorf("%w: ImageScanning: image %q scan status is %s", ports.ErrFailedPrecondition, image.Image, status)
	default:
		if r.imageVulnGate == ImageVulnGateObserve {
			return nil
		}
		return fmt.Errorf("%w: ImageScanning: image %q scan status is %s", ports.ErrFailedPrecondition, image.Image, scan.Status)
	}
	if scan.Critical > 0 || scan.High > 0 {
		if r.imageVulnGate == ImageVulnGateObserve {
			return nil
		}
		return fmt.Errorf("%w: ImageVulnerabilityBlocked: image %q has critical=%d high=%d", ports.ErrFailedPrecondition, image.Image, scan.Critical, scan.High)
	}
	return nil
}

func withImageScanAuditAnnotations(annotations map[string]string, scan ports.RegistryScanResult, gate ImageVulnGateMode) map[string]string {
	if annotations == nil {
		annotations = map[string]string{}
	} else {
		cloned := make(map[string]string, len(annotations)+6)
		for key, value := range annotations {
			cloned[key] = value
		}
		annotations = cloned
	}
	status := string(scan.Status)
	if status == "" {
		status = string(ports.RegistryScanNotScanned)
	}
	gateMode := string(gate)
	if gateMode == "" {
		gateMode = string(ImageVulnGateEnforce)
	}
	annotations[imageScanStatusAnnotation] = status
	annotations[imageScanCriticalAnnotation] = strconv.Itoa(scan.Critical)
	annotations[imageScanHighAnnotation] = strconv.Itoa(scan.High)
	annotations[imageScanMediumAnnotation] = strconv.Itoa(scan.Medium)
	annotations[imageScanLowAnnotation] = strconv.Itoa(scan.Low)
	annotations[imageVulnGateAnnotation] = gateMode
	return annotations
}

// inferImagePurpose mirrors Harbor/local registry naming fallback for historical
// artifacts that lack ani-purpose-* provider labels.
func inferImagePurpose(repository, tag string) string {
	value := strings.ToLower(strings.TrimSpace(repository) + ":" + strings.TrimSpace(tag))
	switch {
	case strings.HasPrefix(value, "gpu") || strings.Contains(value, "/gpu"):
		return "gpu"
	case strings.HasPrefix(value, "sandbox") || strings.Contains(value, "/sandbox"):
		return "sandbox"
	case strings.HasPrefix(value, "system") || strings.Contains(value, "/system"):
		return "system"
	default:
		return "container"
	}
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
	return ports.RegistryImage{}, fmt.Errorf("%w: ImageNotFound: image %q was not found for tenant %q", ports.ErrNotFound, imageRef, tenantID)
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
		// WaitForFirstConsumer PVCs remain Pending until a consumer Pod mounts them.
		// For container create-time orchestration, Pending means the PVC intent exists
		// and is attachable; Failed/Deleting/Deleted remain reject states.
		if volume.State != ports.StorageResourceAvailable && volume.State != ports.StorageResourcePending {
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
		// WaitForFirstConsumer PVCs remain Pending until a consumer Pod mounts
		// them. Mounting the filesystem is itself the first consumer for RWX
		// PVCs, so Pending means the PVC intent exists and is attachable;
		// Failed/Deleting/Deleted remain reject states. Without this allowance
		// a WFFC filesystem can never be attached by any instance.
		if filesystem.State != ports.StorageResourceAvailable && filesystem.State != ports.StorageResourcePending {
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
			spec.Storage = upsertResolvedStorageAttachment(spec.Storage, ports.WorkloadStorageAttachment{
				Name:         storageMountName("volume", mount.VolumeID),
				Kind:         ports.StorageAttachmentSharedPVC,
				ResourceType: "volume",
				ResourceID:   strings.TrimSpace(mount.VolumeID),
				MountPath:    strings.TrimSpace(mount.MountPath),
				ReadOnly:     mount.ReadOnly,
				SourceRef:    storageProviderName("vol", mount.VolumeID),
				Required:     true,
				Status:       "resolved",
			})
		}
		for _, mount := range spec.Container.FilesystemMounts {
			if err := checkFilesystem(mount.FilesystemID); err != nil {
				return nil, err
			}
			spec.Storage = upsertResolvedStorageAttachment(spec.Storage, ports.WorkloadStorageAttachment{
				Name:         storageMountName("filesystem", mount.FilesystemID),
				Kind:         ports.StorageAttachmentSharedPVC,
				ResourceType: "filesystem",
				ResourceID:   strings.TrimSpace(mount.FilesystemID),
				MountPath:    strings.TrimSpace(mount.MountPath),
				ReadOnly:     mount.ReadOnly,
				SourceRef:    storageProviderName("fs", mount.FilesystemID),
				Required:     true,
				Status:       "resolved",
			})
		}
	}
	return refs, nil
}

func upsertResolvedStorageAttachment(items []ports.WorkloadStorageAttachment, next ports.WorkloadStorageAttachment) []ports.WorkloadStorageAttachment {
	if strings.TrimSpace(next.ResourceID) == "" || strings.TrimSpace(next.MountPath) == "" {
		return items
	}
	for i, item := range items {
		if item.ResourceType == next.ResourceType && item.ResourceID == next.ResourceID && item.MountPath == next.MountPath {
			items[i] = next
			return items
		}
	}
	return append(items, next)
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
