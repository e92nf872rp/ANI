# TENANT-BILLING-B — 租户计费操作历史（抽屉「操作历史」Tab）

完成日期：2026-09-08
对应 Sprint：Services 受控并行 PR（Core Sprint 13/14 既有事实继续有效）
前置：TENANT-BILLING-A（5 端点计费结算）已合入本分支；本批次为同一 PR 范围内的功能追加批次（kjs-study 文档中又称 OPERATION-HISTORY 批次）。
验证结果：契约类校验（services boundary / YAML 结构 / 语义契约 / 路由契约 / spec-split / openapi_spec_validator）全绿；单测 tenant-service（service+core）与 gateway（router+authz）全过；SDK/docs 生成物幂等零漂移；混合联调矩阵 13 用例（e30–e43）全部通过（本地 gateway/tenant-service 进程 × 测试环境 PG/Redis/auth-service，真实鉴权 401 与 operator 透传经环境库复核）。Windows 本机 `make` 聚合入口存在环境性缺陷（子 make 路径含空格、bash echo/date 不兼容），按约定直跑各底层校验脚本，`make validate-services` 聚合复核由人工补跑。未部署 K8s，不标 live/runtime ready。

## 实现了什么

在 TENANT-BILLING-A 基础上追加第 6 个只读端点 `GET /api/v1/svc/billing/operations`（抽屉「操作历史」Tab 数据源），并新建 `billing_operation_logs` 同事务操作流水：

- **契约先行**：`services/v1.yaml` 新增端点 + `BillingOperationLog`/`BillingOperationsResponse` schema + x-ani-authz（resource=billing，action=read，boundary=platform，principal_kinds=[user]）；proto 加 `ListBillingOperations` RPC；pb/Services SDK 四语言/docs/api 重生成（幂等零漂移）。
- **同事务流水**：新表 `billing_operation_logs`（迁移 `20260908_001_billing_operation_logs.sql` + atlas.sum），`tenant_id/period/action/ref_id/message/operator/created_at`；生成账单/结清/授信冲抵/调账 4 个写点与业务写在**同一事务**落流水——业务写回滚则流水不落；**幂等重放与 409 冲突不产生流水**（混合联调 e34/e37/e38 实测锁定）。
- **读接口**：`tenant_id` 必填（缺失/非 UUID → 400 `VALIDATION_FAILED`）、`limit` 默认 50 上限 200（钳制）、`offset` 默认 0；`created_at` 倒序分页；无足迹租户返回空列表不 404（与 overview 对齐）；`operator` 透传网关 token user_id（经环境库与登录账号复核一致）。
- **网关**：`/billing/operations` 路由注册 + optional（period/ref_id）null 映射 + 错误结构透传；fake gRPC 客户端补齐新 RPC。
- **契约缺陷修复**：`services/v1.yaml` `BillingInvoiceSummary` 的裸 `no`（required 项与属性名）被 YAML 1.1 解析为布尔 `False`，严格校验失败；加引号 `'no'` 修复，JSON 字段名不变。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/services/v1.yaml` | 修改 | `/billing/operations` 端点 + 2 schema + x-ani-authz；`no` 引号修复 |
| `api/proto/tenant/v1/billing.proto` | 修改 | `ListBillingOperations` RPC + `BillingOperationLog`/`ListBillingOperationsRequest/Response` 消息 |
| `pkg/generated/pb/tenant/v1/billing.pb.go`、`billing_grpc.pb.go` | 修改 | proto 生成物 |
| `deploy/migrations/20260908_001_billing_operation_logs.sql` + `atlas.sum` | 新增 | 流水表 + 索引 + ani_app_user 授权；已在测试环境 PG Pod 内执行落表 |
| `services/tenant-service/internal/repo/ports/billing.go` | 修改 | 流水写入/查询 port + `BillingOperationLogInput` + action 常量 |
| `services/tenant-service/internal/repo/adapters/postgres/billing_store.go` | 修改 | 4 写点同事务插流水（`QueryRow ... RETURNING id`，不经 pgconn）+ `ListOperations` 分页查询 |
| `services/tenant-service/internal/service/billing_svc.go` | 修改 | 4 写点构造流水输入 + `ListBillingOperations` 实现（limit/offset 钳制） |
| `services/tenant-service/internal/service/billing_svc_test.go` | 修改 | +4 单测（3 写点同事务落流水 + 分页/校验） |
| `services/tenant-service/internal/service/billing_calc.go`、`billing_calc_test.go` | 修改 | gofmt 对齐 |
| `services/ani-gateway/internal/router/billing_resources.go` | 修改 | `/billing/operations` 路由 + handler + `billingOperationJSON`（optional null） |
| `services/ani-gateway/internal/router/billing_resources_test.go` | 修改 | +2 单测（handler 映射/错误映射）+ fake 客户端补 `ListBillingOperations` |
| `sdks/services/**`、`docs/api/services.html`、`index.html` | 修改 | 生成物重生成（幂等零漂移） |

## 验证闭环

- 单测：svc 25 个 billing 用例（+4）+ gateway 11 个 billing 用例（+2）全 PASS。
- 混合联调（本地进程 × 环境 PG 30945/Redis 30453/auth-service 30091，`ANI_AUTH_MODE=auth_service`）：e30 无凭证 401、e31 参数 400、e40 空列表、e33–e37 流水同事务/幂等/409 语义、e39 分页钳制、e41 operator 透传（与环境库 `local:root` user_id 一致）、e42 overview / e43 CSV 导出合并后全接口冒烟，13/13 PASS；矩阵脚本与证据存 `repo/.tmp/`（不入库）。测试数据已清零（流水/账单/调账 0|0|0，credit 复位 104.6）。
- 合并 main（56a5f0b，含 #148 组件状态 / #150 KB API / #152 IAM 隔离）后全量回归通过：契约校验、单测、生成物幂等、混合联调 13/13。
- 门禁：批次内直跑底层脚本全绿；`make` 聚合入口因 Windows 本机环境缺陷未跑（子 make 路径含空格），由人工补跑复核。

## 备注

- main #152（ANI-IAM-PRE930-CONTAINMENT）已移除 dp2 operation-registry 机制：TENANT-BILLING-A 时代记录的 `validate-gateway-authz` operation-registry 漂移 main 既有问题**已不复存在**。
- 与实施方案《租户计费操作历史-实施方案.md》的偏差：端点形态为 `GET /billing/operations?tenant_id=...`（query 参数，对齐既有端点风格），未做 `period/action` 过滤（原型无对应 UI 入口）；详见 `kjs-study/租户与计费用量/implementation-diff-tenant-billing.md` D12/D13。
- 操作历史无存量追溯：上线前已发生的计费动作不补记流水（billing 为新域未上生产，实际无影响）；operator 为 user_id，显示名快照挂后续。
- 测试报告：`kjs-study/租户与计费用量/tenant-billing-test-report.md`（门禁层补充节 + 操作历史补充矩阵）；前端接口文档：`kjs-study/租户与计费用量/tenant-billing-api.md`（3.6 节）。
