package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

type KubernetesStorageRenderer struct{}

// defaultVolumeStorageClassName is the Kubernetes StorageClass used when a
// volume request does not declare one. The legacy "standard" fallback has no
// matching StorageClass in target clusters, which leaves volume PVCs Pending
// forever and blocks VM/instance scheduling; ani-block matches the sandbox
// workspace precedent.
const defaultVolumeStorageClassName = "ani-block"

func NewKubernetesStorageRenderer() *KubernetesStorageRenderer {
	return &KubernetesStorageRenderer{}
}

func (r *KubernetesStorageRenderer) RenderVolume(_ context.Context, record ports.StorageVolumeRecord) ([]ports.WorkloadManifest, error) {
	if err := requireStorageRecord(record.TenantID, record.VolumeID, record.Name, record.State); err != nil {
		return nil, err
	}
	if record.SizeGiB <= 0 {
		return nil, fmt.Errorf("%w: volume size_gib must be greater than zero", ports.ErrInvalid)
	}
	name := storageProviderName("vol", record.VolumeID)
	content := manifest(map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"metadata":   storageProviderNamespacedMetadata(record.TenantID, name, "volume", record.VolumeID),
		"spec": map[string]any{
			"accessModes":      []any{"ReadWriteOnce"},
			"storageClassName": firstNetworkNonEmpty(record.StorageClass, defaultVolumeStorageClassName),
			"volumeMode":       "Filesystem",
			"resources":        pvcStorageResources(record.SizeGiB),
		},
	})
	return []ports.WorkloadManifest{{Name: name, Kind: "PersistentVolumeClaim", Provider: "kubernetes", Content: content}}, nil
}

func (r *KubernetesStorageRenderer) RenderFilesystem(_ context.Context, record ports.StorageFilesystemRecord) ([]ports.WorkloadManifest, error) {
	if err := requireStorageRecord(record.TenantID, record.FilesystemID, record.Name, record.State); err != nil {
		return nil, err
	}
	if record.SizeGiB <= 0 {
		return nil, fmt.Errorf("%w: filesystem size_gib must be greater than zero", ports.ErrInvalid)
	}
	protocol := strings.ToLower(strings.TrimSpace(record.Protocol))
	if protocol == "" {
		protocol = "nfs"
	}
	if protocol != "nfs" && protocol != "cephfs" {
		return nil, fmt.Errorf("%w: unsupported filesystem protocol %q", ports.ErrUnsupported, record.Protocol)
	}
	name := storageProviderName("fs", record.FilesystemID)
	content := manifest(map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"metadata":   storageProviderFilesystemMetadata(record, name, protocol),
		"spec": map[string]any{
			"accessModes":      []any{"ReadWriteMany"},
			"storageClassName": filesystemStorageClass(record, protocol),
			"volumeMode":       "Filesystem",
			"resources":        pvcStorageResources(record.SizeGiB),
		},
	})
	return []ports.WorkloadManifest{{Name: name, Kind: "PersistentVolumeClaim", Provider: "kubernetes", Content: content}}, nil
}

func (r *KubernetesStorageRenderer) RenderObject(_ context.Context, record ports.StorageObjectRecord) ([]ports.WorkloadManifest, error) {
	if strings.TrimSpace(record.TenantID) == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.ObjectID) == "" {
		return nil, fmt.Errorf("%w: object id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.Bucket) == "" || strings.TrimSpace(record.Key) == "" {
		return nil, fmt.Errorf("%w: bucket and key are required", ports.ErrInvalid)
	}
	if record.State == "" {
		return nil, fmt.Errorf("%w: state is required", ports.ErrInvalid)
	}
	if record.SizeBytes < 0 {
		return nil, fmt.Errorf("%w: object size_bytes must not be negative", ports.ErrInvalid)
	}
	name := storageProviderName("obj", record.ObjectID)
	content := manifest(map[string]any{
		"apiVersion": "ani.kubercloud.io/v1alpha1",
		"kind":       "ObjectMetadata",
		"metadata": map[string]any{
			"name":   name,
			"labels": storageProviderLabels(record.TenantID, "object", record.ObjectID),
		},
		"spec": map[string]any{
			"tenantID":    record.TenantID,
			"bucket":      record.Bucket,
			"key":         record.Key,
			"sizeBytes":   record.SizeBytes,
			"contentType": firstNetworkNonEmpty(record.ContentType, "application/octet-stream"),
		},
	})
	return []ports.WorkloadManifest{{Name: name, Kind: "ObjectMetadata", Provider: "objectstore", Content: content}}, nil
}

func (r *KubernetesStorageRenderer) RenderVolumeSnapshot(_ context.Context, record ports.VolumeSnapshotRecord) ([]ports.WorkloadManifest, error) {
	if strings.TrimSpace(record.TenantID) == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.SnapshotID) == "" {
		return nil, fmt.Errorf("%w: snapshot id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.VolumeID) == "" {
		return nil, fmt.Errorf("%w: volume id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.Name) == "" {
		return nil, fmt.Errorf("%w: snapshot name is required", ports.ErrInvalid)
	}
	if record.Status == "" {
		return nil, fmt.Errorf("%w: snapshot status is required", ports.ErrInvalid)
	}
	if record.SizeBytes < 0 {
		return nil, fmt.Errorf("%w: snapshot size_bytes must not be negative", ports.ErrInvalid)
	}
	name := storageProviderName("snap", record.SnapshotID)
	content := manifest(map[string]any{
		"apiVersion": "snapshot.storage.k8s.io/v1",
		"kind":       "VolumeSnapshot",
		"metadata":   storageProviderNamespacedMetadata(record.TenantID, name, "volume_snapshot", record.SnapshotID),
		"spec": map[string]any{
			"source": map[string]any{
				"persistentVolumeClaimName": storageProviderName("vol", record.VolumeID),
			},
		},
	})
	return []ports.WorkloadManifest{{Name: name, Kind: "VolumeSnapshot", Provider: "kubernetes", Content: content}}, nil
}

func (r *KubernetesStorageRenderer) RenderFilesystemMountTarget(_ context.Context, record ports.FilesystemMountTargetRecord) ([]ports.WorkloadManifest, error) {
	if strings.TrimSpace(record.TenantID) == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.MountTargetID) == "" {
		return nil, fmt.Errorf("%w: mount target id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.FilesystemID) == "" {
		return nil, fmt.Errorf("%w: filesystem id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.SubnetID) == "" {
		return nil, fmt.Errorf("%w: subnet id is required", ports.ErrInvalid)
	}
	if record.Status == "" {
		return nil, fmt.Errorf("%w: mount target status is required", ports.ErrInvalid)
	}
	name := storageProviderName("mt", record.MountTargetID)
	metadata := storageProviderNamespacedMetadata(record.TenantID, name, "filesystem_mount_target", record.MountTargetID)
	metadata["annotations"] = map[string]string{
		"ani.kubercloud.io/filesystem-id":    record.FilesystemID,
		"ani.kubercloud.io/subnet-id":        record.SubnetID,
		"ani.kubercloud.io/mount-target-ip":  record.IPAddress,
		"ani.kubercloud.io/storage-contract": "filesystem-mount-target",
	}
	content := manifest(map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   metadata,
		"spec": map[string]any{
			"type": "ClusterIP",
			"selector": map[string]string{
				"ani.kubercloud.io/storage-kind":     "filesystem",
				"ani.kubercloud.io/storage-resource": record.FilesystemID,
			},
			"ports": []any{
				map[string]any{
					"name":       "nfs",
					"port":       2049,
					"targetPort": 2049,
					"protocol":   "TCP",
				},
			},
		},
	})
	return []ports.WorkloadManifest{{Name: name, Kind: "Service", Provider: "kubernetes", Content: content}}, nil
}

func storageProviderNamespacedMetadata(tenantID string, name string, resourceKind string, resourceID string) map[string]any {
	return map[string]any{
		"name":      name,
		"namespace": tenantNamespace(tenantID),
		"labels":    storageProviderLabels(tenantID, resourceKind, resourceID),
	}
}

func storageProviderFilesystemMetadata(record ports.StorageFilesystemRecord, name string, protocol string) map[string]any {
	metadata := storageProviderNamespacedMetadata(record.TenantID, name, "filesystem", record.FilesystemID)
	metadata["annotations"] = map[string]string{
		"ani.kubercloud.io/filesystem-protocol": protocol,
		"ani.kubercloud.io/filesystem-endpoint": record.Endpoint,
	}
	return metadata
}

func storageProviderLabels(tenantID string, resourceKind string, resourceID string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/part-of":          "ani-platform",
		"app.kubernetes.io/managed-by":       "ani-core",
		"ani.kubercloud.io/tenant-id":        tenantID,
		"ani.kubercloud.io/storage-kind":     resourceKind,
		"ani.kubercloud.io/storage-resource": resourceID,
	}
}

func pvcStorageResources(sizeGiB int64) map[string]any {
	return map[string]any{
		"requests": map[string]any{
			"storage": fmt.Sprintf("%dGi", sizeGiB),
		},
	}
}

func filesystemStorageClass(record ports.StorageFilesystemRecord, protocol string) string {
	if protocol == "cephfs" {
		return firstNetworkNonEmpty(record.Protocol, "cephfs")
	}
	return firstNetworkNonEmpty(record.Protocol, "nfs")
}

func storageProviderName(prefix string, value string) string {
	return networkProviderName(prefix, value)
}

var _ ports.StorageProviderRenderer = (*KubernetesStorageRenderer)(nil)
