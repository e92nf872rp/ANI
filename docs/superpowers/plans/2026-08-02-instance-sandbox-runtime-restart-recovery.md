# Sandbox Runtime 契约级无状态化实施计划

> **供执行代理使用：** 必须按任务顺序执行并使用测试驱动开发。未经用户明确确认，不执行 commit、push 或 PR。

**目标：** 使 Kubernetes SandboxRuntime 不依赖 Gateway 进程内 session，并让异步任务与幂等行为在 Gateway 重启后继续满足现有 Core v1 契约。

**架构：** 应用层从 PG 单次读取实例 record，构造 `SandboxExecutionContext` 显式传给真实 runtime；物理状态按请求从 Kubernetes 发现。异步任务写入 PostgreSQL，HTTP 幂等响应写入 Redis，真实 checkpoint 未实现时返回 422。

**技术栈：** Go 1.25、PostgreSQL、Redis、Kubernetes REST API、Python 3、真实 Kubernetes live gate。

## 全局约束

- 仅修改 ANI Core，不修改 Services。
- 不修改 `repo/api/openapi/v1.yaml`、SDK、CLI 或 Console。
- Migration 只增加履行现有 `AsyncTask` 契约所需的最小表和索引。
- `KubernetesSandboxRuntime` 不依赖 `WorkloadInstanceStore`，不读取共享 session/refs/ports map。
- 每次请求只由应用层执行一次 tenant-scoped PG 实例读取。
- Token、code、stdin、stdout、stderr 不进入普通日志、普通审计或 evidence。
- Provider 资源操作必须校验 tenant 和 instance 标签。
- 未经用户明确确认不得 commit。

---

### 任务一：建立请求级 SandboxExecutionContext

**文件：**
- 修改：`repo/pkg/ports/sandbox_runtime.go`
- 修改：`repo/pkg/adapters/runtime/instance_service.go`
- 修改：`repo/services/ani-gateway/internal/router/instances.go`
- 测试：`repo/pkg/adapters/runtime/instance_service_test.go`
- 测试：`repo/services/ani-gateway/internal/router/instances_test.go`

**接口：**

```go
type SandboxExecutionContext struct {
    TenantID     string
    InstanceID   string
    Name         string
    Provider     string
    State        SandboxState
    Config       SandboxConfig
    ResourceRefs []string
}
```

在 lifecycle、token、port、file、checkpoint 和 code-run request 增加内部 `Execution *SandboxExecutionContext` 字段；不添加 JSON tag，不改变 HTTP schema。

- [ ] 先写 `sandboxExecutionContextFromRecord` 失败测试：只接受 Sandbox；内外身份不一致返回 `ErrFailedPrecondition`；外层 record 决定 tenant/name/provider/refs。
- [ ] 先写 Handler/InstanceService 失败测试：每个 Sandbox 操作收到 execution，计数 Store 的 `Get` 调用只能为 1。
- [ ] 运行红灯：

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime ./services/ani-gateway/internal/router -run 'TestSandboxExecutionContextFromRecord|TestSandboxHandlersPassExecutionContext|TestLocalInstanceServiceSandboxLifecycleReadsStoreOnce' -count=1
```

- [ ] 实现 context 构造和传递；禁止 Runtime 再查 Store。
- [ ] 重跑上述命令，预期全部通过。

---

### 任务二：将 Kubernetes SandboxRuntime 改为无状态执行器

**文件：**
- 修改：`repo/pkg/adapters/runtime/local_sandbox_runtime.go`
- 修改：`repo/pkg/adapters/runtime/kubernetes_sandbox_runtime.go`
- 修改：`repo/pkg/adapters/runtime/kubernetes_sandbox_files.go`
- 修改：`repo/pkg/adapters/runtime/kubernetes_sandbox_ports.go`
- 修改：`repo/pkg/adapters/runtime/kubernetes_sandbox_pod_exec.go`
- 测试：`repo/pkg/adapters/runtime/kubernetes_sandbox_runtime_test.go`
- 测试：`repo/pkg/adapters/runtime/local_sandbox_runtime_test.go`

**行为：** Local profile 可以继续使用内存；Kubernetes profile 的已有实例操作只使用 `request.Execution`。Kubernetes Create 使用 `sandbox_<uuid>`，Local Create 保持数字序列。

- [ ] 先写失败测试：全新的 Kubernetes runtime 未调用 Create，只靠 execution 完成 List/WriteFile 和 Code-run。
- [ ] 先写失败测试：全新的 runtime 只靠 execution refs 完成 pause/resume/delete。
- [ ] 先写失败测试：两个独立 Kubernetes runtime 生成不同 ID；Local 首个 ID 仍为 `sandbox_1`。
- [ ] 运行红灯：

```bash
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'TestKubernetesSandboxRuntimeExistingInstanceWorksWithoutSessionMap|TestKubernetesSandboxRuntimeLifecycleUsesExecutionRefs|TestKubernetesSandboxRuntimeUsesCollisionResistantIDs|TestLocalSandboxRuntimeCreatesRunningSessionWithDevProfile' -count=1
```

- [ ] 实现 files/code-run/lifecycle 无状态路径和内部 ID generator。
- [ ] 先写 Port 失败测试：Create GET 已有 Service 或 404 后 apply；Delete GET、标签校验、DELETE；403/429/500 保持 provider 错误。
- [ ] 实现无状态 Port，禁止调用 local port map。
- [ ] 运行全部 Sandbox adapter 测试：

```bash
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'TestKubernetesSandboxRuntime|TestSandboxFileScripts' -count=1
```

---

### 任务三：持久化 Sandbox 摘要更新

**文件：**
- 修改：`repo/pkg/ports/sandbox_runtime.go`
- 修改：`repo/pkg/adapters/runtime/instance_service.go`
- 修改：`repo/pkg/adapters/runtime/instance_store.go`
- 修改：`repo/services/ani-gateway/internal/router/instances.go`
- 测试：`repo/pkg/adapters/runtime/instance_service_test.go`
- 测试：`repo/pkg/adapters/runtime/instance_store_test.go`
- 测试：`repo/services/ani-gateway/internal/router/instances_test.go`

**行为：** 在内部 `SandboxInstanceStatus` 和 HTTP response mapper 中补齐契约已有的 ports 摘要；`sandbox_status.ports` 是 Core 摘要，Kubernetes Service 是物理真实来源；不新增端口表或 OpenAPI 字段。

- [ ] 先写失败测试：CreatePort 后 PG 摘要包含 available 端口；DeletePort 后移除；pause/resume 外层 state 与 session state 一致。
- [ ] 先写失败测试：provider 成功但 PG 写失败时不得返回成功。
- [ ] 实现 Port handler 和生命周期摘要写回，复用现有 `UpsertStatus`。
- [ ] 验证：

```bash
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime ./services/ani-gateway/internal/router -run 'Test.*Sandbox.*Persists|TestMetadataInstanceStore' -count=1
```

---

### 任务四：新增 PostgreSQL AsyncTaskStore

**文件：**
- 新增：`repo/pkg/ports/async_task.go`
- 新增：`repo/pkg/adapters/runtime/async_task_store.go`
- 新增：`repo/pkg/adapters/runtime/async_task_store_test.go`
- 新增：`repo/deploy/migrations/20260802_001_async_tasks.sql`
- 新增：`repo/scripts/validate_async_task_store.py`
- 新增：`repo/scripts/validate_async_task_store_test.py`
- 修改：`repo/Makefile`
- 修改：`repo/pkg/bootstrap/deps.go`
- 修改：`repo/pkg/bootstrap/instance.go`
- 修改：`repo/pkg/bootstrap/deps_test.go`
- 修改：`repo/services/ani-gateway/main.go`
- 修改：`repo/services/ani-gateway/internal/router/task_resources.go`
- 修改：`repo/services/ani-gateway/internal/router/storage_resources.go`
- 修改：`repo/services/ani-gateway/internal/router/vector_store_resources.go`
- 修改：`repo/services/ani-gateway/internal/router/instances.go`
- 测试：相关 router 测试

**接口：**

```go
type AsyncTaskStore interface {
    Create(context.Context, AsyncTaskRecord) (AsyncTaskRecord, bool, error)
    Get(context.Context, string, string) (AsyncTaskRecord, error)
    Update(context.Context, AsyncTaskUpdate) (AsyncTaskRecord, error)
}
```

表字段覆盖现有 `AsyncTask`：tenant、task UUID、idempotency key、task/resource 类型、状态、attempt、progress、result、error、dead-letter、created/completed 时间。

- [ ] 先写 Local/Metadata Store 失败测试：tenant scoped Get、UUID、状态校验、Create 幂等、Update、result JSON、跨租户 404。
- [ ] 运行红灯：

```bash
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'Test.*AsyncTaskStore' -count=1
```

- [ ] 实现 migration、port、Local adapter 和 Metadata adapter；所有 SQL 使用 tenant transaction。
- [ ] 先写 Router 重启模拟测试：两个 router 实例共享同一 TaskStore，第二个仍能 GET 第一个返回的 task ID。
- [ ] 替换包级 `completedTasks`；Sandbox、storage、vector 的现有 202 helper 都写入注入的 Store。
- [ ] 运行：

```bash
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime ./pkg/bootstrap ./services/ani-gateway/internal/router -run 'Test.*Task|Test.*AsyncTask' -count=1
make validate-async-task-store
```

---

### 任务五：修正真实 Checkpoint 能力边界

**文件：**
- 修改：`repo/pkg/adapters/runtime/kubernetes_sandbox_runtime.go`
- 测试：`repo/pkg/adapters/runtime/kubernetes_sandbox_runtime_test.go`
- 测试：`repo/services/ani-gateway/internal/router/instances_test.go`

- [ ] 先写失败测试：Kubernetes list/create/restore/clone checkpoint 均返回能力不足，HTTP 映射 422，不创建 Task 或 local checkpoint。
- [ ] 运行测试，确认当前 local checkpoint 行为导致红灯。
- [ ] Kubernetes runtime 改为明确 `ErrUnsupported`/`ErrFailedPrecondition`；Local profile 保持现状。
- [ ] 验证：

```bash
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime ./services/ani-gateway/internal/router -run 'Test.*SandboxCheckpoint' -count=1
```

---

### 任务六：实现契约级 Redis 幂等策略

**文件：**
- 修改：`repo/services/ani-gateway/internal/middleware/idempotency.go`
- 测试：`repo/services/ani-gateway/internal/middleware/idempotency_test.go`
- 测试：`repo/services/ani-gateway/internal/router/instances_test.go`

**行为：** 普通记录增加请求指纹；带 key 的 DELETE 进入 middleware；Token 使用 response 和 metadata/tombstone 双记录。

- [ ] 先写失败测试：相同 DELETE key 重放第一次响应，handler 只执行一次。
- [ ] 先写失败测试：相同 key/path 不同 body 返回 409，第二次不执行 handler；缓存只保存 SHA-256 指纹。
- [ ] 实现 DELETE 和 fingerprint；冲突错误码固定为 `IDEMPOTENCY_KEY_REUSED`。
- [ ] 先写可控时钟 Token 测试：首次 201、有效期内原响应、过期后 409 `IdempotencyResultExpired`、tombstone 无 token 原文。
- [ ] 实现 Token 双记录：response TTL 到 expires_at，metadata TTL 24h。
- [ ] 验证：

```bash
GOCACHE=/tmp/ani-go-cache go test ./services/ani-gateway/internal/middleware ./services/ani-gateway/internal/router -run 'Test.*Idempot|Test.*SandboxToken' -count=1
```

---

### 任务七：增加契约级 Gateway 重启 Live Gate

**文件：**
- 新增：`repo/deploy/real-k8s-lab/instance-sandbox-stateless-live-gate.yaml`
- 新增：`repo/scripts/validate_instance_sandbox_stateless_live_gate.py`
- 新增：`repo/scripts/validate_instance_sandbox_stateless_live_gate_test.py`
- 修改：`repo/Makefile`
- 新增：`repo/development-records/live-evidence/instance-sandbox-stateless-live-20260802.json`

**必需检查：**

```text
core-sandbox-create-running
file-port-coderun-before-restart
gateway-rollout-restart
file-port-task-after-restart
idempotency-replay-after-restart
token-expiry-conflict
checkpoint-provider-capability-422
sandbox-pause-resume-delete
postgres-and-kubernetes-cleanup
```

- [ ] 先写 Validator 契约测试：必需 checks、task persistence、token 409、checkpoint 422、evidence 敏感字段禁令。
- [ ] 实现静态 gate 和 validator；异常与 evidence 不输出 token、code、输出、preview URL、IP 或 endpoint。
- [ ] 构建、push、部署可回溯 Gateway 镜像，记录标签和 digest。
- [ ] 执行真实顺序：

```text
create → file/port/code-run → save task/key
restart Gateway
file read → port close → task GET
same-key replay → different-intent 409
short token replay → expiry 409
checkpoint 422
pause → resume → delete
PG deleted → no Deployment/Pod/Service
```

- [ ] 归档脱敏 evidence 并运行：

```bash
cd repo
make validate-instance-sandbox-stateless-live-gate
python3 scripts/validate_yaml.py deploy/real-k8s-lab/instance-sandbox-stateless-live-gate.yaml
```

---

### 任务八：记录 Feature Batch 并执行完整门禁

**文件：**
- 新增：`repo/development-records/instance-sandbox-stateless-a.md`
- 修改：`repo/development-records/README.md`
- 修改：`repo/CURRENT-SPRINT.md`
- 修改：`ANI-06-开发计划.md`

- [ ] 更新四份记录：契约依据、无状态 Runtime、PG TaskStore、Redis 幂等、checkpoint 422、真实重启结果和 `emptyDir` 风险。
- [ ] 运行 focused 和专项门禁：

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime ./pkg/bootstrap ./services/ani-gateway/internal/middleware ./services/ani-gateway/internal/router -count=1
python3 scripts/validate_instance_sandbox_stateless_live_gate_test.py
PATH=/tmp/ani-pybin:$PATH make validate-openapi-spec validate-core-api-compatibility validate-architecture validate-doc-entrypoints validate-instance-sandbox-stateless-live-gate
```

- [ ] 运行完整回归：

```bash
PATH=/tmp/ani-pybin:$PATH make test
git diff --check
```

- [ ] 扫描 evidence 和记录中的 bearer、JWT、token 原文、code/stdout/stderr、credential、password、IP、URL 和完整 endpoint，预期无敏感值。
- [ ] 输出改动、migration、测试、live evidence、剩余风险和 `git status --short`；不执行 commit，等待用户明确确认。
