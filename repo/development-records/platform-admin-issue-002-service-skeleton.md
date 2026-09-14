# PLATFORM-ADMIN-ISSUE-02：platform-settings-service 服务骨架、接口/模型与 SDK

> **批次类型：** Feature batch（BOSS 平台运营账号功能流 Issue #2）
> **完成日期：** 2026-08-31（本地未提交：`services/platform-settings-service/` 全部为工作区新增文件）
> **Scope：** `repo/services/platform-settings-service/`（新建）、`repo/pkg/ports/platform_user_admin.go`、`repo/pkg/adapters/postgres/platform_user_admin.go`、`repo/api/proto/platform_settings/v1/`、`repo/pkg/generated/pb/platform_settings/v1/`、`repo/go.work`
> **依赖：** #1（OpenAPI 契约，commit `7b5a68d`）
> **Product line：** core + services
> **本地 issue：** `repo/services/tasks/modules/issue/boss/settings/platform-admin/issue-002-interfaces-sdk-gen.md`

## 交付内容

新建 Services 层微服务 **platform-settings-service**（平台设置通用服务），本批承载平台运营账号模块。全部为 501 骨架，不含业务逻辑。

### 1. 服务骨架（对齐 tenant-service 建服模式）

| 文件 | 内容 |
|---|---|
| [main.go](file:///d:/Jczn/project/ANI/ANI/repo/services/platform-settings-service/main.go) | `config.Load` → `bootstrap.MustConnect` → `NewPlatformAdminService` → `bootstrap.RunGRPC`（注册 PlatformAdminService），19 行 |
| [go.mod](file:///d:/Jczn/project/ANI/ANI/repo/services/platform-settings-service/go.mod) | module `github.com/kubercloud/ani/services/platform-settings-service`，go 1.25.0，replace `services/pkg` / `ani/pkg` / `ani-sdks/core-go` 三条（与 tenant-service 同模式） |
| [internal/config/config.go](file:///d:/Jczn/project/ANI/ANI/repo/services/platform-settings-service/internal/config/config.go) | `GRPC_PORT=9106` / `HEALTH_PORT=9206` / `ServiceName: platform-settings-service`，返回 `bootstrap.Config` |
| [Dockerfile](file:///d:/Jczn/project/ANI/ANI/repo/services/platform-settings-service/Dockerfile) | golang:1.25.13-alpine 两段构建（`-tags stdjson -ldflags "-s -w"`），非 root 用户 65532，`EXPOSE 9106 9206` |
| [go.work](file:///d:/Jczn/project/ANI/ANI/repo/go.work) | 追加 `./services/platform-settings-service` |

### 2. Core 端口接口与数据模型（pkg/ports）

[platform_user_admin.go](file:///d:/Jczn/project/ANI/ANI/repo/pkg/ports/platform_user_admin.go)：
- `PlatformUserAdminStore` 接口 9 方法：Create / List / Get / ChangeRole / ResetPassword / SetStatus / SoftDelete / CountActivePlatformAdmins / **ListPlatformRoles**
- 5 结构体：`PlatformUser`（9 字段，DisplayName/LastLoginAt 可空）、`PlatformUserCreate`（PasswordHash 由网关 bcrypt，Store 不哈希）、`PlatformUserFilter`（cursor/role/status/source/search）、`PlatformUserListResult`、`PlatformRole`（Permissions `map[string]string` 四维）
- 接口文档注释声明约束：平台旁路 RLS（WithPlatformTx）、永不设 tenant_id、username 前缀 Store 负责 prepend `local:`

[adapters/postgres/platform_user_admin.go](file:///d:/Jczn/project/ANI/ANI/repo/pkg/adapters/postgres/platform_user_admin.go)：`PostgresPlatformUserAdminStore` 9 方法全部返回 `ports.ErrNotImplemented`，接口断言 `var _ ports.PlatformUserAdminStore` 保证签名同步。

### 3. gRPC proto 与生成物

[platform_admin_service.proto](file:///d:/Jczn/project/ANI/ANI/repo/api/proto/platform_settings/v1/platform_admin_service.proto)：
- package `platform_settings.v1`，go_package `github.com/kubercloud/ani/pkg/generated/pb/platform_settings/v1;platformsettingsv1`
- `PlatformAdminService` 10 RPC 与 OpenAPI issue-001 的 10 个 Services 端点一一对应
- 复用 `common/v1.IdempotentResult`（5 个写 RPC）与 `common/v1.CursorPageRequest`（2 个列表 RPC），不重复定义
- `PlatformRolePermissions` 用 4 个独立 string 字段（tenant_ops/resource_pool/platform_user/audit_export → read/write/none）而非 `map<string,string>`——proto3 map 无序，4 字段契约可读且字段级校验
- 生成物 [platform_admin_service.pb.go](file:///d:/Jczn/project/ANI/ANI/repo/pkg/generated/pb/platform_settings/v1/platform_admin_service.pb.go) + grpc.pb.go 就位（10 RPC server interface + Unimplemented embed）

### 4. Services 内部端口与 adapter 骨架

- [ports/platform_admin_store.go](file:///d:/Jczn/project/ANI/ANI/repo/services/platform-settings-service/internal/repo/ports/platform_admin_store.go)：审计 store（CreateAudit / ListAuditLogs）+ 4 领域模型（AuditCreateInput 含 action 枚举注释 / AuditLogFilter / AuditLogListItem / AuditLogListResult）。注释硬约束「**仅操作 audit_logs，不得触碰 users/roles/user_roles**」
- [ports/core_platform_user.go](file:///d:/Jczn/project/ANI/ANI/repo/services/platform-settings-service/internal/repo/ports/core_platform_user.go)：`CorePlatformUserClient` 8 方法（含 ListPlatformRoles）+ DTO 6 个
- [ports/errors.go](file:///d:/Jczn/project/ANI/ANI/repo/services/platform-settings-service/internal/repo/ports/errors.go)：服务级 `ErrNotImplemented = errors.New("NOT_IMPLEMENTED")`
- [adapters/core/platform_user_client.go](file:///d:/Jczn/project/ANI/ANI/repo/services/platform-settings-service/internal/repo/adapters/core/platform_user_client.go) + [adapters/postgres/platform_admin_store.go](file:///d:/Jczn/project/ANI/ANI/repo/services/platform-settings-service/internal/repo/adapters/postgres/platform_admin_store.go)：全部 501 占位，接口断言就位

### 5. gRPC server 骨架

[platform_admin_service.go](file:///d:/Jczn/project/ANI/ANI/repo/services/platform-settings-service/internal/service/platform_admin_service.go)：`PlatformAdminService` embed `UnimplementedPlatformAdminServiceServer`，10 RPC 全部 `status.Error(codes.Unimplemented, ...)`，`Register` 方法挂到 grpc.Server。

## Design Decisions

1. **Services 承载服务选择（本流最大决策）**：平台运营账号放新建 `platform-settings-service` 而非 tenant-service。理由：tenant-service 语义是「租户管理员/租户域」，平台账号 `tenant_id IS NULL` 与其 RLS 心智相反；且 SPEC 决策表将「平台设置」定位为通用域（后续平台级配置项可复用），避免 tenant-service 无限膨胀。代价是多一个服务的部署/运维面（9106/9206 需占用、Dockerfile、go.work 条目）。
2. **双 ErrNotImplemented**：`pkg/ports/errors.go` 加 `ErrNotImplemented = errors.New("501 Not Implemented")`（Core 侧 adapter 用），同时 platform-settings-service 内部 `ports/errors.go` 另有 `ErrNotImplemented = errors.New("NOT_IMPLEMENTED")`（gRPC 层转 `codes.Unimplemented` 用）。理由：两层错误域不同（Core 501 语义 vs gRPC code 语义），服务内不依赖 `pkg/ports` 的错误定义可减少将来语义耦合。代价：同名变量两处维护，将来实现期可能需要统一。
3. **proto 权限矩阵用 4 字段而非 map**：`PlatformRolePermissions{tenant_ops, resource_pool, platform_user, audit_export}` 每维度取值 read/write/none。理由：proto3 map 无法表达「四维固定键」的文档语义，字段级定义让 buf/protoc 可校验存在性，gRPC 消费方不用做 map key 检查。与 OpenAPI `PlatformRole.permissions`（对象）形状一致，仅序列化形态不同。
4. **写 RPC 复用 common.v1.IdempotentResult**：不另建 `PlatformAdminMutationResult`。理由：与 tenant_admin_service proto 先例一致（幂等键回落/复用语义完全相同），减少 pb 面。
5. **main.go 先 `MustConnect` 再建 service**：deps 由 bootstrap 统一持有 DB 连接，`defer deps.Close()`；service 构造暂不注入依赖（全部 501），后续 issue 实现时改为 `NewPlatformAdminService(store, coreClient, auditStore)` 注入。骨架阶段保持构造函数零参，避免提前固化依赖形态。

## Deviations

| Spec / Issue | 实现 | 原因 |
|---|---|---|
| Issue AC「`make proto`（buf generate）生成 pb」 | pb 已生成到位（`platform_admin_service.pb.go` / `_grpc.pb.go`），但本次会话内 `buf lint` 因沙箱拒绝写 `C:\Users\...\AppData\Local\buf\v3\wasmruntime` 无法复跑 | 生成物已存在且 `go vet` 通过；buf 工具链问题与代码正确性无关，可在非沙箱环境复验 |
| Issue AC「`make gen-core-sdk` 重新生成，**新增 PlatformUser 相关方法**」 | anisdk 已含 8 个 PlatformUser 方法（`createPlatformUser` 等，issue-001 commit `7b5a68d` 已交付），本批**无增量** | SDK 生成物在 issue-001 已随契约一并交付（两 issue 实际合并交付）；本批仅消费现成 SDK，AC 中「重新生成」无新内容可刷 |
| Issue AC「`make test` 通过」 | `go build ./pkg/ports/... ./pkg/adapters/... ./services/platform-settings-service/...` + `go vet` + `go test`（相关包）全 PASS。期间发现并已修复：初版 `PlatformUser` 与 [password_login.go:46](file:///d:/Jczn/project/ANI/ANI/repo/pkg/ports/password_login.go#L46) 同名冲突（`pkg/ports` 编译失败），**已改名 `PlatformUserAdmin`**（含 Create/Get/List Result Items 引用，共 5 处），编译恢复 | 冲突为真实阻塞缺陷，按用户拍板采用「改新 struct 名」方案（与 store/文件名 `platform_user_admin` 对齐，不动已上线密码登录代码）；`pkg/adapters/runtime` 的 symlink 测试失败为沙箱环境问题，与本流无关 |
| SPEC §3.2.2「`CorePlatformUserClient` 8 方法（含 ListPlatformRoles）」 | 与 SPEC 一致；但 `PlatformUserCreateInput` 含明文 `Password`（Core 端口 `PlatformUserCreate` 含 `PasswordHash`） | 分层职责：网关 bcrypt → Core handler 收明文转 hash → Services 层经 Core SDK HTTP 传明文（TLS 内网）→ Core 端口已是 hash。与 SPEC §3.2.1「passwordHash is pre-computed by the caller (gateway bcrypt)」逐层语义一致，非笔误 |
| Issue「adapter/handler 返回 501 占位」 | Core 侧 postgres adapter 返回 `errors.New("501 Not Implemented")`；Services 侧 gRPC 返回 `codes.Unimplemented`（HTTP 语义 501 对应） | 两层占位错误域不同（Design Decision 2），行为均符合「占位不实现」意图 |

## Tradeoffs

- **新建服务 vs 复用 tenant-service**：新建胜出（语义纯净 + 平台设置域可扩展），代价是部署面 +1。备选方案 tenant-service 加路由前缀曾被 SPEC 早期采用，用户明确否决（2026-08-31 修订）。
- **端口 9106/9206 vs 复用其他端口段**：9101/9103/9104/9105 已占用（tenant/inference 等），9106 顺延无冲突；代价是 `deploy/` 侧将来要补端口分配文档。
- **骨架期构造函数零参 vs 提前注入依赖**：选零参。备选提前定义 `NewPlatformAdminService(deps...)` 签名可让后续 issue 不改 main.go，但依赖形态（audit store + core client + 幂等存储）在后续 issue 才定型，提前固化反而返工。
- **ListPlatformRoles 放 Core 端口 + Core SDK client 双侧**：Core 侧 `PlatformUserAdminStore.ListPlatformRoles`（直查 roles 表，issue-007 供 Core HTTP 端点用）与 Services 侧 `CorePlatformUserClient.ListPlatformRoles`（经 Core SDK 调 HTTP）。两条链路并存因为 roles 数据的 owner 是 Core（users/roles/user_roles 三表归 Core 管），Services 不得绕过 Core 直查——遵守 SPEC §12「Core 拥有用户/角色数据」边界。

## Open Questions

1. ~~**【已解决】`PlatformUser` 同名冲突**~~：与 [password_login.go:46](file:///d:/Jczn/project/ANI/ANI/repo/pkg/ports/password_login.go#L46) 冲突（平台密码登录既有 struct，仅 ID/PasswordHash/Status 三字段）。2026-08-31 已按用户拍板改新 struct 名为 `PlatformUserAdmin`（[platform_user_admin.go](file:///d:/Jczn/project/ANI/ANI/repo/pkg/ports/platform_user_admin.go)，5 处引用同步），`go build` / `go vet` / `go test` 相关包全部恢复 PASS。
2. **`buf lint` 沙箱不可用**：proto 生成物已存在，但 lint/格式（`buf format`）未复验。建议在非沙箱终端跑 `make proto && buf lint api/proto` 确认无 STYLE 告警。
3. **issue-001/002 交付边界实际合并**：SDK 生成与契约在同一 commit `7b5a68d` 交付，本批无 SDK 增量。若后续 `/review-it` 或 `/ship-it` 按 issue 独立验收，需在 PR 描述说明「SDK 部分 see 7b5a68d」。
4. **`go.work` 未含本服务时 IDE 可能报错**：go.work 已加条目（本批完成）；但 Dockerfile 构建依赖 `go.work.sum` 同步——若 go.work.sum 缺新条目，CI 镜像构建会失败。建议 `go work sync` 后提交。
5. **部署侧未动**：`deploy/`（compose/k8s/端口分配/PLATFORM_SETTINGS_SERVICE_ADDR 环境变量）不在本 issue Scope，issue-005 网关接入时统一补。9106/9206 端口是否需要在某处集中登记，值得确认。

## Verification

```bash
cd repo
go build ./pkg/ports/... ./pkg/adapters/... ./services/platform-settings-service/...   # PASS（冲突已修，PlatformUserAdmin）
go vet ./pkg/ports/... ./pkg/adapters/postgres/... ./services/platform-settings-service/...  # PASS
go test ./pkg/ports/... ./pkg/adapters/... ./services/platform-settings-service/... ./services/auth-service/...  # PASS
#   （pkg/adapters/runtime symlink 测试在沙箱内必挂，属环境问题，与本流无关，已排除）
# 文件核对：
#   services/platform-settings-service/ 全 12 文件就位（main/go.mod/go.sum/Dockerfile/config + ports×3 + adapters×2 + service×1）
#   pkg/generated/pb/platform_settings/v1/ 2 文件就位（含 10 RPC Server interface）
#   pkg/ports/platform_user_admin.go 9 方法 + 5 struct 对齐 SPEC §3.2.1（struct 现名 PlatformUserAdmin）
#   go.work 追加 ./services/platform-settings-service
#   未触碰 services/tenant-service/（git status 无该路径变更）
# buf lint api/proto  → 沙箱拒绝（wasmruntime 目录不可写），需非沙箱环境复验
```

## 边界声明

- 本批次仅骨架：所有方法（Core adapter / Services adapter / gRPC RPC）均 501 / `codes.Unimplemented` 占位，无 SQL、无 HTTP 调用、无幂等逻辑。
- Core handler（HTTP 端点实现）属 issue-004；ListPlatformRoles 具体实现属 issue-007；网关接入属 issue-005。
- 不声明 runtime ready 或 production ready；同名冲突已修复（2026-08-31，`PlatformUserAdmin`），相关包 build/vet/test 全 PASS，issue-002 骨架交付完整。
