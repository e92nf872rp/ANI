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
	sandboxFileExitUnsafe   = 20
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
	if execResult.ExitCode == sandboxFileExitUnsafe {
		return ports.SandboxFileListResult{}, fmt.Errorf("%w: unsafe sandbox file path", ports.ErrInvalid)
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
	case sandboxFileExitUnsafe:
		return ports.SandboxFileResult{}, fmt.Errorf("%w: unsafe sandbox file path", ports.ErrInvalid)
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
	case sandboxFileExitUnsafe:
		return fmt.Errorf("%w: unsafe sandbox file path", ports.ErrInvalid)
	default:
		return fmt.Errorf("%w: delete sandbox file failed: %s", ports.ErrFailedPrecondition, strings.TrimSpace(execResult.Stderr))
	}
}

const sandboxFilePythonPathGuard = `
import errno, os, stat, sys

UNSAFE_PATH_EXIT = 20

def reject_unsafe_path(message):
    sys.stderr.write(message)
    sys.exit(UNSAFE_PATH_EXIT)

def relative_parts(rel, allow_root=False):
    if "\x00" in rel or "\\" in rel or os.path.isabs(rel):
        reject_unsafe_path("unsafe sandbox file path")
    if rel in ("", "."):
        if allow_root:
            return []
        reject_unsafe_path("sandbox file path is required")
    parts = rel.split("/")
    if any(part in ("", ".", "..") for part in parts):
        reject_unsafe_path("unsafe sandbox file path")
    return parts

def open_root(root):
    try:
        return os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    except OSError as exc:
        if exc.errno in (errno.ELOOP, errno.ENOTDIR):
            reject_unsafe_path("unsafe sandbox workspace root")
        raise

def open_existing_path(root_fd, parts):
    current_fd = os.dup(root_fd)
    try:
        for index, part in enumerate(parts):
            flags = os.O_RDONLY | os.O_NOFOLLOW
            if index < len(parts) - 1:
                flags |= os.O_DIRECTORY
            try:
                next_fd = os.open(part, flags, dir_fd=current_fd)
            except FileNotFoundError:
                os.close(current_fd)
                return None
            except OSError as exc:
                if exc.errno in (errno.ELOOP, errno.ENOTDIR):
                    reject_unsafe_path("unsafe sandbox file path")
                raise
            os.close(current_fd)
            current_fd = next_fd
        return current_fd
    except BaseException:
        try:
            os.close(current_fd)
        except OSError:
            pass
        raise

def open_parent(root_fd, parts, create=False):
    current_fd = os.dup(root_fd)
    try:
        for part in parts[:-1]:
            try:
                next_fd = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=current_fd)
            except FileNotFoundError:
                if not create:
                    os.close(current_fd)
                    return None
                try:
                    os.mkdir(part, mode=0o755, dir_fd=current_fd)
                except FileExistsError:
                    pass
                try:
                    next_fd = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=current_fd)
                except OSError as exc:
                    if exc.errno in (errno.ELOOP, errno.ENOTDIR):
                        reject_unsafe_path("unsafe sandbox file path")
                    raise
            except OSError as exc:
                if exc.errno in (errno.ELOOP, errno.ENOTDIR):
                    reject_unsafe_path("unsafe sandbox file path")
                raise
            os.close(current_fd)
            current_fd = next_fd
        return current_fd
    except BaseException:
        try:
            os.close(current_fd)
        except OSError:
            pass
        raise
`

const sandboxListFilesPython = sandboxFilePythonPathGuard + `
import json, time

root_fd = open_root(sys.argv[1])
parts = relative_parts(sys.argv[2], allow_root=True)
base_fd = open_existing_path(root_fd, parts)
os.close(root_fd)
items = []

def append_item(path, kind, info):
    items.append({
        "path": path,
        "kind": kind,
        "size_bytes": 0 if kind == "directory" else int(info.st_size),
        "updated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(info.st_mtime)),
    })

def walk(directory_fd, prefix):
    for name in sorted(os.listdir(directory_fd)):
        try:
            info = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
        except FileNotFoundError:
            continue
        path = name if not prefix else prefix + "/" + name
        if stat.S_ISLNK(info.st_mode):
            continue
        if stat.S_ISDIR(info.st_mode):
            append_item(path, "directory", info)
            try:
                child_fd = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=directory_fd)
            except OSError as exc:
                if exc.errno in (errno.ELOOP, errno.ENOTDIR, errno.ENOENT):
                    continue
                raise
            try:
                walk(child_fd, path)
            finally:
                os.close(child_fd)
        elif stat.S_ISREG(info.st_mode):
            append_item(path, "file", info)

if base_fd is not None:
    try:
        base_info = os.fstat(base_fd)
        base_path = "/".join(parts)
        if stat.S_ISDIR(base_info.st_mode):
            walk(base_fd, base_path)
        elif stat.S_ISREG(base_info.st_mode):
            append_item(base_path, "file", base_info)
    finally:
        os.close(base_fd)
print(json.dumps(items))
`

const sandboxWriteFilePython = sandboxFilePythonPathGuard + `
root_fd = open_root(sys.argv[1])
parts = relative_parts(sys.argv[2])
overwrite = sys.argv[3] == "1"
limit = int(sys.argv[4])
data = sys.stdin.buffer.read()
if len(data) > limit:
    sys.stderr.write("payload too large")
    sys.exit(19)
parent_fd = open_parent(root_fd, parts, create=True)
os.close(root_fd)
name = parts[-1]
try:
    existing = None
    try:
        existing = os.stat(name, dir_fd=parent_fd, follow_symlinks=False)
        if stat.S_ISLNK(existing.st_mode):
            reject_unsafe_path("unsafe sandbox file path")
        if not stat.S_ISREG(existing.st_mode) or existing.st_nlink != 1:
            reject_unsafe_path("unsafe sandbox file target")
        if not overwrite:
            sys.exit(17)
    except FileNotFoundError:
        pass
    flags = os.O_WRONLY | os.O_NOFOLLOW
    if existing is None:
        flags |= os.O_CREAT | os.O_EXCL
    try:
        target_fd = os.open(name, flags, 0o644, dir_fd=parent_fd)
    except FileExistsError:
        sys.exit(17)
    except OSError as exc:
        if exc.errno in (errno.ELOOP, errno.ENOTDIR):
            reject_unsafe_path("unsafe sandbox file path")
        raise
    try:
        opened = os.fstat(target_fd)
        if not stat.S_ISREG(opened.st_mode) or opened.st_nlink != 1:
            reject_unsafe_path("unsafe sandbox file target")
        if existing is not None:
            os.ftruncate(target_fd, 0)
        with os.fdopen(target_fd, "wb", closefd=False) as target:
            target.write(data)
    finally:
        os.close(target_fd)
finally:
    os.close(parent_fd)
print(len(data))
`

const sandboxDeleteFilePython = sandboxFilePythonPathGuard + `
root_fd = open_root(sys.argv[1])
parts = relative_parts(sys.argv[2])
parent_fd = open_parent(root_fd, parts, create=False)
os.close(root_fd)
if parent_fd is None:
    sys.exit(18)
name = parts[-1]
try:
    try:
        info = os.stat(name, dir_fd=parent_fd, follow_symlinks=False)
    except FileNotFoundError:
        sys.exit(18)
    if stat.S_ISLNK(info.st_mode):
        reject_unsafe_path("unsafe sandbox file path")
    if not stat.S_ISREG(info.st_mode):
        sys.exit(18)
    os.unlink(name, dir_fd=parent_fd)
finally:
    os.close(parent_fd)
`
