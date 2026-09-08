# TENANT-BILLING-B — 租户计费操作历史（抽屉「操作历史」Tab）+ 账单软删除

完成日期：2026-09-08
对应 Sprint：Services 受控并行 PR（Core Sprint 13/14 既有事实继续有效）
前置：TENANT-BILLING-A（5 端点计费结算）已合入本分支；本批次为同一 PR 范围内的功能追加批次（kjs-study 文档中又称 OPERATION-HISTORY 批次）。
验证结果：契约类校验（services boundary / YAML 结构 / 语义契约 / 路由契约 / spec-split / openapi_spec_validator）全绿；单测 tenant-service（service+core）与 gateway（router+authz）全过；SDK/docs 生成物幂等零漂移；混合联调矩阵 13 用例（e30–e43）全部通过（本地 gateway/tenant-service 进程 × 测试环境 PG/Redis/auth-service，真实鉴权 401 与 operator 透传经环境库复核）。本批次后续追加账单软删除 `DELETE /billing/invoices/{invoiceId}`（第 7 个端点，见下方"追加范围"节），单测全过、迁移已应用测试环境 PG、混合联调 6/6 PASS（e44–e48）。Windows 本机 `make` 聚合入口存在环境性缺陷（子 make 路径含空格、bash echo/date 不兼容），按约定直跑各底层校验脚本，`make validate-services` 聚合复核由人工补跑。未部署 K8s，不标 live/runtime ready。

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

## 追加范围：账单软删除 `DELETE /api/v1/svc/billing/invoices/{invoiceId}`（2026-09-08 同批次追加）

对应 BOSS 租户计费页「更多操作 → 删除」按钮的后端能力。第 7 个 billing 端点（6 读/写 + 1 删除）。

### 语义（与产品确认的边界）

- **软删除**：`deleted_at` 时间戳标记，行保留可审计；总览/对账/操作历史/预读一律不再返回该账单。
- **仅 issued 可删**：settled/credited 终态 → 409 `BILLING_STATE_CONFLICT`；不存在或已删除 → 404 `BILLING_INVOICE_NOT_FOUND`（重复删除 404 幂等无害）。
- **唯一键释放**：`(tenant_id, period)` 一期一单约束改为部分唯一索引（仅约束 `deleted_at IS NULL` 活跃行），删除后同账期可重新出账；账单号 seq 含软删行（历史单号不复用，重新出账为 INV-xx-02 而非复用 01）。
- **同事务流水**：删除与 `invoice.deleted` 操作流水同一事务落库；CAS 失败（404/409）事务回滚、不落流水。删除动作计入操作历史（抽屉可见「删除账单 …（软删除，可重新出账）」）。
- **无幂等键**：DELETE 非创建类写，重放同一 id 幂等返回 404。
- **权限**：x-ani-authz resource=billing，action=write，boundary=platform，principal_kinds=[user]（与出账/结清一致）。

### 实现要点

- **契约先行**：`services/v1.yaml` 新增 `DELETE /billing/invoices/{invoiceId}`（响应 200 返回删除前账单快照，status 仍为 issued）+ proto `DeleteInvoice` RPC；pb/Services SDK 四语言/docs/api 重生成幂等零漂移。
- **迁移** `20260908_002_billing_invoice_soft_delete.sql`（+atlas.sum）：`billing_invoices` 加 `deleted_at TIMESTAMPTZ`；原唯一约束 `billing_invoices_tenant_id_period_key` 替换为部分唯一索引 `billing_invoices_tenant_period_active_uidx`（`WHERE deleted_at IS NULL`）；`billing_operation_logs.action` CHECK 扩展 `invoice.deleted`。
- **store**：`SoftDeleteInvoice`（CAS 软删 SQL `WHERE id=$1 AND status='issued' AND deleted_at IS NULL`；op 非 nil 时事务路径——CAS 软删 + 流水原子落库，CAS 未命中在事务内甄别 404/409 后回滚）；读路径过滤软删行（List/GetByPeriod/ListTenantIDs）；`CountInvoicesByNoPrefix` 含软删行（seq 不复用）。
- **边界缺陷修复（本批次实现中发现并修复）**：`GetInvoice` 预读与 `casUpdateBillingInvoiceSQL`（settle/credit CAS）未排除软删行——已删除账单仍可被结清/授信，且 EXISTS 甄别会把已删除误判为 409。修复：预读与 CAS、404/409 甄别查询统一加 `deleted_at IS NULL`，删除后账单对外完全不可见（再 settle/credit → 404）；svc 单测补断言锁定该语义。
- **gateway**：`svc.DELETE("/billing/invoices/:invoiceId", api.deleteInvoice)`，operator 透传 token user_id；fake gRPC 客户端补 `DeleteInvoice`。

### 追加范围验证

- 单测：svc `TestBillingDeleteInvoiceSuccessAndReissue`（删除成功快照+流水、重复删除 404、删除后不可 settle 404、同账期重新出账 INV-2609-02）、`TestBillingDeleteInvoiceConflictsAndValidation`（终态 409、404、invoice_id 缺失/非 UUID 400）；gateway `TestBillingDeleteInvoiceHandler`（200 快照 + operator 透传、409/404 错误映射）+ nil-guard 补 DELETE 路径，全 PASS。
- 迁移已应用到测试环境 PG（SSH + `kubectl exec psql`，幂等脚本可重放）：`deleted_at` 列、部分唯一索引 `billing_invoices_tenant_period_active_uidx`、action CHECK 扩展 `invoice.deleted` 三项均已在环境库复核。
- 混合联调 6/6 PASS（本地 gateway/tenant-service 进程 × 环境 PG 30945/Redis 30453/auth-service 30091，测试租户 tc-billing-env-test，跑前预清理/跑后复位 credit=104.6）：e48 无凭证 401 + invoiceId 非 UUID 400；e44 删除主链路（出账 INV-2606-01 → 删除 200 快照 status=issued → 总览 invoices 1→0 → 操作历史含 invoice.deleted 同事务流水）；e44b 删除后同账期重新出账 INV-2606-02（唯一键释放、seq 含软删行）；e45 重复删除 404 不落流水（流水 2→3 仅新增重出账）；e46 终态账单删除 409 不落流水；e47 已删账单再结清 404（软删行对 settle CAS 不可见）。证据存 `repo/.tmp/billing-del-report/`（不入库）。未部署 K8s，不标 live/runtime ready。
