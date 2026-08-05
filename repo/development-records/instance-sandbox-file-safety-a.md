# INSTANCE-SANDBOX-FILE-SAFETY-A

> 日期：2026-08-02
> 范围：ANI Core / Instance Management / Sandbox files containment

## 目标

修复 Sandbox files real-provider 在 Pod `/workspace` 内执行 list/write/delete 时可能通过符号链接、硬链接或目录重命名越过工作区边界的问题。

## 边界

- 不改 Core OpenAPI v1；保留 POST write=201、DELETE+`Idempotency-Key`=204、409/413/422 语义
- 不改变 1 MiB inline 文件上限；`upload_id` 仍不在本批实现
- 不处理 Sandbox 状态持久化、checkpoint、共享 token signing key 或 Kubernetes exec client 配置
- 本批为 local/logic verified 安全加固，没有重跑真实集群 live gate

## 实现要点

- Pod 内 Python 脚本从 `/workspace` 目录文件描述符开始逐级解析相对路径
- Sandbox Deployment 为 `/workspace` 挂载独立 `emptyDir`，以文件系统边界阻断跨目录硬链接和 rename-out
- 中间目录使用 `O_DIRECTORY | O_NOFOLLOW`，写入目标使用 `dir_fd + O_NOFOLLOW`
- 覆盖已有文件前先检查 regular file 与单链接计数，验证完成后再截断，拒绝 hard-link target
- delete 只通过已验证父目录的 `dir_fd` 执行；list 不遍历 symlink
- symlink/不安全路径使用专用退出码返回 `ports.ErrInvalid`，Gateway 延续 v1 HTTP 400 映射
- 回归测试真实执行嵌入脚本，覆盖 list symlink、write parent symlink、write target symlink、delete parent symlink 和 hard-link write target

## 验证

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'Test.*Sandbox.*File|TestKubernetesSandboxRuntimeFilesExecuteInPod|TestKubernetesSandboxRuntimeWriteFileConflict|TestKubernetesSandboxRuntimeUnsafeFilePathMapsInvalid' -count=1
GOCACHE=/tmp/ani-go-cache go test ./services/ani-gateway/internal/router -run 'Test.*SandboxFile|TestWriteListAndDeleteSandboxFile' -count=1
PATH=/tmp/ani-pybin:$PATH make validate-openapi-spec validate-core-api-compatibility validate-architecture validate-doc-entrypoints
PATH=/tmp/ani-pybin:$PATH make test
git diff --check
```

结果：focused adapter/router、Core OpenAPI、v1 compatibility、architecture 和 `make test` 均通过；真实 live 未执行。
