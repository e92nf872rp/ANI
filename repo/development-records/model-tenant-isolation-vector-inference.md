# MODEL-TENANT-ISOLATION-VECTOR-INFERENCE-A

日期：2026-08-25  
状态：live passed（限定 Model 多租户隔离与内部 ClusterIP embedding 部署链路）

## 目标与根因

本批次同时关闭两个 Services 层缺口：

1. model-service 的部分 repository 查询和 mutation 只依赖 PostgreSQL RLS，没有把调用方 `tenant_id` 固化在每条权威 SQL 谓词中；service 也未对 repository 返回的 Model 所属租户做最终复核，因此错误的数据库会话上下文可能使 Get/List 暴露其他租户的模型元数据。
2. inference-service 只按 Chat/Generate 任务生成 vLLM 启动命令并执行 readiness smoke，`embedding` 模型没有可冻结、可跨重启/回滚使用的内部 task，因而不能走完整部署到 `running` 的链路。

真实环境首次运行又确认两个兼容问题：当前 vLLM 镜像不接受旧的
`--task embed`，要求 `--runner pooling --convert embed`；真实 embedding
响应约 19 KiB，超过原 smoke 的 4 KiB 读取上限，截断后被误判为非 JSON。

PostgreSQL RLS 继续保留为第二道防线；本批次没有以显式 SQL fence 替换 RLS。

## 实现

### Model tenant fence

- Get Model：`id=$1 AND tenant_id=$2 AND status <> 'deleted'`。
- Get ModelVersion：通过 `model_versions JOIN models` 同时匹配 version ID、父 Model 的 `tenant_id` 与非删除状态。
- List/count：`tenant_id` 固定为首个参数，在 status、cursor 和 limit 之前参与 count/list 的共同过滤。
- Soft Delete：同时匹配 Model ID、`tenant_id` 与非删除状态；零更新映射为 NotFound。
- Create Version：使用 `INSERT ... SELECT ... FROM models` 先证明父 Model 归属；随后更新父 Model 时再次匹配 Model ID、`tenant_id` 与非删除状态；任一步骤零行均映射为 NotFound。
- List Versions：同一 tenant transaction 中先验证父 Model 归属，再通过 `JOIN models` 和 `m.tenant_id` 查询版本；foreign/deleted/missing 父资源统一为 NotFound，合法但无版本仍返回空列表。
- service 将解析后的 tenant UUID 显式传给所有 repository 调用；Get/GetVersion 对返回父 Model 再做所有权检查；List 在构造 `total` / `next_cursor` 前检查所有行，发现 foreign/nil row 时返回脱敏 Internal，不返回由外租户行派生的分页元数据。

跨租户资源 ID 与不存在资源保持不可区分，继续使用既有 NotFound 语义。

### Internal task derive/freeze

`Model.capabilities` 是唯一任务来源，不增加公开 task selector：

| capabilities | 冻结 task | 结果 |
| --- | --- | --- |
| 空 | `generate` | 向后兼容默认值 |
| 含 `text-generation` 或 `sglang` | `generate` | 保留既有生成路径 |
| 同时含生成与 `embedding` | `generate` | 生成任务优先 |
| 仅含受支持的 `embedding` | `embed` | 选择 vLLM embed CPU/GPU profile |
| 仅 `speech-to-text` 或未知值 | 无 | 无兼容 profile |

选出的 task 写入既有 `ExecutionProfile` JSONB，与 engine/artifact 一起冻结。旧记录的空 task 和未知 task 均通过 `NormalizeInferenceTask` 回退为 `generate`，无需数据库 schema migration。

### vLLM launch 与 readiness

- 平台生成的 vLLM CPU/GPU embedding 命令追加
  `--runner pooling --convert embed`；不再使用当前镜像不支持的 `--task`。
- `generate` 和旧的空 task 保持既有 argv，不添加 task override。
- 租户提供的完整 `engine.command` 仍是权威 argv，不追加、不重写；`LaunchLeader` 继续复用同一冻结 task。
- `/health` 成功后，`generate` 使用 `POST /v1/chat/completions` 并要求 JSON `choices`；`embed` 使用 `POST /v1/embeddings`、`input: ["ping"]`，并要求非空 JSON `data`。smoke 响应读取上限提升为有界 1 MiB，既容纳真实向量响应，也拒绝无界响应。
- 正常 reconcile 使用目标 spec 的冻结 task；scale rollback 使用前一 applied spec 的冻结 task。响应正文不写日志，也不进入对外错误。

## TDD 证据

实现前的有效 RED 覆盖：

- foreign Get 返回 OK 而非 NotFound，foreign List 返回 OK 而非 fail-closed Internal；repository tenant SQL 常量/builders 尚不存在导致编译失败。
- embedding profile/task 类型和 Creator task persistence 尚不存在导致编译失败；仅补字段后 Creator 仍冻结空 task 而非 `embed`。
- embedding vLLM CPU/GPU/leader argv 缺少当前 vLLM 所需的
  `--runner pooling --convert embed`。
- runtime probe 与 worker 尚未接受/传播 `InferenceTask`，任务感知测试编译失败。
- live 首次请求返回约 19 KiB 合法 JSON，而 4 KiB 截断测试稳定复现
  `runtime smoke returned a non-json body`；改为 1 MiB 有界读取后转绿。

对应 GREEN 已覆盖同租户成功路径、foreign Get/GetVersion/List、SQL tenant 参数顺序与零行映射、capability 矩阵、profile slot invariant、legacy task normalization、vLLM generate/embed/frozen command、Chat/Embeddings probe 以及正常/rollback task propagation。

## 变更文件

Model service：

- `services/model-service/internal/repo/model_repo.go`
- `services/model-service/internal/repo/model_repo_test.go`
- `services/model-service/internal/service/model_service.go`
- `services/model-service/internal/service/model_service_test.go`

Inference service：

- `services/inference-service/internal/domain/resource.go`
- `services/inference-service/internal/catalog/catalog.go`
- `services/inference-service/internal/catalog/modelsvc/adapter.go`
- `services/inference-service/internal/catalog/modelsvc/adapter_test.go`
- `services/inference-service/internal/service/service.go`
- `services/inference-service/internal/service/service_test.go`
- `services/inference-service/internal/engine/launch.go`
- `services/inference-service/internal/engine/launch_test.go`
- `services/inference-service/internal/runtime/runtime.go`
- `services/inference-service/internal/runtime/coresdk/adapter.go`
- `services/inference-service/internal/runtime/coresdk/adapter_test.go`
- `services/inference-service/internal/runtime/fake/fake.go`
- `services/inference-service/internal/reconcile/worker.go`
- `services/inference-service/internal/reconcile/worker_test.go`

本记录、三份状态索引与脱敏 live evidence 之外，没有修改公开 OpenAPI、
数据库 migration 或 Envoy 配置。验证期间只临时滚动两个 Services 控制面
Deployment 并创建唯一命名测试资源；结束时均已清理或恢复。

## 真实环境验证

2026-08-25 使用用户提供的一小时 tenant bearer token 执行产品 API 闭环；
token 未写入仓库、证据或日志，验证后已从进程环境清除并关闭 port-forward。

- Model tenant fence：数据库中选择一个明确属于另一租户的 Model ID；当前租户
  `GET /models/{id}` 返回 HTTP 404 / `NOT_FOUND`，`GET /models` 返回 200 且
  `foreign_in_list=false`。
- Vector deployment：通过 Services API 创建 capability=`embedding` 的 Model、
  ModelVersion 与 CPU InferenceService；生成的工作负载 argv 包含
  `--runner pooling --convert embed` 且不含 `--task`，最终状态为 `running`、
  `ready_replicas=1`。
- Direct data plane：从 inference-service 直连测试 Service ClusterIP 调用
  `POST /v1/embeddings`，HTTP 200，响应 19,451 bytes，JSON `data` 与首个
  `embedding` 均非空；未记录完整向量。
- 清理与回滚：删除测试 InferenceService 和两次尝试产生的测试 Model；测试
  Kubernetes workload 消失；model-service / inference-service 均恢复验证前
  镜像且各 1 个 Ready replica。用户此前明确允许下线的旧推理实例未恢复。

镜像证据：

- model-service live 镜像 digest：
  `sha256:7f4f53e153df23eb1fd1cdd658a69591797de644445c753acba0041edd3621bc`
- inference-service 最终 live 镜像 digest：
  `sha256:d5ea34b4bfb67b02378c2ffe5d105d1c691719107b420eb511a1875de2bcafbf`
- 脱敏证据：`development-records/live-evidence/model-tenant-vector-inference-live-20260825.json`

## 新鲜验证

根目录在 `GOWORK=off` 下不能跨两个独立 module 解析 `./services/model-service/... ./services/inference-service/...`，因此按计划分别进入两个 service module 执行等价全量测试；没有为此修改 `go.work.sum`。

```text
cd repo/services/model-service
GOCACHE=/tmp/ani-task5-go-cache GOWORK=off go test ./... -count=1
PASS

cd repo/services/inference-service
GOCACHE=/tmp/ani-task5-go-cache GOWORK=off go test ./... -count=1
PASS（现有 httptest 需要本机 loopback 权限）
```

仓库级门禁最终结果：

```text
PATH=/tmp/ani-pybin:$PATH make validate-services
PASS（现有 httptest 使用已批准的本机 loopback 权限）

PATH=/tmp/ani-pybin:$PATH make validate-architecture
PASS

PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
PASS（首次扫描时前端 node_modules 目录瞬时缺失；目录恢复后重跑通过）

PATH=/tmp/ani-pybin:$PATH make test
PASS（现有 httptest 使用已批准的本机 loopback 权限）
```

`validate-services` 包含 SDK/API docs 重新生成漂移检查，完成后没有
`sdks/`、`docs/api/` 或 `go.work.sum` 新漂移。

live 修复后的新鲜回归：

```text
cd repo/services/inference-service
GOWORK=off go test ./... -count=1
PASS

GOWORK=off go test -race ./... -count=1
PASS

GOWORK=off go vet ./...
PASS

cd repo
PATH=/tmp/ani-pybin:$PATH make test
PASS

PATH=/tmp/ani-pybin:$PATH make validate-services
PASS（含 model-service / inference-service、SDK/API docs 漂移和 architecture gate）

PATH=/tmp/ani-pybin:$PATH make validate-architecture validate-doc-entrypoints
PASS

git diff --check
PASS
```

## 明确未完成范围

- 未新增或发布 Envoy AI Gateway 的公开 `/v1/embeddings` 路由。
- 未恢复、修改或部署此前暂存的 Envoy AI Gateway/C40 工作。
- 本次使用 PVC 中既有 Qwen2 权重验证 vLLM pooling/embed 协议与产品部署链路；
  不声明 Qwen3 embedding 模型质量或专用向量权重已完成导入。
- 不据此声明公开 embedding 产品入口 live、GPU ready 或 full platform
  production ready；结论仅限 CPU、内部 ClusterIP 与上述租户隔离路径。
