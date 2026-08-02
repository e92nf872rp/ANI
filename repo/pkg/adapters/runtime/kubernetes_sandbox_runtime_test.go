package runtime

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestKubernetesSandboxRuntimeCreateAppliesDeploymentWithRuntimeClass(t *testing.T) {
	provider := &sandboxApplyTransport{}
	client := newTestKubernetesRESTClient(t, provider)
	runtime := NewKubernetesSandboxRuntime(
		client,
		WithKubernetesSandboxApplyEnabled(true),
		WithKubernetesSandboxClock(func() time.Time { return time.Unix(1700, 0) }),
	)

	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "sbx-01",
		Image:     "docker.kubercon.local/common/mirror/busybox:latest",
		AutoStart: true,
		CreatedAt: time.Unix(1700, 0),
		Config: ports.SandboxConfig{
			RuntimeClass: "sandbox-kata",
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !instance.DevProfile.RealProvider || instance.Provider != "kubernetes_sandbox_runtime" {
		t.Fatalf("DevProfile/Provider = %#v", instance)
	}
	if _, err := uuid.Parse(instance.InstanceID); err != nil {
		t.Fatalf("InstanceID = %q, want UUID: %v", instance.InstanceID, err)
	}
	if len(instance.ResourceRefs) != 1 || !strings.Contains(instance.ResourceRefs[0], "Deployment/sbx-01") {
		t.Fatalf("ResourceRefs = %#v", instance.ResourceRefs)
	}
	if !strings.Contains(provider.applyBody, `"runtimeClassName":"sandbox-kata"`) && !strings.Contains(provider.applyBody, `"runtimeClassName": "sandbox-kata"`) {
		t.Fatalf("apply body missing runtimeClassName sandbox-kata: %s", provider.applyBody)
	}
	if !strings.Contains(provider.applyBody, `"mountPath":"/workspace"`) && !strings.Contains(provider.applyBody, `"mountPath": "/workspace"`) {
		t.Fatalf("apply body missing isolated /workspace mount: %s", provider.applyBody)
	}
	if !strings.Contains(provider.applyBody, `"emptyDir":{}`) && !strings.Contains(provider.applyBody, `"emptyDir": {}`) {
		t.Fatalf("apply body missing workspace emptyDir: %s", provider.applyBody)
	}
}

func TestKubernetesSandboxRuntimeLifecycleUsesPersistedContextAfterRestart(t *testing.T) {
	provider := &recordingProviderTransport{}
	client := newTestKubernetesRESTClient(t, provider)
	first := NewKubernetesSandboxRuntime(client, WithKubernetesSandboxApplyEnabled(true))
	instance, err := first.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID: "tenant-a", Name: "sbx-restart", Image: "busybox:1.36", AutoStart: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	restarted := NewKubernetesSandboxRuntime(client, WithKubernetesSandboxApplyEnabled(true))
	paused, err := restarted.ApplyLifecycle(context.Background(), ports.SandboxLifecycleRequest{
		TenantID:   instance.TenantID,
		InstanceID: instance.InstanceID,
		Execution: &ports.SandboxExecutionContext{
			TenantID: instance.TenantID, InstanceID: instance.InstanceID, Name: instance.Name,
			Provider: instance.Provider, State: instance.State, SessionState: instance.SessionState,
			Config: instance.Config, DevProfile: instance.DevProfile,
			ResourceRefs: append([]string(nil), instance.ResourceRefs...),
			CreatedAt:    instance.CreatedAt, UpdatedAt: instance.UpdatedAt,
		},
		Action: ports.WorkloadLifecyclePause,
	})
	if err != nil {
		t.Fatalf("ApplyLifecycle() after restart error = %v", err)
	}
	if paused.State != ports.SandboxStatePaused {
		t.Fatalf("paused state = %s, want paused", paused.State)
	}
	if !provider.seen("PATCH", "/apis/apps/v1/namespaces/ani-tenant-tenant-a/deployments/sbx-restart/scale", "") {
		t.Fatalf("provider requests = %#v, want scale from persisted refs", provider.requests)
	}
}

func TestKubernetesSandboxRuntimeRejectsUnsupportedCheckpoints(t *testing.T) {
	runtime := NewKubernetesSandboxRuntime(newTestKubernetesRESTClient(t, &recordingProviderTransport{}), WithKubernetesSandboxApplyEnabled(true))
	execution := &ports.SandboxExecutionContext{
		TenantID: "tenant-a", InstanceID: uuid.NewString(), Name: "sbx-checkpoint",
		Provider: "kubernetes_sandbox_runtime", State: ports.SandboxStateRunning,
	}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "list", call: func() error {
			_, err := runtime.ListCheckpoints(context.Background(), ports.SandboxCheckpointListRequest{TenantID: execution.TenantID, InstanceID: execution.InstanceID, Execution: execution})
			return err
		}},
		{name: "create", call: func() error {
			_, err := runtime.CreateCheckpoint(context.Background(), ports.SandboxCheckpointCreateRequest{TenantID: execution.TenantID, InstanceID: execution.InstanceID, Execution: execution, IdempotencyKey: "checkpoint-create"})
			return err
		}},
		{name: "restore", call: func() error {
			_, err := runtime.RestoreCheckpoint(context.Background(), ports.SandboxCheckpointRestoreRequest{TenantID: execution.TenantID, InstanceID: execution.InstanceID, Execution: execution, CheckpointID: "checkpoint-a", IdempotencyKey: "checkpoint-restore"})
			return err
		}},
		{name: "clone", call: func() error {
			_, err := runtime.CloneCheckpoint(context.Background(), ports.SandboxCheckpointCloneRequest{TenantID: execution.TenantID, InstanceID: execution.InstanceID, Execution: execution, CheckpointID: "checkpoint-a", IdempotencyKey: "checkpoint-clone", Name: "clone-a"})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, ports.ErrUnsupported) {
				t.Fatalf("checkpoint call error = %v, want ErrUnsupported", err)
			}
		})
	}
}

func TestKubernetesSandboxRuntimePauseResumeDeleteScalesAndDeletes(t *testing.T) {
	provider := &recordingProviderTransport{}
	client := newTestKubernetesRESTClient(t, provider)
	runtime := NewKubernetesSandboxRuntime(client, WithKubernetesSandboxApplyEnabled(true))

	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "sbx-02",
		Image:     "busybox:1.36",
		AutoStart: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	paused, err := runtime.ApplyLifecycle(context.Background(), ports.SandboxLifecycleRequest{
		TenantID:   instance.TenantID,
		InstanceID: instance.InstanceID,
		Action:     ports.WorkloadLifecyclePause,
	})
	if err != nil {
		t.Fatalf("Pause() error = %v", err)
	}
	if paused.State != ports.SandboxStatePaused {
		t.Fatalf("paused state = %s", paused.State)
	}
	if !provider.seen("PATCH", "/apis/apps/v1/namespaces/ani-tenant-tenant-a/deployments/sbx-02/scale", "") {
		t.Fatalf("provider requests = %#v, want scale on pause", provider.requests)
	}

	if _, err := runtime.ApplyLifecycle(context.Background(), ports.SandboxLifecycleRequest{
		TenantID:   instance.TenantID,
		InstanceID: instance.InstanceID,
		Action:     ports.WorkloadLifecycleResume,
	}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}

	if _, err := runtime.ApplyLifecycle(context.Background(), ports.SandboxLifecycleRequest{
		TenantID:   instance.TenantID,
		InstanceID: instance.InstanceID,
		Action:     ports.WorkloadLifecycleDelete,
	}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !provider.seen("DELETE", "/apis/apps/v1/namespaces/ani-tenant-tenant-a/deployments/sbx-02", "") {
		t.Fatalf("provider requests = %#v, want delete", provider.requests)
	}
}

func TestKubernetesSandboxRuntimeDisabledStaysLocal(t *testing.T) {
	provider := &recordingProviderTransport{}
	client := newTestKubernetesRESTClient(t, provider)
	runtime := NewKubernetesSandboxRuntime(client, WithKubernetesSandboxApplyEnabled(false))

	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "sbx-local",
		Image:     "busybox:1.36",
		AutoStart: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if instance.DevProfile.RealProvider || len(provider.requests) != 0 {
		t.Fatalf("disabled runtime should stay local, got %#v requests=%d", instance.DevProfile, len(provider.requests))
	}
}

func TestKubernetesSandboxRuntimeCreateCodeRunExecutesInPod(t *testing.T) {
	provider := &sandboxPodListTransport{
		podList: `{"items":[{"metadata":{"name":"sbx-code-abc"},"status":{"phase":"Running","containerStatuses":[{"name":"sbx-code","ready":true}]}}]}`,
	}
	client := newTestKubernetesRESTClient(t, provider)
	executor := &fakeSandboxPodExecutor{
		result: sandboxPodExecResult{Stdout: "2\n", ExitCode: 0},
	}
	runtime := NewKubernetesSandboxRuntime(
		client,
		WithKubernetesSandboxApplyEnabled(true),
		WithKubernetesSandboxPodExecutor(executor),
		WithKubernetesSandboxClock(func() time.Time { return time.Unix(1800, 0) }),
	)

	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "sbx-code",
		Image:     "python:3.12-alpine",
		AutoStart: true,
		CreatedAt: time.Unix(1800, 0),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	result, err := runtime.CreateCodeRun(context.Background(), ports.SandboxCodeRunRequest{
		TenantID:       instance.TenantID,
		InstanceID:     instance.InstanceID,
		IdempotencyKey: "code-1",
		Language:       "python",
		Code:           "print(1+1)",
		TimeoutSeconds: 30,
		RequestedAt:    time.Unix(1800, 0),
	})
	if err != nil {
		t.Fatalf("CreateCodeRun() error = %v", err)
	}
	if result.Status != "succeeded" || result.Stdout != "2\n" || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("CreateCodeRun() = %#v", result)
	}
	if len(executor.requests) != 1 {
		t.Fatalf("executor requests = %#v", executor.requests)
	}
	got := executor.requests[0]
	if got.Pod != "sbx-code-abc" || got.Container != "sbx-code" {
		t.Fatalf("exec target = %#v", got)
	}
	if len(got.Command) < 3 || got.Command[0] != "python3" || got.Command[1] != "-c" || got.Command[2] != "print(1+1)" {
		t.Fatalf("exec command = %#v", got.Command)
	}

	again, err := runtime.CreateCodeRun(context.Background(), ports.SandboxCodeRunRequest{
		TenantID:       instance.TenantID,
		InstanceID:     instance.InstanceID,
		IdempotencyKey: "code-1",
		Language:       "python",
		Code:           "print(1+1)",
		TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatalf("idempotent CreateCodeRun() error = %v", err)
	}
	if again.ID != result.ID || len(executor.requests) != 1 {
		t.Fatalf("idempotency failed: again=%#v requests=%d", again, len(executor.requests))
	}
}

func TestKubernetesSandboxRuntimeFilesExecuteInPod(t *testing.T) {
	provider := &sandboxPodListTransport{
		podList: `{"items":[{"metadata":{"name":"sbx-files-abc"},"status":{"phase":"Running","containerStatuses":[{"name":"sbx-files","ready":true}]}}]}`,
	}
	client := newTestKubernetesRESTClient(t, provider)
	executor := &fakeSandboxPodExecutor{
		handler: func(request sandboxPodExecRequest) (sandboxPodExecResult, error) {
			script := ""
			if len(request.Command) >= 3 {
				script = request.Command[2]
			}
			switch {
			case strings.Contains(script, "target.write(data)"):
				if request.Stdin != "hello-sandbox" {
					t.Fatalf("write stdin = %q", request.Stdin)
				}
				return sandboxPodExecResult{Stdout: "13\n", ExitCode: 0}, nil
			case strings.Contains(script, "def walk(directory_fd, prefix)"):
				return sandboxPodExecResult{
					Stdout:   `[{"path":"workspace/hello.txt","kind":"file","size_bytes":13,"updated_at":"2026-08-01T10:00:00Z"}]`,
					ExitCode: 0,
				}, nil
			case strings.Contains(script, "unlink"):
				return sandboxPodExecResult{ExitCode: 0}, nil
			default:
				return sandboxPodExecResult{ExitCode: 1, Stderr: "unexpected command"}, nil
			}
		},
	}
	runtime := NewKubernetesSandboxRuntime(
		client,
		WithKubernetesSandboxApplyEnabled(true),
		WithKubernetesSandboxPodExecutor(executor),
		WithKubernetesSandboxClock(func() time.Time { return time.Unix(1900, 0) }),
	)
	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "sbx-files",
		Image:     "python:3.12-alpine",
		AutoStart: true,
		CreatedAt: time.Unix(1900, 0),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	written, err := runtime.WriteFile(context.Background(), ports.SandboxFileWriteRequest{
		TenantID:       instance.TenantID,
		InstanceID:     instance.InstanceID,
		IdempotencyKey: "file-1",
		Path:           "workspace/hello.txt",
		ContentBase64:  "aGVsbG8tc2FuZGJveA==",
		RequestedAt:    time.Unix(1900, 0),
	})
	if err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if written.Path != "workspace/hello.txt" || written.SizeBytes != 13 {
		t.Fatalf("WriteFile() = %#v", written)
	}

	listed, err := runtime.ListFiles(context.Background(), ports.SandboxFileListRequest{
		TenantID:   instance.TenantID,
		InstanceID: instance.InstanceID,
		Path:       "workspace",
	})
	if err != nil {
		t.Fatalf("ListFiles() error = %v", err)
	}
	if listed.Total != 1 || len(listed.Items) != 1 || listed.Items[0].Path != "workspace/hello.txt" {
		t.Fatalf("ListFiles() = %#v", listed)
	}

	if err := runtime.DeleteFile(context.Background(), ports.SandboxFileDeleteRequest{
		TenantID:       instance.TenantID,
		InstanceID:     instance.InstanceID,
		IdempotencyKey: "file-rm-1",
		Path:           "workspace/hello.txt",
	}); err != nil {
		t.Fatalf("DeleteFile() error = %v", err)
	}
	if len(executor.requests) < 3 {
		t.Fatalf("expected write/list/delete execs, got %#v", executor.requests)
	}

	again, err := runtime.WriteFile(context.Background(), ports.SandboxFileWriteRequest{
		TenantID:       instance.TenantID,
		InstanceID:     instance.InstanceID,
		IdempotencyKey: "file-1",
		Path:           "workspace/hello.txt",
		ContentBase64:  "aGVsbG8tc2FuZGJveA==",
	})
	if err != nil {
		t.Fatalf("idempotent WriteFile() error = %v", err)
	}
	if again.Path != written.Path {
		t.Fatalf("idempotent WriteFile() = %#v", again)
	}
}

func TestKubernetesSandboxRuntimeCreatePortAppliesNodePortService(t *testing.T) {
	provider := &sandboxPortTransport{
		podList:    `{"items":[{"metadata":{"name":"sbx-port-abc"},"status":{"phase":"Running","hostIP":"10.0.0.8","containerStatuses":[{"name":"sbx-port","ready":true}]}}]}`,
		serviceGet: `{"spec":{"ports":[{"port":8765,"nodePort":30065}]}}`,
	}
	t.Setenv(sandboxPortPreviewHostEnv, "")
	client := newTestKubernetesRESTClient(t, provider)
	runtime := NewKubernetesSandboxRuntime(
		client,
		WithKubernetesSandboxApplyEnabled(true),
		WithKubernetesSandboxClock(func() time.Time { return time.Unix(2100, 0) }),
	)
	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "sbx-port",
		Image:     "python:3.12-alpine",
		AutoStart: true,
		CreatedAt: time.Unix(2100, 0),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	result, err := runtime.CreatePort(context.Background(), ports.SandboxPortRequest{
		TenantID:       instance.TenantID,
		InstanceID:     instance.InstanceID,
		IdempotencyKey: "port-1",
		Port:           8765,
		Protocol:       "http",
		RequestedAt:    time.Unix(2100, 0),
	})
	if err != nil {
		t.Fatalf("CreatePort() error = %v", err)
	}
	if result.Status != "available" || result.PreviewURL != "http://10.0.0.8:30065" {
		t.Fatalf("CreatePort() = %#v", result)
	}
	if !strings.Contains(provider.applyBody, `"kind":"Service"`) || !strings.Contains(provider.applyBody, `"type":"NodePort"`) {
		t.Fatalf("expected NodePort Service apply, got %s", provider.applyBody)
	}

	runtime = NewKubernetesSandboxRuntime(client, WithKubernetesSandboxApplyEnabled(true))
	closed, err := runtime.DeletePort(context.Background(), ports.SandboxPortDeleteRequest{
		TenantID:       instance.TenantID,
		InstanceID:     instance.InstanceID,
		Execution:      sandboxExecutionContextForTest(instance),
		IdempotencyKey: "port-close-1",
		Port:           8765,
	})
	if err != nil {
		t.Fatalf("DeletePort() error = %v", err)
	}
	if closed.Status != "closing" {
		t.Fatalf("DeletePort() = %#v", closed)
	}
	if provider.deletePath == "" || !strings.Contains(provider.deletePath, "/services/") {
		t.Fatalf("expected service delete, got %q", provider.deletePath)
	}
}

func sandboxExecutionContextForTest(instance ports.SandboxInstanceStatus) *ports.SandboxExecutionContext {
	return &ports.SandboxExecutionContext{
		TenantID: instance.TenantID, InstanceID: instance.InstanceID, Name: instance.Name,
		Provider: instance.Provider, State: instance.State, SessionState: instance.SessionState,
		Config: instance.Config, DevProfile: instance.DevProfile,
		ResourceRefs: append([]string(nil), instance.ResourceRefs...),
		CreatedAt:    instance.CreatedAt, UpdatedAt: instance.UpdatedAt,
	}
}

func TestKubernetesSandboxRuntimeWriteFileConflict(t *testing.T) {
	provider := &sandboxPodListTransport{
		podList: `{"items":[{"metadata":{"name":"sbx-conflict-abc"},"status":{"phase":"Running","containerStatuses":[{"name":"sbx-conflict","ready":true}]}}]}`,
	}
	client := newTestKubernetesRESTClient(t, provider)
	executor := &fakeSandboxPodExecutor{
		result: sandboxPodExecResult{ExitCode: sandboxFileExitConflict},
	}
	runtime := NewKubernetesSandboxRuntime(
		client,
		WithKubernetesSandboxApplyEnabled(true),
		WithKubernetesSandboxPodExecutor(executor),
		WithKubernetesSandboxClock(func() time.Time { return time.Unix(2000, 0) }),
	)
	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "sbx-conflict",
		Image:     "python:3.12-alpine",
		AutoStart: true,
		CreatedAt: time.Unix(2000, 0),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err = runtime.WriteFile(context.Background(), ports.SandboxFileWriteRequest{
		TenantID:       instance.TenantID,
		InstanceID:     instance.InstanceID,
		IdempotencyKey: "conflict-1",
		Path:           "workspace/exists.txt",
		ContentBase64:  "eA==",
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("WriteFile() error = %v, want conflict", err)
	}
}

func TestKubernetesSandboxRuntimeUnsafeFilePathMapsInvalid(t *testing.T) {
	provider := &sandboxPodListTransport{
		podList: `{"items":[{"metadata":{"name":"sbx-unsafe-abc"},"status":{"phase":"Running","containerStatuses":[{"name":"sbx-unsafe","ready":true}]}}]}`,
	}
	client := newTestKubernetesRESTClient(t, provider)
	executor := &fakeSandboxPodExecutor{result: sandboxPodExecResult{ExitCode: sandboxFileExitUnsafe}}
	runtime := NewKubernetesSandboxRuntime(
		client,
		WithKubernetesSandboxApplyEnabled(true),
		WithKubernetesSandboxPodExecutor(executor),
	)
	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "sbx-unsafe",
		Image:     "python:3.12-alpine",
		AutoStart: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = runtime.WriteFile(context.Background(), ports.SandboxFileWriteRequest{
		TenantID:       instance.TenantID,
		InstanceID:     instance.InstanceID,
		IdempotencyKey: "unsafe-1",
		Path:           "escape/owned.txt",
		ContentBase64:  "b3duZWQ=",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("WriteFile() error = %v, want ErrInvalid", err)
	}
}

func TestSandboxFileScriptsRejectSymlinks(t *testing.T) {
	const unsafePathExitCode = 20

	t.Run("list symlinked directory", func(t *testing.T) {
		workspace := t.TempDir()
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
			t.Fatal(err)
		}

		exitCode, output := runSandboxPythonScript(t, sandboxListFilesPython, "", workspace, "escape")
		if exitCode != unsafePathExitCode {
			t.Fatalf("list exit code = %d, want %d; output=%s", exitCode, unsafePathExitCode, output)
		}
	})

	t.Run("write through symlinked directory", func(t *testing.T) {
		workspace := t.TempDir()
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
			t.Fatal(err)
		}

		exitCode, output := runSandboxPythonScript(t, sandboxWriteFilePython, "owned", workspace, "escape/owned.txt", "1", "1048576")
		if exitCode != unsafePathExitCode {
			t.Fatalf("write parent exit code = %d, want %d; output=%s", exitCode, unsafePathExitCode, output)
		}
		if _, err := os.Stat(filepath.Join(outside, "owned.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("outside file was written through symlink: %v", err)
		}
	})

	t.Run("overwrite symlinked file", func(t *testing.T) {
		workspace := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.txt")
		if err := os.WriteFile(outside, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(workspace, "target.txt")); err != nil {
			t.Fatal(err)
		}

		exitCode, output := runSandboxPythonScript(t, sandboxWriteFilePython, "changed", workspace, "target.txt", "1", "1048576")
		if exitCode != unsafePathExitCode {
			t.Fatalf("write target exit code = %d, want %d; output=%s", exitCode, unsafePathExitCode, output)
		}
		content, err := os.ReadFile(outside)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != "original" {
			t.Fatalf("outside file content = %q, want original", content)
		}
	})

	t.Run("overwrite hard-linked file", func(t *testing.T) {
		workspace := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.txt")
		if err := os.WriteFile(outside, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(outside, filepath.Join(workspace, "target.txt")); err != nil {
			t.Fatal(err)
		}

		exitCode, output := runSandboxPythonScript(t, sandboxWriteFilePython, "changed", workspace, "target.txt", "1", "1048576")
		if exitCode != unsafePathExitCode {
			t.Fatalf("write hard link exit code = %d, want %d; output=%s", exitCode, unsafePathExitCode, output)
		}
		content, err := os.ReadFile(outside)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != "original" {
			t.Fatalf("outside hard-linked file content = %q, want original", content)
		}
	})

	t.Run("delete through symlinked directory", func(t *testing.T) {
		workspace := t.TempDir()
		outsideDir := t.TempDir()
		outside := filepath.Join(outsideDir, "outside.txt")
		if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideDir, filepath.Join(workspace, "escape")); err != nil {
			t.Fatal(err)
		}

		exitCode, output := runSandboxPythonScript(t, sandboxDeleteFilePython, "", workspace, "escape/outside.txt")
		if exitCode != unsafePathExitCode {
			t.Fatalf("delete exit code = %d, want %d; output=%s", exitCode, unsafePathExitCode, output)
		}
		if _, err := os.Stat(outside); err != nil {
			t.Fatalf("outside file was removed through symlink: %v", err)
		}
	})
}

func TestSandboxFileScriptsAllowWorkspaceOperations(t *testing.T) {
	workspace := t.TempDir()
	exitCode, output := runSandboxPythonScript(t, sandboxWriteFilePython, "hello", workspace, "nested/hello.txt", "0", "1048576")
	if exitCode != 0 || strings.TrimSpace(output) != "5" {
		t.Fatalf("write exit code = %d, output=%q", exitCode, output)
	}

	exitCode, output = runSandboxPythonScript(t, sandboxListFilesPython, "", workspace, "nested")
	if exitCode != 0 || !strings.Contains(output, `"path": "nested/hello.txt"`) {
		t.Fatalf("list exit code = %d, output=%q", exitCode, output)
	}

	exitCode, output = runSandboxPythonScript(t, sandboxDeleteFilePython, "", workspace, "nested/hello.txt")
	if exitCode != 0 {
		t.Fatalf("delete exit code = %d, output=%q", exitCode, output)
	}
	if _, err := os.Stat(filepath.Join(workspace, "nested", "hello.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted file still exists: %v", err)
	}
}

func runSandboxPythonScript(t *testing.T, script string, stdin string, args ...string) (int, string) {
	t.Helper()
	command := exec.Command("python3", append([]string{"-c", script}, args...)...)
	command.Stdin = strings.NewReader(stdin)
	output, err := command.CombinedOutput()
	if err == nil {
		return 0, string(output)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("execute sandbox file script: %v", err)
	}
	return exitErr.ExitCode(), string(output)
}

type sandboxPodListTransport struct {
	recordingProviderTransport
	podList string
}

func (t *sandboxPodListTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/pods") && !strings.Contains(request.URL.Path, "/exec") {
		t.requests = append(t.requests, providerRequest{method: request.Method, path: request.URL.Path, query: request.URL.RawQuery})
		return jsonResponse(http.StatusOK, t.podList), nil
	}
	return t.recordingProviderTransport.RoundTrip(request)
}

type fakeSandboxPodExecutor struct {
	requests []sandboxPodExecRequest
	result   sandboxPodExecResult
	err      error
	handler  func(request sandboxPodExecRequest) (sandboxPodExecResult, error)
}

func (e *fakeSandboxPodExecutor) Exec(_ context.Context, request sandboxPodExecRequest) (sandboxPodExecResult, error) {
	e.requests = append(e.requests, request)
	if e.handler != nil {
		return e.handler(request)
	}
	return e.result, e.err
}

type sandboxApplyTransport struct {
	recordingProviderTransport
	applyBody string
}

func (t *sandboxApplyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil && request.Method == http.MethodPatch && strings.Contains(request.URL.Path, "/deployments/") && !strings.HasSuffix(request.URL.Path, "/scale") {
		buf := make([]byte, 1<<20)
		n, _ := request.Body.Read(buf)
		t.applyBody = string(buf[:n])
		request.Body = http.NoBody
	}
	return t.recordingProviderTransport.RoundTrip(request)
}

type sandboxPortTransport struct {
	recordingProviderTransport
	podList    string
	serviceGet string
	applyBody  string
	deletePath string
}

func (t *sandboxPortTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	path := request.URL.Path
	switch {
	case request.Method == http.MethodGet && strings.Contains(path, "/pods") && !strings.Contains(path, "/exec"):
		return jsonResponse(http.StatusOK, t.podList), nil
	case request.Method == http.MethodGet && strings.Contains(path, "/services/"):
		return jsonResponse(http.StatusOK, t.serviceGet), nil
	case request.Method == http.MethodPatch && strings.Contains(path, "/services/"):
		if request.Body != nil {
			buf := make([]byte, 1<<20)
			n, _ := request.Body.Read(buf)
			t.applyBody = string(buf[:n])
			request.Body = http.NoBody
		}
		return jsonResponse(http.StatusOK, t.serviceGet), nil
	case request.Method == http.MethodDelete && strings.Contains(path, "/services/"):
		t.deletePath = path
		return jsonResponse(http.StatusOK, `{}`), nil
	default:
		return t.recordingProviderTransport.RoundTrip(request)
	}
}
