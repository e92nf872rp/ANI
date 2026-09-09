# TENANT-BILLING-A — BOSS 租户计费结算接口

完成日期：2026-09-08
对应 Sprint：Services 受控并行 PR（Core Sprint 13/14 既有事实继续有效）
验证结果：`make validate-services` EXIT:0（✅ Services PR gate valid）；`make validate-architecture` EXIT:0（✅ architecture guardrails valid）；`git diff --check` 干净；`go test ./services/tenant-service/...` 全过（其中 billing service 21 用例、metering client 4 用例）；`go test ./services/ani-gateway/internal/router/ -run TestBilling` 9 用例全过。本地集成实测 21 用例全过（见测试报告）。`make test` 中 `validate-gateway-authz` 的 `operation-registry.v1.json` 生成物漂移为 main 既有问题，与本分支无关（本分支未触碰 Core API）。

## 实现了什么

Services 层新增 BOSS 租户计费结算 5 个 REST 端点（`/api/v1/svc/billing/*`）：账务总览（含余额推导与 overdue 动态计算）、CSV 导出、生成账单（幂等、一期一单、生成即出账）、账单动作（settle/credit 状态机）、调账（正/负金额）。契约在 `services/v1.yaml`，实现在 tenant-service（gRPC），网关 mixed handler 转发；用量数据经 Core SDK 调 `GET /api/v1/metering/usage/platform`，单价只读 `billing_pricing` 表（种子值随迁移 `ON CONFLICT DO NOTHING` 落库），代码零价格字面量。本次仅本地验证（测试环境占用，未部署 K8s），不标 live/runtime ready。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/services/v1.yaml` | 修改 | 5 个 `/billing/*` 端点 + 7 个 Billing schema + x-ani-authz（resource=billing，action=read/write/export，boundary=platform，principal_kinds=[user]） |
| `api/proto/tenant/v1/billing.proto` | 新增 | BillingService 5 个 RPC（GetBillingOverview/ExportBillingOverview/GenerateInvoice/InvoiceAction/CreateAdjustment）+ 消息体 |
| `pkg/generated/pb/tenant/v1/billing.pb.go`、`billing_grpc.pb.go` | 新增 | `make gen-proto` 生成物 |
| `sdks/services/**`（go/java/python/typescript + sdk-metadata） | 修改 | Services SDK 随契约重生成，零漂移 |
| `docs/api/services.html`、`index.html` | 修改 | 静态 API 文档重生成 |
| `services/tenant-service/internal/repo/ports/billing.go` | 新增 | 计费域 port（invoice/adjustment/credit/pricing store 接口 + 领域错误哨兵） |
| `services/tenant-service/internal/repo/adapters/postgres/billing_store.go` | 新增 | 4 表 CRUD、状态机迁移、幂等 unique-violation 映射 |
| `services/tenant-service/internal/repo/adapters/core/metering_client.go` | 新增 | 经 Core SDK 调 `/metering/usage/platform` 平台聚合（月窗口 + group_by=tenant_id） |
| `services/tenant-service/internal/repo/adapters/core/sdk_client.go` | 修改 | 补 `float64Field` 解析助手 |
| `services/tenant-service/internal/service/billing_svc.go` | 新增 | 5 RPC 业务实现：幂等重放、一期一单冲突、账单号 seq、到期日 +30 天、状态机、调账 |
| `services/tenant-service/internal/service/billing_calc.go` | 新增 | 成本折算（gpu/cpu/memory ÷3600×单价、tokens ×单价）、usage breakdown、余额推导（§6.3 口径）、overdue 动态计算、CSV 渲染 |
| `services/tenant-service/internal/service/errors.go` | 修改 | 8 个 `BILLING_*` 领域错误映射到 gRPC 码 |
| `services/tenant-service/main.go` | 修改 | billing store + metering client + service 装配与 Register |
| `services/ani-gateway/internal/router/billing_resources.go` | 新增 | REST→gRPC 代理 handler，错误结构透传，幂等键 body/`Idempotency-Key` 头双通道 |
| `services/ani-gateway/internal/router/billing_resources_test.go` | 新增 | 9 个 handler 单测 |
| `services/ani-gateway/internal/router/router.go` | 修改 | billing 路由注册 |
| `deploy/migrations/20260907_001_tenant_billing.sql` | 新增 | 4 表（billing_invoices/billing_adjustments/billing_credit_accounts/billing_pricing）+ pricing 6 行种子（ON CONFLICT DO NOTHING） |
| `deploy/migrations/atlas.sum` | 修改 | atlas 重算校验和 |
| `architecture/component-import-allowlist.yaml` | 修改 | billing_store.go pgx bounded_direct 入 allowlist（对齐 tenant admin 先例） |

## 单测覆盖

- service 层（21 个 billing 用例）：成本折算换算系数（metered 全量/无用量/未定价）、状态 derive、余额推导、账单号与到期日、CSV 渲染、generate 成功/幂等重放/参数校验/冲突重试、状态机（settle/credit/二次动作）、调账（含幂等冲突与重放）、overview（空态/仅用量/overdue+余额/状态过滤/租户过滤排序/period 校验+Core 不可用）、导出
- core client 层（4 个 metering 用例）：平台聚合成功/服务不可用/缺 items/租户过滤（httptest mock）
- gateway handler 层（9 个用例）：JSON 映射、generate 200、409 透传、nil client 守卫、query 透传、CSV 响应、action/adjustment

## 完工标准达成

- [x] `make validate-services` EXIT:0（含 API split、route contract、语义契约、生成物零漂移、validate-architecture）
- [x] `make validate-architecture` EXIT:0
- [x] `git diff --check` 干净
- [x] 契约先行：services/v1.yaml → proto 生成物 → ports → store/service → gateway 注册顺序实施
- [x] 幂等：写接口 body `idempotency_key`（generate 另支持 `Idempotency-Key` 头回退）；`(tenant_id, period)` 唯一；同 key 重放返回原账单
- [x] 生成即出账（status=issued，无 draft）；overdue 读取时动态计算、不落库、无定时 worker
- [x] 单价零代码字面量；种子迁移重跑不回滚人工改价（本地实测验证）
- [x] 用量仅经 Core SDK，未直查 Core 库 `metering_usage_records`
- [x] 仅 token_total 计费；storage/kb/Token 落库前 `data_source=unavailable`、cost 返回 null（不伪造 0）
- [x] 本地集成实测 21 用例通过（本地便携版 PostgreSQL 15432 + tenant-service + gateway 双进程 + curl 矩阵，含幂等重放、409 冲突、状态机、余额推导、overdue 动态化、CSV、400 矩阵）

## 备注

- `make test` 中 `validate-gateway-authz` 报 `operation-registry.v1.json` 生成物漂移：main 既有问题（本分支未触碰 Core API 与该注册表），已在 main 复现确证，不阻塞本批次。
- 本地模式下 auth 走 dev/bypass，401/403 RBAC 边界以单测与契约（x-ani-authz）覆盖，真环境 RBAC 回归留待测试环境空闲后另行批次。
- Token 用量落库为外部团队 P2 范围；落库后 tokens 行自动出数，无需改本批代码。
- 遗留：真环境成本与平台聚合对账、欠费解冻联动（方案 §10.4 处置）、Services SDK Java/Python 消费方回归。
- 差异文档：`kjs-study/租户与计费用量/implementation-diff-tenant-billing.md`；测试报告：`kjs-study/租户与计费用量/tenant-billing-test-report.md`；前端接口文档：`kjs-study/租户与计费用量/tenant-billing-api.md`。
