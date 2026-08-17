# Issue 003: 新建 metering-service go.mod 和 config

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/metering/prd-metering-consumer.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/metering/spec-metering-consumer.md`

## Description

新建 metering-service 独立 module 的 go.mod（补 pgx/v5 v5.9.2 及 pgxpool 依赖）和 config 加载模块。PR-M1 的 meteringCollectionService 实现需要 pgx 依赖才能编译，go.mod 必须在 PR-M1 就建立。

## Scope
- Product line: core
- Code paths allowed: `repo/services/metering-service/`

## Acceptance Criteria
- [ ] 新增 `services/metering-service/go.mod`（独立 module，module name: `github.com/kubercloud/ani/services/metering-service`）
- [ ] go.mod 补 `github.com/jackc/pgx/v5 v5.9.2` 及 `pgxpool` 依赖（版本与 auth-service go.mod 一致）
- [ ] go.mod 通过 `replace github.com/kubercloud/ani/pkg => ../../pkg` 引用 monorepo 共享 pkg 模块
- [ ] 新增 `services/metering-service/internal/config/config.go`
- [ ] config.go 嵌入 `bootstrap.Config` 获得公共字段（DatabaseURL/NATSURL/GRPCPort/HealthPort/ServiceName）
- [ ] config.go 加载环境变量：`DATABASE_URL`、`NATS_URL`、`METERING_PROMETHEUS_URL`、`METERING_COLLECTION_INTERVAL_SECONDS`（默认 60）、`HEALTH_PORT`
- [ ] Typecheck/lint 通过

## Dependencies
- Issue #002（port 接口先就位）

## Type
core

## Priority
high

## Labels
core

## Batch
PR-M1

## SPEC Reference
- SPEC §2.4 File Structure
- SPEC §11.3 Assumptions（pgx v5.9.2）
- PRD US-003 AC: go.mod 项
- PRD US-006 AC: config 项
