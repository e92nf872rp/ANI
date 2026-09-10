# Console 模块状态与冻结路径对照表

## 说明

本表用于汇总当前已经进入稳定维护范围的 `Console` 模块，便于一次性复核：

- 当前模块归属 `Core` 还是 `Services`
- 当前已冻结的正式路径
- 当前已明确写入文档的待补边界
- 当前主维护源所在位置
- 当前是否已进入 `可用` 基线

本表只覆盖当前状态板中已判定为 `可用` 的模块，不外推到未来新增模块，也不把导航占位页统计进正式能力。

## 当前可用模块总表

| 模块 | 当前状态 | 分层 | 主维护源 | 说明 |
|---|---|---|---|---|
| 平台概览 | 可用 | 聚合总览（Core + Services） | `docs/console-modules/home/platform-overview.md` | 作为首页聚合入口，不单独自造业务资源 |
| GPU 算力管理 | 可用 | Core | `docs/console-modules/compute/gpu-management.md` | GPU 页面定义与待补边界 |
| 云主机 VM | 可用 | Core | `docs/console-modules/compute/vm-management.md` | 统一实例 `kind=vm` |
| 容器实例 | 可用 | Core | `docs/console-modules/compute/container-instance-management.md` | 统一实例 `kind=container` |
| GPU 容器实例 | 可用 | Core | `docs/console-modules/compute/gpu-container-instance-management.md` | 统一实例 `kind=gpu_container` |
| Sandbox 实例 | 可用 | Core | `docs/console-modules/compute/sandbox-instance-management.md` | 统一实例 `kind=sandbox` |
| K8s 集群 | 可用 | Core | `docs/console-modules/compute/k8s-cluster-management.md` | `K8sClusters` 资源组 |
| 网络管理 | 可用 | Core | `docs/console-modules/compute/network-management.md` | VPC / 子网 / 安全组 / 负载均衡总览 |
| 存储管理 | 可用 | Core | `docs/console-modules/compute/storage-management.md` | 存储总览入口 |
| 块存储 | 可用 | Core | `docs/console-modules/compute/storage/block-storage.md` | `Volumes` 资源组 |
| 对象存储 | 可用 | Core | `docs/console-modules/compute/storage/object-storage.md` | `StorageObject` 元数据资源 |
| 文件存储 | 可用 | Core | `docs/console-modules/compute/storage/file-storage.md` | `Filesystems` 资源组 |
| 向量存储 | 可用 | Core | `docs/console-modules/compute/storage/vector-storage.md` | `VectorStore` 资源组 |
| API Key 管理 | 可用 | Core | `docs/console-modules/tenant/api-key-management.md` | `Auth / API Keys` |
| 安全与身份概览 | 可用 | Core 总入口 | `docs/console-modules/tenant/security-identity-overview.md` | 仅收口 `Auth` 已冻结能力 |
| 租户用量报表 | 可用 | Core | `docs/console-modules/tenant/usage-report.md` | `Metering` 聚合读能力 |
| 租户管理 | 可用 | Services | `docs/console-modules/tenant/tenant-management.md` | `/tenant/*` 8 条路径 |
| 模型中心 | 可用 | Services | `docs/console-modules/inference/model-center.md` | `Models` 资源组 |
| 推理服务 | 可用 | Services | `docs/console-modules/inference/inference-service.md` | `InferenceServices` 资源组 |
| 知识库管理 | 可用 | Services | `docs/console-modules/knowledge/knowledge-base.md` | `KnowledgeBases` 资源组 |
| 开放与集成总入口 | 可用 | 接入总入口 | `docs/console-modules/integration/open-integration-overview.md` | 只收口可证明的接入面 |

## P0 子模块（2026-06-17 文档收口）

| 模块 | 当前状态 | 分层 | 主维护源 | 说明 |
|---|---|---|---|---|
| 告警与待处理事项 | 可用 | Core + Services 聚合 | `docs/console-modules/alerts/alerts-pending-items.md` | `TODO-YAML` listAlerts |
| 异步任务中心 | 可用 | Core | `docs/console-modules/alerts/async-task-center.md` | 单任务查询已声明；list TODO |
| 推理服务调用测试 | 可用 | Services | `docs/console-modules/inference/inference-call-test.md` | `POST .../test` Phase 2 |
| 推理服务可观测性 | 可用 | Core + Services | `docs/console-modules/inference/inference-observability.md` | logs + PromQL；events TODO |
| 知识库问答流程 | 可用 | Services | `docs/console-modules/knowledge/kb-qa-flow.md` | query/stream/sessions |
| 首页资源使用概览 | 可用 | 聚合 | `docs/console-modules/home/home-resource-overview.md` | 无独立 API |
| 首页推理服务状态 | 可用 | Services 聚合 | `docs/console-modules/home/home-inference-status.md` | listInferenceServices |
| 首页 GPU 利用率 | 可用 | Core 聚合 | `docs/console-modules/home/home-gpu-utilization.md` | gpu-inventory Phase 2 |

## P1 子模块（2026-06-17 文档收口）

| 模块 | 状态 | 分层 | 主维护源 |
|---|---|---|---|
| VPC / 子网 / 安全组 / LB / 路由 | 可用 | Core | `compute/network/*.md` |
| 批任务实例 | 可用 | Core | `compute/batch-job-instances.md` |
| 容器可观测性 | 可用 | Core | `compute/container-observability.md` |
| VM 快照恢复 | 可用 | Core | `compute/vm-snapshot-restore.md` |
| 资源池概览 | 可用 | 聚合 | `compute/resource-pool-overview.md` |
| 卷/对象/向量存储子能力 | 可用 | Core | `compute/storage/*-snapshot/upload/write.md` |
| 用户/LDAP/角色编辑 | 可用 | Services | `tenant/user-management.md` 等 |
| 推理策略 / OpenAI 兼容 | 可用 | Services / Envoy AI Gateway | `inference/inference-rate-limit-policy.md`、`integration/openai-compatible-api.md` |
| 全局搜索 | 可用 | 聚合 | `home/global-search.md` |

## P2 子模块（2026-06-17 YAML 已有项文档收口）

| 模块 | 状态 | 分层 | 主维护源 |
|---|---|---|---|
| GPU 清单 UI | 可用 | Core | `compute/gpu-inventory-ui.md` |
| Sandbox 模板/安全事件 | 可用 | Core | `compute/sandbox-templates.md` |
| K8s 工作负载 | 可用 | Core | `compute/k8s-workloads.md` |
| 文件系统挂载目标 | 可用 | Core | `compute/storage/filesystem-mount-targets.md` |
| 知识库来源/历史/权限 | 可用 | Services | `knowledge/kb-source-citation.md` 等 |
| 首页知识库趋势 | 可用 | 聚合 | `home/home-kb-trend.md` |
| Webhook / Bot / 第三方集成 | 可用 | Services | `integration/integration-*.md` |
| 租户成员详情 / Webhook 运维 | 可用 | Services | `tenant/tenant-member-detail.md`、`tenant-webhook-ops.md` |

## P2 新模块（2026-06-17 文档收口 · 26 项）

| 模块 | 状态 | 分层 | 主维护源 | 说明 |
|---|---|---|---|---|
| AI 原生（×7） | 待 YAML | Services/Core | `ai-native/*.md` | Agent/Sandbox/MCP 整域 TODO-YAML |
| 知识库智能（×3） | 待 YAML | Services | `knowledge/kb-*-intelligence.md` | Phase 2 |
| 裸金属 / DPU | 待 YAML | Core | `compute/bare-metal-dpu.md` | kind 枚举已有 |
| 模型加密/推荐/统计 | 待 YAML / 部分 | Services + Metering | `inference/model-*.md` | usage 可复用 metering |
| API Key 审计 / 账单导出 | 待 YAML | Core/Services/BOSS | `tenant/api-key-audit.md`、`billing-export.md` | |
| 密钥 / 国密 / 审计 | 可用 / 待 YAML | Core | `security/secrets-management.md` 等 | secrets/encryption YAML 已有 |
| 网络安全 / 合规 | 待 YAML | Core/Services | `security/netsec-policy.md`、`compliance.md` | |
| SDK + CLI 交付页（×5） | 可用 | 文档 | `integration/integration-*.md`、`cli-download-page.md` | TODO-YAML N/A |

## 当前不计入正式能力的导航占位

以下内容即使在 `prototypes/ani-services-prototype-console.html` 中保留入口，也不统计进上表的 `可用` 模块：

- ~~`Go SDK（待补）`~~ — **已有详文**（`integration-go-sdk.md`；TODO-YAML N/A）
- ~~`Python SDK（待补）`~~ — **已有详文**（`integration-py-sdk.md`）
- ~~`TypeScript Client（待补）`~~ — **已有详文**（`integration-ts-client.md`）
- ~~`Java SDK（待补）`~~ — **已有详文**（`integration-java-sdk.md`）
- `OpenAI 兼容 API`（详文见 `openai-compatible-api.md`；独立 Envoy AI Gateway 数据面，ANI Gateway 不代理）
- `Webhook`、`企业微信 / 钉钉 Bot`、`第三方业务系统集成` — **已有子模块详文**（Phase 2 YAML；handler stub），不再标「待补」导航占位

额外说明：

- `ani CLI（入口说明）` 详文见 `integration/cli-download-page.md`；源码 `ANI-main/repo/cli/ani/main.go`

## Core 模块冻结路径

### 统一实例类

| 模块 | 已冻结路径 | 已明确待补边界 |
|---|---|---|
| 云主机 VM | `/api/v1/instances?kind=vm`、`/api/v1/instances/{instance_id}`、`/api/v1/instances`、`/api/v1/instances/{instance_id}/lifecycle`、`/api/v1/instances/{instance_id}/console`、`/api/v1/instances/{instance_id}/operations`、`/api/v1/instance-operations/{operation_id}` | VM 高级运维与平台侧能力不混入当前模块 |
| 容器实例 | `/api/v1/instances?kind=container`、`/api/v1/instances/{instance_id}`、`/api/v1/instances`、`/api/v1/instances/{instance_id}/lifecycle`、`/api/v1/instances/{instance_id}/operations`、`/api/v1/instance-operations/{operation_id}` | `logs / events / metrics / terminal / exec / dashboard` |
| Sandbox 实例 | `/api/v1/instances?kind=sandbox`、… | 独立 `/sandboxes*`（deprecated）、模板/安全事件 **YAML 已声明**（见 `sandbox-templates.md`）、延长/暂停 |
| GPU 容器实例 | `/api/v1/instances?kind=gpu_container`、…（同上 lifecycle 组） | 独立 `/gpu-containers*`（**services/v1.yaml 已 deprecated**）、logs、metrics、exec |

### 资源管理类

| 模块 | 已冻结路径 | 已明确待补边界 |
|---|---|---|
| GPU 算力管理 | 当前以 `Core` GPU 资源域口径维护 | `GET /api/v1/gpu-inventory`、`/gpu-inventory/occupancy` **YAML 已声明**（Phase 2）；handler stub |
| K8s 集群 | `/api/v1/k8s-clusters`、…、`/proxy` | 工作负载 **YAML 已声明**（`k8s-workloads.md`）；事件、Helm / 应用市场 |
| 网络管理 | `/api/v1/networks/vpcs*`、`/subnets*`、`/security-groups*`、`/load-balancers*` | 路由等待补能力 |
| 存储管理 | 存储总览，不自造子资源路径 | 仅承接子模块入口与边界 |
| 块存储 | `/api/v1/volumes`、`/api/v1/volumes/{volume_id}` | 快照与挂载等细化能力按边界处理 |
| 对象存储 | `/api/v1/objects`、`/api/v1/objects/{object_id}` | 上传、下载、桶、桶策略 |
| 文件存储 | `/api/v1/filesystems`、`/api/v1/filesystems/{filesystem_id}` | 挂载目标 **YAML 已声明**（`filesystem-mount-targets.md`）；活跃挂载关系、访问策略细化 |
| 向量存储 | `/api/v1/vector-stores`、`/api/v1/vector-stores/{vector_store_id}`、`/api/v1/vector-stores/{vector_store_id}/search` | 向量写入、索引维护、批量导入 |

### 租户与接入类

| 模块 | 已冻结路径 | 已明确待补边界 |
|---|---|---|
| API Key 管理 | `/api/v1/auth/api-keys`、`/api/v1/auth/api-keys/{key_id}` | API Key 审计、风控、合规导出、明文重看 |
| 安全与身份概览 | `/api/v1/auth/oidc/begin`、`/auth/token`、`/auth/refresh`、`/auth/logout`、`/auth/api-keys*` | 用户管理、LDAP、审计、合规、密钥、国密（租户成员/SSO/Webhook 见 `tenant-management.md`） |
| 租户用量报表 | `/api/v1/metering/usage` | 账单、发票、对账、平台运营分析 |
| 租户管理 | `/api/v1/svc/tenant/members*`、`/tenant/roles`、`/tenant/sso`、`/tenant/webhooks` | 角色编辑、Webhook 删除/日志、审计导出 |
| 开放与集成总入口 | 以接入入口为主，收口 REST API 文档、OIDC、API Key、CLI | OpenAI 兼容 API、SDK、Webhook、Bot、第三方业务系统集成 |

## Services 模块冻结路径

| 模块 | 已冻结路径 | 已明确待补边界 |
|---|---|---|
| 模型中心 | `/api/v1/svc/models`、`/api/v1/svc/models/import`、`/api/v1/svc/models/{model_id}`、`/api/v1/svc/models/{model_id}/versions` | 模型加密、推荐配置、调用统计 |
| 推理服务 | `/api/v1/svc/inference-services`、`/api/v1/svc/inference-services/{service_id}`、`PATCH` 变配 | 子模块：test/logs；OpenAI Gateway、policies 见 YAML |
| 知识库管理 | `/api/v1/svc/knowledge-bases`、…、`/query/stream` | 向量化与索引、**来源引用/对话历史/权限 YAML 已声明**（P2 子模块）、文档/会议/视频智能 |
| 租户管理 | `/api/v1/svc/tenant/members*`、…、`/tenant/webhooks` | 角色编辑、**Webhook 删除/投递日志、成员详情 YAML 已声明**（P2 子模块） |
| 开放与集成总入口 | 以接入入口为主，… | OpenAI 兼容 API、SDK 交付页；**Webhook/Bot/第三方集成已有子模块详文** |

## 复审结论

- 当前 `可用` 模块都已经写出**冻结能力**与**待补边界**的明确分界
- `Core` 模块与 `Services` 模块已按路径前缀和资源归属拆分
- `prototypes/ani-services-prototype-console.html` 只保留摘要、边界和主文档入口，`prototypes/ani-services-prototype.html` 仅保留为旧版参考
- 若后续继续扩展，应先更新状态板，再修改主维护源和 HTML 摘要
