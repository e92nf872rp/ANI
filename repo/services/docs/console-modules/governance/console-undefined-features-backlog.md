# Console 未定义功能 Backlog

> **生成日期**：2026-06-16  
> **用途**：汇总 Console 导航/信息架构中已规划、但尚未有独立模块详文（或仅有「待补边界」）的功能项。  
> **判定标准**：`docs/console-modules/` 无对应主维护文档 = 未定义；有主文档但仅写待补边界 = 部分定义。

## 图例

| 需先补 OpenAPI | 含义 |
|---|---|
| **是** | 当前 `v1.yaml` 或 `services/v1.yaml` 无对应路径/schema，需 Core/Services 团队先扩契约 |
| **部分** | YAML 有路径但 response schema 不完整或 handler 为 stub |
| **否** | 已有冻结路径，缺的是 Console 模块文档/UI 拆分 |
| **N/A** | 纯 UI 聚合/视角说明，不新增 API |

---

## P0 — 高优先级（影响主流程或 IA P0）

> **2026-06-17 更新**：P0 八项均已补模块详文 + PRD/SPEC + HTML 摘要；下表「文档」列指向主维护源。OpenAPI / handler 缺口仍按「需先补 OpenAPI」列执行。

| # | 建议模块名 | 导航/来源 | 归属 | 需先补 OpenAPI | 文档 | 说明 |
|---|---|---|---|---|---|---|
| P0-01 | `alerts-pending-items` | 首页 / 告警与待处理事项 | Core + Services 聚合 | **是** | `alerts/alerts-pending-items.md` | `TODO-YAML`：无 listAlerts；页面层聚合 |
| P0-02 | `async-task-center` | 全局 / 任务中心 | Core tasks | **部分** | `alerts/async-task-center.md` | `GET /tasks/{id}` 已声明；list TODO-YAML |
| P0-03 | `inference-call-test` | 推理 / 调用测试 | Services | **否** | `inference/inference-call-test.md` | Phase 2 已加 `POST .../test` |
| P0-04 | `inference-observability` | 推理 / 日志·事件·指标 | Core + Services | **部分** | `inference/inference-observability.md` | logs + observability/query；events TODO |
| P0-05 | `kb-qa-flow` | 知识库 / 问答 | Services | **部分** | `knowledge/kb-qa-flow.md` | query/stream/sessions 已声明 |
| P0-06 | `home-resource-overview` | 首页 / 资源使用概览 | 聚合 | **N/A** | `home/home-resource-overview.md` | Core list 页面聚合 |
| P0-07 | `home-inference-status` | 首页 / 推理服务状态 | Services 聚合 | **否** | `home/home-inference-status.md` | listInferenceServices |
| P0-08 | `home-gpu-utilization` | 首页 / GPU 利用率 | Core 聚合 | **部分** | `home/home-gpu-utilization.md` | gpu-inventory Phase 2 已声明；handler stub |

---

## P0 历史条目（已收口，保留追溯）

<details>
<summary>展开查看 2026-06-16 原始 P0 表</summary>

| # | 建议模块名 | 需先补 OpenAPI | 原说明 |
|---|---|---|---|
| P0-01 | alerts-pending-items | 是 | 无 alerts/ 模块 |
| P0-02 | async-task-center | 部分 | 无任务中心模块 |
| P0-03 | inference-call-test | 是→否 | 原缺 test API |
| P0-04 | inference-observability | 部分 | 缺 Console 聚合 |
| P0-05 | kb-qa-flow | 部分 | 缺独立模块 |
| P0-06 | home-resource-overview | N/A | 聚合页 |
| P0-07 | home-inference-status | 否 | 复用 list |
| P0-08 | home-gpu-utilization | 阻塞→部分 | gpu-inventory 已声明 |

</details>

---

## P1 — 中优先级（IA P1 或安全/网络子资源）

> **2026-06-17 更新**：P1 十八项均已补模块详文 + PRD/SPEC + HTML 摘要。Phase 2 后多项 OpenAPI 口径已变化，见「需先补 OpenAPI」列。

| # | 建议模块名 | 归属 | 需先补 OpenAPI | 文档 | 说明 |
|---|---|---|---|---|---|
| P1-01 | `network-vpc` | Core | **否** | `compute/network/vpc.md` | vpcs* 已冻结 |
| P1-02 | `network-subnet` | Core | **否** | `compute/network/subnet.md` | subnets* |
| P1-03 | `network-security-group` | Core | **否** | `compute/network/security-group.md` | security-groups* |
| P1-04 | `network-load-balancer` | Core | **否** | `compute/network/load-balancer.md` | load-balancers* |
| P1-05 | `network-route` | Core | **部分** | `compute/network/route.md` | Phase 2 已加 routes；DELETE 未声明 |
| P1-06 | `batch-job-instances` | Core | **部分** | `compute/batch-job-instances.md` | kind=batch_job；list filter 缺枚举 |
| P1-07 | `user-management` | Services | **是** | `tenant/user-management.md` | 独立 User CRUD TODO |
| P1-08 | `role-permission-edit` | Services | **否** | `tenant/role-permission-edit.md` | PUT roles/{id} Phase 2 |
| P1-09 | `ldap-config` | Services/Core | **是** | `tenant/ldap-config.md` | LDAP 专有路径 TODO |
| P1-10 | `container-observability` | Core | **部分** | `compute/container-observability.md` | logs/events/metrics/exec Phase 2 |
| P1-11 | `vm-snapshot-restore` | Core | **部分** | `compute/vm-snapshot-restore.md` | lifecycle snapshot + 卷快照 |
| P1-12 | `block-storage-snapshot` | Core | **部分** | `compute/storage/block-storage-snapshot.md` | snapshots Phase 2；DELETE TODO |
| P1-13 | `object-storage-upload` | Core | **部分** | `compute/storage/object-storage-upload.md` | upload/buckets/download Phase 2 |
| P1-14 | `vector-store-write` | Core | **部分** | `compute/storage/vector-store-write.md` | insert documents Phase 2 |
| P1-15 | `inference-rate-limit-policy` | Services | **否** | `inference/inference-rate-limit-policy.md` | PUT policies Phase 2 |
| P1-16 | `openai-compatible-api` | Envoy AI Gateway | **部分** | `integration/openai-compatible-api.md` | 独立数据面路由与发布流程仍需 live 验证；ANI Gateway 不代理 |
| P1-17 | `resource-pool-overview` | Core 聚合 | **是** | `compute/resource-pool-overview.md` | 无独立 pool API |
| P1-18 | `global-search` | 聚合 | **是** | `home/global-search.md` | 无 search API |

---

## P2 — 低优先级 / Phase 2–3 占位

> **2026-06-17 更新**：P2 **39 项全部**已补模块详文 + PRD/SPEC + HTML 摘要（见下表）。OpenAPI 已声明 ≠ handler 已实现。

| # | 建议模块名 | 导航/来源 | 归属 | 需先补 OpenAPI | 文档 | 说明 |
|---|---|---|---|---|---|---|
| P2-12 | `k8s-workloads` | K8s 工作负载 | Core k8s-clusters | **部分** | `compute/k8s-workloads.md` | workloads Phase 2；事件/Helm 仍待补 |
| P2-13 | `sandbox-templates` | Sandbox 模板/安全事件 | Core instances | **部分** | `compute/sandbox-templates.md` | templates + security-events Phase 2 |
| P2-14 | `gpu-page-inventory-ui` | GPU 算力独立页 | Core | **部分** | `compute/gpu-inventory-ui.md` | gpu-inventory Phase 2 已声明；无 GPU CRUD |
| P2-18 | `kb-source-citation` | 知识库 / 来源引用 | Services | **部分** | `knowledge/kb-source-citation.md` | citations Phase 2 |
| P2-19 | `kb-chat-history` | 知识库 / 对话历史 | Services | **部分** | `knowledge/kb-chat-history.md` | sessions Phase 2 |
| P2-20 | `kb-permissions` | 知识库 / 权限 | Services | **部分** | `knowledge/kb-permissions.md` | permissions PUT Phase 2 |
| P2-32 | `integration-webhook` | 接入 / Webhook | Services | **部分** | `integration/integration-webhook-overview.md` | tenant webhooks + 接入域 |
| P2-33 | `integration-bot` | 接入 / 企微·钉钉 Bot | Services | **部分** | `integration/integration-bot.md` | integrations/bots Phase 2 |
| P2-34 | `integration-third-party` | 接入 / 第三方集成 | Services | **部分** | `integration/integration-third-party.md` | integrations CRUD Phase 2 |
| P2-36 | `home-kb-trend` | 首页 / 知识库调用趋势 | Services 聚合 | **否** | `home/home-kb-trend.md` | metering/query 聚合 |
| P2-37 | `filesystem-mount-targets` | 文件存储挂载目标 | Core filesystems | **部分** | `compute/storage/filesystem-mount-targets.md` | mount-targets Phase 2 |
| P2-38 | `tenant-webhook-ops` | 租户 Webhook 运维 | Services tenant | **部分** | `tenant/tenant-webhook-ops.md` | deliveries/delete Phase 2 |
| P2-39 | `tenant-member-detail` | 租户成员详情 | Services tenant | **部分** | `tenant/tenant-member-detail.md` | members/{id} Phase 2 |
| P2-01 | `ai-native-sandbox-security` | AI 原生 / Sandbox 安全沙箱 | Services/Core | **是** | `ai-native/ai-native-sandbox-security.md` | 草案 ✅ `../openapi-drafts/phase3/openapi-phase3-ai-native-sandbox-security-draft.md` |
| P2-02 | `agent-session` | AI 原生 / Agent 会话 | Services | **是** | `ai-native/agent-session.md` | 草案 ✅ |
| P2-03 | `agent-tool-permission` | AI 原生 / 工具权限控制 | Services | **是** | `ai-native/agent-tool-permission.md` | 草案 ✅ |
| P2-04 | `agent-audit` | AI 原生 / Agent 行为审计 | Services | **是** | `ai-native/agent-audit.md` | 草案 ✅ |
| P2-05 | `prompt-injection-guard` | AI 原生 / Prompt 防护 | Services | **是** | `ai-native/prompt-injection-guard.md` | 草案 ✅ |
| P2-06 | `mcp-tool-market` | AI 原生 / MCP 市场 | Services | **是** | `ai-native/mcp-tool-market.md` | 草案 ✅ |
| P2-07 | `agent-orchestration` | AI 原生 / Agent 编排 | Services | **是** | `ai-native/agent-orchestration.md` | 草案 ✅ |
| P2-08 | `kb-doc-intelligence` | 知识库 / 文档智能 | Services | **是** | `knowledge/kb-doc-intelligence.md` | 草案 ✅ |
| P2-09 | `kb-meeting-intelligence` | 知识库 / 会议智能 | Services | **是** | `knowledge/kb-meeting-intelligence.md` | 草案 ✅ |
| P2-10 | `kb-video-intelligence` | 知识库 / 视频智能 | Services | **是** | `knowledge/kb-video-intelligence.md` | 草案 ✅ |
| P2-11 | `bare-metal-dpu` | 实例 / 裸金属·DPU | Core instances | **部分** | `compute/bare-metal-dpu.md` | kind 枚举已有；dpu-inventory 待声明 |
| P2-15 | `model-encryption` | 模型 / 加密与密钥绑定 | Services | **是** | `inference/model-encryption.md` | 草案 ✅ |
| P2-16 | `model-recommend-config` | 模型 / 部署推荐配置 | Services | **是** | `inference/model-recommend-config.md` | 草案 ✅ |
| P2-17 | `model-usage-stats` | 模型 / 调用统计 | Services + Metering | **部分** | `inference/model-usage-stats.md` | 草案 ✅ Core metering 扩展 |
| P2-21 | `api-key-audit` | API Key 审计 | Core Auth | **是** | `tenant/api-key-audit.md` | 草案 ✅ |
| P2-22 | `secrets-management` | 安全 / 密钥管理 | Core secrets | **部分** | `security/secrets-management.md` | Core `/secrets*` 已声明 |
| P2-23 | `crypto-sm` | 安全 / 国密加解密 | Core encryption | **部分** | `security/crypto-sm.md` | Core `/encryption/*` 已声明 |
| P2-24 | `netsec-policy` | 安全 / 网络安全策略 | Core network | **是** | `security/netsec-policy.md` | 草案 ✅ |
| P2-25 | `audit-log` | 安全 / 审计日志 | Core observability | **部分** | `security/audit-log.md` | Audit tag 有；list 路径待补 |
| P2-26 | `compliance` | 安全 / 合规能力 | Services/BOSS | **是** | `security/compliance.md` | 草案 ✅ Console 只读 |
| P2-27 | `billing-export` | 用量 / 账单导出 | Services/BOSS | **是** | `tenant/billing-export.md` | 草案 ✅ |
| P2-28 | `integration-go-sdk` | 接入 / Go SDK | 文档 | **N/A** | `integration/integration-go-sdk.md` | TODO-YAML N/A |
| P2-29 | `integration-py-sdk` | 接入 / Python SDK | 文档 | **N/A** | `integration/integration-py-sdk.md` | TODO-YAML N/A |
| P2-30 | `integration-ts-client` | 接入 / TS Client | 文档 | **N/A** | `integration/integration-ts-client.md` | TODO-YAML N/A |
| P2-31 | `integration-java-sdk` | 接入 / Java SDK | 文档 | **N/A** | `integration/integration-java-sdk.md` | TODO-YAML N/A |
| P2-35 | `cli-download-page` | 接入 / ani CLI | 文档 | **N/A** | `integration/cli-download-page.md` | TODO-YAML N/A |

---

## 已解决同步债（2026-06-16）

| 项 | 处理 |
|---|---|
| 租户管理 HTML 摘要 | ✅ `prototypes/ani-services-prototype-console.html` 已补全 PAGE 块 |
| 安全域「租户管理（待补）」 | ✅ 导航改为「租户管理」 |
| 角色 / SSO 与安全域口径 | ✅ 角色→租户管理只读；SSO→区分 Core 登录 vs Services 配置 |
| 安全与身份概览文案 | ✅ 指向 `tenant-management.md` |

---

## 统计

| 优先级 | 条目数 | 需先补 OpenAPI（是/部分） |
|---|---|---|
| P0 | 8（**文档已补**） | 4 仍待 YAML（P0-01/02/04 部分 + 聚合口径） |
| P1 | 18（**文档已补**） | 6 仍待 YAML（user/ldap/pool/search + 部分 DELETE/list） |
| P2 | 39（**文档已补**） | 整域/Phase 2–3 仍待 YAML 或 handler |
| **合计** | **65**（P0+P1+P2 **全部**已移出「未定义」） | **~38 需 handler** |

---

## 建议执行顺序

1. ~~**Phase 2 handler 文档验收**~~ ✅ 2026-06-17（`PHASE2-HANDLER-ACCEPTANCE-RECORD.md`）
2. **运行时 handler 验通**：Core/Services 按指南实现并 PR
3. **P0 YAML 评审**：**待定** — `openapi-p0-yaml-draft.md`
4. ~~Phase 3 Agent Session 详化~~ ✅ 2026-06-17 — `../openapi-drafts/phase3/openapi-phase3-agent-session-draft.md`
5. Phase 3 整域其余项：`../openapi-drafts/phase3/openapi-phase3-domain-draft.md` → TASK-SVC-018

---

## 相关文档

- 已定义模块：`docs/console-modules/governance/module-completion-matrix.md`
- 状态板：`docs/console-modules/governance/console-document-status-board.md`
- Schema 进度：`docs/console-modules/governance/schema-completion-tracker.md`
- P0 YAML 草案：`docs/console-modules/openapi-drafts/p0/openapi-p0-yaml-draft.md`
- Phase 3 草案：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-domain-draft.md`
- 开发任务：`tasks/execution/CORE-TEAM-TASKS.md`、`tasks/execution/SERVICES-TEAM-TASKS.md`
