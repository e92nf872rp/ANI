# INSTANCE-CONTAINER-ENV-ECHO-A

> **日期**：2026-09-16　**分支**：`hotfix/network-store-read`（fork: djm-afk）　**PR**：e92nf872rp/ANI#168　**状态**：live verified（隔离测试环境 ani-test2，镜像 tag `test2-20260916-gpu5env`）
> **性质**：Feature batch（hotfix 系列，容器/GPU 容器实例 record 回显能力补齐）
> **来源**：测试异常结果记录 GPU 容器系列与 `特有Bug修复问题清单.md`（GPU-5）

## 背景

创建 GPU 容器实例时填写的环境变量，创建成功后详情/列表展示为空。排查确认创建链路完整：`env` 透传 `spec.Container.Env` 并经 dryrun 渲染器 `containerEnv` 渲染进 Deployment（value 与 secret_ref 两型均支持），集群侧真实生效。缺口在回读链路三层全缺：record 的 `ContainerInstanceStatus` 不持久化 Env、gateway 详情响应 `instanceResponse` 无 env 字段、Core OpenAPI 契约 `InstanceRecord.container` 无 env 定义——"写入但不回读"，详情页必然为空。container 与 gpu_container 共用该缺陷（测试仅在 GPU 侧发现）。

## 修复内容

### 环境变量持久化与 API 回显（按 API-first 分四层）

1. **契约**：`v1.yaml` 的 `InstanceRecord.container` 新增 `env` 数组（复用创建请求 `InstanceEnvVar` schema，oneOf value/secret_ref 语义一致），描述注明"创建时设定的环境变量回显；历史实例未记录时缺省"。
2. **持久化**：ports `ContainerInstanceStatus` 新增 `Env []InstanceEnvVar`，随 `container_status` JSON 列存储（无 DB 迁移）；`instance_orchestrator.go` 的 `containerStatusInfo` 在创建时从 `spec.Container.Env` 克隆写入（副本隔离防外部改写）。
3. **回显**：gateway `instanceContainerResponse` 新增 `env`（`instanceEnvResponse`：name/value/secret_ref；secret_ref 型不带 value 字段，secret_ref 指向的 secret 内容永不返回，与 SSH key_ref 同一原则）。
4. **不丢数据核验**：逐路径确认 reconciler 刷新（refreshOneStoreStatus 原地更新）、scale、update_image、rollback（`next := *record.Container` 浅拷贝）均不重建 record.Container，env 不会丢；markApplyFailed 经 instanceRecordFromResult 重建时 spec 仍带 env。

一处修复同时覆盖 container 与 gpu_container（共用 containerStatusInfo 与同一响应组装）。

## 变更文件

- `repo/api/openapi/v1.yaml`：`InstanceRecord.container.env`
- `repo/pkg/ports/workload_runtime.go`：`ContainerInstanceStatus.Env`
- `repo/pkg/adapters/runtime/instance_orchestrator.go`：`containerStatusInfo` 写入 Env
- `repo/services/ani-gateway/internal/router/instances.go`：响应结构 + 组装回显
- 单测：orchestrator GPU 容器创建后 record 持久化 env（value+secret_ref 两型）；gateway 响应 JSON 回显（secret_ref 型无 value 字段）
- 未改：SDK/生成物、Services 层、前端、DB schema

## 验证汇总

- 单测：`go build ./...`、`go vet`、`go test ./adapters/runtime/ ./internal/router/`（Windows 下 sandbox symlink 用例为既有环境限制，CI Linux 正常）；gofmt 干净；`validate_openapi_spec.py` 及其测试通过；`git diff --check` 干净
- 真实环境（ani-test2 隔离环境，gateway 镜像 `test2-20260916-gpu5env`）：
  - GPU 容器（rtx-4090 规格）带 2 个 value 型环境变量（GPU5_TEST/APP_MODE）创建 → 创建响应即时回显 → 详情 t+15s（reconciler 已跑过）回显一致
  - 测试实例已通过 lifecycle delete 清理
- CI：PR #168 GitHub Actions 为准

## 遗留

- 修复前创建的历史实例 record 无 env，详情仍为空；如需历史回显需从 Deployment 反读 env 做回填（独立工作）
- 前端（测试所用 console）需在实例详情环境变量模块消费新增的 `container.env` 字段
