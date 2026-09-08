# [BOSS] API 客户端 + 类型生成——coreClient.ts + core-schema.d.ts

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/boss/services/metering/spec-boss-metering-service.md`

## Description

创建 BOSS API 客户端 `src/api/coreClient.ts`（openapi-fetch, baseUrl `/api/v1`）、`src/api/auth.ts`（JWT Bearer 中间件）、`scripts/gen-core-schema.mjs`（从 v1.yaml 生成 core-schema.d.ts）。需包含 `/metering/usage/platform` 路径类型。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/api/`, `repo/frontends/boss/scripts/`

## Acceptance Criteria
- [ ] `coreClient.ts`: `createClient<paths>()`, baseUrl `/api/v1`
- [ ] `auth.ts`: setAuthToken + Bearer 中间件
- [ ] `gen-core-schema.mjs`: 从 `repo/api/openapi/v1.yaml` 生成 `core-schema.d.ts`
- [ ] `core-schema.d.ts` 包含 `/metering/usage/platform` 路径 + `MeteringUsageResponse` 类型
- [ ] `package.json` gen-api 脚本

## Dependencies
#1（v1.yaml 变更后类型生成才含平台路径）, #13（BOSS scaffold）

## Type
boss

## Priority
high

## Labels
boss

## Batch
TBD

## References
- SPEC: BOSS §2.4, §4.1, §4.2
