# Issue #6: 队列 CRUD live gate

> **Priority:** medium
> **Depends On:** #2, #4
> **Product line:** Core
> **Document Links:**
> - SPEC: `repo/services/tasks/modules/spec/core/gpu-inventory/spec-core-gpu-scheduling.md` §10.1 P0-⑤

## Scope

- `repo/pkg/adapters/runtime/volcano_queue_store.go`（MODIFY：cluster-scoped REST path）
- `repo/pkg/adapters/runtime/kubernetes_rest_client.go`（MODIFY：新增 `Do()` + `Host()` 方法）
- `repo/services/ani-gateway/gpu_inventory_runtime.go`（MODIFY：wire VolcanoQueueStore 到 volcano_rest 分支）
- `repo/services/ani-gateway/main.go`（MODIFY：处理 queue store 返回 error）
- `repo/deploy/real-k8s-lab/queue-crud-live-gate.yaml`（NEW）
- `repo/scripts/validate_queue_crud_live_gate.py`（NEW）
- `repo/Makefile`（MODIFY：新增 `validate-queue-crud-live-gate` target）

## Description

队列 CRUD 5 端点在真实 lab 环境的 live gate 验证。将 VolcanoQueueStore adapter 接线到 Gateway，通过真实 Volcano Queue CRD 验证端到端 CRUD。

## Acceptance Criteria

- [x] 队列 CRUD 5 端点在 lab 验证通过
- [x] 创建队列 → Volcano Queue CRD 存在（`kubectl get queues.scheduling.volcano.sh`）
- [x] 删除队列 → CRD 删除
- [x] 平台默认队列 DELETE → `403 PlatformDefaultProtected`
- [x] 跨租户读队列 → `404`（不泄露存在性）
- [x] live gate profile YAML 定义完整
- [x] `make validate-queue-crud-live-gate` 通过

## Validation

```bash
cd repo
make validate-queue-crud-live-gate
```

## Development Record

### 变更文件

| 文件 | 类型 | 说明 |
|---|---|---|
| `repo/pkg/adapters/runtime/volcano_queue_store.go` | MODIFY | 修正 cluster-scoped REST path（移除 namespaces 段） |
| `repo/pkg/adapters/runtime/kubernetes_rest_client.go` | MODIFY | 新增 `Do()` 方法（实现 VolcanoHTTPDoer 接口）+ `Host()` 方法 + 修复 CA 文件加载逻辑 |
| `repo/services/ani-gateway/gpu_inventory_runtime.go` | MODIFY | volcano_rest 分支接线 VolcanoQueueStore + K8s REST client |
| `repo/services/ani-gateway/main.go` | MODIFY | 处理 newGatewayGPUSchedulingQueueStore 返回 error |
| `repo/deploy/real-k8s-lab/queue-crud-live-gate.yaml` | NEW | Live gate profile（6 live_checks + 2 readiness_levels） |
| `repo/scripts/validate_queue_crud_live_gate.py` | NEW | Gate YAML 结构验证脚本 |
| `repo/Makefile` | MODIFY | 新增 `validate-queue-crud-live-gate` target |

### 集群验证结果

```
=== Gateway LIST ===
{"items":[{"id":"test-platform-default-id","name":"ani-test-platform","weight":10,...,"is_platform_default":true},...]}

=== Gateway CREATE ===
{"id":"8b611c6d-5827-4564-90a9-d0455e4f2abe","name":"test-live-01","weight":5,"reclaimable":true,"workload_class":"inference","project_id":"proj-001",...}

=== Volcano CRD ===
NAME                PARENT
ani-inference       root
ani-test-platform   root
ani-training        root
default             root
root
test-live-01        root      ← Gateway 创建的队列

=== Gateway DELETE platform default ===
{"code":"PlatformDefaultProtected","message":"平台默认队列不可修改或删除"}
HTTP: 403
```

### 遇到的问题

1. **Volcano Queue CRD scope**：CRD 是 `Cluster` scoped（非 `Namespaced`），REST path 不含 `namespaces/{ns}` 段。修正为 `/apis/scheduling.volcano.sh/v1beta1/queues`。
2. **K8s TLS CA**：外部访问 K8s API 时 `kubernetesHTTPClient` 不会加载 CA 文件（仅在 inCluster=true 时加载）。修复为即使 `inCluster=false`，只要提供了 `CAFile` 就加载。
3. **KubernetesRESTClient 缺少 Do/Host 方法**：`VolcanoHTTPDoer` 接口需要 `Do(ctx, method, endpoint, contentType, body) ([]byte, int, error)`，但 `KubernetesRESTClient` 只有私有 `do` 方法返回 `([]byte, error)`。新增公开 `Do()` 方法从 `resilience.StatusError` 提取 HTTP status code；新增 `Host()` 方法暴露 base URL。
4. **RBAC 权限**：`kube-system:default` ServiceAccount 无 Volcano Queue 操作权限。创建 `ani-gpu-scheduler` SA + `ani-gpu-queue-manager` ClusterRole/Binding。
5. **Dev mode tenant ID**：`ANI_AUTH_MODE=dev` 时 tenant ID 默认为 `00000000-0000-0000-0000-000000000001`，Volcano Queue 按 `ani.kubercloud.io/tenant` label 过滤。
