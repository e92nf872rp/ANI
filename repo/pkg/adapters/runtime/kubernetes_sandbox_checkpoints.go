package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

const (
	sandboxCheckpointLabelManaged    = "ani.kubercloud.io/sandbox-checkpoint"
	sandboxCheckpointLabelTenant     = "ani.kubercloud.io/tenant-id"
	sandboxCheckpointLabelInstanceID = "ani.kubercloud.io/sandbox-instance-id"
	sandboxCheckpointLabelID         = "ani.kubercloud.io/sandbox-checkpoint-id"
	sandboxCheckpointAnnotationName  = "ani.kubercloud.io/sandbox-checkpoint-name"
	sandboxCheckpointPollBudget      = 60 * time.Second
	sandboxCheckpointPollInterval    = 500 * time.Millisecond
)

type sandboxVolumeSnapshot struct {
	Metadata struct {
		Name              string            `json:"name"`
		CreationTimestamp string            `json:"creationTimestamp"`
		Labels            map[string]string `json:"labels"`
		Annotations       map[string]string `json:"annotations"`
	} `json:"metadata"`
	Status struct {
		ReadyToUse  bool   `json:"readyToUse"`
		RestoreSize string `json:"restoreSize"`
		Error       *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"status"`
}

func (r *KubernetesSandboxRuntime) createWorkspaceCheckpoint(ctx context.Context, request ports.SandboxCheckpointCreateRequest, instance ports.SandboxInstanceStatus) (ports.SandboxCheckpointResult, error) {
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: checkpoint name is required", ports.ErrInvalid)
	}
	pvcRef, ok := sandboxWorkspacePVCRef(instance.ResourceRefs)
	if !ok {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: legacy emptyDir sandbox must be recreated before checkpoint", ports.ErrUnsupported)
	}
	if instance.State != ports.SandboxStateRunning {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: sandbox checkpoint requires running sandbox", ports.ErrFailedPrecondition)
	}
	pvc, err := resourceFromRef("kubernetes", tenantNamespace(request.TenantID), pvcRef)
	if err != nil {
		return ports.SandboxCheckpointResult{}, err
	}
	checkpointID := sandboxCheckpointID(request.TenantID, request.InstanceID, request.IdempotencyKey)
	snapshotName := sandboxCheckpointObjectName(checkpointID)
	createdAt := firstNonZeroTime(request.RequestedAt, r.now().UTC())

	if err := r.scaleDeployment(ctx, request.TenantID, instance.ResourceRefs, 0); err != nil {
		return ports.SandboxCheckpointResult{}, err
	}
	if err := r.waitSandboxPodsGone(ctx, instance, sandboxCheckpointPollBudget); err != nil {
		_ = r.resumeSandboxAfterCheckpoint(ctx, instance)
		return ports.SandboxCheckpointResult{}, err
	}

	manifestBody := manifest(map[string]any{
		"apiVersion": "snapshot.storage.k8s.io/v1",
		"kind":       "VolumeSnapshot",
		"metadata": map[string]any{
			"name":      snapshotName,
			"namespace": tenantNamespace(request.TenantID),
			"labels": map[string]string{
				sandboxCheckpointLabelManaged:    "true",
				sandboxCheckpointLabelTenant:     request.TenantID,
				sandboxCheckpointLabelInstanceID: request.InstanceID,
				sandboxCheckpointLabelID:         checkpointID,
			},
			"annotations": map[string]string{sandboxCheckpointAnnotationName: name},
		},
		"spec": map[string]any{
			"source": map[string]any{"persistentVolumeClaimName": pvc.Name},
		},
	})
	_, applyErr := r.client.ApplyManifests(ctx, []ports.WorkloadManifest{{
		Name: snapshotName, Kind: "VolumeSnapshot", Provider: "kubernetes", Content: manifestBody,
	}})
	if applyErr != nil {
		return ports.SandboxCheckpointResult{}, r.checkpointErrorWithResume(ctx, instance, applyErr)
	}
	result, waitErr := r.waitWorkspaceCheckpoint(ctx, request.TenantID, request.InstanceID, checkpointID, sandboxCheckpointPollBudget)
	if waitErr != nil {
		return ports.SandboxCheckpointResult{}, r.checkpointErrorWithResume(ctx, instance, waitErr)
	}
	if err := r.resumeSandboxAfterCheckpoint(ctx, instance); err != nil {
		return ports.SandboxCheckpointResult{}, err
	}
	if result.CreatedAt.IsZero() {
		result.CreatedAt = createdAt
	}
	return result, nil
}

func (r *KubernetesSandboxRuntime) listWorkspaceCheckpoints(ctx context.Context, request ports.SandboxCheckpointListRequest, instance ports.SandboxInstanceStatus) (ports.SandboxCheckpointListResult, error) {
	if _, ok := sandboxWorkspacePVCRef(instance.ResourceRefs); !ok {
		return ports.SandboxCheckpointListResult{}, fmt.Errorf("%w: legacy emptyDir sandbox must be recreated before checkpoint", ports.ErrUnsupported)
	}
	selector := url.Values{}
	selector.Set("labelSelector", strings.Join([]string{
		sandboxCheckpointLabelManaged + "=true",
		sandboxCheckpointLabelTenant + "=" + request.TenantID,
		sandboxCheckpointLabelInstanceID + "=" + request.InstanceID,
	}, ","))
	resource, _ := resourceMapping("kubernetes", "", "VolumeSnapshot")
	resource.Namespace = tenantNamespace(request.TenantID)
	endpoint := r.client.host + resource.collectionPath() + "?" + selector.Encode()
	body, err := r.client.doIdempotent(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		return ports.SandboxCheckpointListResult{}, err
	}
	var list struct {
		Items []sandboxVolumeSnapshot `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return ports.SandboxCheckpointListResult{}, fmt.Errorf("%w: decode sandbox checkpoints: %v", ports.ErrInvalid, err)
	}
	items := make([]ports.SandboxCheckpointResult, 0, len(list.Items))
	for _, snapshot := range list.Items {
		result, ok := sandboxCheckpointFromSnapshot(snapshot, request.TenantID, request.InstanceID)
		if ok {
			items = append(items, result)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return paginateSandboxCheckpoints(items, request.Limit, request.Cursor)
}

func (r *KubernetesSandboxRuntime) restoreWorkspaceCheckpoint(ctx context.Context, request ports.SandboxCheckpointRestoreRequest, instance ports.SandboxInstanceStatus) (ports.SandboxCheckpointResult, error) {
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	pvcRef, ok := sandboxWorkspacePVCRef(instance.ResourceRefs)
	if !ok {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: legacy emptyDir sandbox must be recreated before checkpoint restore", ports.ErrUnsupported)
	}
	checkpoint, err := r.getWorkspaceCheckpoint(ctx, request.TenantID, request.InstanceID, request.CheckpointID)
	if err != nil {
		return ports.SandboxCheckpointResult{}, err
	}
	if checkpoint.Status != "available" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: checkpoint is not available", ports.ErrFailedPrecondition)
	}
	pvc, err := resourceFromRef("kubernetes", tenantNamespace(request.TenantID), pvcRef)
	if err != nil {
		return ports.SandboxCheckpointResult{}, err
	}
	wasRunning := instance.State == ports.SandboxStateRunning
	if wasRunning {
		if err := r.scaleDeployment(ctx, request.TenantID, instance.ResourceRefs, 0); err != nil {
			return ports.SandboxCheckpointResult{}, err
		}
	}
	if err := r.waitSandboxPodsGone(ctx, instance, sandboxCheckpointPollBudget); err != nil {
		return ports.SandboxCheckpointResult{}, err
	}
	if _, status, err := r.client.Do(ctx, http.MethodDelete, r.client.resourceURL(pvc, ""), "", nil); err != nil && status != http.StatusNotFound {
		return ports.SandboxCheckpointResult{}, err
	}
	if err := r.waitSandboxPVCDeleted(ctx, pvc, sandboxCheckpointPollBudget); err != nil {
		return ports.SandboxCheckpointResult{}, err
	}
	pvcBody := manifest(map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"metadata": map[string]any{
			"name":      pvc.Name,
			"namespace": pvc.Namespace,
		},
		"spec": map[string]any{
			"accessModes": []any{"ReadWriteOnce"},
			"resources": map[string]any{
				"requests": map[string]any{"storage": "5Gi"},
			},
			"dataSource": map[string]any{
				"apiGroup": "snapshot.storage.k8s.io",
				"kind":     "VolumeSnapshot",
				"name":     sandboxCheckpointObjectName(checkpoint.ID),
			},
		},
	})
	if _, err := r.client.ApplyManifests(ctx, []ports.WorkloadManifest{{
		Name: pvc.Name, Kind: "PersistentVolumeClaim", Provider: "kubernetes", Content: pvcBody,
	}}); err != nil {
		return ports.SandboxCheckpointResult{}, err
	}
	if wasRunning {
		if err := r.resumeSandboxAfterCheckpoint(ctx, instance); err != nil {
			return ports.SandboxCheckpointResult{}, err
		}
	}
	return checkpoint, nil
}

func (r *KubernetesSandboxRuntime) cloneWorkspaceCheckpoint(ctx context.Context, request ports.SandboxCheckpointCloneRequest, instance ports.SandboxInstanceStatus) (ports.SandboxCheckpointResult, error) {
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: clone name is required", ports.ErrInvalid)
	}
	if _, ok := sandboxWorkspacePVCRef(instance.ResourceRefs); !ok {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: legacy emptyDir sandbox must be recreated before checkpoint clone", ports.ErrUnsupported)
	}
	checkpoint, err := r.getWorkspaceCheckpoint(ctx, request.TenantID, request.InstanceID, request.CheckpointID)
	if err != nil {
		return ports.SandboxCheckpointResult{}, err
	}
	if checkpoint.Status != "available" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: checkpoint is not available", ports.ErrFailedPrecondition)
	}
	checkpoint.Name = name
	return checkpoint, nil
}

func (r *KubernetesSandboxRuntime) cleanupWorkspaceCheckpoints(ctx context.Context, tenantID, instanceID string) error {
	selector := url.Values{}
	selector.Set("labelSelector", strings.Join([]string{
		sandboxCheckpointLabelManaged + "=true",
		sandboxCheckpointLabelTenant + "=" + tenantID,
		sandboxCheckpointLabelInstanceID + "=" + instanceID,
	}, ","))
	resource, _ := resourceMapping("kubernetes", "", "VolumeSnapshot")
	resource.Namespace = tenantNamespace(tenantID)
	body, err := r.client.doIdempotent(ctx, http.MethodGet, r.client.host+resource.collectionPath()+"?"+selector.Encode(), "", nil)
	if err != nil {
		return err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return fmt.Errorf("%w: decode sandbox checkpoints for cleanup: %v", ports.ErrInvalid, err)
	}
	for _, item := range list.Items {
		name := strings.TrimSpace(item.Metadata.Name)
		if name == "" {
			continue
		}
		resource.Name = name
		if _, status, err := r.client.Do(ctx, http.MethodDelete, r.client.resourceURL(resource, ""), "", nil); err != nil && status != http.StatusNotFound {
			return err
		}
	}
	return nil
}

func (r *KubernetesSandboxRuntime) waitSandboxPVCDeleted(ctx context.Context, pvc kubernetesResource, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	for {
		_, err := r.client.doIdempotent(ctx, http.MethodGet, r.client.resourceURL(pvc, ""), "", nil)
		if isKubernetesNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%w: sandbox workspace PVC did not terminate for checkpoint restore", ports.ErrFailedPrecondition)
		}
		if err := checkpointPollWait(ctx); err != nil {
			return err
		}
	}
}

func (r *KubernetesSandboxRuntime) getWorkspaceCheckpoint(ctx context.Context, tenantID, instanceID, checkpointID string) (ports.SandboxCheckpointResult, error) {
	checkpointID = strings.TrimSpace(checkpointID)
	if checkpointID == "" {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: checkpointID is required", ports.ErrInvalid)
	}
	resource, _ := resourceMapping("kubernetes", "", "VolumeSnapshot")
	resource.Namespace = tenantNamespace(tenantID)
	resource.Name = sandboxCheckpointObjectName(checkpointID)
	body, err := r.client.doIdempotent(ctx, http.MethodGet, r.client.resourceURL(resource, ""), "", nil)
	if err != nil {
		if isKubernetesNotFound(err) {
			return ports.SandboxCheckpointResult{}, ports.ErrNotFound
		}
		return ports.SandboxCheckpointResult{}, err
	}
	var snapshot sandboxVolumeSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: decode sandbox checkpoint: %v", ports.ErrInvalid, err)
	}
	result, ok := sandboxCheckpointFromSnapshot(snapshot, tenantID, instanceID)
	if !ok || result.ID != checkpointID {
		return ports.SandboxCheckpointResult{}, ports.ErrNotFound
	}
	return result, nil
}

func (r *KubernetesSandboxRuntime) waitWorkspaceCheckpoint(ctx context.Context, tenantID, instanceID, checkpointID string, budget time.Duration) (ports.SandboxCheckpointResult, error) {
	deadline := time.Now().Add(budget)
	for {
		result, err := r.getWorkspaceCheckpoint(ctx, tenantID, instanceID, checkpointID)
		if err != nil {
			return ports.SandboxCheckpointResult{}, err
		}
		switch result.Status {
		case "available":
			return result, nil
		case "failed":
			return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: checkpoint failed: %s", ports.ErrFailedPrecondition, result.Reason)
		}
		if !time.Now().Before(deadline) {
			return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: checkpoint did not become ready", ports.ErrFailedPrecondition)
		}
		if err := checkpointPollWait(ctx); err != nil {
			return ports.SandboxCheckpointResult{}, err
		}
	}
}

func (r *KubernetesSandboxRuntime) waitSandboxPodsGone(ctx context.Context, instance ports.SandboxInstanceStatus, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	for {
		selector := url.Values{}
		selector.Set("labelSelector", fmt.Sprintf("ani.kubercloud.io/tenant-id=%s,ani.kubercloud.io/instance=%s", instance.TenantID, instance.Name))
		endpoint := r.client.host + "/api/v1/namespaces/" + url.PathEscape(tenantNamespace(instance.TenantID)) + "/pods?" + selector.Encode()
		body, err := r.client.doIdempotent(ctx, http.MethodGet, endpoint, "", nil)
		if err != nil {
			return err
		}
		var list struct {
			Items []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			return fmt.Errorf("%w: decode sandbox pod list: %v", ports.ErrInvalid, err)
		}
		if len(list.Items) == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%w: sandbox pods did not terminate for checkpoint", ports.ErrFailedPrecondition)
		}
		if err := checkpointPollWait(ctx); err != nil {
			return err
		}
	}
}

func (r *KubernetesSandboxRuntime) resumeSandboxAfterCheckpoint(ctx context.Context, instance ports.SandboxInstanceStatus) error {
	if err := r.scaleDeployment(ctx, instance.TenantID, instance.ResourceRefs, 1); err != nil {
		return err
	}
	_, _, err := r.waitReadySandboxPod(ctx, instance, sandboxCheckpointPollBudget)
	return err
}

func (r *KubernetesSandboxRuntime) checkpointErrorWithResume(ctx context.Context, instance ports.SandboxInstanceStatus, checkpointErr error) error {
	if resumeErr := r.resumeSandboxAfterCheckpoint(ctx, instance); resumeErr != nil {
		return errors.Join(checkpointErr, fmt.Errorf("resume sandbox after checkpoint failure: %w", resumeErr))
	}
	return checkpointErr
}

func checkpointPollWait(ctx context.Context) error {
	timer := time.NewTimer(sandboxCheckpointPollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func sandboxCheckpointID(tenantID, instanceID, idempotencyKey string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(tenantID+"\x00"+instanceID+"\x00"+idempotencyKey)).String()
}

func sandboxCheckpointObjectName(checkpointID string) string {
	return "sandbox-checkpoint-" + strings.ToLower(strings.TrimSpace(checkpointID))
}

func sandboxCheckpointFromSnapshot(snapshot sandboxVolumeSnapshot, tenantID, instanceID string) (ports.SandboxCheckpointResult, bool) {
	labels := snapshot.Metadata.Labels
	if labels[sandboxCheckpointLabelManaged] != "true" || labels[sandboxCheckpointLabelTenant] != tenantID || labels[sandboxCheckpointLabelInstanceID] != instanceID {
		return ports.SandboxCheckpointResult{}, false
	}
	id := strings.TrimSpace(labels[sandboxCheckpointLabelID])
	if id == "" || snapshot.Metadata.Name != sandboxCheckpointObjectName(id) {
		return ports.SandboxCheckpointResult{}, false
	}
	createdAt, _ := time.Parse(time.RFC3339, snapshot.Metadata.CreationTimestamp)
	status := "creating"
	reason := "CSI VolumeSnapshot is creating"
	if snapshot.Status.Error != nil {
		status = "failed"
		reason = strings.TrimSpace(snapshot.Status.Error.Message)
	} else if snapshot.Status.ReadyToUse {
		status = "available"
		reason = "CSI VolumeSnapshot is ready"
	}
	return ports.SandboxCheckpointResult{
		ID: id, Name: snapshot.Metadata.Annotations[sandboxCheckpointAnnotationName], Status: status,
		KeepMemory: false, CreatedAt: createdAt, SizeBytes: parseCheckpointSize(snapshot.Status.RestoreSize),
		Reason: reason, ProviderRef: "kubernetes/VolumeSnapshot/" + snapshot.Metadata.Name,
	}, true
}

func paginateSandboxCheckpoints(items []ports.SandboxCheckpointResult, limit int, cursor string) (ports.SandboxCheckpointListResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	start := 0
	if strings.TrimSpace(cursor) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(cursor))
		if err != nil || parsed < 0 {
			return ports.SandboxCheckpointListResult{}, fmt.Errorf("%w: cursor is invalid", ports.ErrInvalid)
		}
		start = parsed
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return ports.SandboxCheckpointListResult{Items: append([]ports.SandboxCheckpointResult(nil), items[start:end]...), Total: len(items), NextCursor: next}, nil
}

func parseCheckpointSize(value string) int64 {
	value = strings.TrimSpace(value)
	multipliers := []struct {
		suffix string
		value  int64
	}{{"Gi", 1 << 30}, {"Mi", 1 << 20}, {"Ki", 1 << 10}}
	for _, multiplier := range multipliers {
		if strings.HasSuffix(value, multiplier.suffix) {
			number, err := strconv.ParseInt(strings.TrimSuffix(value, multiplier.suffix), 10, 64)
			if err == nil && number >= 0 {
				return number * multiplier.value
			}
			return 0
		}
	}
	number, _ := strconv.ParseInt(value, 10, 64)
	return number
}
