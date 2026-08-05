# Sandbox Runtime 契约级无状态化设计

> 日期：2026-08-02
> 批次：`INSTANCE-SANDBOX-STATELESS-A`
> 范围：仅 ANI Core
> 状态：已确认设计

## 目标

按照现有 Core OpenAPI v1 契约，把真实 Kubernetes SandboxRuntime 从进程内 session 模型改为无状态执行模型。Gateway 重启或多副本切换后，文件、预览端口、code-run、生命周期、幂等重放和异步任务查询必须继续符合 v1；未实现的真实 checkpoint 能力必须明确返回 422，不得用本地内存结果冒充 provider 能力。

## 契约依据

- `SandboxInstanceStatus` 已定义 session、ports、checkpoints 和 files summary，Core 必须返回稳定摘要。
- Token 在有效期内使用同一幂等键必须重放原结果；过期后必须返回 `409 IdempotencyResultExpired`。
- Port、File、Checkpoint 和 Code-run 写操作都携带幂等键。
- Checkpoint 和 Code-run 返回 `AsyncTask`，并通过 `/tasks/{task_id}` 查询。
- 跨租户资源按 404 处理；kind、状态或 provider 能力不满足时返回 422。
- Token、code、stdin、stdout 和 stderr 不得进入普通日志或普通审计。

本批不修改上述契约，只修正实现使其符合已经冻结的 v1。

## 当前问题

### 真实 Runtime 依赖进程内状态

`KubernetesSandboxRuntime` 内嵌 `LocalSandboxRuntime`，并依赖 `instances`、`refs`、`ports` 等 map。PG 和 Kubernetes 资源仍在时，Gateway 重启会清空这些 map，导致真实资源被错误返回为 `ErrNotFound`。

### 异步任务只存在 Gateway 内存

`/tasks/{task_id}` 当前读取包级 `completedTasks` map。Gateway 重启后，已经返回给客户端的 task ID 变成 404，违反 `AsyncTask` 查询契约。

### 通用幂等实现不完整

现有 Redis middleware 只覆盖 POST/PUT/PATCH，不覆盖带 `Idempotency-Key` 的 DELETE；也不比较请求指纹。Token 响应固定缓存 24 小时，会在 token 已过期后继续重放 201，而契约要求返回 409。

### 真实 Checkpoint 能力被本地实现伪装

Kubernetes runtime 把 checkpoint 方法委托给进程内 local runtime。该结果既不是 Kata/CRI provider checkpoint，也无法跨重启查询，不符合 provider 能力语义。

### 实例 ID 可能重启后重复

真实 Sandbox ID 使用进程内数字序列。Gateway 重启或多副本并发时可能重新生成同租户已有 ID，并覆盖 PG 记录。

## 选定架构

```text
PostgreSQL
  ├─ workload_instances / sandbox_status：Core 实例摘要和 provider refs
  └─ async_tasks：可查询异步任务

Redis
  ├─ 普通写请求 24h 幂等响应
  └─ Token 有效期响应 + 24h 过期 tombstone

Kubernetes
  ├─ Deployment / Pod：运行状态和文件执行目标
  └─ Service：预览端口物理状态

Gateway
  └─ 只编排单次请求，不保存权威 Sandbox 状态
```

## 一、真实 Runtime 无状态化

### 请求级执行上下文

应用层每个请求已经通过 `WorkloadInstanceStore.Get(tenantID, instanceID)` 取得 PG record。新增内部执行上下文：

```go
type SandboxExecutionContext struct {
    Record       WorkloadInstanceRecord
    Sandbox      SandboxInstanceStatus
    ResourceRefs []string
}
```

由应用层完成：

1. tenant-scoped PG 查询。
2. kind、state、provider 和 Sandbox 摘要校验。
3. 从同一个 record 构建 `SandboxExecutionContext`。
4. 将 context 和操作请求一起交给真实 runtime。

真实 Kubernetes runtime 不查询 PG，不恢复共享 session map，也不把 map 当作 fallback。Local profile 可以继续使用内存实现，但不得参与真实 provider 状态判断。

### 内部 Port 形态

保留公开 HTTP 契约，调整内部 Sandbox port，使已存在实例的操作显式接收执行上下文。创建仍通过 `Create` 返回初始状态；后续能力以 context 为输入：

```go
ApplyLifecycle(ctx, execution, request)
CreateToken(ctx, execution, request)
CreatePort(ctx, execution, request)
DeletePort(ctx, execution, request)
ListFiles(ctx, execution, request)
WriteFile(ctx, execution, request)
DeleteFile(ctx, execution, request)
CreateCodeRun(ctx, execution, request)
```

这比 `RestoreSession(record)` 更安全：没有跨请求共享的可变 session，不存在 Gateway A/B 缓存状态不同的问题。

## 二、各能力的数据来源

| 能力 | Core 权威来源 | Provider 权威来源 | 写回 |
|---|---|---|---|
| 实例身份、配置、状态 | PG `workload_instances` | 无 | PG |
| Deployment refs | PG `resource_refs` | Kubernetes Deployment | PG |
| Preview ports | PG `sandbox_status.ports` 摘要 | Kubernetes Service | PG 摘要 |
| Files | PG 只保存 summary | Pod `/workspace` | PG summary（可选更新） |
| Lifecycle | PG 当前状态 | Deployment scale/delete | PG 状态 |
| Token 重放 | Redis 有效期记录 | 无 | Redis，不进普通审计 |
| Code-run Task | PG `async_tasks` | Pod exec | PG task result |
| Checkpoint | PG task/checkpoint metadata | 真实 provider（当前未实现） | 未实现时 422 |

## 三、Preview Port 无状态操作

Service 名称由实例名称和目标端口确定。每次操作直接观察 Kubernetes，不读取本地 port map。

### CreatePort

1. 从执行上下文取得 tenant、instance name 和 running 状态。
2. GET 确定名称的 Service。
3. 已存在时校验 tenant、instance、managed 和 target-port 标签，读取 NodePort 并返回稳定结果。
4. 不存在时 server-side apply Service，读取 NodePort。
5. 更新 `sandbox_status.ports` 摘要。

### DeletePort

1. GET Service 并校验归属标签。
2. DELETE Service；provider 已经 404 时按契约和幂等记录处理。
3. 从 `sandbox_status.ports` 移除端口并写回 PG。

403、429、5xx、超时和网络错误保持 provider 错误，不得转换为 404。

## 四、Files 和 Code-run

Files 与 code-run 每次根据执行上下文中的 tenant 和实例名称，用标签查询 Ready Pod，再操作 `/workspace`。

- Gateway 重启不影响 Pod 内现有 workspace。
- Pod 重建或节点故障会丢失 `emptyDir`，属于后续持久化 workspace 批次。
- 文件路径安全继续使用目录 fd、`O_NOFOLLOW`、`dir_fd` 和 hard-link 防护。
- code、stdin、stdout、stderr 不写普通日志或普通审计。

## 五、持久化 AsyncTask

新增 Core port：

```go
type AsyncTaskStore interface {
    Create(ctx context.Context, task AsyncTaskRecord) (AsyncTaskRecord, bool, error)
    Get(ctx context.Context, tenantID, taskID string) (AsyncTaskRecord, error)
    Update(ctx context.Context, task AsyncTaskUpdate) (AsyncTaskRecord, error)
}
```

实现：

- `LocalAsyncTaskStore`：dev/local profile。
- `MetadataAsyncTaskStore`：真实 PG profile。

新增 `async_tasks` 表，字段覆盖现有 `AsyncTask` schema：tenant、task UUID、幂等键、task type、resource type/id、状态、attempt、progress、result、error、dead-letter 和时间戳。所有查询必须 tenant scoped。

Checkpoint、code-run 以及当前使用 `storageCompletedTask` 的 Core 202 路径逐步接入该 Store。本批至少完成 Sandbox checkpoint/code-run 和 `/tasks/{task_id}`，并让 router 不再依赖包级内存 map。

Task result 是专用任务数据，不属于普通审计；访问保持租户隔离，handler 和日志不得打印 result 内容。

## 六、契约级幂等

### 普通写操作

Redis 幂等记录包含：

```text
tenant_id
method
route/path
idempotency_key_hash
request_fingerprint
state
status_code
content_type
response_body
created_at
```

- 覆盖 POST、PUT、PATCH，以及携带 `Idempotency-Key` 的 DELETE。
- 同 key、同请求指纹：重放第一次完整响应。
- 同 key、不同请求指纹：返回 409，不执行 provider mutation。
- processing 冲突返回现有 `IDEMPOTENCY_IN_PROGRESS`。
- Redis 不可用时失败关闭为 503。
- 普通记录 TTL 为 24 小时。

Fingerprint 只保存 SHA-256，不保存 code、stdin、token 或文件正文。

### Token 特殊策略

Token 使用两条 Redis 记录：

1. 有效期 response：保存原始 token 响应，TTL 到 `expires_at`。
2. 24 小时 metadata/tombstone：只保存请求 fingerprint 和 token expiry，不保存 token。

重放规则：

- response 仍存在：返回原 201 响应。
- metadata 存在但 response 已过期：返回 `409 IdempotencyResultExpired`。
- 两者都不存在：签发新 token。

Token 原文不得写 PG、普通审计、日志或 evidence。

## 七、Checkpoint 能力边界

在真实 Kata/CRI checkpoint provider 接入前：

- Kubernetes Sandbox 的 list/create/restore/clone checkpoint 返回 422 provider capability unavailable。
- 不创建 local checkpoint，不创建虚假 `available` 状态。
- Local/dev profile 可以保留现有内存 checkpoint，用 `dev_profile.real_provider=false` 明确边界。

真实 checkpoint 单独作为后续 feature batch 实施。

## 八、防冲突实例 ID

- Local/dev profile 保留 `sandbox_1` 数字序列。
- Kubernetes real provider 使用 `sandbox_<uuid>`。
- PG 主键和 tenant 隔离保持不变。
- OpenAPI 已把 instance ID 定义为不透明字符串，不需要契约变更。

## 错误与状态语义

- tenant scoped PG 查询找不到：404。
- 跨租户：404，不泄漏资源存在性。
- 非 Sandbox、非 running 写操作或 provider 能力不足：422。
- 请求格式、路径、端口非法：400。
- 相同幂等键不同 intent：409。
- Token 幂等结果过期：409 `IdempotencyResultExpired`。
- 文件已存在且 `overwrite=false`：409。
- 文件过大：413。
- provider 主资源丢失：由 reconcile 写回 `failed/ProviderResourceLost`。

## 真实验证

真实门禁必须覆盖：

1. 创建真实 Sandbox，写文件、开端口、执行 code-run，保存 task ID 和幂等键。
2. 重启 Gateway。
3. 文件仍可查询，端口仍可读取和关闭，task ID 仍可 GET。
4. 相同幂等键重放相同响应；相同 key 不同 intent 返回 409。
5. 签发短期 token，有效期内重放原结果；过期后同 key 返回 409。
6. 真实 checkpoint 返回 422，不产生虚假数据。
7. pause、resume、delete 正常，PG 最终 `deleted`，Kubernetes 无 Deployment、Pod、Service 残留。
8. Evidence 不包含 token、code、输出、IP、preview URL、凭据或完整内部 endpoint。

## 明确不做

- 修改 Core OpenAPI v1、SDK、CLI、Console 或 Services。
- 在本批实现真实 Kata/CRI checkpoint。
- 让 `emptyDir` 跨 Pod 或节点故障持久化。
- 把敏感 token 或 code-run 内容写入普通审计。
- 声明完整平台 production ready。

## 验收标准

- Kubernetes Sandbox 已存在实例操作不依赖任何进程内 session/refs/ports map。
- 每次请求只读取一次 PG record，并显式传入 runtime。
- `/tasks/{task_id}` 在 Gateway 重启后仍可查询。
- DELETE、请求指纹和 Token 过期重放符合 v1。
- 真实 checkpoint 未实现时稳定返回 422。
- 实例 ID 不因重启或多副本冲突。
- OpenAPI v1 和 Services 无改动。
- focused tests、migration tests、完整 `make test`、架构、兼容、文档、live gate 和 diff 检查全部通过。
