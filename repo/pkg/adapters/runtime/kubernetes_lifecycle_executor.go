package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/adapters/resilience"
	"github.com/kubercloud/ani/pkg/ports"
)

type KubernetesLifecycleExecutor struct {
	client  *KubernetesRESTClient
	enabled bool
	now     func() time.Time
	// translator converts a target GPUSpec spec_id into Volcano scheduling
	// fragments (nodeSelector/schedulerName/resourceRequests/queue annotation)
	// used to rebuild the workload on resize. nil disables spec_id resize.
	translator *VolcanoResourceTranslator
}

type KubernetesLifecycleOption func(*KubernetesLifecycleExecutor)

func WithKubernetesLifecycleEnabled(enabled bool) KubernetesLifecycleOption {
	return func(executor *KubernetesLifecycleExecutor) {
		executor.enabled = enabled
	}
}

func WithKubernetesLifecycleTranslator(translator *VolcanoResourceTranslator) KubernetesLifecycleOption {
	return func(executor *KubernetesLifecycleExecutor) {
		executor.translator = translator
	}
}

func WithKubernetesLifecycleClock(now func() time.Time) KubernetesLifecycleOption {
	return func(executor *KubernetesLifecycleExecutor) {
		if now != nil {
			executor.now = now
		}
	}
}

func NewKubernetesLifecycleExecutor(client *KubernetesRESTClient, options ...KubernetesLifecycleOption) *KubernetesLifecycleExecutor {
	executor := &KubernetesLifecycleExecutor{client: client, now: time.Now}
	for _, option := range options {
		option(executor)
	}
	return executor
}

func (e *KubernetesLifecycleExecutor) Apply(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest, record ports.WorkloadInstanceRecord) (ports.WorkloadInstanceLifecycleResult, error) {
	if err := validateLifecycleExecutionRequest(request, record); err != nil {
		return ports.WorkloadInstanceLifecycleResult{}, err
	}
	if !e.enabled {
		return ports.WorkloadInstanceLifecycleResult{
			Action:    request.Action,
			Accepted:  false,
			Reason:    "kubernetes lifecycle execution is disabled by execution switch",
			CheckedAt: e.now().UTC(),
		}, nil
	}
	if e.client == nil {
		return ports.WorkloadInstanceLifecycleResult{}, ports.ErrNotConfigured
	}

	if request.Action == ports.WorkloadLifecycleDelete {
		if err := e.deleteResources(ctx, record); err != nil {
			return ports.WorkloadInstanceLifecycleResult{}, err
		}
		return ports.WorkloadInstanceLifecycleResult{
			Action:    request.Action,
			Accepted:  true,
			Reason:    "accepted by Kubernetes lifecycle executor",
			CheckedAt: e.now().UTC(),
		}, nil
	}

	if request.Action == ports.WorkloadLifecycleResize {
		if err := e.applyResize(ctx, request, record); err != nil {
			return ports.WorkloadInstanceLifecycleResult{}, err
		}
		return ports.WorkloadInstanceLifecycleResult{
			Action:    request.Action,
			Accepted:  true,
			Reason:    "resized by Kubernetes lifecycle executor (targeted patch)",
			CheckedAt: e.now().UTC(),
		}, nil
	}

	if request.Action == ports.WorkloadLifecycleAttachVolume || request.Action == ports.WorkloadLifecycleDetachVolume {
		if err := e.applyKubeVirtVolume(ctx, request, record); err != nil {
			return ports.WorkloadInstanceLifecycleResult{}, err
		}
		return ports.WorkloadInstanceLifecycleResult{
			Action:    request.Action,
			Accepted:  true,
			Reason:    "volume change accepted by KubeVirt lifecycle executor",
			CheckedAt: e.now().UTC(),
		}, nil
	}

	if request.Action == ports.WorkloadLifecycleSnapshot && record.Kind == ports.WorkloadKindVM {
		if err := e.applyKubeVirtSnapshot(ctx, request, record); err != nil {
			return ports.WorkloadInstanceLifecycleResult{}, err
		}
		return ports.WorkloadInstanceLifecycleResult{
			Action:    request.Action,
			Accepted:  true,
			Reason:    "snapshot accepted by KubeVirt lifecycle executor",
			CheckedAt: e.now().UTC(),
		}, nil
	}

	if request.Action == ports.WorkloadLifecycleRollback && record.Kind == ports.WorkloadKindVM {
		if err := e.applyKubeVirtRestore(ctx, request, record); err != nil {
			return ports.WorkloadInstanceLifecycleResult{}, err
		}
		return ports.WorkloadInstanceLifecycleResult{
			Action:    request.Action,
			Accepted:  true,
			Reason:    "restore accepted by KubeVirt lifecycle executor",
			CheckedAt: e.now().UTC(),
		}, nil
	}

	if request.Action == ports.WorkloadLifecycleRebuild && record.Kind == ports.WorkloadKindVM {
		if err := e.applyKubeVirtRebuild(ctx, request, record); err != nil {
			return ports.WorkloadInstanceLifecycleResult{}, err
		}
		return ports.WorkloadInstanceLifecycleResult{
			Action:    request.Action,
			Accepted:  true,
			Reason:    "rebuild accepted by KubeVirt lifecycle executor",
			CheckedAt: e.now().UTC(),
		}, nil
	}

	if request.Action == ports.WorkloadLifecycleAttachFilesystem || request.Action == ports.WorkloadLifecycleDetachFilesystem {
		if err := e.applyKubeVirtFilesystem(ctx, request, record); err != nil {
			return ports.WorkloadInstanceLifecycleResult{}, err
		}
		return ports.WorkloadInstanceLifecycleResult{
			Action:    request.Action,
			Accepted:  true,
			Reason:    "filesystem change accepted by KubeVirt lifecycle executor",
			CheckedAt: e.now().UTC(),
		}, nil
	}

	resource, err := resourceFromRecord(record)
	if err != nil {
		return ports.WorkloadInstanceLifecycleResult{}, err
	}
	if err := e.execute(ctx, request.Action, resource, replicasFromRequest(request.Replicas)); err != nil {
		return ports.WorkloadInstanceLifecycleResult{}, err
	}
	return ports.WorkloadInstanceLifecycleResult{
		Action:    request.Action,
		Accepted:  true,
		Reason:    "accepted by Kubernetes lifecycle executor",
		CheckedAt: e.now().UTC(),
	}, nil
}

func (e *KubernetesLifecycleExecutor) applyKubeVirtVolume(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest, record ports.WorkloadInstanceRecord) error {
	if record.Kind != ports.WorkloadKindVM {
		return fmt.Errorf("%w: Kubernetes volume lifecycle execution is only supported for vm instances", ports.ErrUnsupported)
	}
	resource, err := kubeVirtVMResourceFromRecord(record)
	if err != nil {
		return err
	}
	volumeID := strings.TrimSpace(request.VolumeID)
	if volumeID == "" {
		return fmt.Errorf("%w: volume_id is required for KubeVirt volume lifecycle execution", ports.ErrInvalid)
	}
	volumeName := kubeVirtVolumeName(record, volumeID)
	var body []byte
	switch request.Action {
	case ports.WorkloadLifecycleAttachVolume:
		body, err = json.Marshal(map[string]any{
			"name": volumeName,
			"disk": map[string]any{
				"disk": map[string]any{"bus": "virtio"},
			},
			"volumeSource": map[string]any{
				"persistentVolumeClaim": map[string]any{
					"claimName": storageProviderName("vol", volumeID),
					"readOnly":  request.ReadOnly != nil && *request.ReadOnly,
				},
			},
		})
	case ports.WorkloadLifecycleDetachVolume:
		body, err = json.Marshal(map[string]any{"name": volumeName})
	default:
		return fmt.Errorf("%w: unsupported KubeVirt volume lifecycle action %q", ports.ErrUnsupported, request.Action)
	}
	if err != nil {
		return fmt.Errorf("%w: marshal KubeVirt volume request: %v", ports.ErrInvalid, err)
	}
	subresource := "addvolume"
	if request.Action == ports.WorkloadLifecycleDetachVolume {
		subresource = "removevolume"
	}
	_, err = e.client.do(ctx, http.MethodPut, e.client.host+kubeVirtVMSubresourcePath(resource.Namespace, resource.Name, subresource), "application/json", body)
	return err
}

// applyKubeVirtFilesystem attaches/detaches a shared filesystem PVC
// (NFS/CephFS) to a VM via virtiofs. virtiofs cannot be hot-plugged through
// the addvolume subresource (a shared-filesystem PVC presented as a virtio
// disk lacks disk.img and crashes the VMI), so the spec is rewritten instead:
// a running VM is stopped first, the spec is re-applied with (or without) the
// virtiofs device plus its backing volume, and the VM is started again. The
// flow runs detached from the request context (same as rollback/rebuild) so a
// client disconnect cannot leave the VM stopped.
func (e *KubernetesLifecycleExecutor) applyKubeVirtFilesystem(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest, record ports.WorkloadInstanceRecord) error {
	if record.Kind != ports.WorkloadKindVM {
		return fmt.Errorf("%w: Kubernetes filesystem lifecycle execution is only supported for vm instances", ports.ErrUnsupported)
	}
	vm, err := kubeVirtVMResourceFromRecord(record)
	if err != nil {
		return err
	}
	filesystemID := strings.TrimSpace(request.FilesystemID)
	if filesystemID == "" {
		return fmt.Errorf("%w: filesystem_id is required for KubeVirt filesystem lifecycle execution", ports.ErrInvalid)
	}
	attach := request.Action == ports.WorkloadLifecycleAttachFilesystem
	if attach && strings.TrimSpace(request.MountPath) == "" {
		return fmt.Errorf("%w: mount_path is required to attach a filesystem", ports.ErrInvalid)
	}
	// Volume name doubles as the virtiofs tag the guest mounts by.
	volumeName := kubeVirtFilesystemVolumeName(filesystemID)
	claimName := storageProviderName("fs", filesystemID)

	ctx = context.WithoutCancel(ctx)
	wasRunning := false
	if running, err := e.kubeVirtVMRunning(ctx, vm); err == nil && running {
		wasRunning = true
		if err := e.stop(ctx, vm); err != nil {
			return fmt.Errorf("stop VM %q before filesystem %s: %w", vm.Name, request.Action, err)
		}
		if err := e.waitKubeVirtVMStopped(ctx, vm); err != nil {
			// Best-effort: try to power the VM back on before surfacing
			// the wait failure, otherwise it stays stopped.
			if startErr := e.start(ctx, vm); startErr != nil {
				return fmt.Errorf("%w (VM %q left stopped: start after wait failure also failed: %v)", err, vm.Name, startErr)
			}
			return err
		}
	}
	// Anything that fails after the stop above would otherwise leave a
	// previously running VM powered off; best-effort restore before returning.
	restoreRunning := func(cause error) error {
		if !wasRunning {
			return cause
		}
		if startErr := e.start(ctx, vm); startErr != nil {
			return fmt.Errorf("%w (VM %q left stopped: start after failure also failed: %v)", cause, vm.Name, startErr)
		}
		return cause
	}
	body, err := e.client.do(ctx, http.MethodGet, e.client.resourceURL(vm, ""), "", nil)
	if err != nil {
		return restoreRunning(fmt.Errorf("read VM %q spec for filesystem %s: %w", vm.Name, request.Action, err))
	}
	var doc map[string]any
	if json.Unmarshal(body, &doc) != nil {
		return restoreRunning(fmt.Errorf("%w: VM %q spec is not valid JSON", ports.ErrInvalid, vm.Name))
	}
	spec, ok := doc["spec"].(map[string]any)
	if !ok {
		return restoreRunning(fmt.Errorf("%w: VM %q has no spec for filesystem %s", ports.ErrInvalid, vm.Name, request.Action))
	}
	applyKubeVirtFilesystemMutation(spec, volumeName, claimName, attach)
	metadata := map[string]any{"name": vm.Name, "namespace": vm.Namespace}
	if src, ok := doc["metadata"].(map[string]any); ok {
		for _, key := range []string{"labels", "annotations"} {
			if value, ok := src[key].(map[string]any); ok && len(value) > 0 {
				metadata[key] = value
			}
		}
	}
	manifest, err := json.Marshal(map[string]any{
		"apiVersion": "kubevirt.io/v1",
		"kind":       "VirtualMachine",
		"metadata":   metadata,
		"spec":       spec,
	})
	if err != nil {
		return restoreRunning(fmt.Errorf("%w: marshal VirtualMachine manifest for filesystem %s: %v", ports.ErrInvalid, request.Action, err))
	}
	query := "fieldManager=" + url.QueryEscape(e.client.fieldManager) + "&force=true"
	if _, err := e.client.do(ctx, http.MethodPatch, e.client.resourceURL(vm, query), kubernetesApplyPatchContentType, manifest); err != nil {
		return restoreRunning(fmt.Errorf("apply VM %q spec for filesystem %s: %w", vm.Name, request.Action, err))
	}
	// The stop above (when needed) left spec.running=false, so the explicit
	// start owns power-on; a previously stopped VM stays stopped.
	if wasRunning {
		if err := e.start(ctx, vm); err != nil {
			return fmt.Errorf("start VM %q after filesystem %s: %w", vm.Name, request.Action, err)
		}
	}
	return nil
}

// kubeVirtFilesystemVolumeName derives the KubeVirt volume/filesystem device
// name for a filesystem attach. The name doubles as the virtiofs mount tag,
// and QEMU rejects tags longer than 36 bytes, so dashes are dropped from UUID
// ids and the result is truncated to the limit. The mapping is deterministic
// so detach finds the entry added by attach.
func kubeVirtFilesystemVolumeName(filesystemID string) string {
	name := strings.ToLower(strings.TrimSpace(filesystemID))
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, "-", "")
	name = "fs-" + name
	if len(name) > 36 {
		name = name[:36]
	}
	return name
}

// applyKubeVirtFilesystemMutation rewrites the VM pod template in place,
// replacing any prior entry for the volume so repeated attach/detach calls are
// idempotent.
func applyKubeVirtFilesystemMutation(spec map[string]any, volumeName string, claimName string, attach bool) {
	tmpl, ok := spec["template"].(map[string]any)
	if !ok {
		tmpl = map[string]any{}
		spec["template"] = tmpl
	}
	podSpec, ok := tmpl["spec"].(map[string]any)
	if !ok {
		podSpec = map[string]any{}
		tmpl["spec"] = podSpec
	}
	volumes := kubeVirtRemoveNamedEntry(asAnySlice(podSpec["volumes"]), volumeName)
	if attach {
		volumes = append(volumes, map[string]any{
			"name":                  volumeName,
			"persistentVolumeClaim": map[string]any{"claimName": claimName},
		})
	}
	podSpec["volumes"] = volumes
	domain, ok := podSpec["domain"].(map[string]any)
	if !ok {
		domain = map[string]any{}
		podSpec["domain"] = domain
	}
	devices, ok := domain["devices"].(map[string]any)
	if !ok {
		devices = map[string]any{}
		domain["devices"] = devices
	}
	filesystems := kubeVirtRemoveNamedEntry(asAnySlice(devices["filesystems"]), volumeName)
	if attach {
		filesystems = append(filesystems, map[string]any{
			"name":     volumeName,
			"virtiofs": map[string]any{},
		})
	}
	devices["filesystems"] = filesystems
}

func asAnySlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func kubeVirtRemoveNamedEntry(items []any, name string) []any {
	kept := make([]any, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if ok && entry["name"] == name {
			continue
		}
		kept = append(kept, item)
	}
	return kept
}

func kubeVirtVMResourceFromRecord(record ports.WorkloadInstanceRecord) (kubernetesResource, error) {
	namespace := tenantNamespace(record.TenantID)
	for _, ref := range record.ResourceRefs {
		resource, err := resourceFromRef("", namespace, ref)
		if err != nil {
			continue
		}
		if resource.Provider == "kubevirt" && resource.Kind == "VirtualMachine" && strings.TrimSpace(resource.Name) != "" {
			return resource, nil
		}
	}
	return kubernetesResource{}, fmt.Errorf("%w: KubeVirt VirtualMachine resource ref is required for volume lifecycle execution", ports.ErrInvalid)
}

const (
	kubeVirtSnapshotAPIVersion = "snapshot.kubevirt.io/v1beta1"
	kubeVirtSnapshotPoll       = 3 * time.Second
	kubeVirtSnapshotTimeout    = 5 * time.Minute
	// CSI RBD volume restore is slow: a ~20Gi root-volume rollback measured
	// 5m13s on the real lab, so allow generous headroom.
	kubeVirtRestoreTimeout = 15 * time.Minute
)

// applyKubeVirtSnapshot creates a VirtualMachineSnapshot CR named after the
// instance snapshot record ID, then waits until KubeVirt reports the snapshot
// Succeeded. The CR name doubles as the rollback target, so the record ID and
// the provider snapshot stay 1:1.
func (e *KubernetesLifecycleExecutor) applyKubeVirtSnapshot(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest, record ports.WorkloadInstanceRecord) error {
	vm, err := kubeVirtVMResourceFromRecord(record)
	if err != nil {
		return err
	}
	name := e.kubeVirtSnapshotCRName(request, record)
	snapshot := kubernetesResource{
		Provider: "kubevirt", APIGroup: "snapshot.kubevirt.io", APIVersion: "v1beta1",
		Resource: "virtualmachinesnapshots", Kind: "VirtualMachineSnapshot",
		Namespaced: true, Namespace: vm.Namespace, Name: name,
	}
	body, err := json.Marshal(map[string]any{
		"apiVersion": kubeVirtSnapshotAPIVersion,
		"kind":       "VirtualMachineSnapshot",
		"metadata":   map[string]any{"name": name, "namespace": vm.Namespace},
		"spec": map[string]any{
			"source": map[string]any{"apiGroup": "kubevirt.io", "kind": "VirtualMachine", "name": vm.Name},
		},
	})
	if err != nil {
		return fmt.Errorf("%w: marshal VirtualMachineSnapshot manifest: %v", ports.ErrInvalid, err)
	}
	query := "fieldManager=" + url.QueryEscape(e.client.fieldManager) + "&force=true"
	if _, err := e.client.do(ctx, http.MethodPatch, e.client.resourceURL(snapshot, query), kubernetesApplyPatchContentType, body); err != nil {
		return fmt.Errorf("apply VirtualMachineSnapshot %q: %w", name, err)
	}
	return e.waitKubeVirtCRPhase(ctx, snapshot, "Succeeded", kubeVirtSnapshotTimeout, "snapshot")
}

// applyKubeVirtRestore restores the VM from the VirtualMachineSnapshot that
// was created for the record snapshot ID. Snapshots recorded before
// provider-backed snapshots existed have no cluster counterpart and are
// rejected with a clear conflict instead of a raw 404.
//
// KubeVirt refuses to restore a running VM ("Waiting for target VM to be
// powered off ... will fail after 5m0s"), so a running target is stopped and
// waited on first, then started again after the restore completes. The whole
// flow runs detached from the request context so a client disconnect cannot
// leave the VM powered off mid-restore.
func (e *KubernetesLifecycleExecutor) applyKubeVirtRestore(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest, record ports.WorkloadInstanceRecord) error {
	vm, err := kubeVirtVMResourceFromRecord(record)
	if err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	// Legacy record IDs may contain underscores (pre DNS-1123 IDs); normalize
	// so the lookup name matches how the snapshot CR was created.
	snapshotName := strings.ToLower(sanitizeSnapshotID(strings.TrimSpace(request.SnapshotID)))
	if snapshotName == "" {
		return fmt.Errorf("%w: snapshot_id is required for VM rollback", ports.ErrInvalid)
	}
	snapshot := kubernetesResource{
		Provider: "kubevirt", APIGroup: "snapshot.kubevirt.io", APIVersion: "v1beta1",
		Resource: "virtualmachinesnapshots", Kind: "VirtualMachineSnapshot",
		Namespaced: true, Namespace: vm.Namespace, Name: snapshotName,
	}
	if _, status, err := e.client.Do(ctx, http.MethodGet, e.client.resourceURL(snapshot, ""), "", nil); err != nil {
		if status == http.StatusNotFound {
			return fmt.Errorf("%w: provider snapshot %q not found in cluster; only snapshots created after provider-backed snapshots are rollback-able", ports.ErrConflict, snapshotName)
		}
		return err
	}
	wasRunning, err := e.kubeVirtVMRunning(ctx, vm)
	if err != nil {
		return err
	}
	if wasRunning {
		if err := e.stop(ctx, vm); err != nil {
			return fmt.Errorf("stop VM %q before restore: %w", vm.Name, err)
		}
		if err := e.waitKubeVirtVMStopped(ctx, vm); err != nil {
			return err
		}
	}
	if err := e.applyKubeVirtRestoreCR(ctx, vm, snapshotName, request.IdempotencyKey); err != nil {
		return err
	}
	if wasRunning {
		if err := e.start(ctx, vm); err != nil {
			return fmt.Errorf("start VM %q after restore: %w", vm.Name, err)
		}
	}
	return nil
}

// kubeVirtVMRunning reports whether the VirtualMachine currently reports
// printableStatus Running.
func (e *KubernetesLifecycleExecutor) kubeVirtVMRunning(ctx context.Context, vm kubernetesResource) (bool, error) {
	body, err := e.client.do(ctx, http.MethodGet, e.client.resourceURL(vm, ""), "", nil)
	if err != nil {
		return false, err
	}
	var doc map[string]any
	if json.Unmarshal(body, &doc) != nil {
		return false, nil
	}
	return phaseFromKubernetesObject(vm, doc) == "Running", nil
}

// applyKubeVirtRestoreCR applies the VirtualMachineRestore CR and waits for it
// to complete.
func (e *KubernetesLifecycleExecutor) applyKubeVirtRestoreCR(ctx context.Context, vm kubernetesResource, snapshotName string, idempotencyKey string) error {
	// Restore CR name must be unique per rollback attempt: re-applying an
	// already Completed VirtualMachineRestore would succeed without actually
	// restoring again. The idempotency key keeps replays idempotent (same CR)
	// while distinct requests get a fresh restore.
	seed := snapshotIDPattern.ReplaceAllString(snapshotName+"-"+strings.TrimSpace(idempotencyKey), "-")
	if len(seed) > 200 {
		seed = seed[:200]
	}
	restoreName := "restore-" + strings.ToLower(strings.Trim(seed, "-"))
	restore := kubernetesResource{
		Provider: "kubevirt", APIGroup: "snapshot.kubevirt.io", APIVersion: "v1beta1",
		Resource: "virtualmachinerestores", Kind: "VirtualMachineRestore",
		Namespaced: true, Namespace: vm.Namespace, Name: restoreName,
	}
	body, err := json.Marshal(map[string]any{
		"apiVersion": kubeVirtSnapshotAPIVersion,
		"kind":       "VirtualMachineRestore",
		"metadata":   map[string]any{"name": restoreName, "namespace": vm.Namespace},
		"spec": map[string]any{
			"target":                     map[string]any{"apiGroup": "kubevirt.io", "kind": "VirtualMachine", "name": vm.Name},
			"virtualMachineSnapshotName": snapshotName,
		},
	})
	if err != nil {
		return fmt.Errorf("%w: marshal VirtualMachineRestore manifest: %v", ports.ErrInvalid, err)
	}
	query := "fieldManager=" + url.QueryEscape(e.client.fieldManager) + "&force=true"
	if _, err := e.client.do(ctx, http.MethodPatch, e.client.resourceURL(restore, query), kubernetesApplyPatchContentType, body); err != nil {
		return fmt.Errorf("apply VirtualMachineRestore %q: %w", restoreName, err)
	}
	// VirtualMachineRestore signals completion via status.complete (it has no
	// status.phase), so it needs a dedicated wait.
	return e.waitKubeVirtRestoreComplete(ctx, restore, kubeVirtRestoreTimeout)
}

// applyKubeVirtRebuild recreates the VirtualMachine object from its current
// spec: stop (idempotent) → capture spec → delete the VM CR → wait for it to
// disappear → re-apply the same spec → start again when it was running.
//
// The containerDisk system disk is image-derived, so the recreated VMI boots a
// fresh system disk ("重装系统"); data volume claims are untouched and
// re-attach automatically. The flow runs detached from the request context so
// a client disconnect cannot leave the VM deleted or powered off.
func (e *KubernetesLifecycleExecutor) applyKubeVirtRebuild(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest, record ports.WorkloadInstanceRecord) error {
	vm, err := kubeVirtVMResourceFromRecord(record)
	if err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	body, status, err := e.client.Do(ctx, http.MethodGet, e.client.resourceURL(vm, ""), "", nil)
	if status == http.StatusNotFound {
		return fmt.Errorf("%w: VM %q not found for rebuild", ports.ErrNotFound, vm.Name)
	}
	if err != nil {
		return err
	}
	var current map[string]any
	if json.Unmarshal(body, &current) != nil {
		current = nil
	}
	wasRunning := phaseFromKubernetesObject(vm, current) == "Running"
	if wasRunning {
		if err := e.stop(ctx, vm); err != nil {
			return fmt.Errorf("stop VM %q before rebuild: %w", vm.Name, err)
		}
		if err := e.waitKubeVirtVMStopped(ctx, vm); err != nil {
			return err
		}
	}
	// Capture the spec after the stop: spec.running now reflects the stopped
	// desired state, so re-applying it cannot race the controller into an
	// unwanted boot; the explicit start below owns power-on.
	body, err = e.client.do(ctx, http.MethodGet, e.client.resourceURL(vm, ""), "", nil)
	if err != nil {
		return fmt.Errorf("read VM %q spec for rebuild: %w", vm.Name, err)
	}
	var doc map[string]any
	if json.Unmarshal(body, &doc) != nil {
		return fmt.Errorf("%w: VM %q spec is not valid JSON", ports.ErrInvalid, vm.Name)
	}
	spec, ok := doc["spec"].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: VM %q has no spec to rebuild from", ports.ErrInvalid, vm.Name)
	}
	// The re-apply manifest must carry name/namespace explicitly: server-side
	// apply on the resource URL derives the object name from the body, and the
	// captured metadata keeps only carry-over fields.
	metadata := map[string]any{"name": vm.Name, "namespace": vm.Namespace}
	if src, ok := doc["metadata"].(map[string]any); ok {
		for _, key := range []string{"labels", "annotations"} {
			if value, ok := src[key].(map[string]any); ok && len(value) > 0 {
				metadata[key] = value
			}
		}
	}
	if _, _, err := e.client.Do(ctx, http.MethodDelete, e.client.resourceURL(vm, ""), "", nil); err != nil {
		return fmt.Errorf("delete VM %q for rebuild: %w", vm.Name, err)
	}
	if err := e.waitKubeVirtVMGone(ctx, vm); err != nil {
		return err
	}
	manifest, err := json.Marshal(map[string]any{
		"apiVersion": "kubevirt.io/v1",
		"kind":       "VirtualMachine",
		"metadata":   metadata,
		"spec":       spec,
	})
	if err != nil {
		return fmt.Errorf("%w: marshal rebuilt VirtualMachine manifest: %v", ports.ErrInvalid, err)
	}
	// Re-apply must create the object from scratch: the captured spec is used
	// verbatim via a forced server-side apply on the deleted-then-recreated CR.
	query := "fieldManager=" + url.QueryEscape(e.client.fieldManager) + "&force=true"
	if _, err := e.client.do(ctx, http.MethodPatch, e.client.resourceURL(vm, query), kubernetesApplyPatchContentType, manifest); err != nil {
		return fmt.Errorf("re-apply VM %q after rebuild: %w", vm.Name, err)
	}
	if wasRunning {
		if err := e.start(ctx, vm); err != nil {
			return fmt.Errorf("start VM %q after rebuild: %w", vm.Name, err)
		}
	}
	return nil
}

// waitKubeVirtVMGone polls until the VirtualMachine object disappears so the
// re-apply creates a fresh object instead of patching the old one.
func (e *KubernetesLifecycleExecutor) waitKubeVirtVMGone(ctx context.Context, resource kubernetesResource) error {
	deadline := e.now().Add(kubeVirtRestartStopTimeout)
	for {
		_, status, err := e.client.Do(ctx, http.MethodGet, e.client.resourceURL(resource, ""), "", nil)
		if status == http.StatusNotFound {
			return nil
		}
		if err != nil {
			return fmt.Errorf("poll VM %q deletion: %w", resource.Name, err)
		}
		if !e.now().Before(deadline) {
			return fmt.Errorf("%w: kubevirt VM %q was not deleted within %s during rebuild", ports.ErrConflict, resource.Name, kubeVirtRestartStopTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(kubeVirtRestartPollInterval):
		}
	}
}

// waitKubeVirtRestoreComplete polls a VirtualMachineRestore until
// status.complete becomes true. Failed restores keep complete=false, so they
// surface as a timeout conflict with the CR still inspectable in the cluster.
func (e *KubernetesLifecycleExecutor) waitKubeVirtRestoreComplete(ctx context.Context, resource kubernetesResource, timeout time.Duration) error {
	deadline := e.now().Add(timeout)
	for {
		body, err := e.client.do(ctx, http.MethodGet, e.client.resourceURL(resource, ""), "", nil)
		if err == nil {
			var doc map[string]any
			if json.Unmarshal(body, &doc) == nil {
				status, _ := doc["status"].(map[string]any)
				if complete, _ := status["complete"].(bool); complete {
					return nil
				}
			}
		}
		if !e.now().Before(deadline) {
			return fmt.Errorf("%w: kubevirt restore %q did not complete within %s", ports.ErrConflict, resource.Name, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(kubeVirtSnapshotPoll):
		}
	}
}

// kubeVirtSnapshotCRName mirrors the snapshot record ID generated by
// vmSnapshotFor so the instance record and the cluster CR refer to the same
// object; CR names must be lowercase DNS-1123.
func (e *KubernetesLifecycleExecutor) kubeVirtSnapshotCRName(request ports.WorkloadInstanceLifecycleRequest, record ports.WorkloadInstanceRecord) string {
	idSeed := strings.TrimSpace(request.SnapshotID)
	if idSeed == "" {
		now := firstNonZeroTime(request.RequestedAt, e.now())
		name := firstNonEmpty(strings.TrimSpace(request.SnapshotName), "snapshot-"+now.Format("20060102150405"))
		idSeed = firstNonEmpty(strings.TrimSpace(request.IdempotencyKey), record.InstanceID+"-"+name+"-"+now.Format("20060102150405"))
	}
	return strings.ToLower(sanitizeSnapshotID(idSeed))
}

// waitKubeVirtCRPhase polls a snapshot.kubevirt.io CR until status.phase
// reaches want, treating Failed as an immediate conflict.
func (e *KubernetesLifecycleExecutor) waitKubeVirtCRPhase(ctx context.Context, resource kubernetesResource, want string, timeout time.Duration, noun string) error {
	deadline := e.now().Add(timeout)
	for {
		body, err := e.client.do(ctx, http.MethodGet, e.client.resourceURL(resource, ""), "", nil)
		if err == nil {
			var doc map[string]any
			if json.Unmarshal(body, &doc) == nil {
				switch kubeVirtCRPhase(doc) {
				case want:
					return nil
				case "Failed":
					return fmt.Errorf("%w: kubevirt %s %q reported Failed", ports.ErrConflict, noun, resource.Name)
				}
			}
		}
		if !e.now().Before(deadline) {
			return fmt.Errorf("%w: kubevirt %s %q did not reach %s within %s", ports.ErrConflict, noun, resource.Name, want, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(kubeVirtSnapshotPoll):
		}
	}
}

func kubeVirtCRPhase(doc map[string]any) string {
	status, _ := doc["status"].(map[string]any)
	phase, _ := status["phase"].(string)
	return phase
}

func kubeVirtVolumeName(record ports.WorkloadInstanceRecord, volumeID string) string {
	for _, attachments := range [][]ports.WorkloadStorageAttachment{record.Status.Storage, record.StorageAttachments} {
		for _, attachment := range attachments {
			if sameVolume(attachment, volumeID) && strings.TrimSpace(attachment.Name) != "" && strings.TrimSpace(attachment.Name) != strings.TrimSpace(volumeID) {
				return strings.TrimSpace(attachment.Name)
			}
		}
	}
	return storageProviderName("volume", volumeID)
}

func (e *KubernetesLifecycleExecutor) deleteResources(ctx context.Context, record ports.WorkloadInstanceRecord) error {
	namespace := tenantNamespace(record.TenantID)
	var deleteErrors []error
	for _, ref := range record.ResourceRefs {
		// Each ref encodes its own provider (kubevirt/... vs kubernetes/Secret/...).
		// Do not force record.Provider onto every ref or mixed-provider cleanup fails.
		resource, err := resourceFromRef("", namespace, ref)
		if err != nil {
			deleteErrors = append(deleteErrors, err)
			continue
		}
		_, status, err := e.client.Do(ctx, http.MethodDelete, e.client.resourceURL(resource, ""), "", nil)
		if err != nil && status != http.StatusNotFound {
			deleteErrors = append(deleteErrors, err)
		}
	}
	return errors.Join(deleteErrors...)
}

func (e *KubernetesLifecycleExecutor) execute(ctx context.Context, action ports.WorkloadLifecycleAction, resource kubernetesResource, replicas int) error {
	switch action {
	case ports.WorkloadLifecycleStart:
		return e.start(ctx, resource)
	case ports.WorkloadLifecycleStop:
		return e.stop(ctx, resource)
	case ports.WorkloadLifecycleRestart:
		return e.restart(ctx, resource)
	case ports.WorkloadLifecycleScale:
		if replicas < 1 {
			return fmt.Errorf("%w: scale replicas must be at least 1, got %d", ports.ErrInvalid, replicas)
		}
		return e.patchScale(ctx, resource, replicas)
	default:
		return fmt.Errorf("%w: unsupported Kubernetes lifecycle action %q", ports.ErrUnsupported, action)
	}
}

// replicasFromRequest returns the target replica count carried on a lifecycle
// request, 0 when unset so callers can validate before applying.
func replicasFromRequest(replicas *int32) int {
	if replicas == nil {
		return 0
	}
	return int(*replicas)
}

func (e *KubernetesLifecycleExecutor) start(ctx context.Context, resource kubernetesResource) error {
	if resource.Kind == "VirtualMachine" {
		// KubeVirt VM lifecycle subresources accept PUT with an empty body.
		_, err := e.client.do(ctx, http.MethodPut, e.client.host+kubeVirtVMSubresourcePath(resource.Namespace, resource.Name, "start"), "", nil)
		return ignoreKubeVirtLifecycleConflict(err, "already running")
	}
	return e.patchScale(ctx, resource, 1)
}

func (e *KubernetesLifecycleExecutor) stop(ctx context.Context, resource kubernetesResource) error {
	if resource.Kind == "VirtualMachine" {
		// KubeVirt VM lifecycle subresources accept PUT with an empty body.
		_, err := e.client.do(ctx, http.MethodPut, e.client.host+kubeVirtVMSubresourcePath(resource.Namespace, resource.Name, "stop"), "", nil)
		return ignoreKubeVirtLifecycleConflict(err, "not running", "is not running", "halted")
	}
	return e.patchScale(ctx, resource, 0)
}

func ignoreKubeVirtLifecycleConflict(err error, messageSnippets ...string) error {
	if err == nil {
		return nil
	}
	var statusErr *resilience.StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusConflict {
		return err
	}
	body := strings.ToLower(statusErr.Body)
	for _, snippet := range messageSnippets {
		if snippet != "" && strings.Contains(body, strings.ToLower(snippet)) {
			return nil
		}
	}
	return err
}

func (e *KubernetesLifecycleExecutor) restart(ctx context.Context, resource kubernetesResource) error {
	if resource.Kind == "VirtualMachine" {
		// ANI VM manifests use legacy spec.running without a runStrategy, so
		// KubeVirt's native restart subresource only performs the stop phase
		// (it patches spec.running=false and no controller restarts the VM).
		// Stop is asynchronous: a start issued while the VM is still shutting
		// down is rejected, which left restarts permanently stopped (VM-09).
		// So: stop, wait until the VM actually stopped, then start again.
		if err := e.stop(ctx, resource); err != nil {
			return err
		}
		if err := e.waitKubeVirtVMStopped(ctx, resource); err != nil {
			return err
		}
		return e.start(ctx, resource)
	}
	body := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"ani.kubercloud.io/restarted-at":%q}}}}}`, e.now().UTC().Format(time.RFC3339))
	_, err := e.client.do(ctx, http.MethodPatch, e.client.resourceURL(resource, ""), "application/merge-patch+json", []byte(body))
	return err
}

const (
	kubeVirtRestartPollInterval = 2 * time.Second
	kubeVirtRestartStopTimeout  = 2 * time.Minute
)

// waitKubeVirtVMStopped polls the VirtualMachine until the graceful shutdown
// triggered by the stop subresource has fully landed (printableStatus reaches
// the terminal "Stopped" state and the VMI is gone), so the follow-up spec
// rewrite and start are guaranteed to be accepted.
func (e *KubernetesLifecycleExecutor) waitKubeVirtVMStopped(ctx context.Context, resource kubernetesResource) error {
	deadline := e.now().Add(kubeVirtRestartStopTimeout)
	for {
		body, err := e.client.do(ctx, http.MethodGet, e.client.resourceURL(resource, ""), "", nil)
		if err == nil {
			var doc map[string]any
			// Require the terminal "Stopped" status, not merely "not
			// Running": during graceful shutdown printableStatus passes
			// through Stopping/Terminating while the VMI is still being
			// deleted, and a spec rewrite + start issued in that window is
			// rejected (the VMI still exists), leaving the VM stopped.
			if json.Unmarshal(body, &doc) == nil && phaseFromKubernetesObject(resource, doc) == "Stopped" {
				return nil
			}
		}
		if !e.now().Before(deadline) {
			return fmt.Errorf("%w: kubevirt VM %q did not stop within %s before restart", ports.ErrConflict, resource.Name, kubeVirtRestartStopTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(kubeVirtRestartPollInterval):
		}
	}
}

// applyResize rerenders the workload in place for a resize action via a
// targeted strategic-merge patch (方案B): it rewrites only the Volcano
// scheduling fragments and container GPU resources triggered by spec_id and
// cpu/memory, leaving env/ports/command and the rest of the Deployment intact.
// The patch content type is strategic-merge so the containers list is keyed by
// name and nested resource maps merge rather than being wholesale replaced.
func (e *KubernetesLifecycleExecutor) applyResize(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest, record ports.WorkloadInstanceRecord) error {
	resource, err := resourceFromRecord(record)
	if err != nil {
		return err
	}
	if resource.Kind == "VirtualMachine" {
		if strings.TrimSpace(request.SpecID) != "" {
			return fmt.Errorf("%w: gpu spec resize is only supported for container and gpu_container instances", ports.ErrUnsupported)
		}
		// VM has no container resources to patch; keep the historical
		// stop+start restart behaviour for cpu/memory-only resize.
		return e.restart(ctx, resource)
	}
	patch, err := e.buildResizePatch(ctx, request, record, resource.Name)
	if err != nil {
		return err
	}
	_, err = e.client.do(ctx, http.MethodPatch, e.client.resourceURL(resource, ""), "application/strategic-merge-patch+json", patch)
	return err
}

// GPU resource keys retained for spec-mode switches. Swapping from vGPU to
// wholecard (or back) must clear the other mode's resource keys, otherwise
// both stale and new GPU resources would be requested simultaneously.
var (
	volcanoVGPUResourceKeys = []string{"volcano.sh/vgpu-number", volcanoVGPUResourceName, "volcano.sh/vgpu-cores"}
	legacyGPUResourceKeys   = []string{"nvidia.com/gpu", "nvidia.com/vgpu"}
)

func (e *KubernetesLifecycleExecutor) buildResizePatch(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest, record ports.WorkloadInstanceRecord, containerName string) ([]byte, error) {
	specID := strings.TrimSpace(request.SpecID)
	requests := map[string]any{}
	limits := map[string]any{}
	podAnnotations := map[string]string{}
	schedulerName := ""
	var nodeSelector map[string]string

	if specID != "" {
		if e.translator == nil {
			return nil, fmt.Errorf("%w: volcano translator is not configured for spec_id resize", ports.ErrNotConfigured)
		}
		count := record.GPU.Count
		if count < 1 {
			count = 1
		}
		translation, err := e.translator.Translate(ctx, specID, record.GPU.QueueName, count)
		if err != nil {
			return nil, err
		}
		wholecard := false
		for key := range translation.ResourceRequests {
			if strings.EqualFold(key, "nvidia.com/gpu") {
				wholecard = true
			}
		}
		stale := legacyGPUResourceKeys
		if wholecard {
			stale = volcanoVGPUResourceKeys
		}
		for _, key := range stale {
			requests[key] = nil
			limits[key] = nil
		}
		for key, value := range translation.ResourceRequests {
			requests[key] = value
			limits[key] = value
		}
		for key, value := range translation.Annotations {
			podAnnotations[key] = value
		}
		if translation.SchedulerName != "" {
			schedulerName = translation.SchedulerName
			podAnnotations["ani.kubercloud.io/scheduler-name"] = translation.SchedulerName
		}
		if len(translation.NodeSelector) > 0 {
			nodeSelector = translation.NodeSelector
			data, _ := json.Marshal(translation.NodeSelector)
			podAnnotations[volcanoNodeSelectorAnnotation] = string(data)
		}
		if len(translation.ResourceRequests) > 0 {
			data, _ := json.Marshal(translation.ResourceRequests)
			podAnnotations[volcanoResourceRequestAnnotation] = string(data)
		}
	}

	if cpu := strings.TrimSpace(request.Resources.CPU); cpu != "" {
		requests["cpu"] = cpu
		limits["cpu"] = cpu
	}
	if memory := strings.TrimSpace(request.Resources.Memory); memory != "" {
		requests["memory"] = memory
		limits["memory"] = memory
	}

	templateSpec := map[string]any{}
	if schedulerName != "" {
		templateSpec["schedulerName"] = schedulerName
	}
	if len(nodeSelector) > 0 {
		// strategic-merge patches maps by merging, so a spec switch (e.g.
		// vGPU -> wholecard) would leave the other mode's node labels behind.
		// $patch: replace makes the nodeSelector wholesale-replaced instead.
		nodeSelector["$patch"] = "replace"
		templateSpec["nodeSelector"] = nodeSelector
	}
	if len(requests) > 0 || len(limits) > 0 {
		templateSpec["containers"] = []any{
			map[string]any{
				"name": containerName,
				"resources": map[string]any{
					"requests": requests,
					"limits":   limits,
				},
			},
		}
	}
	templateMeta := map[string]any{}
	if len(podAnnotations) > 0 {
		templateMeta["annotations"] = podAnnotations
	}
	if len(templateSpec) == 0 && len(templateMeta) == 0 {
		return nil, fmt.Errorf("%w: resize carries no cpu/memory/spec_id change", ports.ErrInvalid)
	}
	template := map[string]any{}
	if len(templateMeta) > 0 {
		template["metadata"] = templateMeta
	}
	if len(templateSpec) > 0 {
		template["spec"] = templateSpec
	}
	return json.Marshal(map[string]any{"spec": map[string]any{"template": template}})
}

func kubeVirtVMSubresourcePath(namespace string, vmName string, subresource string) string {
	return "/apis/subresources.kubevirt.io/v1/namespaces/" + url.PathEscape(namespace) + "/virtualmachines/" + url.PathEscape(vmName) + "/" + url.PathEscape(subresource)
}

func (e *KubernetesLifecycleExecutor) patchScale(ctx context.Context, resource kubernetesResource, replicas int) error {
	endpoint := e.client.host + resource.resourcePath() + "/scale"
	body := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	_, err := e.client.do(ctx, http.MethodPatch, endpoint, "application/merge-patch+json", []byte(body))
	return err
}

func validateLifecycleExecutionRequest(request ports.WorkloadInstanceLifecycleRequest, record ports.WorkloadInstanceRecord) error {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" {
		return fmt.Errorf("%w: tenantID and instanceID are required for lifecycle execution", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.UserID) == "" || strings.TrimSpace(request.PermissionProof) == "" {
		return fmt.Errorf("%w: user id and permission proof are required for lifecycle execution", ports.ErrInvalid)
	}
	if request.TenantID != record.TenantID || request.InstanceID != record.InstanceID {
		return fmt.Errorf("%w: lifecycle request does not match instance record", ports.ErrInvalid)
	}
	if len(record.ResourceRefs) == 0 {
		return fmt.Errorf("%w: resource refs are required for lifecycle execution", ports.ErrInvalid)
	}
	return nil
}

func resourceFromRecord(record ports.WorkloadInstanceRecord) (kubernetesResource, error) {
	namespace := tenantNamespace(record.TenantID)
	provider := record.Provider
	if provider == "" && len(record.ResourceRefs) > 0 {
		provider = strings.Split(record.ResourceRefs[0], "/")[0]
	}
	resource, err := resourceFromRef(provider, namespace, record.ResourceRefs[0])
	if err != nil {
		return kubernetesResource{}, err
	}
	if resource.Name == "" {
		return kubernetesResource{}, fmt.Errorf("%w: lifecycle resource name is required", ports.ErrInvalid)
	}
	return resource, nil
}

var _ ports.WorkloadInstanceLifecycleExecutor = (*KubernetesLifecycleExecutor)(nil)
