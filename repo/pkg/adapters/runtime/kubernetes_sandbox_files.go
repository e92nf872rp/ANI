package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const (
	sandboxWorkspaceRoot   = "/workspace"
	sandboxFileMaxBytes    = 1 << 20 // 1 MiB
	sandboxFileExecTimeout = 60 * time.Second

	sandboxFileExitConflict = 17
	sandboxFileExitNotFound = 18
)

func (r *KubernetesSandboxRuntime) wireFileBackend() {
	if r.local == nil {
		return
	}
	r.local.fileLister = r.listFilesInPod
	r.local.fileWriter = r.writeFileInPod
	r.local.fileDeleter = r.deleteFileInPod
}

func (r *KubernetesSandboxRuntime) listFilesInPod(ctx context.Context, request ports.SandboxFileListRequest, instance ports.SandboxInstanceStatus) (ports.SandboxFileListResult, error) {
	if !r.enabled || r.client == nil || r.executor == nil {
		return ports.SandboxFileListResult{}, ports.ErrNotConfigured
	}
	podName, containerName, err := r.waitReadySandboxPod(ctx, instance, sandboxFileExecTimeout)
	if err != nil {
		return ports.SandboxFileListResult{}, err
	}
	dir := request.Path
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	execResult, execErr := r.executor.Exec(ctx, sandboxPodExecRequest{
		Namespace: tenantNamespace(instance.TenantID),
		Pod:       podName,
		Container: containerName,
		Command:   []string{"python3", "-c", sandboxListFilesPython, sandboxWorkspaceRoot, dir},
		Timeout:   sandboxFileExecTimeout,
	})
	if execErr != nil {
		return ports.SandboxFileListResult{}, execErr
	}
	if execResult.ExitCode != 0 {
		return ports.SandboxFileListResult{}, fmt.Errorf("%w: list sandbox files failed: %s", ports.ErrFailedPrecondition, strings.TrimSpace(execResult.Stderr))
	}
	var raw []struct {
		Path      string `json:"path"`
		Kind      string `json:"kind"`
		SizeBytes int64  `json:"size_bytes"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(execResult.Stdout)), &raw); err != nil {
		return ports.SandboxFileListResult{}, fmt.Errorf("%w: decode sandbox file list: %v", ports.ErrInvalid, err)
	}
	now := r.now().UTC()
	items := make([]ports.SandboxFileResult, 0, len(raw))
	for _, item := range raw {
		kind := item.Kind
		if kind != "file" && kind != "directory" {
			kind = "file"
		}
		updatedAt := now
		if strings.TrimSpace(item.UpdatedAt) != "" {
			if parsed, parseErr := time.Parse(time.RFC3339, item.UpdatedAt); parseErr == nil {
				updatedAt = parsed
			}
		}
		items = append(items, ports.SandboxFileResult{
			Path:      item.Path,
			Kind:      kind,
			SizeBytes: item.SizeBytes,
			UpdatedAt: updatedAt,
		})
	}
	return ports.SandboxFileListResult{Items: items, Total: len(items)}, nil
}

func (r *KubernetesSandboxRuntime) writeFileInPod(ctx context.Context, request ports.SandboxFileWriteRequest, content []byte, instance ports.SandboxInstanceStatus) (ports.SandboxFileResult, error) {
	if !r.enabled || r.client == nil || r.executor == nil {
		return ports.SandboxFileResult{}, ports.ErrNotConfigured
	}
	if len(content) > sandboxFileMaxBytes {
		return ports.SandboxFileResult{}, fmt.Errorf("%w: sandbox file exceeds %d bytes", ports.ErrPayloadTooLarge, sandboxFileMaxBytes)
	}
	podName, containerName, err := r.waitReadySandboxPod(ctx, instance, sandboxFileExecTimeout)
	if err != nil {
		return ports.SandboxFileResult{}, err
	}
	overwrite := "0"
	if request.Overwrite {
		overwrite = "1"
	}
	execResult, execErr := r.executor.Exec(ctx, sandboxPodExecRequest{
		Namespace: tenantNamespace(instance.TenantID),
		Pod:       podName,
		Container: containerName,
		Command: []string{
			"python3", "-c", sandboxWriteFilePython,
			sandboxWorkspaceRoot, request.Path, overwrite, strconv.Itoa(sandboxFileMaxBytes),
		},
		Stdin:   string(content),
		Timeout: sandboxFileExecTimeout,
	})
	if execErr != nil {
		return ports.SandboxFileResult{}, execErr
	}
	switch execResult.ExitCode {
	case 0:
	case sandboxFileExitConflict:
		return ports.SandboxFileResult{}, fmt.Errorf("%w: sandbox file already exists", ports.ErrConflict)
	default:
		return ports.SandboxFileResult{}, fmt.Errorf("%w: write sandbox file failed: %s", ports.ErrFailedPrecondition, strings.TrimSpace(execResult.Stderr))
	}
	now := firstNonZeroTime(request.RequestedAt, r.now().UTC())
	return ports.SandboxFileResult{
		Path:      request.Path,
		Kind:      "file",
		SizeBytes: int64(len(content)),
		UpdatedAt: now,
	}, nil
}

func (r *KubernetesSandboxRuntime) deleteFileInPod(ctx context.Context, request ports.SandboxFileDeleteRequest, instance ports.SandboxInstanceStatus) error {
	if !r.enabled || r.client == nil || r.executor == nil {
		return ports.ErrNotConfigured
	}
	podName, containerName, err := r.waitReadySandboxPod(ctx, instance, sandboxFileExecTimeout)
	if err != nil {
		return err
	}
	execResult, execErr := r.executor.Exec(ctx, sandboxPodExecRequest{
		Namespace: tenantNamespace(instance.TenantID),
		Pod:       podName,
		Container: containerName,
		Command:   []string{"python3", "-c", sandboxDeleteFilePython, sandboxWorkspaceRoot, request.Path},
		Timeout:   sandboxFileExecTimeout,
	})
	if execErr != nil {
		return execErr
	}
	switch execResult.ExitCode {
	case 0:
		return nil
	case sandboxFileExitNotFound:
		return ports.ErrNotFound
	default:
		return fmt.Errorf("%w: delete sandbox file failed: %s", ports.ErrFailedPrecondition, strings.TrimSpace(execResult.Stderr))
	}
}

const sandboxListFilesPython = `
import json, sys, time
from pathlib import Path
root = Path(sys.argv[1])
rel = sys.argv[2]
base = root if rel in (".", "") else (root / rel)
items = []
if base.exists():
    paths = [base] if base.is_file() else sorted(p for p in base.rglob("*") if p.is_file() or p.is_dir())
    for p in paths:
        try:
            rel_path = str(p.relative_to(root)).replace("\\", "/")
            st = p.stat()
            items.append({
                "path": rel_path,
                "kind": "directory" if p.is_dir() else "file",
                "size_bytes": 0 if p.is_dir() else int(st.st_size),
                "updated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(st.st_mtime)),
            })
        except Exception:
            continue
print(json.dumps(items))
`

const sandboxWriteFilePython = `
from pathlib import Path
import sys
root = Path(sys.argv[1])
rel = sys.argv[2]
overwrite = sys.argv[3] == "1"
limit = int(sys.argv[4])
data = sys.stdin.buffer.read()
if len(data) > limit:
    sys.stderr.write("payload too large")
    sys.exit(19)
target = root / rel
if target.exists() and not overwrite:
    sys.exit(17)
target.parent.mkdir(parents=True, exist_ok=True)
target.write_bytes(data)
print(len(data))
`

const sandboxDeleteFilePython = `
from pathlib import Path
import sys
root = Path(sys.argv[1])
rel = sys.argv[2]
target = root / rel
if not target.is_file():
    sys.exit(18)
target.unlink()
`
