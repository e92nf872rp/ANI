# AUTHZ-POLICY-COMPAT-CONTRACT-PILOT — Gateway OpenAPI 鉴权四批次落地

> 计划来源：`repo/services/tasks/modules/plan/plan-authz-policy-compat-contract-pilot-v4.md`
> 覆盖批次：PR1 AUTHZ-POLICY-A / PR2 AUTHZ-COMPAT-B0 / PR3 AUTHZ-CONTRACT-B1 / PR4 AUTHZ-PILOT-C
> 实施分支：`feat/gateway-authz-policy`（原始开发在 `feat/authz-policy-compat-contract-pilot` @ `43a8558`，后迁移至目标分支）
> 完成日期：2026-08-24

## 实现了什么

把 V4 方案中四个 Functional MVP 批次从计划落地为可编译、可测试的代码，通过四批次 commit 分阶段引入 Gateway OpenAPI 鉴权策略注册表、统一 Principal 与 identity key、V2 授权契约和 pilot 启用。全程 `mode=off` 默认不切流，仅在 PR4 对 `listQuotaMeta` 启用 pilot。

## 关键文件改动

46 files changed, +7007/−563。核心分布：

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/generate_gateway_authz.py` | 新增 | 解析 Core OpenAPI、校验 x-ani-authz 扩展、生成 Go registry |
| `scripts/generate_gateway_authz_test.py` | 新增 | schema/OR/AND 拒绝/public 冲突/legacy/determinism 测试 |
| `scripts/validate_gateway_authz_drift.py` | 新增 | 临时生成并比较 committed artifact |
| `scripts/validate_core_gateway_authz_routes.py` | 新增 | 校验已注册 Core route 与生成 registry 覆盖关系 |
| `services/ani-gateway/internal/authz/policy.go` | 新增 | Policy 类型、枚举和 registry lookup |
| `services/ani-gateway/internal/authz/zz_generated_core_policies.go` | 生成 | Core public/generated/legacy 统一注册表 |
| `services/ani-gateway/internal/authz/principal.go` | 新增 | 规范 Principal + LegacyPrincipalView + IdentityKey |
| `services/ani-gateway/internal/authz/mode.go` | 新增 | Mode/Config/ConfigFromEnv/ValidateBase/EffectiveSource + C2 Validate |
| `services/ani-gateway/internal/middleware/policy.go` | 新增 | ResolveAuthzPolicy 中间件（按 mode 分流） |
| `services/ani-gateway/internal/middleware/auth.go` | 修改 | AuthWithResolvedPolicy（generated 调 V2，legacy 调 ValidateToken） |
| `services/ani-gateway/internal/middleware/rbac.go` | 修改 | RBACWithResolvedPolicy（generated 调 CheckPermissionV2，legacy 调 CheckPermission） |
| `services/ani-gateway/internal/middleware/chain.go` | 修改 | B0 registerLegacyCompatibleChain → C registerChain + cfg.Validate(registry) |
| `services/ani-gateway/internal/middleware/generated_authz.go` | 新增 | AuthenticatePrincipal/AuthorizePrincipal V2 链路 |
| `services/ani-gateway/internal/middleware/pilot_test.go` | 新增 | quota-meta E2E 逐场景 V2/legacy RPC 调用次数断言 |
| `api/proto/auth/v1/auth_service.proto` | 修改 | additive V2 RPC：ValidatePrincipal/CheckPermissionV2 |
| `pkg/generated/pb/auth/v1/auth_service.pb.go` | 生成 | V2 message + gRPC stub |
| `services/auth-service/internal/service/permissions.go` | 新增 | 权威 permission store/evaluator |
| `services/auth-service/internal/service/jwt.go` | 修改 | principal_kind/credential_domain/aud=ani-core |
| `services/auth-service/internal/service/auth_service.go` | 修改 | ValidatePrincipal/CheckPermissionV2 handler + permissions 字段 |
| `api/openapi/v1.yaml` | 修改 | listQuotaMeta 增加 security + x-ani-authz（C1，9 行） |
| `architecture/component-import-allowlist.yaml` | 修改 | auth_service.go 加 pgx；permissions.go 新增条目 |
| `deploy/real-k8s-lab/sprint13-production-shaped-gateway-deployment.yaml` | 修改 | pilot mode + allowlist env |

## Commit 清单

| commit | 批次 | 内容 |
|---|---|---|
| `7440445` | PR1 AUTHZ-POLICY-A | 从 Core OpenAPI 生成授权策略注册表（generator + 生成物 + Makefile + policy.go） |
| `e2eb502` | PR2 AUTHZ-COMPAT-B0 | 统一 Principal 与 identity key（默认 off，gateway 走旧校验链路） |
| `65f00f3` | PR3 AUTHZ-CONTRACT-B1 | V2 授权契约：additive proto + ValidatePrincipal/CheckPermissionV2 + auth-service JWT/API Key principal + permission evaluator（gateway 仍 mode=off） |
| `ad83e41` | PR4 AUTHZ-PILOT-C | listQuotaMeta pilot：v1.yaml security 注解 + mode Validate + V2 授权链路 + E2E 测试 |
| `cfe5b30` | style | gofmt 格式化 PR1-PR4 涉及的 Go 文件 |

---

## Design Decisions

### 1. 四批次分阶段引入，每批次独立可编译

- **歧义**：V4 方案定义了四个批次但未明确每批次的编译边界。
- **选择**：每个 PR 必须独立编译通过；B0 不引用 B1 的 V2 方法，B1 不调用 C 的 Validate(registry)。
- **理由**：计划 1.4 冻结修正"运行时接口"项要求"四个 PR 必须能独立编译，B1 mode=off 的 V2 调用数必须为零"。

### 2. B0 中间态重建：chain.go/principal.go/mode.go/zz_generated 按批次递进

- **歧义**：迁移到目标分支时，源分支终态包含所有批次的最终代码，但 B0 commit 需要是"不含 B1/C 的中间态"。
- **选择**：
  - chain.go B0 版用 `registerLegacyCompatibleChain`（不调 `cfg.Validate(registry)`）
  - principal.go B0 版剥掉 `PrincipalFromProto`/`Proto`（B1）和 `authv1` import
  - mode.go B0 版剥掉 C2 段（`functionalMVPPilotOperations`/`sameOperationSet`/`Validate(registry)`）
  - zz_generated B0 版用基线 v1.yaml 重新生成（listQuotaMeta = legacy）
  - service_token_test.go B0 版恢复基线版（无 V2 stub）
  - policy_test.go（authz）B0 版把 C1 的 generated 断言改回 legacy
- **理由**：保持 commit 语义清晰，每个 commit 反映该批次的真实交付物。

### 3. LegacyPrincipalView 作为旧 TenantContext 的只读投影

- **歧义**：旧 `TenantContext` 没有 principal kind 和 API Key credential ID，如何安全过渡？
- **选择**：引入 `LegacyPrincipalView`，只保留 `CredentialScheme/TenantID/SubjectID/Scope/Roles/SandboxClaims`，不能伪造完整 Principal。
- **理由**：计划 1.4"B0 API Key"项要求"旧 TenantContext 没有 API Key ID，禁止伪造或记录原始 key"。

### 4. identity key 按 credential scheme 分层

- **选择**：规范 Principal.IdentityKey() 按 kind/domain 生成（如 `tenant:{id}:api_key:{credential_id}`）；LegacyPrincipalView.IdentityKey() 保留 tenant 粒度（如 `tenant:{id}:api_key:legacy`）。
- **理由**：B0 没有 credential ID，保留 tenant 粒度；B1 得到 credential ID 后才切 per-key identity。

### 5. tenant pb.go 不迁移

- **选择**：`tenant_plan.pb.go`（+399）/ `tenant_admin_service.pb.go`（+348）不挪到目标分支。
- **理由**：这两个生成物的改动只涉及 tenant-plan 审计字段的 proto 重新生成（IdempotencyKey 注释 + 审计字段），与 authz 四批次无任何依赖关系。挪过去会把无关功能混入 authz PR，破坏批次语义。

---

## Deviations

### 1. auth_rpc_errors.go 未单独建文件

- **计划 5.1 交付物表**：列出 `repo/services/ani-gateway/internal/middleware/auth_rpc_errors.go` 新增。
- **实际实现**：43a8558 diff stat 无此文件；gRPC 到 HTTP 的错误映射（`writeAuthRPCError`）实际内联在 `auth_client.go` 中。
- **原因**：以源分支终态为准。`writeAuthRPCError` 作为 AuthClient 的私有 helper 放在 auth_client.go 更紧凑，不需要单独文件。

### 2. v1.yaml C1 改动手工插入而非整文件 checkout

- **计划 6.2**：修改 Core OpenAPI 的 `/admin/quota-meta` 增加 `x-ani-authz`。
- **实际实现**：v1.yaml 在源分支还包含预存改动（updateBranding/uploadBrandingLogo/cancelTask +81 行），整文件 checkout 会带入无关改动。因此只手工插入 C1 的 9 行（security + x-ani-authz + x-ani-rbac-scope）。
- **原因**：用户要求"不是你改的就不用挪过去"。

### 3. B0 中间态 chain.go 不调 cfg.Validate(registry)

- **计划 4.4**：B0 固化 `ValidateBase` 和 `Register error` 通道。
- **实际实现**：B0 的 `Register` 调用 `ConfigFromEnv()`（内含 `ValidateBase`），但 `registerLegacyCompatibleChain` 不调 `cfg.Validate(registry)`（C2 才引入）。
- **原因**：`Validate(registry)` 依赖 `functionalMVPPilotOperations`/`sameOperationSet`，这些是 C2 的内容。

---

## Tradeoffs

### 1. git show 重定向 vs git checkout 迁移

- **备选 A**：`git checkout 43a8558 -- <file>`（批量 checkout）
- **备选 B**：`git show 43a8558:<file> > <file>`（逐文件重定向）
- **选择**：B。因为 v1.yaml 等文件在源分支含预存改动，checkout 会带入。
- **代价**：需逐文件操作，大批量命令偶发 502 审查拒绝，拆成 4-6 文件小批执行。

### 2. zz_generated 用 python 直接生成 vs git show 取终态

- **B0 阶段**：用 `python generate_gateway_authz.py --input <基线v1.yaml> --output <file>` 重新生成（listQuotaMeta=legacy）。
- **C1 阶段**：直接取 43a8558 终态（listQuotaMeta=generated）。
- **选择**：B0 用 python 生成确保与基线 v1.yaml 一致；C1 取终态确保与源分支一致。
- **代价**：B0 生成后需 `gofmt -w` 处理格式。

---

## Open Questions

### 1. v1.yaml 预存改动（branding/cancelTask）如何处理

源分支 v1.yaml 含 `updateBranding`/`uploadBrandingLogo`/`cancelTask`（+81 行）预存改动，本次未迁移。这些改动是否需要在 `feat/gateway-authz-policy` 分支单独处理，由用户决定。

### 2. tenant pb.go 重新生成

`tenant_plan.pb.go`/`tenant_admin_service.pb.go` 的重新生成产物（+399/+348）不在 authz 范围。如果 tenant-plan 审计功能需要在这些分支上落地，应在单独 PR 中处理。

### 3. router/gpu_scheduling_resources.go 的 :id→:queue_id 改动

源分支含 `gpu_scheduling_resources.go` 的路由参数改名（`:id`→`:queue_id`，3 路由 + 3 c.Param），属于预存改动，本次未迁移。

---

## Verification Commands Run

```bash
# 每批次 commit 前验证
cd repo
go build ./services/ani-gateway/... ./services/auth-service/...
go test -count=1 ./services/ani-gateway/... ./services/auth-service/...

# gofmt 检查
gofmt -l repo/services/ani-gateway/ repo/services/auth-service/ repo/pkg/generated/pb/auth/

# 最终验证（四批次 + gofmt 全部完成后）
git diff --stat 0424d42 HEAD -- repo/  # 46 files changed, +7007/−563
go build ./services/...
go test -count=1 ./services/ani-gateway/... ./services/auth-service/...
```

## 完工标准达成

- [x] PR1：generator + 生成注册表 + drift 门禁，A 不改 quota-meta
- [x] PR2：Principal/identity key/mode=off，gateway 走旧链路
- [x] PR3：V2 proto + auth-service evaluator，gateway 仍 mode=off 不调 V2
- [x] PR4：listQuotaMeta pilot，v1.yaml security 注解 + Validate + V2 链路 + E2E
- [x] gofmt 全部通过
- [x] 全程未 git push
- [x] tenant pb.go / v1.yaml 预存改动 / gpu路由改动 不迁移
