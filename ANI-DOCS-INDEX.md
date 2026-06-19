# KuberCloud ANI · 文档导航与一致性矩阵

> 最后更新：2026-06-19
> 目的：让人类开发者和 AI 工具在 5 分钟内判断当前开发阶段、文档职责、下一步入口和闭环规则。

---

## 当前结论

```text
当前阶段：Phase 1 / Sprint 12 / Core「Services 支撑 Handler」实现中
当前不是 Phase 2：Phase 2 指 2026-10 以后延期能力
当前入口：repo/CURRENT-SPRINT.md
当前执行：A SPRINT12-KICKOFF-A 已完成；B1 CORE-SVC-SUPPORT-OBSERVABILITY-A 已完成；B2 CORE-SVC-SUPPORT-NETSTORE-A 已完成并复审收口；B3 CORE-SVC-SUPPORT-OBJVEC-A 待执行
代码边界：Sprint 12 状态必须关联 api/openapi/v1.yaml operationId、pkg/ports、pkg/adapters 与 services/ani-gateway/internal/router handler
Sprint 13 准备：真实 provider / live gate 计划见 repo/development-records/sprint13-real-provider-readiness-plan.md；未跑通 live gate 前不得标 runtime/production ready
真实底座门禁：REAL-K8S-LAB-A / make validate-real-k8s-profile；Sprint 5 八个 live gate（网络/VM/vCluster/upgrade/Secret/HA/KMS-SM4/GPU-CAPK）已归档 evidence
真实底座状态：Sprint 11 真实服务器只读验证与 Rook-Ceph 正式部署已闭环并转历史回归门禁（CephCluster Ready/HEALTH_OK，5 SSD OSD，ani-rbd-ssd StorageClass 上线）；RBD smoke test 与逐节点 reboot resilience 已通过
不是实际 v1.0.0 发布；backup/restore 演练、故障注入、soak 及破坏性磁盘操作须单独审批
```

Sprint 6-10 完成 contract/local/release-prep scaffold（installer、offline、CLI、RC readiness 均为 contract/local validation，非真实发布）；Sprint 11 为首次真实物理服务器验证阶段，包含 Rook-Ceph VM 优先块存储 live 部署，现已转为历史回归门禁。历史 Sprint 4 回归门禁（`SPEC-SPLIT-A`、`SPEC-CORE-BETA`、`SPEC-COMPAT-A`、`MOCK-A`、`DOC-API-A`、`SDK-BETA-*`、`SDK-MOCK-SMOKE-*`、`SPRINT4-CLOSURE-A`）有效。详细技术边界与验收命令见 [`repo/CURRENT-SPRINT.md`](repo/CURRENT-SPRINT.md)，已完成批次见 [`repo/development-records/README.md`](repo/development-records/README.md)。当前活跃冲刺为 **Sprint 12 / Core「Services 支撑 Handler」实现**（基于真实代码与 `api/openapi/v1.yaml` GAP，闭合 19 个 Core handler 缺口 + 2 个 422，仅 ANI Core，Tier1 local profile）；A/B1/B2 已完成且 B2 已复审收口，B3 待执行；状态矩阵见 [`repo/CURRENT-SPRINT.md`](repo/CURRENT-SPRINT.md)，Sprint 13 真实 provider 代码关联计划见 [`repo/development-records/sprint13-real-provider-readiness-plan.md`](repo/development-records/sprint13-real-provider-readiness-plan.md)。

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
3. `repo/CURRENT-SPRINT.md`
4. `ANI-06-开发计划.md` Section 零和当前 Sprint
5. `ANI-05-系统架构设计.md`
6. `repo/api/openapi/v1.yaml` + `repo/api/openapi/services/v1.yaml` + 相关代码入口

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
