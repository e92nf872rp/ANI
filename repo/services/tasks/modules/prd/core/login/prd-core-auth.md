# PRD: Core Auth — 登录认证后端

> **来源：** 拆分自 `prd-console-login-page.md` v2.0
> **日期：** 2026-07-03
> **范围：** Core API（OpenAPI `v1.yaml`）、auth-service handler、错误码、dev profile

---

## 1. Overview

Core Auth 提供 OIDC、账密、平台管理员认证 API，供 Console 和 BOSS 消费。所有新增能力**必须先改 `repo/api/openapi/v1.yaml`**，再实现 handler。

**一级权威源：**

- `repo/api/openapi/v1.yaml` — Auth 路径与 schema（**新增能力须先改此文件**）

**现有实现基线（2026-07-03）：**

- Core OpenAPI 已声明：`/auth/oidc/begin`、`/auth/token`、`/auth/refresh`、`/auth/logout`
- **未声明：** 账密登录、BOSS 平台登录、dev token、扩展错误码枚举

---

## 2. Goals

- P0 实现并验收现有 OIDC 四端点
- P0 统一 Auth 错误 `code` 枚举
- P0 提供 dev profile 可测登录端点；生产门禁禁止
- P1 新增账密登录 OpenAPI 并实现 handler
- P1 新增平台管理员认证 API，与租户 token 隔离
- 新增 POST 写路径支持 `idempotency_key`

---

## 3. User Stories

### US-011: Core — OIDC 链路实现与错误码（P0）

**Description:** 作为 Core 开发者，我希望 OIDC 四端点在 lab 可重复验收，并返回统一错误结构。

**Acceptance Criteria:**

- [ ] `beginOIDCLogin`、`completeOIDCLogin`、`refresh`、`logout` handler 实现与 `v1.yaml` 一致
- [ ] `begin` 租户不存在 → `404` 或 `400`，`code=TENANT_NOT_FOUND`
- [ ] `begin` IdP/Dex 不可达 → `503` 或 `422`，`code=IDP_UNAVAILABLE`
- [ ] `token` state/code 无效 → `401`，`code=OIDC_EXCHANGE_FAILED`
- [ ] 错误体均为 `ErrorResponse`：`{ code, message, request_id }`
- [ ] `make test`、`make validate-architecture` 通过；OIDC 集成测试或 live gate 证据归档

### US-012: Core — 账密登录 API（P1）

**Description:** 作为 Core 开发者，我希望提供租户账密登录 API，供 Console 消费。

**Acceptance Criteria:**

- [ ] **先改** `repo/api/openapi/v1.yaml`，新增 `POST /api/v1/auth/password/login`（operationId `passwordLogin`）
- [ ] Request：`tenant_name`、`username`、`password`；可选 `idempotency_key`
- [ ] Response 200：`TokenPairResponse`（与 OIDC 一致）
- [ ] 失败：`401 INVALID_CREDENTIALS`；租户不存在：`TENANT_NOT_FOUND`；账户锁定等扩展码 SPEC 列举
- [ ] 禁止在 Gateway 日志打印明文密码
- [ ] RBAC：返回 token 的 `scope` / `tenant_id` 与租户成员身份一致

### US-013: Core — Dev Profile 可测登录（P0，仅非生产）

**Description:** 作为前端开发者，我希望本地 dev profile 在无 IdP 时仍能联调登录与会话链路。

**Acceptance Criteria:**

- [ ] dev profile 启用条件由 deploy profile / 环境变量冻结（如 `CORE_DEV_PROFILE_A`）；**生产 preflight 必须失败**若 dev 登录端点可达
- [ ] 提供 SPEC 冻结的 dev 登录方式之一：专用 `POST /api/v1/auth/dev/token` **或** profile 下 password 固定测试账号 — **须先改 `v1.yaml`**
- [ ] 响应仍为 `TokenPairResponse`；前端 **不得** 内置硬编码 JWT
- [ ] Console 在 `import.meta.env.DEV` 且配置允许时展示「开发登录」入口（可折叠）；**生产构建不包含该 UI**
- [ ] 文档与代码注释标明 **Non-Goal：禁止进生产**

### US-015: Core — BOSS 平台认证 API（P1）

**Description:** 作为 Core 开发者，我希望平台管理员认证与租户认证在 API 层可区分。

**Acceptance Criteria:**

- [ ] `v1.yaml` 新增或扩展：平台 OIDC begin、平台账密登录（路径前缀如 `/api/v1/auth/platform/*` 或 `audience=platform` — **SPEC 二选一冻结**）
- [ ] 平台 token 的 claims 含 `role/platform_scope`，不得与租户 token 混用
- [ ] 租户 token 不能访问 BOSS 平台写接口；平台 token 不能冒充租户业务 API（集成测试覆盖）
- [ ] 错误码与 US-011 同一 `ErrorResponse` 规范

---

## 4. Functional Requirements

- **FR-11:** P0 必须实现并验收现有 `/auth/oidc/begin`、`/auth/token`、`/auth/refresh`、`/auth/logout`
- **FR-12:** P0 必须统一 Auth 错误 `code` 枚举（至少：`TENANT_NOT_FOUND`、`IDP_UNAVAILABLE`、`OIDC_EXCHANGE_FAILED`、`INVALID_CREDENTIALS`）
- **FR-13:** P0 必须提供 dev profile 可测登录端点；生产门禁禁止
- **FR-14:** P1 必须新增账密登录 OpenAPI 并实现 handler
- **FR-15:** P1 必须新增平台管理员认证 API，与租户 token 隔离
- **FR-16:** 新增 POST 写路径必须支持 `idempotency_key`（按 OpenAPI 约定）
- **FR-17:** 必须先改 `repo/api/openapi/v1.yaml`，再写 handler / SDK / 前端

---

## 5. Non-Goals

- 生产环境启用 dev 登录或前端 dev bypass
- 修改 `repo/api/openapi/services/v1.yaml` 承载登录
- 注册、忘记密码、邮箱验证、MFA

---

## 6. Technical Considerations

### OpenAPI 变更清单（实施前强制）

| 优先级 | 能力 | 路径（草案，以 YAML 为准） |
|--------|------|---------------------------|
| P0 | OIDC 错误码文档化 | 现有四端点 responses 增补 `code` 示例 |
| P0 | Dev 登录 | `POST /api/v1/auth/dev/token` 或等价 — **TODO-YAML** |
| P1 | 租户账密 | `POST /api/v1/auth/password/login` — **TODO-YAML** |
| P1 | 平台 OIDC/账密 | `/api/v1/auth/platform/...` — **TODO-YAML** |

### 实施顺序（强制）

```text
① v1.yaml（错误码 + dev + 账密 + platform）
② Core handler + 测试 + live/dev gate
③ Console P0 消费
④ Console P1 账密 Tab
⑤ BOSS P1 登录页
```

### Dev profile

- 对齐 `CORE-DEV-PROFILE-A`；与 `ANI_GPU_*` 类门禁相同：**local 可证边界，不能标 production ready**

---

## 7. Success Metrics

| 指标 | 目标 |
|------|------|
| Console P0 OIDC（或 dev 等价） | 3 分钟内测试可走通完整链路 |
| 401/refresh | 无死循环；用户见明确提示 |
| 生产安全 | dev 登录端点在生产 preflight **失败** |
| P1 账密 | 错误凭证 100% 返回 `INVALID_CREDENTIALS`，不泄露用户是否存在 |

---

## 8. Open Questions

1. **平台认证路径形态：** `/auth/platform/*` 独立前缀 vs 同一端点 + `audience` 字段 — **SPEC 冻结**（US-015）
2. **账密登录路径最终命名：** `password/login` vs `login` — **以 `v1.yaml` PR 为准**
3. **BOSS 与 Console 是否共用同一 IdP Dex realm** — 运维/Core 确认
4. **生产 `redirect_uri` 白名单** — 运维登记

---

## 9. ANI Boundaries

| Item | Value |
|------|-------|
| **Product line** | core |
| **Code scope** | Core: Auth handlers、`repo/api/openapi/v1.yaml` |
| **OpenAPI authority** | **必须先改** `v1.yaml`；P0 消费既有 OIDC 四端点；P1 新增 password/platform/dev |
| **Frozen exclusions** | 不改 Services `v1.yaml`；不在生产启用 dev bypass |
| **idempotency_key** | 新增 POST：账密登录、dev token（若定义为可重试创建）；OIDC begin 按 SPEC 判定 |

---

## 10. 参考文档

- Console 登录 PRD: `repo/services/tasks/modules/prd/console/login/prd-console-login-page.md`
- BOSS 登录 PRD: `repo/services/tasks/modules/prd/boss/settings/prd-boss-login-page.md`
- SPEC: `repo/services/tasks/modules/spec/console/tenant/spec-console-login-page.md`
- 主维护文档: `repo/services/docs/console-modules/tenant/security-identity-overview.md`
