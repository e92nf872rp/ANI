# KuberCloud ANI · 文档导航与一致性矩阵

> 最后更新：2026-07-14
> 目的：让人类开发者和 AI 工具在 5 分钟内判断当前开发阶段、文档职责、下一步入口和闭环规则。

---

## 当前结论

```text
当前阶段：Phase 1 / Sprint 13 / Core real provider 与 live gate 收敛
当前不是 Phase 2：Phase 2 指 2026-10 以后延期能力
当前入口：repo/CURRENT-SPRINT.md
代码边界：Core 保护范围沿用 Sprint 12 已闭合的 api/openapi/v1.yaml operationId、pkg/ports、pkg/adapters 与 Gateway handler；不得绕过 port，不得把 Services 业务逻辑新增到 Core API/handler/ports；Services 业务实现只在受控 Services PR 主责目录推进。
Core Sprint 13/14 既有事实继续有效：Sprint 13 production-shaped evidence 与 Sprint 14 resilience 隔离 fixture 结论保留，不外推 full platform production ready。
Services 受控并行 PR 阶段：不再按旧冻结规则处理；目录、API、handler、生成物和跨层边界分别受 CODEOWNERS 共同审查。Services PR 必须运行 `make validate-services`，该门禁包含 API split、Services boundary gate、OpenAPI/Gateway route contract、语义契约、SDK/API docs 生成漂移、模块检查和 `make validate-architecture`。
Sprint 14 分支态：feature/sprint14-core-resilience-semantics 已完成 Core resilience R-P0-0..R-P2-7 local/logic 批次；SPRINT14-CORE-RESILIENCE-LIVE-GATE / validate-sprint14-resilience-live-gate / Sprint14 resilience live gate 已在 ani-sprint14-resilience 隔离 namespace 真实通过 P0 strong backend kill、P1 weak dependency degraded、P2 controller primary kill / follower failover，并归档脱敏 evidence；production-ready 范围仅限隔离 Sprint14 Core resilience fixture，不外推到现有 Sprint13 单副本后端或 full platform。
Sprint 13 结果：S01-S07 均已通过 --production-shaped live gate，七份 evidence 均为 production_shape.status=passed，并由 validate-sprint13-b-track-production-shape 固定 proof_items。
S03 token：SPRINT13-STORAGE-ROOK-CEPH-A-TRACK / validate-storage-live-gate / Rook-Ceph / LIVE PENDING（历史兼容语境）。
S04 token：SPRINT13-GPU-INVENTORY-DCGM-A-TRACK / validate-gpu-inventory-live-gate / NVIDIA device-plugin / DCGM / LIVE PENDING（历史兼容语境）。
S05 token：SPRINT13-OBJECTSTORE-MINIO-A-TRACK / validate-object-store-live-gate / MinIO / pre-signed URL / LIVE PENDING（历史兼容语境）。
S06 token：SPRINT13-VECTOR-MILVUS-A-TRACK / validate-vector-store-live-gate / Milvus / LIVE PENDING（历史兼容语境）。
S07 token：SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A-TRACK / validate-instance-observability-live-gate / Prometheus / kubelet / LIVE PENDING（历史兼容语境）。
Auth 边界：SPRINT13-AUTH-DEX-PRODUCTION-GATE / Auth/Dex production gate 已通过；production-shaped Gateway 使用 ANI_AUTH_MODE=auth_service。
兼容硬门禁 token：S05-S07 B 轨可以继续；截至 2026-06-21，S05/S06/S07 均已 passed。
真实底座门禁：REAL-K8S-LAB-A / make validate-real-k8s-profile；Sprint 11 Rook-Ceph 正式部署结果保留为历史回归边界。
生产化边界：production-shaped acceptance passed 不等于 full platform production ready，也不是实际 v1.0.0 发布；backup/restore、故障注入、soak、正式镜像发布/升级仍需后续 release gate。
```

Sprint 6-10 完成 contract/local/release-prep scaffold（installer、offline、CLI、RC readiness 均为 contract/local validation，非真实发布）；Sprint 11 为首次真实物理服务器验证阶段，包含 Rook-Ceph VM 优先块存储 live 部署，现已转为历史回归门禁；Sprint 12 已完成 Core「Services 支撑 Handler」19 个 handler + 2 个 422 的 Tier1 local profile 收口。历史 Sprint 4 回归门禁（`SPEC-SPLIT-A`、`SPEC-CORE-BETA`、`SPEC-COMPAT-A`、`MOCK-A`、`DOC-API-A`、`SDK-BETA-*`、`SDK-MOCK-SMOKE-*`、`SPRINT4-CLOSURE-A`）有效。详细技术边界与验收命令见 [`repo/CURRENT-SPRINT.md`](repo/CURRENT-SPRINT.md)，已完成批次见 [`repo/development-records/README.md`](repo/development-records/README.md)。当前活跃冲刺为 **Sprint 13 / Core real provider 与 live gate 收敛**：S01 网络路由 Kube-OVN、S02 K8s workloads vCluster、S03 storage Rook-Ceph（SPRINT13-STORAGE-ROOK-CEPH-A-TRACK / validate-storage-live-gate）、S04 GPU NVIDIA device-plugin/DCGM（SPRINT13-GPU-INVENTORY-DCGM-A-TRACK / validate-gpu-inventory-live-gate）、S05 object-store MinIO、S06 vector Milvus 与 S07 instance observability Prometheus 已通过 production-shaped live gate 并归档 `production_shape.status=passed` evidence；从 Sprint 12 已闭合的 handler、ports、local adapters 接入真实组件和 evidence JSON 门禁；production-shaped acceptance passed 不等于 full platform production ready。Sprint14 分支已通过 SPRINT14-CORE-RESILIENCE-LIVE-GATE / validate-sprint14-resilience-live-gate / Sprint14 resilience live gate，真实动作限定在 ani-sprint14-resilience 隔离 namespace，evidence 已脱敏归档；仍不声明 full platform production ready。历史冻结原因保留在历史批次语境中，不再作为当前 PR 规则。

---

## 唯一真实来源矩阵

| 问题 | 先看哪里 | 说明 |
|---|---|---|
| 当前做什么 | `repo/CURRENT-SPRINT.md` | 当前 Sprint 的执行入口，状态、任务、验收命令以它为准 |
| 全局开发节奏 | `ANI-06-开发计划.md` | Sprint 计划、Services 解锁门禁、延期项以它为准 |
| 产品功能边界 | `ANI-02-产品功能设计.md` | Core/Services 分层、v1.0.0 P0 能力边界以它为准 |
| 系统架构图和模块边界 | `ANI-05-系统架构设计.md` | Core/Services、API/SDK、ports/adapters、local profile/real provider 的结构图以它为准 |
| 路线图阶段 | `ANI-03-产品路线图.md` | Phase 1/2/3 与版本号关系以它为准 |
| 工程约定和 AI 工作规则 | `CLAUDE.md` | AI/人类开发前必须先读；只维护稳定规则和入口，不维护批次流水账 |
| GitHub 协作、提交纪律与 AI Coding | `ANI-15-GitHub-协作规范与提交纪律.md` | 多人/多 Agent 并行开发、PR 证据、main 受保护串行收口和 public 仓库安全的统一规范 |
| API 契约 | `repo/api/openapi/v1.yaml` | Core OpenAPI REST API 与 Core/Services 跨层控制面契约的唯一真实来源 |
| Services API 契约 | `repo/api/openapi/services/v1.yaml` | Services 层业务 API 契约 |
| 当前团队任务清单 | `repo/services/tasks/execution/CORE-TEAM-TASKS.md`、`repo/services/tasks/execution/SERVICES-TEAM-TASKS.md` | Core/Services 当前待办、执行状态和 AI coding 输入；Services 团队维护于此（ANI-14 规范：评审后迁入 `repo/docs/`） |
| 跨团队依赖图 | `repo/services/tasks/execution/TASK-DEPENDENCY-MAP.md` | 当前 Core/Services 任务依赖、并行开发建议和关键路径 |
| Services 功能定义与接口规划 | `repo/services/docs/`、`repo/services/tasks/` | Console/BOSS 模块详文、YAML 草案、PRD/SPEC；见 `repo/services/README.md` |
| 已完成批次 | `repo/development-records/README.md` | 历史完成记录索引，不作为当前任务清单 |
| 单批次细节 | `repo/development-records/*.md` | 已完成批次的历史归档、验证证据和实现追溯；不承载当前任务状态 |
| guard 微批次系列详情 | `repo/development-records/guard-series/{series}-guard-index.md` | guard 系列完整 ID 列表和批次链接；新 guard 微批次只更新此文件，不更新主文档 |

---

## 推荐阅读路径

### 人类开发者

1. `ANI-DOCS-INDEX.md`
2. `CLAUDE.md` 的 5 分钟快速上手
3. `ANI-15-GitHub-协作规范与提交纪律.md`（提交、PR、并行协作和 AI Coding）
4. `repo/CURRENT-SPRINT.md`
5. `ANI-06-开发计划.md` Section 零和当前 Sprint
6. `ANI-05-系统架构设计.md`
7. `repo/api/openapi/v1.yaml` + `repo/api/openapi/services/v1.yaml` + 相关代码入口

### AI 编码工具

1. 必须先读 `CLAUDE.md`
2. 再读 `ANI-DOCS-INDEX.md`
3. 再读 `repo/CURRENT-SPRINT.md`
4. 开发前检查 `ANI-06-开发计划.md` Section 零
5. 涉及架构边界时检查 `ANI-05-系统架构设计.md`
6. 涉及接口时先改 `repo/api/openapi/v1.yaml` 或 `repo/api/openapi/services/v1.yaml`
7. 完成后按 `CLAUDE.md` 的进度更新规约闭环

---

## 当前开发门禁

| 日期 | 门禁 | 当前影响 |
|---|---|---|
| 2026-05-31 | P0 依赖矩阵冻结 | 已完成历史批次归档，后续只按当前 Sprint 补缺口 |
| 2026-06-10 | Core API Alpha Freeze | 已完成 instances 等核心路径冻结；新增能力必须保持兼容性 |
| 2026-06-20 | SDK Alpha | 四语言 Core/Services SDK 已可生成，并由 SDK Beta/Mock smoke 持续校验 |
| 2026-06-30 | Core Dev Profile Ready | Core dev/local profile 边界已建立；Sprint 5 继续补真实 provider |
| 2026-07-31 | Core Real Path Beta | Sprint 5 已通过：K8s/Kube-OVN/KubeVirt/vCluster/KMS-SM4/Secrets/HA 8 个真实 live gate 已归档 evidence；后续作为回归门禁保留 |
| 2026-09-30 | v1.0.0 Final Delivery | ANI Core v1.0.0 + ANI Services P0 |

---

## 文档维护规则

1. 当前阶段变更时，必须同步 `ANI-DOCS-INDEX.md`、`ANI-06-开发计划.md` 和 `repo/CURRENT-SPRINT.md`。
2. 批次完成时，必须新增或更新 `repo/development-records/{批次名}.md`，并更新 `repo/development-records/README.md`。
3. `repo/services/tasks/execution/CORE-TEAM-TASKS.md`、`repo/services/tasks/execution/SERVICES-TEAM-TASKS.md` 和 `repo/services/tasks/execution/TASK-DEPENDENCY-MAP.md` 是当前协作执行入口（Services 团队维护于此；ANI-14 规范：评审后迁入 `repo/docs/`）；同名或近似任务清单不得长期双写到 `repo/development-records/`。
4. `repo/development-records/` 只做历史归档：任务完成后记录完成项、验证命令、证据和关键文件；不要在其中维护当前待办状态。
5. 历史归档文档允许保留当时日期和上下文，不反向改写为当前态。
6. 若 `CLAUDE.md` 与其它文档冲突，以 `CLAUDE.md` 的工程规则为准；若是进度状态冲突，以 `ANI-06-开发计划.md` Section 零和 `repo/CURRENT-SPRINT.md` 为准。
7. `CLAUDE.md` 只保留稳定强制规则、读取顺序、架构边界、提交门禁和 Karpathy 五条开发原则；禁止写入单批次完成清单、API path 长列表、文件级变更清单和每日开发流水账。
8. Sprint 状态、完成归档和验证证据维护在 `repo/CURRENT-SPRINT.md`、`ANI-06-开发计划.md` Section 零和 `repo/development-records/*.md`；当前团队任务清单维护在 `repo/services/tasks/execution/`（ANI-14 规范：评审后迁入 `repo/docs/`）。
9. 更换 AI 模型或工具时，必须先重新读取本文件、`CLAUDE.md` 和 `repo/CURRENT-SPRINT.md`，不得依赖上一个会话的记忆。
10. 修改文档入口后必须运行 `make validate-doc-entrypoints`。

<!-- 历史回归门禁校验器兼容标记（请勿删除；对应 dev-records 历史批次与 make validate-* 门禁） -->
**历史回归门禁 token（校验器兼容，勿删）：** SPEC-SPLIT-A、SPEC-CORE-BETA、SPEC-COMPAT-A、SDK-BETA-A、SDK-BETA-B、SDK-BETA-C、SDK-BETA-D、SDK-MOCK-SMOKE-A、SDK-MOCK-SMOKE-B、SDK-MOCK-SMOKE-C、SDK-MOCK-SMOKE-D、MOCK-A、DOC-API-A、SPRINT4-CLOSURE-A（`make validate-sprint4-closure`）；Sprint 11 / Core Real Deployment Validation 正式部署完成；真实服务器只读验证已完成；Rook-Ceph 正式部署已完成；Sprint 11 执行环境：正式部署执行环境。
