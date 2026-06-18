# BOSS 模块文档

按 **业务域** 存放各 BOSS 页面的**主维护源**（模块详文）。

## 快速导航

### 治理与状态

| 文档 | 用途 |
|---|---|
| [boss-document-status-board.md](governance/boss-document-status-board.md) | **唯一总控** — 模块完成状态 |
| [module-completion-matrix.md](governance/module-completion-matrix.md) | 模块 × 冻结路径对照 |
| [module-delivery-workflow.md](governance/module-delivery-workflow.md) | 交付与复审口径 |
| [boss-full-depth-checklist.md](governance/boss-full-depth-checklist.md) | Console 满配章节 + 阻塞项 |
| [notebook/README.md](../notebook/README.md) | 前端开发日志（按日记录） |

### 业务域模块详文

#### 平台运营总览（[`overview/`](overview/)）

- [运营总览](overview/operations-overview.md) — 平台运营总览 / 运营总览
- [资源池与容量态势](overview/resource-capacity-trend.md) — 平台运营总览 / 资源池与容量态势
- [GPU 资源池态势](overview/gpu-pool-trend.md) — 平台运营总览 / GPU 资源池态势
- [AI 服务运营态势](overview/inference-ops-trend.md) — 平台运营总览 / AI 服务运营态势
- [知识库运营态势](overview/kb-ops-trend.md) — 平台运营总览 / 知识库运营态势
- [平台告警与待处理](overview/platform-alerts-pending.md) — 平台运营总览 / 平台告警与待处理

#### 租户与客户管理（[`tenant/`](tenant/) · [域说明](tenant/README.md)）

- [租户列表](tenant/tenant-list.md) — 租户与客户管理 / 租户列表
- [配额策略](tenant/tenant-quota-policy.md) — 租户与客户管理 / 配额策略
- [租户管理员](tenant/tenant-admin.md) — 租户与客户管理 / 租户管理员
- [租户计费与用量](tenant/tenant-usage-billing.md) — 租户与客户管理 / 租户计费与用量

#### 资源池与基础设施（[`ops/`](ops/)）

- [平台资源池总览](ops/platform-resource-pool.md) — 资源池与基础设施 / 平台资源池总览
- [GPU 资源池管理](ops/gpu-pool-management.md) — 资源池与基础设施 / GPU 资源池管理
- [节点状态](ops/node-status.md) — 资源池与基础设施 / 节点状态
- [存储基础设施](ops/storage-infrastructure.md) — 资源池与基础设施 / 存储基础设施
- [网络基础设施](ops/network-infrastructure.md) — 资源池与基础设施 / 网络基础设施
- [镜像仓库 / 项目配额](ops/registry-project-quota.md) — 资源池与基础设施 / 镜像仓库 / 项目配额
- [镜像仓库 / 漏洞扫描](ops/registry-vulnerability-scan.md) — 资源池与基础设施 / 镜像仓库 / 漏洞扫描
- [镜像仓库 / 垃圾回收](ops/registry-garbage-collection.md) — 资源池与基础设施 / 镜像仓库 / 垃圾回收

#### 运维与可观测（[`health/`](health/)）

- [平台健康](health/platform-health.md) — 运维与可观测 / 平台健康
- [GPU 监控](health/gpu-monitoring.md) — 运维与可观测 / GPU 监控
- [推理监控](health/inference-monitoring.md) — 运维与可观测 / 推理监控
- [知识库监控](health/kb-monitoring.md) — 运维与可观测 / 知识库监控
- [日志](health/platform-logs.md) — 运维与可观测 / 日志
- [Trace](health/platform-trace.md) — 运维与可观测 / Trace
- [告警规则](health/alert-rules.md) — 运维与可观测 / 告警规则
- [运维 Skills](health/maint-skills.md) — 运维与可观测 / 运维 Skills
- [任务历史](health/job-history.md) — 运维与可观测 / 任务历史
- [故障处理](health/incident-handling.md) — 运维与可观测 / 故障处理

#### 平台计量与结算（[`metering/`](metering/) · [域索引](metering/README.md)）

- [平台 GPU-Hours](metering/platform-gpu-hours.md) — 平台计量与结算 / 平台 GPU-Hours
- [平台 CPU-Hours](metering/platform-cpu-hours.md) — 平台计量与结算 / 平台 CPU-Hours
- [平台 Memory-GBHours](metering/platform-memory-gbhours.md) — 平台计量与结算 / 平台 Memory-GBHours
- [平台 Storage-GBDays](metering/platform-storage-gbdays.md) — 平台计量与结算 / 平台 Storage-GBDays
- [平台 Input Tokens](metering/platform-input-tokens.md) — 平台计量与结算 / 平台 Input Tokens
- [平台 Output Tokens](metering/platform-output-tokens.md) — 平台计量与结算 / 平台 Output Tokens
- [平台 KB Queries](metering/platform-kb-queries.md) — 平台计量与结算 / 平台 KB Queries

#### 安全审计与合规（[`audit/`](audit/)）

- [平台审计日志](audit/platform-audit-log.md) — 安全审计与合规 / 平台审计日志
- [API Key 审计](audit/platform-apikey-audit.md) — 安全审计与合规 / API Key 审计
- [推理调用审计](audit/platform-inference-audit.md) — 安全审计与合规 / 推理调用审计
- [合规导出与取证](audit/compliance-export.md) — 安全审计与合规 / 合规导出与取证

#### 交付与安装（[`settings/`](settings/)）

- [ani-installer](settings/ani-installer.md) — 交付与安装 / ani-installer
- [裸机部署](settings/baremetal-deploy.md) — 交付与安装 / 裸机部署
- [VM 部署](settings/vm-deploy.md) — 交付与安装 / VM 部署
- [已有 K8s 接入](settings/k8s-attach.md) — 交付与安装 / 已有 K8s 接入
- [硬件检测](settings/hardware-check.md) — 交付与安装 / 硬件检测
- [GPU 驱动安装](settings/gpu-driver-install.md) — 交付与安装 / GPU 驱动安装
- [内部 CA](settings/internal-ca.md) — 交付与安装 / 内部 CA
- [纯 IP HTTPS](settings/ip-https.md) — 交付与安装 / 纯 IP HTTPS
- [离线安装包](settings/offline-package.md) — 交付与安装 / 离线安装包
- [交付验收手册](settings/acceptance-manual.md) — 交付与安装 / 交付验收手册
- [品牌配置](settings/brand-config.md) — 交付与安装 / 品牌配置（Phase 2）
- [升级与备份](settings/upgrade-backup.md) — 交付与安装 / 升级与备份（部分 Phase 2）

#### 平台集成与通知（[`integration/`](integration/)）

- [运维 Webhook](integration/ops-webhook.md) — 平台集成与通知 / 运维 Webhook
- [企业通知集成](integration/enterprise-notification.md) — 平台集成与通知 / 企业通知集成（Phase 2）
- [运营系统对接](integration/ops-system-integration.md) — 平台集成与通知 / 运营系统对接

## 维护规则

1. **HTML 摘要**：[`prototypes/ani-services-prototype-boss.html`](../../prototypes/ani-services-prototype-boss.html) 只保留导航与边界，详文在本目录。
2. **PRD/SPEC**：[`tasks/modules/prd/prd-boss-*.md`](../../tasks/modules/prd/) · [`tasks/modules/spec/spec-boss-*.md`](../../tasks/modules/spec/) — 辅助材料，不替代详文。
3. **流程**：[`ANI-main/ANI-14-API对齐与开发工作流.md`](../../ANI-main/ANI-14-API对齐与开发工作流.md) Phase 1 + [`governance/module-delivery-workflow.md`](governance/module-delivery-workflow.md)
4. **Console 对照**：租户侧能力优先复用 [`docs/console-modules/`](../console-modules/) 的冻结事实，BOSS 只补充平台级差异。
