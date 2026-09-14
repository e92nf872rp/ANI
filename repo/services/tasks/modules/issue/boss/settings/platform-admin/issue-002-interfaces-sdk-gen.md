# 平台设置服务新建：接口、数据模型与 SDK 生成

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/settings/prd-new-boss-platform-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/settings/ux-new-boss-platform-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/settings/spec-new-boss-platform-admin.md`

## Description
**新建 Services 层微服务 `platform-settings-service`**（平台设置通用服务，Go module `github.com/kubercloud/ani/services/platform-settings-service`，gRPC 端口 9106 / health 9206），本批承载平台运营账号模块。搭服务骨架（main.go / config / Dockerfile / go.mod，对齐 tenant-service 建服模式）；设计 Core 端口接口与数据模型（`PlatformUserAdminStore`）；定义 `platform_settings/v1` gRPC proto、审计 store 接口、Core SDK client 接口与领域模型；运行 `make gen-core-sdk` 重新生成 anisdk.Client。本 Issue 只产出服务骨架 + 接口/模型设计与 SDK 生成，不实现具体业务逻辑（adapter/handler 返回 501 占位）。

## Scope
- Product line: core + services
- Code paths allowed: `repo/services/platform-settings-service/`（新建，全部）、`repo/pkg/ports/`、`repo/api/proto/platform_settings/v1/`、`repo/pkg/generated/pb/platform_settings/v1/`、`repo/pkg/adapters/postgres/platform_user_admin.go`（骨架占位）、`repo/sdks/core/`（SDK 重新生成）
- 禁止：触碰 `repo/services/tenant-service/`（平台运营账号不进租户服务）

## Acceptance Criteria
- [ ] 新建服务骨架：`repo/services/platform-settings-service/` 下 `main.go`（bootstrap.MustConnect + bootstrap.RunGRPC，注册 PlatformAdminService）、`go.mod`（module `github.com/kubercloud/ani/services/platform-settings-service`，replace `services/pkg` 与 core SDK 同 tenant-service 模式）、`Dockerfile`、`internal/config/config.go`（`GRPC_PORT=9106` / `HEALTH_PORT=9206` / `ServiceName: platform-settings-service`）
- [ ] `repo/pkg/ports/platform_user_admin.go` 定义 `PlatformUserAdminStore` 接口（Create / List / Get / ChangeRole / ResetPassword / SetStatus / SoftDelete / CountActivePlatformAdmins / **ListPlatformRoles**）与结构体（PlatformUser / PlatformUserCreate / PlatformUserFilter / PlatformUserListResult / **PlatformRole**），对齐 SPEC §3.2.1
- [ ] `repo/pkg/adapters/postgres/platform_user_admin.go` 提供方法签名占位，返回 `501 Not Implemented`
- [ ] `repo/api/proto/platform_settings/v1/platform_admin_service.proto` 定义 PlatformAdminService 全部 RPC（对应 SPEC §4.2 十个 Services 端点）与消息类型（package `platform_settings.v1`，go_package `.../pkg/generated/pb/platform_settings/v1;platformsettingsv1`）
- [ ] `make proto`（buf generate）生成 `repo/pkg/generated/pb/platform_settings/v1/` pb.go / grpc.pb.go
- [ ] `repo/services/platform-settings-service/internal/repo/ports/platform_admin_store.go` 定义审计 store 接口（CreateAudit / ListAuditLogs）与领域模型（AuditCreateInput / AuditLogFilter / AuditLogListItem / AuditLogListResult），对齐 SPEC §3.2.2。**Store 仅操作 audit_logs，不操作 users/roles/user_roles**
- [ ] `repo/services/platform-settings-service/internal/repo/ports/core_platform_user.go` 定义 `CorePlatformUserClient` 接口（Create / List / Get / ChangeRole / ResetPassword / SetStatus / SoftDelete / **ListPlatformRoles**），对齐 SPEC §3.2.2
- [ ] `repo/services/platform-settings-service/internal/repo/adapters/core/platform_user_client.go` 提供 SDK client 骨架（方法签名占位）
- [ ] `repo/services/platform-settings-service/internal/repo/adapters/postgres/platform_admin_store.go` 提供方法签名占位，返回 `501 Not Implemented`
- [ ] `repo/services/platform-settings-service/internal/service/platform_admin_service.go` 定义 gRPC server 骨架，RPC 方法返回 501
- [ ] 运行 `make gen-core-sdk` 重新生成 `repo/sdks/core/go/anisdk/`，新增 PlatformUser 相关方法
- [ ] `make test` 通过（含编译 + 现有测试）
- [ ] PR 描述含服务骨架说明 + 接口签名摘要 + SDK 变更说明

## Dependencies
#1 (OpenAPI 契约)

## Type
backend

## Priority
high

## References
- SPEC: §2.3 Component Design / §2.5 File Structure / §3.2 Entity Definitions / §12 ANI Boundaries（Services 承载）；ListPlatformRoles 实现见 #7
