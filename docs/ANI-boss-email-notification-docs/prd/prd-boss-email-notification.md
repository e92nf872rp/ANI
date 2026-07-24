# PRD: BOSS 通知设置 · 邮件通知

## 1. Introduction / Overview

在 BOSS「通知设置」中提供 **邮件通知** 能力，让 **平台管理员** 配置：平台级邮件发信通道（SMTP）、收件人，以及哪些平台事件走邮件。目标是让 SRE/平台运营在邮箱收到跨租户运维告警、Incident、关键任务失败等通知。

本能力属于 **BOSS 平台侧**，不是 Console 租户自助通知。  
与现有「平台企业通知」（企微/钉钉 IM）并列：IM 管 Bot，本页管 **Email 通道与订阅**。

信息架构：BOSS「平台集成与通知」域下「通知设置 → 邮件通知」子页/子 Tab（与「平台企业通知」同域、职责分离）。

## 2. Goals

- 平台管理员可配置并验证邮件发信通道（首期 SMTP）
- 可维护平台级收件邮箱列表（含启停）
- 可按事件类型开关邮件订阅并保存
- 支持测试发送，确认通道与收件人可用
- 产品边界与 OpenAPI 对齐后可联调；未声明路径须先补 Core 契约，不假装已冻结
- 与「平台企业通知」职责清晰，不互相替代

## 3. User Stories

### US-001: 配置邮件发信通道
**Description:** As a 平台管理员, I want to 配置 SMTP 发信通道, so that 平台能发出邮件通知.

**Acceptance Criteria:**
- [ ] 可填写并保存 SMTP 必要字段（host/port/加密方式/账号等；密码类仅写入不回显明文）
- [ ] 保存成功有明确成功反馈；失败展示统一错误结构可读信息
- [ ] 无权限用户不可进入编辑或保存失败且可理解
- [ ] 写操作携带 `idempotency_key`（契约声明后）
- [ ] Typecheck/lint（BOSS 前端批次）通过
- [ ] 浏览器验证：loading / empty（未配置）/ error

### US-002: 维护收件邮箱列表
**Description:** As a 平台管理员, I want to 管理收件邮箱, so that 通知发到正确的人.

**Acceptance Criteria:**
- [ ] 可新增、编辑、删除/停用收件邮箱；非法邮箱格式有前端校验提示
- [ ] 列表展示地址、备注（若有）、启用状态
- [ ] 空列表有空态与引导
- [ ] 所有已开启订阅的事件共用这份全局启用中的收件人列表
- [ ] Typecheck/lint 通过
- [ ] 浏览器验证：loading / empty / error

### US-003: 配置事件邮件订阅开关
**Description:** As a 平台管理员, I want to 选择哪些平台事件发邮件, so that 只收到关心的通知.

**Acceptance Criteria:**
- [ ] 展示首期冻结事件清单：平台告警 P0、平台告警 P1、Incident 创建、Incident 升级、平台关键任务失败
- [ ] 每个事件可独立开关邮件；支持批量保存
- [ ] 保存后刷新仍保持设置
- [ ] Typecheck/lint 通过
- [ ] 浏览器验证：loading / empty / error

### US-004: 测试发送邮件
**Description:** As a 平台管理员, I want to 发一封测试邮件, so that 上线前确认通道与收件人可用.

**Acceptance Criteria:**
- [ ] 在已配置通道且至少有一个启用收件人时可发起测试发送
- [ ] 未配置通道或无收件人时，操作不可用或给出明确前置条件提示
- [ ] 成功/失败有明确反馈（错误态优先展示可读 message；有 request_id 则展示）
- [ ] 本轮不要求独立投递历史页；测试发送结果反馈即可
- [ ] Typecheck/lint 通过
- [ ] 浏览器验证相关状态

### US-005: 契约与联调闭环
**Description:** As a 研发, I want OpenAPI 与页面字段对齐并可联调, so that 完整交付而非纯文档页.

**Acceptance Criteria:**
- [ ] Core OpenAPI 补齐本页所需 path/schema（当前 `v1.yaml` 仅有 Notifications tag、无 notifications path —— 须先改契约再实现）
- [ ] BOSS 通过 Core Client 调用 `/api/v1/...`，不把平台邮件配置写进 Services 租户契约
- [ ] 本地可联调：Gateway + BOSS；关键写路径可幂等重试验证
- [ ] `make test` / 架构门禁按 Core+BOSS 批次要求通过

## 4. Functional Requirements

- FR-1: 系统必须在 BOSS「通知设置」下提供「邮件通知」配置页（或等价 Tab），归属平台集成与通知域
- FR-2: 系统必须允许平台管理员配置 SMTP 发信通道（含常见加密：STARTTLS/SSL）
- FR-3: 系统必须对密钥/密码类字段脱敏展示，且响应中不回显明文密钥
- FR-4: 系统必须支持平台级收件邮箱的增删改与启停（全局一份列表）
- FR-5: 系统必须支持按首期事件枚举开关邮件订阅并持久化
- FR-6: 系统必须提供测试发送能力，并校验通道与收件人前置条件
- FR-7: 系统必须仅允许具备 `platform.notifications.read` / `platform.notifications.write`（最终以 YAML 为准）的平台管理员读写；与平台企业通知共用同一类权限
- FR-8: 系统必须在 API 未就绪时展示可区分的「契约/服务未就绪」态，不得伪造已保存成功的假数据冒充联调成功
- FR-9: 所有有副作用的 POST/PUT/PATCH 必须支持 `idempotency_key`
- FR-10: 错误响应必须符合 `{ code, message, request_id, details? }`

## 5. Non-Goals

- 不做邮件模板在线编辑/可视化排版
- 不做 Console 租户侧邮件通知自助配置（本 PRD 仅 BOSS）
- 不替代「平台企业通知」企微/钉钉 IM 渠道配置
- 首期不做云厂商邮件 API（SendGrid/SES 等）；标为 P2
- 首期不做按事件组绑定不同收件人；标为 P2
- 首期不做完整投递历史 list/独立历史中心；仅测试发送成功/失败反馈（完整历史 P2）
- 不在本 PRD 实现站内信产品；Webhook 出站见既有 ops-webhook / 企业通知边界
- 不把 Services 租户 `integrations` / Bot API 冒充为平台邮件 list
- 不发明未写入 OpenAPI 的字段并当作已冻结正式契约

## 6. Design Considerations

- 复用 BOSS 既有设置/集成页布局：表单、列表、空态、无权限态、API 未就绪态
- 与 [`enterprise-notification.md`](../../../docs/boss-modules/integration/enterprise-notification.md) 同属「平台集成与通知」：IM vs Email 职责分离
- 密钥字段：编辑时「保留原值 / 重新输入」交互，避免空白覆盖误删
- 详细交互由后续 `/prd-to-ux` 展开

## 7. Technical Considerations

- **权威源：** Core `repo/api/openapi/v1.yaml`；当前仅有 `Notifications` tag，**无** `/notifications*` path
- **规划参考（非冻结）：** `ANI-06` 提及 `CRUD /api/v1/notifications/subscriptions`、`GET /api/v1/notifications/events` —— 本页邮件通道/收件人可能需额外 path，一律 **先改 YAML 再实现**
- **代码范围：** BOSS `repo/frontends/boss/` + Core 契约与 Gateway handler（完整联调批次）；禁止改冻结 Services 业务服务冒充平台邮件
- **租户：** 平台管理员身份；不信任 body 伪造跨租户 `tenant_id`
- **完整交付顺序：** OpenAPI → Gateway → BOSS UI 联调

## 8. Success Metrics

- 平台管理员可在 5 分钟内完成：配 SMTP → 加收件人 → 开订阅 → 测试发送成功
- 联调环境测试邮件可达指定收件箱
- 无权限用户无法改配置
- 与企微/钉钉页无职责混淆（文档与导航可区分）

## 9. Decisions (原 Open Questions，已按推荐收口)

| # | 决议 | 理由摘要 |
|---|------|----------|
| 1 | 模块域：`integration`；IA：通知设置 → 邮件通知 | 与平台企业通知同属通知出口；不与交付安装 settings 域混放 |
| 2 | 首期仅 SMTP（STARTTLS/SSL）；云厂商 API = P2 | 覆盖自建/企业中继，联调成本低；云 API 鉴权差异大 |
| 3 | 首期事件：告警 P0/P1、Incident 创建/升级、平台关键任务失败 | 对齐 incident / platform-alerts；枚举可后续追加 |
| 4 | 全局一份启用中收件人列表；按事件绑定 = P2 | 平台 oncall/组邮箱场景足够；绑定模型另开批次 |
| 5 | 本轮无完整投递历史；仅测试发送反馈；历史 = P2 | 主路径是配通可发；历史接近独立模块 |
| 6 | RBAC 与平台企业通知共用 `platform.notifications.read/write`（以 YAML 为准） | 同为平台通知出口，避免两套权限 |

## 10. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | boss |
| Code scope | `repo/frontends/boss/` + Core OpenAPI/Gateway（完整联调）；不改 Services 冻结后端 |
| OpenAPI authority | **须 Core 变更批次**：先补 `v1.yaml`，再实现；禁止自造已冻结 path |
| Frozen exclusions | Services `model-service`/`kb-service`；不得用租户 integrations 冒充平台邮件 API |
| idempotency_key | 通道保存、收件人写操作、订阅保存、测试发送等写路径 |
| Module main doc | 已建成：`repo/services/docs/boss-modules/integration/email-notification.md` |
| 对照模块 | `boss-modules/integration/enterprise-notification.md`（IM，非 Email） |
