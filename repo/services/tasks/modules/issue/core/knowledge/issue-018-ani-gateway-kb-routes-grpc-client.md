# ani-gateway KB 路由与 gRPC client

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-016)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-ani-gateway-kb.md` (§2, §4, §5)

## Description
作为网关开发者，我需要补齐 ani-gateway 的 KB 路由并用 gRPC client 替换 stub handler。

## Scope
- Product line: core (Services / ani-gateway)
- Code paths allowed: `repo/services/ani-gateway/` only

## Acceptance Criteria
- [ ] [SPEC] 补齐 3 个缺失路由（citations/sessions/permissions），12 端点全部就位（SPEC §4.1 端点表）
- [ ] [SPEC] 用 gRPC client 替换 9 个 stub handler（SPEC §2.2 kb_resources.go MODIFY）
- [ ] [SPEC] `/api/v1/svc/knowledge-bases/*` 路由到 kb-service（gRPC，SPEC §4.1）
- [ ] [SPEC] `/api/v1/vector-stores/*` 路由到 Core vector-store（SPEC §4.1 Core 代理表）
- [ ] [SPEC] `/api/v1/objects/*` 路由到 Core object-store（SPEC §4.1 Core 代理表）
- [ ] [SPEC] RBAC + 租户注入 + 限流生效（SPEC §2.3, §7.1）
- [ ] `make validate-architecture` 通过
- [ ] `make test` 通过

## Dependencies
#6 (kb-service gRPC server) — per SPEC §10.2 (US-016 depends on spec-services-kb-service US-008).

## Type
core (services)

## Priority
high

## Labels
core, services

## Batch
M2.1-TASK-B

## References
- SPEC: spec-services-ani-gateway-kb §2.2, §2.4, §4.1, §5.1
