# BOSS 邮件通知 · 文档包

导出时间：2026-07-10

## 主文档

| 文档 | 路径 |
|------|------|
| PRD | `prd/prd-boss-email-notification.md` |
| UX | `ux/ux-boss-email-notification.md` |

## 引用文档

### BOSS 集成域（对照模块）

| 文档 | 路径 | 引用原因 |
|------|------|----------|
| 平台企业通知（IM） | `references/boss-modules/integration/enterprise-notification.md` | PRD/UX 对照：IM vs Email 职责边界 |
| 运维 Webhook | `references/boss-modules/integration/ops-webhook.md` | PRD Non-Goals：出站 Webhook 边界 |
| 集成域索引 | `references/boss-modules/integration/README.md` | 域内模块导航 |

### 同级参考（企业通知集成三件套）

| 文档 | 路径 |
|------|------|
| PRD | `references/prd-boss-enterprise-notification.md` |
| SPEC | `references/spec-boss-enterprise-notification.md` |

### UI 规范（UX 必读）

| 文档 | 路径 |
|------|------|
| 设计原则 2.0 | `references/UI规范/产品设计规范-设计原则-2.0.md` |
| TDesign 组件与 Token 2.0 | `references/UI规范/产品设计规范-TDesign组件与Token-2.0.md` |
| 页面模板 2.0 | `references/UI规范/产品设计规范-页面模板-2.0.md` |

## 未包含（仓库内引用但未单独导出）

- `repo/api/openapi/v1.yaml` — OpenAPI 权威源（体积大，需在仓库内查看）
- `ANI-06-开发计划.md` — PRD 规划参考（notifications 路径提及）
- `repo/services/docs/boss-modules/integration/email-notification.md` — 模块主文档（已建成）
