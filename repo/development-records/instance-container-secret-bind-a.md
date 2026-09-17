# INSTANCE-CONTAINER-SECRET-BIND-A

> **日期**：2026-09-17　**分支**：`hotfix/network-store-read`（fork: djm-afk）　**PR**：e92nf872rp/ANI#168　**状态**：live verified（隔离测试环境 ani-test2，镜像 tag `test2-20260917-secretbind`）
> **性质**：Feature batch（hotfix 系列，容器/GPU 容器实例密钥绑定/解绑能力落地）
> **来源**：测试异常结果记录 容器/GPU 容器系列与 `特有Bug修复问题清单.md`（容器-6、GPU-6）

## 背景

容器实例与 GPU 容器实例详情页「配置与密钥」点击「绑定/解绑密钥」提示不支持。前端提示即后端真实拒绝的透传：lifecycle 契约（`bind_secret`/`unbind_secret` 动作枚举、`secret_id`/`binding_type`/`env_name`/`mount_path` 请求字段）、路由、服务层校验（`validateLifecycleIntent`/unexpected fields/幂等指纹）与 precheck 均已就绪，缺口仅在 `KubernetesLifecycleExecutor.Apply` 无对应 case——请求落入 default 返回 `unsupported Kubernetes lifecycle action "bind_secret"`。创建期密钥注入链路（envFrom 整 secret / per-key env secretRef / secret volume+volumeMount）也早已存在，运行期变更只缺 executor 的定向 PATCH。

## 修复内容

1. **executor 新分支**：`Apply` 新增 `applyKubernetesSecretBind` 分派（仅 container/gpu_container + Deployment，其余 kind `ErrUnsupported`；`secret_id` 必填）。
2. **bind env**：
   - 带 `env_name`：单次 strategic-merge patch 写入 per-key env 条目 `{"name": env_name, "valueFrom": {"secretKeyRef": {"name": secret_id, "key": env_name}}}`（与创建期 `containerEnv` secretRef 形态一致）；
   - 不带 `env_name`：`envFrom` 整 secret 注入。`envFrom` 是原子列表（无 merge key），先 GET live 全量回写 append `{"secretRef": {"name": secret_id}}`；已绑定则幂等 no-op。
3. **bind file**：`mount_path` 必填（`ErrInvalid`）；secret volume（`secret.secretName = secret_id`）+ workload 容器 readOnly `volumeMount`。卷名不复用创建期 index 依赖的 `secretVolumeName`，改由 `(secret_id, mount_path)` 确定性派生（`secret-` 前缀、非法字符→`-`、截断 63），保证 bind/unbind 双向可定位。
4. **unbind**：不依赖 record（历史实例 record 从未持久化过绑定），统一 GET live Deployment 反查该 secret 的全部注入形态——env（`valueFrom.secretKeyRef.name` 命中）、envFrom（逐条 probe `secretRef.name`）、volumes（`secret.secretName` 命中）、volumeMounts（按卷名）——用 `$patch: delete` 定向移除；四类均为空返回 `ErrNotFound`（未绑定）。
5. **record 持久化**：`ContainerInstanceStatus` 新增 `SecretBindings []WorkloadSecretBinding`（随 `container_status` JSONB 列整列序列化，无 DB 迁移，GPU-5 同模式；reconciler/scale/update_image 原地更新不丢）；`containerStatusInfo` 创建时从 spec 克隆；`applyApprovedLifecycleSummary` 的 bind/unbind 分支 append/remove 绑定记录并置 `RolloutStatus="progressing"`（与 scale/update_image 同机制，由 reconciler 观测收敛）。
6. **ports**：`WorkloadSecretBinding` 新增 `EnvName` 字段（运行期 per-key 绑定；创建期整 secret 绑定沿用 `EnvPrefix`）。

一处修复覆盖 container 与 gpu_container（同一 executor 分支）。

## 变更文件

- `repo/pkg/ports/workload_runtime.go`：`WorkloadSecretBinding.EnvName` + `ContainerInstanceStatus.SecretBindings`
- `repo/pkg/adapters/runtime/kubernetes_lifecycle_executor.go`：`applyKubernetesSecretBind`/`applyKubernetesSecretAttach`/`applyKubernetesSecretDetach`/`patchDeploymentPodSpec`/`kubernetesSecretEnvFrom`/`kubernetesSecretVolumeName`
- `repo/pkg/adapters/runtime/instance_service.go`：`applyApprovedLifecycleSummary` bind/unbind 分支 + `removeSecretBinding`
- `repo/pkg/adapters/runtime/instance_orchestrator.go`：`containerStatusInfo` 克隆 SecretBindings
- 单测：`kubernetes_lifecycle_executor_test.go` 新增 8 用例（env per-key 单次 PATCH、envFrom GET+全量回写+幂等、file 卷+挂载、file 缺 mount_path 422、非法 binding_type 422、VM 拒绝、unbind 四形态删除+其余保留、未绑定 404 且无 PATCH）；`instance_service_test.go` 新增 2 用例（summary 回写增删+progressing、containerStatusInfo 克隆隔离）
- 未改：OpenAPI 契约与生成物、SDK、Services 层、前端、DB schema

## 验证汇总

- 单测：`go build ./pkg/... ./services/ani-gateway/...`、`go vet`、`gofmt -l` 干净、`git diff --check` 干净；runtime 定向用例与 gateway 全量测试通过（Windows 下 sandbox symlink 用例为既有环境限制，CI Linux 正常）
- 真实环境（ani-test2 隔离环境，gateway 镜像 `test2-20260917-secretbind`，验证脚本 `step13secret_verify.py`）：
  - **container**（实例 `secret-bind-ctl-29670`）：bind env（`DATABASE_URL`）→ Deployment env 出现 `valueFrom.secretKeyRef`（secret 名=record ID）且 ready=1；bind file（`/run/secrets/app`）→ secret volume + readOnly volumeMount 出现且 ready=1；psql 直查 `container_status` 含 2 条 `SecretBindings`（env+file）；unbind → env/volume/volumeMount 全部移除、record `SecretBindings=[]`、实例回 `running`
  - **gpu_container**（复用 running 实例）：bind env + unbind 集群侧与收敛断言同 container 全过
- 环境配套：租户 Secret 的 K8s 侧 apply 需 gateway `SECRET_PROVIDER_MODE=kubernetes_rest`（本环境此前未配置，secret 仅落库不在租户 ns 创建 K8s Secret；验证期间已配置并 rollout）；节点公网镜像拉取 DNS 故障期间改用内网 registry 镜像验证，与修复无关
- CI：PR #168 GitHub Actions 为准

## 遗留

- API 详情响应暂不回显 `container.secret_bindings`（record 内部持久化已就绪，如前端需要回显再按 GPU-5 模式补契约+响应字段）
- `bind_secret` 重复绑定同一 secret 的幂等仅在 envFrom 路径显式 no-op；env per-key 与 file 路径重复 PATCH 结果收敛一致（同态覆盖），无副作用
