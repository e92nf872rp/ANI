# KB-API-B1 — issue-044 Gateway 层：UpdateKB + GetDocument handler + 契约基线闭环

> Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-044-b1-gateway-updatekb-getdoc.md`
> Batch: KB-API-B1 (implementation phase) · 产品线: core（Services / ani-gateway）
> Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
> SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`（§2.4 结构、§4.2 路由、§4.1 端点面、§6.1 错误映射、§9.2 测试）

完成日期：2026-09-03
分支：`feat/kb-api-completion`（与 #043 同批次 KB-API-B1 同一 PR）
验证结果：Go `go test ./internal/router/... ./internal/authz/...` ok（router 19.262s / authz 6.399s）；kb-service pytest 48 passed（同 PR 的 #043 侧）；E2E `test_issue044_e2e.py` 39/39 全绿（17 处接口作用打印）；`validate_inference_legacy_control_plane.py` exit 0；`validate_component_imports.py` 过滤 `.run/gomodcache` 后零真实违规；route-contract 基线豁免移除 2 条（新端点已注册，`spec_not_in_code` 转绿）。

## 实现了什么

1. **两个 P0 handler**（`kb_resources.go`）：
   - `updateKnowledgeBase`（PUT `/knowledge-bases/:kb_id`，SPEC §4.3 #5）：BindJSON `UpdateKnowledgeBaseRequest` → idempotency_key TrimSpace 空判 400（**不触 kb-service**）→ `client.UpdateKB(ctx, instanceTenantID(c), kbID, idemKey, name, desc)` → `kbToJSON` 200；错误统一 `writeKBError`。
   - `getKnowledgeBaseDocument`（GET `/knowledge-bases/:kb_id/documents/:doc_id`）：`client.GetDocument(ctx, tenant, kbID, docID)` → `kbDocumentToJSON` 200。
   - 两者均沿用 nil-client 守卫 503 现有模式；注释 "9 P0 handlers" → "11 P0 handlers"。
2. **gRPC client 扩展**（`kb_grpc_client.go`）：`KBGRPCClient` 接口 + `kbGRPCClient` 实现新增 `UpdateKB`（callCtx 超时包装，与既有方法一致）；`GetDocument` 客户端方法已存在（#040 落地），仅接口沿用。
3. **路由注册**（SPEC §4.2）：`svc.PUT("/knowledge-bases/:kb_id", ...)` + `svc.GET("/knowledge-bases/:kb_id/documents/:doc_id", ...)`，注册到 `/api/v1/svc` 组、`svc.METHOD("...")` 直调形式，满足 spec-split 门禁；与 #040 的 v1.yaml 契约同 PR 落地。
4. **测试扩展**（`kb_resources_test.go` +228 行）：`TestKBRoutes_AllEndpointsRegistered` 扩至 13 条路由（+PUT / +GET document）；新增 7 个 handler 单测——UpdateKB Success（idem/name/desc 透传 + tenant 注入）、MissingIdempotencyKey（400 且不调 kb-service）、NameConflict→409、NotFound→404、Update/GetDocument NilClient→503、GetDocument Success（tenant/kb/doc 透传 + JSON 字段断言）、GetDocument NotFound→404。`kb_sse_test.go` 的 fakeKBRetrieveClient 补 `UpdateKB` stub（接口扩成员）。
5. **契约基线闭环**（`architecture/services-route-baseline.yaml`）：移除 #040 登记的 2 条 `spec_not_in_code` 豁免（PUT updateKnowledgeBase / GET getKnowledgeBaseDocument）——契约与代码同 PR 汇合，route-contract 门禁转绿（沿承 issue-040 OQ1 / issue-043 OQ3 的解除）。
6. **E2E 全链路验证**（`services/kb-service/tests/e2e/test_issue044_e2e.py`，未跟踪交付物）：本地起 gateway（Go build）+ kb-service，对 11 个 P0 端点做 39 项断言（含 UpdateKB 幂等 replay、409 冲突、GetDocument 404、SSE 查询流、分段含文字/表格/图片链接的多类型文档解析），17 处接口作用/输入/输出打印双写终端与日志文件；并完成 Milvus 残留核查（PG 权威 vsid vs Milvus collection 精确比对 → 0 残留，DeleteKB 回收链路正确）。
7. **review-it 提交前审查**（本批次收尾）：结论 Review clean，无 actionable findings；两项遗留均有证据链拒绝理由——`make validate-architecture` 失败全部由 `.run/gomodcache/`（未跟踪本地 Go 模块缓存）误扫构成（过滤后零真实违规，校验器排除表缺 `/.run/` 属预存在工具缺口）；migration 重命名（`20260827_001_*` → `20260827000300/0400`，随 #043 记录 D4）虽旧名已发布 main，但两文件均 `IF NOT EXISTS` 幂等，无害重复执行，atlas.sum 四新条目齐全、deploy 目录无旧名残留引用。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/ani-gateway/internal/router/kb_resources.go` | 修改 | 2 个新 handler + 2 条路由注册 + P0 计数注释更新 |
| `services/ani-gateway/internal/router/kb_grpc_client.go` | 修改 | 接口 + 实现新增 `UpdateKB`（callCtx 超时包装） |
| `services/ani-gateway/internal/router/kb_resources_test.go` | 修改 (+228) | 13 条路由注册断言 + 7 个 handler 单测 |
| `services/ani-gateway/internal/router/kb_sse_test.go` | 修改 | fakeKBRetrieveClient 补 `UpdateKB` stub |
| `services/ani-gateway/internal/authz/zz_generated_core_policies.go` | 修改（生成） | admin 路由变化系合并 upstream 后 v1.yaml 的合法再生成（operationId 一一对应已核实） |
| `architecture/services-route-baseline.yaml` | 修改 | 移除 2 条 `spec_not_in_code` 豁免（B1 契约闭环） |
| `services/kb-service/tests/e2e/test_issue044_e2e.py` | 新增（未跟踪） | E2E 39/39 + 17 处接口打印；**必须随批次提交** |

> 同 PR 的 kb-service 侧改动（`grpc_server.py` `_update_kb`、`update_kb`/`complete_task_in_tx`、migration×4、atlas.sum、pytest 48 passed）属 issue-043，记录见 `kb-api-b1-issue-043-kb-service-updatekb.md`。

## 设计决策（Design Decisions）

### D1：idempotency_key 校验放 Gateway 前置（TrimSpace 空判 400），而非全权委托 kb-service 的 INVALID_ARGUMENT
- **模糊点：** SPEC §2.4 只写 "idempotency_key 必填 uuid，缺失 400"，未指定 400 由哪层产生（Gateway 本地 400 vs kb-service INVALID_ARGUMENT 映射 400）。
- **选择：** Gateway BindJSON 后立即 `strings.TrimSpace(req.IdempotencyKey) == ""` 本地 400，不发起 gRPC 调用（测试断言 fake client 未被调用）。
- **理由：** 省一次必然失败的 RPC 往返与 kb-service 事务开销；kb-service 侧三重前置校验（#043 D2）仍然保留，作为纵深防御（其他调用方直连 gRPC 时兜底）。uuid 格式不做 Gateway 正则预检——kb-service 端 `uuid.UUID(kb_id)` 解析失败已有 INVALID_ARGUMENT 路径，避免两份格式规则漂移。

### D2：`updateKnowledgeBaseRequest` 用平铺三字段（idempotency_key/name/description），不复用嵌套 `kb` 对象
- **模糊点：** SPEC §2.4 只列字段名，未规定请求体形态；CreateKB handler 亦为平铺风格。
- **选择：** 与 CreateKB/UploadDocument handler 的既有平铺 BindJSON 结构保持一致。
- **理由：** 契约 `api/openapi/services/v1.yaml` L2246 `UpdateKnowledgeBaseRequest` 即平铺 schema；handler 结构体镜像契约，两侧无映射层。

### D3：name/description 空串语义"不修改"由 kb-service COALESCE+NULLIF 承担，Gateway 原样透传不预判
- **模糊点：** SPEC §5.4 空字段语义在服务层定义；Gateway 是否需区分"未传"与"传空"。
- **选择：** Gateway 不做任何字段级预处理，BindJSON 后整串透传；空串在 kb-service `update_kb` 的 `COALESCE(NULLIF($n,''), col)` 处统一解释。
- **理由：** 语义单点定义、单点演化（若未来允许显式置空需改 SPEC，只动服务层）；Gateway 预判会造成两份语义漂移风险。E2E 用例覆盖"只改 name 不动 description"验证透传正确。

### D4：route-contract 豁免采用"移除"而非"保留到期"
- **模糊点：** 基线豁免可保留至下批次再清。
- **选择：** 本 PR 端点落地即移除 2 条豁免。
- **理由：** SPEC §4.2 规定契约与路由同 PR 硬约束；豁免存留一天，门禁就晚绿一天，且留下"豁免常态化"的坏先例。

## 偏差（Deviations vs PRD/UX/SPEC）

None —— 实现与 SPEC §2.4/§4.2/§4.3/§6.1/§9.2 及 Issue AC 逐条一致；handler 错误映射（409/404/503/400）、路由形式（`svc.METHOD` 直调）、测试矩阵（注册断言 + 成功/nil-client/404/409/400）均按 spec 落地。唯一超出 Issue Scope（`repo/services/ani-gateway/` only）的改动是 `architecture/services-route-baseline.yaml` 豁免移除——该文件是 route-contract 门禁登记处，移除动作本身是 AC 第 4 条（`make validate-services` 通过）的必要组成，非功能越界。

## 权衡（Tradeoffs）

### T1：UpdateKB 的 409/404 错误断言用 fake client 注入 gRPC status error，而非起真 kb-service
- **备选 A（采纳）：** 单测层 fake 注入 `status.Error(codes.AlreadyExists/NotFound, ...)`，走 `writeKBError` 映射断言 409/404；真实 ALREADY_EXISTS→409 链路交由 E2E（真 gateway + 真 kb-service + 真 PG）覆盖。
  - 优点：单测毫秒级、错误注入确定性高；E2E 已在同一 PR 内提供全链路证据（39/39）。
  - 缺点：单测不覆盖 gRPC 错误码到 status.Error 的序列化细节（由同文件既有 `writeKBError` 测试与 E2E 弥补）。
- **备选 B（弃用）：** 单测起 docker kb-service。
  - 弃用理由：单测依赖外部组件违背仓库既有测试模式（fake client 模式贯穿 kb_resources_test.go）；慢且脆。

### T2：`TestKBRoutes_AllEndpointsRegistered` 从 12 改名扩容 13，而非另立新测试函数
- **备选 A（采纳）：** 原测试函数改名（`AllTwelveEndpointsRegistered` → `AllEndpointsRegistered`）+ routes 数组加 2 行。
  - 优点：路由面单一事实源，未来 P1/P2 端点落地只需加行；避免"每批一个注册测试"碎片化。
  - 缺点：函数名失去具体数字（改用注释 "KB-API-B1 #5/#10" 表述批次来源）。
- **备选 B（弃用）：** 新开 `TestKBRoutes_B1EndpointsRegistered`。
  - 弃用理由：同一 `/api/v1/svc` 组的路由面分裂两个测试维护，新增端点时两处都要看。

### T3：E2E 交付物落 `services/kb-service/tests/e2e/`，而非 gateway 侧或独立 `tests/e2e/`
- **备选 A（采纳）：** 与既有 `tests/e2e/`（run_e2e_sse_test.py、fake_rag_engine.py 所在）同目录，复用其起服务/等待就绪的既有惯例。
  - 优点：E2E 工具链（子进程 go build、端口探测）与同目录脚本同构，后续 B2/B3 批次可复用。
  - 缺点：跨服务 E2E（实际测的是 gateway 入口）住在 kb-service 目录下，归属稍显别扭。
- **备选 B（弃用）：** 放仓库根 `tests/e2e/`（该目录现属 Core 域 e2e）。
  - 弃用理由：Core 域 e2e 与 Services 域 e2e 的服务启动方式不同（Core 有 docker compose 惯例），混放引入误导。

## 开放问题（Open Questions）

### OQ1：`make validate-architecture` 在本机因 `.run/gomodcache/` 误报失败（沿承 issue-043 OQ1，本次已定案）
review-it 已完成归因：失败 100% 来自未跟踪的 `.run/gomodcache/`（第三方 SDK 源码被当 business/shared code 扫描）；过滤后跟踪代码零违规；校验脚本排除表（`validate_component_imports.py` L148-153）只含 `/vendor/`、`/.cache/`、`_test.go`，缺 `/.run/`；`.gitignore` 亦缺 `repo/.run/`（`repo/.cache/` 已有）。**非本变更引入，建议独立 follow-up issue**：脚本加 `/.run/` 排除 + `.gitignore` 补 `repo/.run/`。

### OQ2：`zz_generated_core_policies.go` 的 admin 路由变化随本 PR 进入
系合并 upstream/main 带入的 `api/openapi/v1.yaml` 变化（admin 端点增减）的合法再生成产物（operationId 一一对应已核实：listAvailableTenants/batchGetTenantUsers/listAssignableTenantRoles 等）。虽与本 Issue 的 KB 端点无关，但生成文件与 v1.yaml 必须同 PR 保持同步（否则 CI 生成物校验红），随批次合入是仓库惯例。合入前建议在 PR 描述中说明来源，避免 reviewer 误判 scope。

### OQ3：migration 重命名（`20260827_001_*` → `20260827000300/0400`）在已部署环境的重放行为
旧名已随 PR #128 发布 main。review-it 核实：两文件均 `CREATE ... IF NOT EXISTS` 幂等，已部署环境对新名重放为 no-op，无害；但若存在以 atlas 版本表精确记录的环境，会多出两条"已执行"记录（不影响行为）。**建议部署同学知悉**：升级后可在 atlas 版本表看到 4 条 20260827/0903 系记录属预期。

### OQ4：E2E 日志工件与临时产物的提交边界
`test_issue044_e2e.py` 是可重复运行的测试（提交）；`e2e_issue044_result.log`、`e2e_run_console.log`、`gateway/kb-service_issue044.stdout.log` 是本次运行的输出工件——**建议不提交**（或提交一份作为证据快照后删除其余），会话期诊断临时脚本已全部清理（7 个，含 Milvus vsid 比对脚本，核查结果 0 残留无需 drop）。

### OQ5：`name` 字段无 maxLength（沿承 issue-040 OQ4 / issue-043 OQ4）
KB 域既有惯例，若统一收紧应整域同步做，不在本批次。

## 验证命令（已运行）

```
cd services/ani-gateway
go test ./internal/router/... ./internal/authz/...        # ok router 19.262s / ok authz 6.399s
cd ../kb-service
python -m pytest tests/test_grpc_server.py tests/test_grpc_wiring.py tests/test_update_kb.py -q   # 48 passed（#043 侧）
python tests/e2e/test_issue044_e2e.py                     # 39/39 全绿，17 处接口作用打印
cd ../../
python scripts/validate_inference_legacy_control_plane.py  # exit 0
python scripts/validate_component_imports.py --root . 2>&1 | Where-Object { $_ -notmatch 'gomodcache' }   # 零真实违规（.run/ 误报见 OQ1）
git diff --check                                          # exit 0
```

> 注：route-contract 门禁（`make validate-services`）中 2 条 `spec_not_in_code` 豁免移除后，本 PR 的 v1.yaml 契约与 gateway 路由注册同 PR 汇合，门禁恢复绿色（沿承 issue-040 OQ1 / issue-043 OQ3 解除）。E2E 需 `go build` 生成 `repo/bin/ani-gateway.exe` 并占用 8080/8002/50053 端口；本机组件仅本地测试用，不上传服务器（用户既定约束）。
