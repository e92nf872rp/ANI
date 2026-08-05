# Sandbox Checkpoint Real Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标：** 在不修改 ANI Core v1 OpenAPI 的前提下，为新建 Kubernetes Sandbox 提供基于 Rook-Ceph PVC 与 CSI VolumeSnapshot 的文件系统 checkpoint、restore 和 clone，并保证 Gateway 重启后仍可查询和操作。

**架构：** 新建 Sandbox 的 `/workspace` 从 `emptyDir` 改为独立 PVC；VolumeSnapshot CR 是 checkpoint 元数据和状态的 provider source of truth，PG 继续保存实例、resource refs 和 AsyncTask。checkpoint 创建采用短暂停机取得 crash-consistent 文件系统快照；restore 由原 snapshot 重建同名 PVC；clone 将 snapshot source ref 作为内部创建参数传给 Sandbox runtime。旧 `emptyDir` Sandbox 和 `keep_memory=true` 稳定返回 422。

**技术栈：** Go 1.25、CloudWeGo Hertz、PostgreSQL 17、Kubernetes REST API、Rook-Ceph RBD CSI、VolumeSnapshot `snapshot.storage.k8s.io/v1`、Python 3.12 live gate。

## 全局约束

- 只修改 ANI Core；不实现或修改 ANI Services。
- `repo/api/openapi/v1.yaml` 保持不变，所有响应继续符合现有 v1 schema。
- 只在 `main` 分支工作；任何 commit 都必须由用户在当轮明确批准。
- 新 Sandbox 使用默认 StorageClass，默认申请 `5Gi` workspace PVC；不新增公开容量字段。
- `keep_memory=false` 只保存 `/workspace` 文件系统；`keep_memory=true` 返回 `ports.ErrUnsupported`，Gateway 映射 422。
- 旧实例的 `resource_refs` 不含 PVC 时，checkpoint/restore/clone 返回 422，不执行在线迁移。
- VolumeSnapshot 只允许当前 tenant 和 instance 标签匹配时访问。
- v1 没有 checkpoint DELETE API；删除 Sandbox 时同步删除其全部受管 VolumeSnapshot，避免不可回收资源。
- checkpoint 名称、clone 名称和幂等键必须通过现有验证；不得把 Kubernetes 对象、内部地址或凭据暴露到 v1 响应。
- 严格执行 TDD：每个行为先写失败测试、确认 RED，再写最小实现并确认 GREEN。
- Feature batch 完成后更新 development record、README、CURRENT-SPRINT 和 ANI-06 四个位置。

---

## 文件结构

**新增：**

- `repo/pkg/adapters/runtime/kubernetes_sandbox_checkpoints.go`：PVC、VolumeSnapshot、等待、标签过滤和 restore 辅助函数。
- `repo/pkg/adapters/runtime/kubernetes_sandbox_checkpoints_test.go`：真实 adapter HTTP fixture 测试。
- `repo/deploy/real-k8s-lab/instance-sandbox-checkpoint-live-gate.yaml`：真实底座 gate 契约。
- `repo/scripts/validate_instance_sandbox_checkpoint_live_gate.py`：gate/evidence validator 和 live runner 入口。
- `repo/scripts/validate_instance_sandbox_checkpoint_live_gate_test.py`：validator 负例与脱敏测试。
- `repo/development-records/instance-sandbox-checkpoint-a.md`：Feature batch 记录。
- `repo/development-records/live-evidence/instance-sandbox-checkpoint-live-20260802.json`：脱敏 live evidence。

**修改：**

- `repo/pkg/ports/sandbox_runtime.go`：内部 checkpoint source ref、provider ref 和 workspace 常量语义。
- `repo/pkg/ports/workload_runtime.go`：`WorkloadSpec` 增加内部 Sandbox checkpoint source ref。
- `repo/pkg/adapters/runtime/dryrun_renderer.go`：Sandbox PVC manifest 和 PVC volume mount。
- `repo/pkg/adapters/runtime/kubernetes_sandbox_runtime.go`：创建顺序、Deployment ref 查找、checkpoint 接线和删除清理。
- `repo/pkg/adapters/runtime/kubernetes_rest_client.go`：Sandbox observation 从 refs 中定位 Deployment。
- `repo/pkg/adapters/runtime/instance_service.go`：向 Sandbox create request 传递内部 checkpoint source ref。
- `repo/services/ani-gateway/internal/router/instances.go`：clone 传源镜像和 snapshot ref；维持 AsyncTask 写 PG。
- 对应 Go 测试、`repo/Makefile`、四份进度文档。

---

### Task 1：固定内部数据契约和 legacy 拒绝条件

**文件：**

- 修改：`repo/pkg/ports/sandbox_runtime.go`
- 修改：`repo/pkg/ports/workload_runtime.go`
- 测试：`repo/pkg/adapters/runtime/kubernetes_sandbox_runtime_test.go`

**接口：**

- 产出：`SandboxCreateRequest.CheckpointSourceRef string`
- 产出：`WorkloadSpec.SandboxCheckpointSourceRef string`
- 产出：`SandboxCheckpointResult.ProviderRef string`
- 产出：`sandboxWorkspacePVCRef([]string) (string, bool)`

- [ ] **Step 1：先写 legacy 和 keep-memory 失败测试**

```go
func TestKubernetesSandboxCheckpointRejectsLegacyEmptyDir(t *testing.T) {
    runtime := newCheckpointTestRuntime(t)
    execution := checkpointExecution("sandbox-a", []string{"kubernetes/Deployment/sandbox-a"})
    _, err := runtime.CreateCheckpoint(context.Background(), ports.SandboxCheckpointCreateRequest{
        TenantID: "tenant-a", InstanceID: execution.InstanceID, Execution: &execution,
        IdempotencyKey: "checkpoint-a", Name: "before-change",
    })
    if !errors.Is(err, ports.ErrUnsupported) {
        t.Fatalf("CreateCheckpoint() error = %v, want ErrUnsupported", err)
    }
}

func TestKubernetesSandboxCheckpointRejectsKeepMemory(t *testing.T) {
    execution := checkpointExecution("sandbox-a", []string{
        "kubernetes/Deployment/sandbox-a",
        "kubernetes/PersistentVolumeClaim/sandbox-a-workspace",
    })
    _, err := runtime.CreateCheckpoint(context.Background(), ports.SandboxCheckpointCreateRequest{
        TenantID: "tenant-a", InstanceID: execution.InstanceID, Execution: &execution,
        IdempotencyKey: "checkpoint-memory", Name: "memory", KeepMemory: true,
    })
    if !errors.Is(err, ports.ErrUnsupported) {
        t.Fatalf("CreateCheckpoint() error = %v, want ErrUnsupported", err)
    }
}
```

- [ ] **Step 2：运行测试确认 RED**

运行：

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'TestKubernetesSandboxCheckpointRejects(LegacyEmptyDir|KeepMemory)' -count=1
```

预期：FAIL，当前 Kubernetes runtime 对所有 checkpoint 统一返回“provider is not configured”，还不能区分 PVC 与内存能力。

- [ ] **Step 3：增加最小内部字段和 PVC ref 解析**

```go
type SandboxCreateRequest struct {
    // existing fields...
    CheckpointSourceRef string
}

type SandboxCheckpointResult struct {
    // existing public-mapped fields...
    ProviderRef string
}

func sandboxWorkspacePVCRef(refs []string) (string, bool) {
    for _, ref := range refs {
        resource, err := resourceFromRef("kubernetes", "", ref)
        if err == nil && resource.Kind == "PersistentVolumeClaim" {
            return ref, true
        }
    }
    return "", false
}
```

- [ ] **Step 4：实现两个明确错误边界**

```go
if request.KeepMemory {
    return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: sandbox memory checkpoint is not supported", ports.ErrUnsupported)
}
if _, ok := sandboxWorkspacePVCRef(instance.ResourceRefs); !ok {
    return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: legacy emptyDir sandbox must be recreated before checkpoint", ports.ErrUnsupported)
}
```

- [ ] **Step 5：运行聚焦测试确认 GREEN**

运行同 Step 2；预期 PASS。

- [ ] **Step 6：准备提交点**

建议提交信息：`feat(core): define Sandbox filesystem checkpoint boundary`

---

### Task 2：让新 Sandbox 使用 PVC，并修正多 resource refs 生命周期

**文件：**

- 修改：`repo/pkg/adapters/runtime/dryrun_renderer.go`
- 修改：`repo/pkg/adapters/runtime/kubernetes_sandbox_runtime.go`
- 修改：`repo/pkg/adapters/runtime/kubernetes_rest_client.go`
- 测试：`repo/pkg/adapters/runtime/kubernetes_sandbox_runtime_test.go`
- 测试：`repo/pkg/adapters/runtime/kubernetes_rest_client_test.go`

**接口：**

- 产出：`renderSandboxWorkspacePVC(spec ports.WorkloadSpec) ports.WorkloadManifest`
- 产出：`sandboxDeploymentRef([]string) (string, error)`
- 依赖：Task 1 的 `WorkloadSpec.SandboxCheckpointSourceRef`

- [ ] **Step 1：写 PVC manifest 和 refs 顺序失败测试**

```go
func TestKubernetesDryRunRendererRendersSandboxWorkspacePVC(t *testing.T) {
    manifests, err := NewKubernetesDryRunRenderer(nil).Render(context.Background(), ports.WorkloadSpec{
        TenantID: "tenant-a", Name: "sandbox-a", Kind: ports.WorkloadKindSandbox,
        Image: "registry/sandbox:1", Sandbox: &ports.SandboxConfig{RuntimeClass: "sandbox-kata"},
    })
    if err != nil { t.Fatal(err) }
    if manifests[0].Kind != "PersistentVolumeClaim" || manifests[1].Kind != "Deployment" {
        t.Fatalf("manifest kinds = %s,%s, want PVC,Deployment", manifests[0].Kind, manifests[1].Kind)
    }
    requireContains(t, manifests[0].Content, `storage: 5Gi`)
    requireContains(t, manifests[1].Content, `claimName: sandbox-a-workspace`)
    requireNotContains(t, manifests[1].Content, `emptyDir:`)
}
```

- [ ] **Step 2：运行测试确认 RED**

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'TestKubernetesDryRunRendererRendersSandboxWorkspacePVC|TestKubernetesSandboxRuntime.*ResourceRefs' -count=1
```

预期：FAIL，当前 manifest 只有 Deployment 且 workspace 为 `emptyDir`。

- [ ] **Step 3：渲染 PVC 和 snapshot dataSource**

```go
func renderSandboxWorkspacePVC(spec ports.WorkloadSpec) ports.WorkloadManifest {
    pvcSpec := map[string]any{
        "accessModes": []any{"ReadWriteOnce"},
        "resources": map[string]any{"requests": map[string]any{"storage": "5Gi"}},
    }
    if spec.SandboxCheckpointSourceRef != "" {
        pvcSpec["dataSource"] = map[string]any{
            "apiGroup": "snapshot.storage.k8s.io",
            "kind": "VolumeSnapshot",
            "name": checkpointSnapshotName(spec.SandboxCheckpointSourceRef),
        }
    }
    return manifestForPVC(spec, spec.Name+"-workspace", pvcSpec)
}
```

`Render` 对 Sandbox 返回 `[PVC, Deployment]`；Pod volume 固定为：

```go
map[string]any{
    "name": "sandbox-workspace",
    "persistentVolumeClaim": map[string]any{"claimName": spec.Name + "-workspace"},
}
```

- [ ] **Step 4：修复所有依赖 refs[0] 的 Sandbox 路径**

`scaleDeployment`、`Observe` 和 reconcile 不再假设第一个 ref 是 Deployment，而是遍历寻找 `kubernetes/Deployment/*`。删除 refs 使用逆序，因此先删 Deployment、后删 PVC。

```go
func sandboxDeploymentRef(refs []string) (string, error) {
    for _, ref := range refs {
        resource, err := resourceFromRef("kubernetes", "", ref)
        if err == nil && resource.Kind == "Deployment" {
            return ref, nil
        }
    }
    return "", fmt.Errorf("%w: sandbox Deployment ref is required", ports.ErrInvalid)
}
```

- [ ] **Step 5：运行聚焦测试确认 GREEN**

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'Sandbox|KubernetesDryRunRenderer|KubernetesRESTClient' -count=1
```

预期：PASS；现有 Container/VM renderer 测试保持不变。

- [ ] **Step 6：准备提交点**

建议提交信息：`feat(core): back Sandbox workspace with persistent volume`

---

### Task 3：实现 VolumeSnapshot create/list source of truth

**文件：**

- 新增：`repo/pkg/adapters/runtime/kubernetes_sandbox_checkpoints.go`
- 新增：`repo/pkg/adapters/runtime/kubernetes_sandbox_checkpoints_test.go`
- 修改：`repo/pkg/adapters/runtime/kubernetes_sandbox_runtime.go`

**接口：**

- 产出：`createWorkspaceCheckpoint(context.Context, ports.SandboxCheckpointCreateRequest, ports.SandboxInstanceStatus) (ports.SandboxCheckpointResult, error)`
- 产出：`listWorkspaceCheckpoints(context.Context, ports.SandboxCheckpointListRequest, ports.SandboxInstanceStatus) (ports.SandboxCheckpointListResult, error)`
- 产出：`getWorkspaceCheckpoint(context.Context, tenantID, instanceID, checkpointID string) (ports.SandboxCheckpointResult, error)`

- [ ] **Step 1：写 create/list HTTP fixture 失败测试**

测试服务器必须断言：

```text
PATCH /apis/snapshot.storage.k8s.io/v1/namespaces/ani-tenant-tenant-a/volumesnapshots/{deterministic-name}
GET   /apis/snapshot.storage.k8s.io/v1/namespaces/ani-tenant-tenant-a/volumesnapshots?labelSelector=...
```

创建 body 必须包含：

```yaml
spec:
  source:
    persistentVolumeClaimName: sandbox-a-workspace
metadata:
  labels:
    ani.kubercloud.io/sandbox-checkpoint: "true"
    ani.kubercloud.io/sandbox-instance-id: instance-a
```

- [ ] **Step 2：运行测试确认 RED**

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'TestKubernetesSandboxCheckpoint(Create|List)' -count=1
```

预期：FAIL，方法仍返回 `ErrUnsupported`。

- [ ] **Step 3：实现确定性 checkpoint ID 和 manifest**

```go
func sandboxCheckpointID(tenantID, instanceID, idempotencyKey string) string {
    return uuid.NewSHA1(uuid.NameSpaceOID, []byte(tenantID+"\x00"+instanceID+"\x00"+idempotencyKey)).String()
}
```

Kubernetes 对象名使用 `sandbox-checkpoint-` 加 UUID；原始展示名只放 label/annotation，必须经过 Kubernetes label value 校验或放 annotation。

- [ ] **Step 4：创建时短暂停机并等待 readyToUse**

执行顺序固定为：

```text
记录原状态 → running 时 scale 0 → 等待 Pod 消失
→ apply VolumeSnapshot → 轮询 status.readyToUse=true
→ 原状态 running 时 scale 1 → 等待 Ready Pod
```

使用 `defer` 保证 snapshot 创建失败时也尝试恢复原副本数；恢复失败需要和原始错误一起返回，不能伪装成功。

- [ ] **Step 5：实现 list、分页和租户标签校验**

`limit` 保持 v1 的 1..100；cursor 使用十进制 offset，与 Local runtime 行为一致。每个 item 只映射：`id/name/status/keep_memory=false/created_at/size_bytes/reason`，`ProviderRef` 不进入 HTTP mapper。

- [ ] **Step 6：运行聚焦测试确认 GREEN**

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'TestKubernetesSandboxCheckpoint' -count=1
```

预期：create/list、幂等对象名、ready 等待、错误恢复副本数全部 PASS。

- [ ] **Step 7：准备提交点**

建议提交信息：`feat(core): create and list Sandbox volume checkpoints`

---

### Task 4：实现原实例 restore

**文件：**

- 修改：`repo/pkg/adapters/runtime/kubernetes_sandbox_checkpoints.go`
- 测试：`repo/pkg/adapters/runtime/kubernetes_sandbox_checkpoints_test.go`

**接口：**

- 产出：`restoreWorkspaceCheckpoint(context.Context, ports.SandboxCheckpointRestoreRequest, ports.SandboxInstanceStatus) (ports.SandboxCheckpointResult, error)`

- [ ] **Step 1：写 restore 顺序失败测试**

fixture 记录并断言以下调用顺序：

```text
GET VolumeSnapshot（验证 tenant + instance label、readyToUse）
PATCH Deployment/scale replicas=0
DELETE PersistentVolumeClaim/sandbox-a-workspace
GET PVC 直到 404
PATCH PVC，dataSource.name={snapshot-name}
PATCH Deployment/scale replicas=1
GET Pod 直到 Ready
```

- [ ] **Step 2：运行测试确认 RED**

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run TestKubernetesSandboxCheckpointRestore -count=1
```

预期：FAIL，restore 仍返回 `ErrUnsupported`。

- [ ] **Step 3：实现安全 restore**

关键规则：

```go
if checkpoint.Status != "available" {
    return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: checkpoint is not available", ports.ErrFailedPrecondition)
}
```

删除原 PVC 后 snapshot 仍保留，所以失败可重试。PVC 使用原名称，PG resource refs 无需改写。原 Sandbox 若为 paused/stopped，restore 后保持 0 副本；原状态 running 才恢复为 1。

- [ ] **Step 4：覆盖失败恢复语义**

必须有测试证明：

- snapshot 不属于当前实例时返回 `ErrNotFound`；
- snapshot 未 ready 时返回 `ErrFailedPrecondition`；
- PVC recreate 失败时不 scale 1；
- scale 1 或 Pod Ready 失败时返回错误，不生成 completed AsyncTask。

- [ ] **Step 5：运行聚焦测试确认 GREEN**

运行同 Step 2；预期全部 PASS。

- [ ] **Step 6：准备提交点**

建议提交信息：`feat(core): restore Sandbox workspace checkpoints`

---

### Task 5：实现 clone，并修复源镜像传递

**文件：**

- 修改：`repo/pkg/adapters/runtime/instance_service.go`
- 修改：`repo/services/ani-gateway/internal/router/instances.go`
- 修改：`repo/pkg/adapters/runtime/kubernetes_sandbox_runtime.go`
- 测试：`repo/pkg/adapters/runtime/instance_service_test.go`
- 测试：`repo/services/ani-gateway/internal/router/instances_test.go`

**接口：**

- 依赖：Task 1 的 `SandboxCheckpointResult.ProviderRef`
- 产出：clone 创建请求中的 `WorkloadSpec.Image` 和 `WorkloadSpec.SandboxCheckpointSourceRef`

- [ ] **Step 1：写 clone 失败测试**

```go
func TestCloneSandboxCheckpointUsesSourceImageAndSnapshot(t *testing.T) {
    // source record image = registry/sandbox:3.12
    // runtime CloneCheckpoint returns ProviderRef = kubernetes/VolumeSnapshot/sandbox-checkpoint-a
    // assert service.Create receives both values and returns a distinct instance ID
}
```

同时覆盖同租户、checkpoint ownership、clone 名称冲突和相同 idempotency key 重放。

- [ ] **Step 2：运行测试确认 RED**

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./services/ani-gateway/internal/router ./pkg/adapters/runtime -run 'CloneSandboxCheckpoint|SandboxCheckpointClone' -count=1
```

预期：FAIL，当前 clone 没有向新实例传递源 image 和 snapshot source。

- [ ] **Step 3：传递内部 clone source**

Gateway 创建新实例时必须使用：

```go
Spec: ports.WorkloadSpec{
    TenantID: instanceTenantID(c),
    Name: req.Name,
    Kind: ports.WorkloadKindSandbox,
    Image: record.Image.Ref,
    ImageRef: record.Image.Ref,
    Sandbox: &config,
    SandboxCheckpointSourceRef: checkpoint.ProviderRef,
    Lifecycle: ports.InstanceLifecyclePolicy{AutoStart: true},
}
```

Instance service 把 `SandboxCheckpointSourceRef` 传给 `SandboxCreateRequest.CheckpointSourceRef`；renderer 由 Task 2 生成带 VolumeSnapshot dataSource 的新 PVC。

- [ ] **Step 4：运行测试确认 GREEN**

运行同 Step 2；预期 PASS，并验证 clone 不修改源 Sandbox 或源 snapshot。

- [ ] **Step 5：准备提交点**

建议提交信息：`feat(core): clone Sandbox from filesystem checkpoint`

---

### Task 6：收口删除、PG task 和 Gateway 重启语义

**文件：**

- 修改：`repo/pkg/adapters/runtime/kubernetes_sandbox_runtime.go`
- 修改：`repo/pkg/adapters/runtime/kubernetes_sandbox_checkpoints.go`
- 修改：`repo/services/ani-gateway/internal/router/instances_test.go`
- 修改：`repo/services/ani-gateway/internal/middleware/idempotency_test.go`
- 测试：`repo/pkg/adapters/runtime/kubernetes_sandbox_runtime_test.go`

**接口：**

- 产出：`cleanupWorkspaceCheckpoints(context.Context, tenantID, instanceID string) error`

- [ ] **Step 1：写删除和重启失败测试**

测试必须证明：

```text
Sandbox delete → preview Services → Deployment/PVC → managed VolumeSnapshots 全部删除
Gateway/runtime 新实例 → ListCheckpoint 仍从 Kubernetes 返回原 checkpoint
restore/create 完成后 AsyncTask 可从 MetadataAsyncTaskStore 查询
同一 restore idempotency key 重放原 task；不同 checkpoint path 不会命中同一 cache scope
```

- [ ] **Step 2：运行测试确认 RED**

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime ./services/ani-gateway/internal/router ./services/ani-gateway/internal/middleware -run 'Checkpoint|SandboxDeleteCleans' -count=1
```

预期：至少删除 snapshot 和 runtime restart list 测试失败。

- [ ] **Step 3：实现删除垃圾回收**

只删除同时满足以下标签的 VolumeSnapshot：

```text
ani.kubercloud.io/sandbox-checkpoint=true
ani.kubercloud.io/sandbox-instance-id={instanceID}
ani.kubercloud.io/tenant-id={tenantID}
```

任一 checkpoint 删除失败时 Sandbox delete 返回错误，PG 不得先标记 deleted。

- [ ] **Step 4：运行聚焦测试确认 GREEN**

运行同 Step 2；预期 PASS。

- [ ] **Step 5：运行 Core 回归测试**

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime ./pkg/bootstrap ./services/ani-gateway/internal/router ./services/ani-gateway/internal/middleware -count=1
```

预期：PASS。

- [ ] **Step 6：准备提交点**

建议提交信息：`fix(core): clean Sandbox checkpoint resources on delete`

---

### Task 7：真实 Rook-Ceph checkpoint live gate

**文件：**

- 新增：`repo/deploy/real-k8s-lab/instance-sandbox-checkpoint-live-gate.yaml`
- 新增：`repo/scripts/validate_instance_sandbox_checkpoint_live_gate.py`
- 新增：`repo/scripts/validate_instance_sandbox_checkpoint_live_gate_test.py`
- 修改：`repo/Makefile`

**接口：**

- 产出：`make validate-instance-sandbox-checkpoint-live-gate`
- 产出：profile `INSTANCE-SANDBOX-CHECKPOINT-LIVE-GATE-A`

- [ ] **Step 1：先写 validator 负例测试**

必须拒绝：缺 check、`status=live` 但无 evidence、evidence 含 Authorization/JWT/数据库连接串/预览 URL/内部 IP、image digest 非 sha256。

- [ ] **Step 2：运行测试确认 RED**

```bash
cd repo
python3 scripts/validate_instance_sandbox_checkpoint_live_gate_test.py
```

预期：FAIL，validator 尚不存在。

- [ ] **Step 3：实现 gate 契约和 validator**

固定 checks：

```text
sandbox-workspace-pvc-bound
checkpoint-create-ready
checkpoint-list-after-gateway-restart
workspace-restore-content
checkpoint-clone-content
keep-memory-capability-422
legacy-emptydir-capability-422
postgres-task-persistence
sandbox-checkpoint-cleanup
```

- [ ] **Step 4：运行静态 gate 确认 GREEN**

```bash
cd repo
make validate-instance-sandbox-checkpoint-live-gate
```

预期：contract validation 和 validator tests PASS。

- [ ] **Step 5：构建、推送并 rollout Gateway**

镜像标签固定为新的不可变批次标签，例如：

```text
docker.changqingyun.cn/ani/ani-gateway:instance-sandbox-checkpoint-20260802-v1
```

记录 registry 返回的真实 digest，不使用 `latest`。

- [ ] **Step 6：执行真实顺序**

```text
create Sandbox → PVC Bound → 写入 version-1
→ create checkpoint → 等待 readyToUse
→ 写入 version-2 → rollout restart Gateway
→ list checkpoint → restore → 验证 version-1
→ clone → 验证 clone 中为 version-1
→ keep_memory=true 返回 422
→ 删除源与 clone → 验证 Deployment/Pod/PVC/VolumeSnapshot 为 0
→ 查询 PG task 审计行存在
```

- [ ] **Step 7：归档脱敏 evidence 并验证**

```bash
cd repo
python3 scripts/validate_instance_sandbox_checkpoint_live_gate.py \
  --evidence development-records/live-evidence/instance-sandbox-checkpoint-live-20260802.json
```

预期：`sandbox checkpoint live gate validation passed`。

- [ ] **Step 8：准备提交点**

建议提交信息：`test(core): verify Sandbox checkpoint real provider`

---

### Task 8：Feature batch 文档和总门禁

**文件：**

- 新增：`repo/development-records/instance-sandbox-checkpoint-a.md`
- 修改：`repo/development-records/README.md`
- 修改：`repo/CURRENT-SPRINT.md`
- 修改：`ANI-06-开发计划.md`

- [ ] **Step 1：记录准确能力边界**

文档必须明确：

```text
filesystem checkpoint live passed
keep_memory unsupported / 422
legacy emptyDir unsupported / 422
PVC + Rook-Ceph VolumeSnapshot
Gateway restart does not lose checkpoint metadata
Sandbox delete garbage-collects scoped snapshots
not full platform production ready
```

- [ ] **Step 2：运行专项和入口门禁**

```bash
cd repo
make validate-instance-sandbox-checkpoint-live-gate
make validate-doc-entrypoints
make validate-architecture
```

预期：全部 PASS。

- [ ] **Step 3：运行完整仓库测试**

```bash
cd repo
PATH=/tmp/ani-pybin:$PATH make test
```

预期：退出码 0，Go/Python 全部门禁 PASS。

- [ ] **Step 4：运行兼容性和差异检查**

```bash
cd repo
make validate-openapi-spec
make validate-core-api-compatibility
git diff --check
```

预期：OpenAPI v1 无 diff、兼容性 PASS、无空白错误。

- [ ] **Step 5：执行安全扫描**

```bash
cd repo
rg -n 'eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}|Authorization.*Bearer|database_url|preview_url' \
  development-records/live-evidence/instance-sandbox-checkpoint-live-20260802.json
```

预期：无输出。

- [ ] **Step 6：向用户汇报并等待提交指令**

汇报真实执行命令、镜像 digest、evidence 路径和剩余边界；不得自动 commit 或 push。
