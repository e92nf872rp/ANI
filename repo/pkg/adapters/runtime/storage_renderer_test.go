package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestKubernetesStorageRendererRendersVolumePVC(t *testing.T) {
	renderer := NewKubernetesStorageRenderer()

	manifests, err := renderer.RenderVolume(context.Background(), ports.StorageVolumeRecord{
		TenantID:     "tenant-a",
		VolumeID:     "vol_data",
		Name:         "data",
		SizeGiB:      100,
		StorageClass: "fast",
		State:        ports.StorageResourceAvailable,
	})
	if err != nil {
		t.Fatalf("RenderVolume() error = %v", err)
	}
	content := manifests[0].Content
	for _, want := range []string{`"kind": "PersistentVolumeClaim"`, `"storage": "100Gi"`, `"storageClassName": "fast"`, "vol-vol-data", "ani-tenant-tenant-a", "ReadWriteOnce"} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered volume PVC missing %q:\n%s", want, content)
		}
	}
	dryRun, err := NewLocalProviderDryRun().DryRun(context.Background(), manifests, ports.WorkloadAdmissionResult{Allowed: true})
	if err != nil {
		t.Fatalf("DryRun(rendered PVC) error = %v", err)
	}
	if !dryRun.Accepted {
		t.Fatalf("DryRun(rendered PVC) accepted = false, reason = %s", dryRun.Reason)
	}
}

func TestKubernetesStorageRendererRendersVolumeMode(t *testing.T) {
	renderer := NewKubernetesStorageRenderer()
	cases := []struct {
		name       string
		volumeMode string
		want       string
	}{
		{name: "block", volumeMode: ports.StorageVolumeModeBlock, want: `"volumeMode": "Block"`},
		{name: "filesystem", volumeMode: ports.StorageVolumeModeFilesystem, want: `"volumeMode": "Filesystem"`},
		// 存量卷 volume_mode 为空时回退 Filesystem，保护既有 PVC 的 re-observe。
		{name: "empty falls back to filesystem", volumeMode: "", want: `"volumeMode": "Filesystem"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifests, err := renderer.RenderVolume(context.Background(), ports.StorageVolumeRecord{
				TenantID:     "tenant-a",
				VolumeID:     "vol_data",
				Name:         "data",
				SizeGiB:      100,
				StorageClass: "ani-block",
				VolumeMode:   tc.volumeMode,
				State:        ports.StorageResourceAvailable,
			})
			if err != nil {
				t.Fatalf("RenderVolume() error = %v", err)
			}
			if content := manifests[0].Content; !strings.Contains(content, tc.want) {
				t.Fatalf("rendered volume PVC missing %q:\n%s", tc.want, content)
			}
		})
	}
}

func TestKubernetesStorageRendererRendersFilesystemPVC(t *testing.T) {
	renderer := NewKubernetesStorageRenderer()

	manifests, err := renderer.RenderFilesystem(context.Background(), ports.StorageFilesystemRecord{
		TenantID:     "tenant-a",
		FilesystemID: "fs_shared",
		Name:         "shared",
		Protocol:     "cephfs",
		SizeGiB:      500,
		Endpoint:     "local://shared",
		State:        ports.StorageResourceAvailable,
	})
	if err != nil {
		t.Fatalf("RenderFilesystem() error = %v", err)
	}
	content := manifests[0].Content
	for _, want := range []string{`"kind": "PersistentVolumeClaim"`, `"storage": "500Gi"`, `"storageClassName": "cephfs"`, "ReadWriteMany", "filesystem-protocol", "local://shared"} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered filesystem PVC missing %q:\n%s", want, content)
		}
	}
}

func TestKubernetesStorageRendererRendersObjectMetadataIntent(t *testing.T) {
	renderer := NewKubernetesStorageRenderer()

	manifests, err := renderer.RenderObject(context.Background(), ports.StorageObjectRecord{
		TenantID:    "tenant-a",
		ObjectID:    "obj_model",
		Bucket:      "models",
		Key:         "llm/model.bin",
		SizeBytes:   1024,
		ContentType: "application/octet-stream",
		State:       ports.StorageResourceAvailable,
	})
	if err != nil {
		t.Fatalf("RenderObject() error = %v", err)
	}
	if manifests[0].Provider != "objectstore" || manifests[0].Kind != "ObjectMetadata" {
		t.Fatalf("manifest = %#v, want objectstore ObjectMetadata", manifests[0])
	}
	content := manifests[0].Content
	for _, want := range []string{`"kind": "ObjectMetadata"`, `"bucket": "models"`, `"key": "llm/model.bin"`, `"sizeBytes": 1024`, "obj-obj-model"} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered object metadata missing %q:\n%s", want, content)
		}
	}
}

func TestKubernetesStorageRendererRendersVolumeSnapshot(t *testing.T) {
	renderer := NewKubernetesStorageRenderer()

	manifests, err := renderer.RenderVolumeSnapshot(context.Background(), ports.VolumeSnapshotRecord{
		TenantID:   "tenant-a",
		SnapshotID: "snap_daily",
		VolumeID:   "vol_data",
		Name:       "daily",
		Status:     ports.VolumeSnapshotCreating,
		SizeBytes:  8 * 1024 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("RenderVolumeSnapshot() error = %v", err)
	}
	if manifests[0].Provider != "kubernetes" || manifests[0].Kind != "VolumeSnapshot" {
		t.Fatalf("manifest = %#v, want kubernetes VolumeSnapshot", manifests[0])
	}
	content := manifests[0].Content
	for _, want := range []string{`"apiVersion": "snapshot.storage.k8s.io/v1"`, `"kind": "VolumeSnapshot"`, "snap-snap-daily", "ani-tenant-tenant-a", "vol-vol-data"} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered volume snapshot missing %q:\n%s", want, content)
		}
	}
}

func TestKubernetesStorageRendererRendersFilesystemMountTargetService(t *testing.T) {
	renderer := NewKubernetesStorageRenderer()

	manifests, err := renderer.RenderFilesystemMountTarget(context.Background(), ports.FilesystemMountTargetRecord{
		TenantID:      "tenant-a",
		MountTargetID: "mt_shared",
		FilesystemID:  "fs_shared",
		SubnetID:      "subnet-a",
		IPAddress:     "10.0.1.25",
		Status:        ports.MountTargetCreating,
	})
	if err != nil {
		t.Fatalf("RenderFilesystemMountTarget() error = %v", err)
	}
	if manifests[0].Provider != "kubernetes" || manifests[0].Kind != "Service" {
		t.Fatalf("manifest = %#v, want kubernetes Service", manifests[0])
	}
	content := manifests[0].Content
	for _, want := range []string{`"kind": "Service"`, `"type": "ClusterIP"`, "mt-mt-shared", "ani-tenant-tenant-a", "fs_shared", "10.0.1.25"} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered mount target missing %q:\n%s", want, content)
		}
	}
}
