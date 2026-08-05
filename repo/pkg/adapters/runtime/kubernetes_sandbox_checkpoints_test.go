package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestKubernetesSandboxCheckpointCreateAndList(t *testing.T) {
	provider := &sandboxCheckpointTransport{running: true}
	runtime := NewKubernetesSandboxRuntime(
		newTestKubernetesRESTClient(t, provider),
		WithKubernetesSandboxApplyEnabled(true),
		WithKubernetesSandboxClock(func() time.Time { return time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC) }),
	)
	execution := checkpointExecutionContext("sandbox-a")
	provider.instanceID = execution.InstanceID

	created, err := runtime.CreateCheckpoint(context.Background(), ports.SandboxCheckpointCreateRequest{
		TenantID: execution.TenantID, InstanceID: execution.InstanceID, Execution: execution,
		IdempotencyKey: "checkpoint-create-a", Name: "before-change",
		RequestedAt: time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateCheckpoint() error = %v", err)
	}
	if _, err := uuid.Parse(created.ID); err != nil {
		t.Fatalf("checkpoint ID = %q, want UUID: %v", created.ID, err)
	}
	if created.Status != "available" || created.Name != "before-change" || created.KeepMemory {
		t.Fatalf("CreateCheckpoint() = %#v", created)
	}
	if created.ProviderRef != "kubernetes/VolumeSnapshot/sandbox-checkpoint-"+created.ID {
		t.Fatalf("ProviderRef = %q", created.ProviderRef)
	}
	if !strings.Contains(provider.snapshotApplyBody, `"persistentVolumeClaimName": "sandbox-a-workspace"`) {
		t.Fatalf("snapshot apply body missing workspace PVC: %s", provider.snapshotApplyBody)
	}
	if !strings.Contains(provider.snapshotApplyBody, `"ani.kubercloud.io/sandbox-instance-id": "`+execution.InstanceID+`"`) {
		t.Fatalf("snapshot apply body missing instance label: %s", provider.snapshotApplyBody)
	}
	if len(provider.scaleBodies) != 2 || !strings.Contains(provider.scaleBodies[0], `"replicas":0`) || !strings.Contains(provider.scaleBodies[1], `"replicas":1`) {
		t.Fatalf("scale bodies = %#v, want 0 then 1", provider.scaleBodies)
	}

	listed, err := runtime.ListCheckpoints(context.Background(), ports.SandboxCheckpointListRequest{
		TenantID: execution.TenantID, InstanceID: execution.InstanceID, Execution: execution, Limit: 50,
	})
	if err != nil {
		t.Fatalf("ListCheckpoints() error = %v", err)
	}
	if listed.Total != 1 || len(listed.Items) != 1 || listed.Items[0].ID != created.ID {
		t.Fatalf("ListCheckpoints() = %#v", listed)
	}
	if !strings.Contains(provider.snapshotListQuery, "labelSelector=") || !strings.Contains(provider.snapshotListQuery, "sandbox-instance-id") {
		t.Fatalf("snapshot list query = %q", provider.snapshotListQuery)
	}
}

func TestKubernetesSandboxCheckpointRestore(t *testing.T) {
	checkpointID := uuid.NewString()
	provider := &sandboxCheckpointRestoreTransport{checkpointID: checkpointID, pvcExists: true, running: true, ready: true}
	runtime := NewKubernetesSandboxRuntime(
		newTestKubernetesRESTClient(t, provider),
		WithKubernetesSandboxApplyEnabled(true),
	)
	execution := checkpointExecutionContext("sandbox-a")
	provider.instanceID = execution.InstanceID

	restored, err := runtime.RestoreCheckpoint(context.Background(), ports.SandboxCheckpointRestoreRequest{
		TenantID: execution.TenantID, InstanceID: execution.InstanceID, Execution: execution,
		CheckpointID: checkpointID, IdempotencyKey: "checkpoint-restore-a",
	})
	if err != nil {
		t.Fatalf("RestoreCheckpoint() error = %v", err)
	}
	if restored.ID != checkpointID || restored.Status != "available" {
		t.Fatalf("RestoreCheckpoint() = %#v", restored)
	}
	wantCalls := []string{"get-snapshot", "scale-0", "list-pods", "delete-pvc", "get-pvc", "apply-pvc", "scale-1", "list-pods"}
	if strings.Join(provider.calls, ",") != strings.Join(wantCalls, ",") {
		t.Fatalf("restore calls = %#v, want %#v", provider.calls, wantCalls)
	}
	if !strings.Contains(provider.pvcApplyBody, `"name": "sandbox-a-workspace"`) ||
		!strings.Contains(provider.pvcApplyBody, `"name": "sandbox-checkpoint-`+checkpointID+`"`) {
		t.Fatalf("restore PVC apply body = %s", provider.pvcApplyBody)
	}
}

func TestKubernetesSandboxCheckpointRestoreRejectsInvalidSnapshot(t *testing.T) {
	tests := []struct {
		name       string
		ready      bool
		wrongOwner bool
		wantErr    error
	}{
		{name: "wrong owner", ready: true, wrongOwner: true, wantErr: ports.ErrNotFound},
		{name: "not ready", wantErr: ports.ErrFailedPrecondition},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checkpointID := uuid.NewString()
			provider := &sandboxCheckpointRestoreTransport{checkpointID: checkpointID, pvcExists: true, running: true, ready: test.ready}
			runtime := NewKubernetesSandboxRuntime(newTestKubernetesRESTClient(t, provider), WithKubernetesSandboxApplyEnabled(true))
			execution := checkpointExecutionContext("sandbox-a")
			provider.instanceID = execution.InstanceID
			if test.wrongOwner {
				provider.instanceID = uuid.NewString()
			}

			_, err := runtime.RestoreCheckpoint(context.Background(), ports.SandboxCheckpointRestoreRequest{
				TenantID: execution.TenantID, InstanceID: execution.InstanceID, Execution: execution,
				CheckpointID: checkpointID, IdempotencyKey: "checkpoint-restore-invalid",
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("RestoreCheckpoint() error = %v, want %v", err, test.wantErr)
			}
			if strings.Join(provider.calls, ",") != "get-snapshot" {
				t.Fatalf("restore calls = %#v, want snapshot validation only", provider.calls)
			}
		})
	}
}

func TestKubernetesSandboxCheckpointRestoreDoesNotResumeAfterPVCFailure(t *testing.T) {
	checkpointID := uuid.NewString()
	provider := &sandboxCheckpointRestoreTransport{checkpointID: checkpointID, pvcExists: true, running: true, ready: true, failPVCApply: true}
	runtime := NewKubernetesSandboxRuntime(newTestKubernetesRESTClient(t, provider), WithKubernetesSandboxApplyEnabled(true))
	execution := checkpointExecutionContext("sandbox-a")
	provider.instanceID = execution.InstanceID

	_, err := runtime.RestoreCheckpoint(context.Background(), ports.SandboxCheckpointRestoreRequest{
		TenantID: execution.TenantID, InstanceID: execution.InstanceID, Execution: execution,
		CheckpointID: checkpointID, IdempotencyKey: "checkpoint-restore-failed-pvc",
	})
	if err == nil {
		t.Fatal("RestoreCheckpoint() error = nil, want PVC apply failure")
	}
	if strings.Contains(strings.Join(provider.calls, ","), "scale-1") {
		t.Fatalf("restore calls = %#v, must not resume after PVC apply failure", provider.calls)
	}
}

func TestKubernetesSandboxCheckpointRestorePreservesPausedState(t *testing.T) {
	checkpointID := uuid.NewString()
	provider := &sandboxCheckpointRestoreTransport{checkpointID: checkpointID, pvcExists: true, ready: true}
	runtime := NewKubernetesSandboxRuntime(newTestKubernetesRESTClient(t, provider), WithKubernetesSandboxApplyEnabled(true))
	execution := checkpointExecutionContext("sandbox-a")
	execution.State = ports.SandboxStatePaused
	execution.SessionState = "paused"
	provider.instanceID = execution.InstanceID

	if _, err := runtime.RestoreCheckpoint(context.Background(), ports.SandboxCheckpointRestoreRequest{
		TenantID: execution.TenantID, InstanceID: execution.InstanceID, Execution: execution,
		CheckpointID: checkpointID, IdempotencyKey: "checkpoint-restore-paused",
	}); err != nil {
		t.Fatalf("RestoreCheckpoint() error = %v", err)
	}
	for _, call := range provider.calls {
		if strings.HasPrefix(call, "scale-") {
			t.Fatalf("restore calls = %#v, paused sandbox must remain scaled down", provider.calls)
		}
	}
}

func TestKubernetesSandboxCheckpointCloneReturnsSnapshotSource(t *testing.T) {
	checkpointID := uuid.NewString()
	provider := &sandboxCheckpointRestoreTransport{checkpointID: checkpointID, ready: true}
	runtime := NewKubernetesSandboxRuntime(newTestKubernetesRESTClient(t, provider), WithKubernetesSandboxApplyEnabled(true))
	execution := checkpointExecutionContext("sandbox-a")
	provider.instanceID = execution.InstanceID

	cloned, err := runtime.CloneCheckpoint(context.Background(), ports.SandboxCheckpointCloneRequest{
		TenantID: execution.TenantID, InstanceID: execution.InstanceID, Execution: execution,
		CheckpointID: checkpointID, IdempotencyKey: "checkpoint-clone-a", Name: "sandbox-clone-a",
	})
	if err != nil {
		t.Fatalf("CloneCheckpoint() error = %v", err)
	}
	if cloned.Name != "sandbox-clone-a" || cloned.ProviderRef != "kubernetes/VolumeSnapshot/sandbox-checkpoint-"+checkpointID {
		t.Fatalf("CloneCheckpoint() = %#v", cloned)
	}
	if strings.Join(provider.calls, ",") != "get-snapshot" {
		t.Fatalf("clone calls = %#v, want snapshot validation only", provider.calls)
	}
}

func TestKubernetesSandboxDeleteCleansManagedCheckpoints(t *testing.T) {
	provider := &sandboxCheckpointCleanupTransport{}
	runtime := NewKubernetesSandboxRuntime(newTestKubernetesRESTClient(t, provider), WithKubernetesSandboxApplyEnabled(true))
	execution := checkpointExecutionContext("sandbox-a")

	if _, err := runtime.ApplyLifecycle(context.Background(), ports.SandboxLifecycleRequest{
		TenantID: execution.TenantID, InstanceID: execution.InstanceID, Execution: execution,
		Action: ports.WorkloadLifecycleDelete,
	}); err != nil {
		t.Fatalf("ApplyLifecycle(delete) error = %v", err)
	}
	if !strings.Contains(provider.snapshotListQuery, "labelSelector=") || !strings.Contains(provider.snapshotListQuery, "sandbox-instance-id") {
		t.Fatalf("snapshot list query = %q", provider.snapshotListQuery)
	}
	wantDeleted := []string{"sandbox-checkpoint-a", "sandbox-checkpoint-b"}
	if strings.Join(provider.deletedSnapshots, ",") != strings.Join(wantDeleted, ",") {
		t.Fatalf("deleted snapshots = %#v, want %#v", provider.deletedSnapshots, wantDeleted)
	}
}

func TestKubernetesSandboxCheckpointListUsesProviderAfterRuntimeRestart(t *testing.T) {
	execution := checkpointExecutionContext("sandbox-a")
	provider := &sandboxCheckpointTransport{
		running: true, instanceID: execution.InstanceID, checkpointID: uuid.NewString(),
	}
	restarted := NewKubernetesSandboxRuntime(newTestKubernetesRESTClient(t, provider), WithKubernetesSandboxApplyEnabled(true))

	listed, err := restarted.ListCheckpoints(context.Background(), ports.SandboxCheckpointListRequest{
		TenantID: execution.TenantID, InstanceID: execution.InstanceID, Execution: execution,
	})
	if err != nil {
		t.Fatalf("ListCheckpoints() after restart error = %v", err)
	}
	if listed.Total != 1 || len(listed.Items) != 1 || listed.Items[0].ID != provider.checkpointID {
		t.Fatalf("ListCheckpoints() after restart = %#v", listed)
	}
}

func checkpointExecutionContext(name string) *ports.SandboxExecutionContext {
	return &ports.SandboxExecutionContext{
		TenantID: "tenant-a", InstanceID: uuid.NewString(), Name: name,
		Provider: "kubernetes_sandbox_runtime", State: ports.SandboxStateRunning, SessionState: "running",
		ResourceRefs: []string{
			"kubernetes/PersistentVolumeClaim/" + name + "-workspace",
			"kubernetes/Deployment/" + name,
		},
		CreatedAt: time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC),
	}
}

type sandboxCheckpointTransport struct {
	running           bool
	instanceID        string
	checkpointID      string
	snapshotApplyBody string
	snapshotListQuery string
	scaleBodies       []string
}

func (t *sandboxCheckpointTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	path := request.URL.Path
	switch {
	case request.Method == http.MethodPatch && strings.HasSuffix(path, "/scale"):
		body, _ := io.ReadAll(request.Body)
		t.scaleBodies = append(t.scaleBodies, string(body))
		t.running = strings.Contains(string(body), `"replicas":1`)
		return jsonResponse(http.StatusOK, `{}`), nil
	case request.Method == http.MethodGet && strings.HasSuffix(path, "/pods"):
		if !t.running {
			return jsonResponse(http.StatusOK, `{"items":[]}`), nil
		}
		return jsonResponse(http.StatusOK, `{"items":[{"metadata":{"name":"sandbox-a-ready"},"status":{"phase":"Running","containerStatuses":[{"name":"sandbox-a","ready":true}]}}]}`), nil
	case request.Method == http.MethodPatch && strings.Contains(path, "/volumesnapshots/"):
		body, _ := io.ReadAll(request.Body)
		t.snapshotApplyBody = string(body)
		t.checkpointID = strings.TrimPrefix(path[strings.LastIndex(path, "/")+1:], "sandbox-checkpoint-")
		return jsonResponse(http.StatusOK, `{}`), nil
	case request.Method == http.MethodGet && strings.Contains(path, "/volumesnapshots/"):
		return jsonResponse(http.StatusOK, t.snapshotDocument()), nil
	case request.Method == http.MethodGet && strings.HasSuffix(path, "/volumesnapshots"):
		t.snapshotListQuery = request.URL.RawQuery
		return jsonResponse(http.StatusOK, `{"items":[`+t.snapshotDocument()+`]}`), nil
	default:
		return jsonResponse(http.StatusOK, `{}`), nil
	}
}

func (t *sandboxCheckpointTransport) snapshotDocument() string {
	return `{"metadata":{"name":"sandbox-checkpoint-` + t.checkpointID + `","creationTimestamp":"2026-08-02T09:00:00Z","labels":{"ani.kubercloud.io/sandbox-checkpoint":"true","ani.kubercloud.io/tenant-id":"tenant-a","ani.kubercloud.io/sandbox-instance-id":"` + t.instanceID + `","ani.kubercloud.io/sandbox-checkpoint-id":"` + t.checkpointID + `"},"annotations":{"ani.kubercloud.io/sandbox-checkpoint-name":"before-change"}},"status":{"readyToUse":true,"restoreSize":"5Gi"}}`
}

type sandboxCheckpointRestoreTransport struct {
	checkpointID string
	instanceID   string
	pvcExists    bool
	running      bool
	ready        bool
	failPVCApply bool
	calls        []string
	pvcApplyBody string
}

type sandboxCheckpointCleanupTransport struct {
	snapshotListQuery string
	deletedSnapshots  []string
}

func (t *sandboxCheckpointCleanupTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	path := request.URL.Path
	switch {
	case request.Method == http.MethodGet && strings.HasSuffix(path, "/services"):
		return jsonResponse(http.StatusOK, `{"items":[]}`), nil
	case request.Method == http.MethodGet && strings.HasSuffix(path, "/volumesnapshots"):
		t.snapshotListQuery = request.URL.RawQuery
		return jsonResponse(http.StatusOK, `{"items":[{"metadata":{"name":"sandbox-checkpoint-a"}},{"metadata":{"name":"sandbox-checkpoint-b"}}]}`), nil
	case request.Method == http.MethodDelete && strings.Contains(path, "/volumesnapshots/"):
		t.deletedSnapshots = append(t.deletedSnapshots, path[strings.LastIndex(path, "/")+1:])
		return jsonResponse(http.StatusOK, `{}`), nil
	default:
		return jsonResponse(http.StatusOK, `{}`), nil
	}
}

func (t *sandboxCheckpointRestoreTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	path := request.URL.Path
	switch {
	case request.Method == http.MethodGet && strings.Contains(path, "/volumesnapshots/"):
		t.calls = append(t.calls, "get-snapshot")
		return jsonResponse(http.StatusOK, `{"metadata":{"name":"sandbox-checkpoint-`+t.checkpointID+`","creationTimestamp":"2026-08-02T09:00:00Z","labels":{"ani.kubercloud.io/sandbox-checkpoint":"true","ani.kubercloud.io/tenant-id":"tenant-a","ani.kubercloud.io/sandbox-instance-id":"`+t.instanceID+`","ani.kubercloud.io/sandbox-checkpoint-id":"`+t.checkpointID+`"}},"status":{"readyToUse":`+strconv.FormatBool(t.ready)+`,"restoreSize":"5Gi"}}`), nil
	case request.Method == http.MethodPatch && strings.HasSuffix(path, "/scale"):
		body, _ := io.ReadAll(request.Body)
		replicas := "scale-0"
		if strings.Contains(string(body), `"replicas":1`) {
			replicas = "scale-1"
			t.running = true
		} else {
			t.running = false
		}
		t.calls = append(t.calls, replicas)
		return jsonResponse(http.StatusOK, `{}`), nil
	case request.Method == http.MethodGet && strings.HasSuffix(path, "/pods"):
		t.calls = append(t.calls, "list-pods")
		if !t.running {
			return jsonResponse(http.StatusOK, `{"items":[]}`), nil
		}
		return jsonResponse(http.StatusOK, `{"items":[{"metadata":{"name":"sandbox-a-ready"},"status":{"phase":"Running","containerStatuses":[{"name":"sandbox-a","ready":true}]}}]}`), nil
	case request.Method == http.MethodDelete && strings.Contains(path, "/persistentvolumeclaims/"):
		t.calls = append(t.calls, "delete-pvc")
		t.pvcExists = false
		return jsonResponse(http.StatusOK, `{}`), nil
	case request.Method == http.MethodGet && strings.Contains(path, "/persistentvolumeclaims/"):
		t.calls = append(t.calls, "get-pvc")
		if !t.pvcExists {
			return jsonResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
		return jsonResponse(http.StatusOK, `{}`), nil
	case request.Method == http.MethodPatch && strings.Contains(path, "/persistentvolumeclaims/"):
		body, _ := io.ReadAll(request.Body)
		t.calls = append(t.calls, "apply-pvc")
		t.pvcApplyBody = string(body)
		if t.failPVCApply {
			return jsonResponse(http.StatusUnprocessableEntity, `{"message":"restore failed"}`), nil
		}
		t.pvcExists = true
		return jsonResponse(http.StatusOK, `{}`), nil
	default:
		return jsonResponse(http.StatusOK, `{}`), nil
	}
}
