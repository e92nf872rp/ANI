# KB-API-B2 — issue-046 Gateway 层：chunks / sessions / messages handler + citations 增强字段 + B2 契约闭环

> Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-046-b2-gateway-chunks-sessions-citations.md`
> Batch: KB-API-B2 (implementation phase) · 产品线: core（Services / ani-gateway）
> Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
> SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`（§2.4 结构、§3.2 JSONB 序列化、§4.3 Endpoints #11/#15/#16/#17/#18、§5.1 游标分页、§5.4 边界场景、§9.2 测试策略）

完成日期：2026-09-04
分支：`feat/kb-api-completion`（同 PR 含 issue-043/044 B1 + issue-045 B2 kb-service 侧）
验证结果：Go `go test ./internal/router/...` ok 1.605s；kb-service 聚焦 pytest 95 passed（test_b2_chunks_sessions_citations + test_grpc_server + test_grpc_wiring + test_update_kb）；E2E `test_issue046_e2e.py` 115/115 全绿（本地 gateway:8080 + kb-service:8002/:50053，服务器 PG/Redis，五接口全链路含种子数据构造/分页翻页/幂等删除）；`make validate-architecture` 通过（移出 `.run/gomodcache` 本地缓存后）；`make validate-services-route-contract` 通过（31 条已登记豁免、0 错误）；review-it 收尾审查 Review clean（0 actionable findings）。

## 实现了什么

1. **3 个 B2 handler + 路由注册**（`kb_resources.go`，SPEC §4.3 #11/#17/#18），KB 路由面 13 → 18：
   - `listKnowledgeBaseDocumentChunks`（GET `.../documents/:doc_id/chunks`）：limit 默认 50（≤0 回落默认值）、>100 → 400 本地拒绝；chunk_type 白名单（child/parent/doc_summary）透传 kb-service 校验；nil-client 503 守卫。
   - `listKnowledgeBaseSessionMessages`（GET `.../sessions/:session_id/messages`）：limit 默认 100、>100 → 400；复合游标透传。
   - `deleteKnowledgeBaseSession`（DELETE `.../sessions/:session_id`）：client → 204 无 body；幂等语义由 kb-service 保证（session 不存在仍 204、仅 KB 缺失 404），错误统一 `writeKBError`。
   - 三条路由均 `svc.METHOD` 直调、`/api/v1/svc` 组，满足 spec-split 门禁。
2. **gRPC client 扩展**（`kb_grpc_client.go`）：`KBGRPCClient` 接口 + `kbGRPCClient` 实现新增 `ListDocumentChunks`/`GetSessionMessages`/`DeleteSession`（`callCtx` 超时包装，与既有方法一致）；测试 fake 同步扩展。
3. **JSONB 透传序列化**（SPEC §3.2 / §7.2）：新增 `jsonbToRaw(raw string) (json.RawMessage, error)` helper——TrimSpace 后 empty/`"null"` → `nil, nil`；`json.Valid` 校验失败 → error（handler 映射 500 INTERNAL）。
   - `custom_metadata`（chunks，object）：proto string → object 输出；空值字段 omitted（omitempty）。
   - `sources`（messages，array）：assistant 消息 proto string → 数组输出；user 消息 proto 空串 → JSON `null`（契约 nullable 语义，与 KBQueryResponse.sources 一致）。
4. **proto Timestamp 序列化修复**：`protoTimestampToRFC3339` 增加 epoch-0 判空（`t.IsZero() || t.Equal(time.Unix(0,0).UTC())` → 空串）——proto3 optional Timestamp 在旧生成物路径下可能以 epoch-0 而非 nil 表达"无值"，与 kb-service 侧 `_ts`/`_cursor_ts` 的空串约定双端配合。
5. **citations 增强字段映射**（SPEC §4.3 #15）：`kbCitationJSON`/`kbCitationToJSON` 追加 `message_id`/`session_id`（`omitempty` 空串不出），既有 `listKnowledgeBaseCitations`/`listKnowledgeBaseSessions` handler 随 #045 合入自动激活（501 → 真实实现）。
6. **测试扩展**（`kb_resources_test.go`）：
   - `TestKBRoutes_AllEndpointsRegistered` 扩至 18 条路由（沿 B1 #044 T2 的单一事实源决策）。
   - 11 个 B2 handler 单测：ListDocumentChunks Success（custom_metadata object 断言）/ EmptyCustomMetadata（omitted）/ InvalidCustomMetadata（500）/ DefaultLimitAndNotFound / LimitOutOfRange；ListSessionMessages Success（user sources null + assistant sources array）/ InvalidSources（500）/ LimitOutOfRangeAndNotFound；DeleteSession SuccessAndNotFound；B2Handlers_NilClientReturn503；citations 增强字段断言（message_id/session_id 透传 + omitempty）。
7. **契约基线闭环**（`architecture/services-route-baseline.yaml`）：移除 B2 5 条 `spec_not_in_code` 豁免（B1 2 + B2 3：PUT updateKnowledgeBase / GET getKnowledgeBaseDocument / GET chunks / GET session messages / DELETE session）——issue-040/041 契约批次登记的豁免全部解除，route-contract 门禁全绿（KB 域 v1.yaml ↔ gateway 路由零漂移）。
8. **E2E 全链路验证**（`services/kb-service/tests/e2e/test_issue046_e2e.py`，未跟踪交付物）：起本地 gateway + kb-service，直连种子（chunks 7 行含 parent/child/doc_summary、sessions、messages 含 assistant source_chunks），对 5 个 B2 接口 115 项断言——chunk_type 三态过滤、limit 越界 400、复合游标翻页（next_cursor 续读至尽）、user/assistant sources 序列化、幂等删除 204、citations 最高分/uuid5/message_id/session_id、跨 KB 404；含 gRPC 通道 warmup 轮询（非阻塞 dial 退避窗口 503 重试）。`show_issue046_api.py` 为接口输入输出展示脚本（双 KB 展示运行）。
9. **review-it 收尾审查**（本批次收尾）：Review clean，0 actionable findings；三项调查均定性拒绝——①`make validate-architecture` 失败 100% 来自未跟踪的 `.run/gomodcache/`（本地 e2e Go 缓存落仓库内被误扫；标准缓存在 `E:\GoModCache`，无脚本引用 `.run/gomodcache`，CI 唯一 workflow `build-image.yml` 不跑该门禁且全新 checkout 无此目录）→ 已物理移出仓库，门禁即通过，真实代码零违规；②`zz_generated_core_policies.go` admin 路由变化系 upstream merge 后 v1.yaml 的合法再生成（available-tenants/users/batch 对应、transfer-ownership/changeable-roles 已移除，逐条核实）；③`sdks/` + `docs/api/` 的 M 状态为 CRLF 行尾噪音（`git diff` 0 行内容），非真实改动。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/ani-gateway/internal/router/kb_resources.go` | 修改 (+302) | 3 个 B2 handler + 3 条路由（12→18 endpoints）+ `jsonbToRaw` + `protoTimestampToRFC3339` epoch-0 修复 + `kbCitationJSON`/`kbCitationToJSON` 增强字段 + `kbChunkToJSON`/`kbSessionMessageToJSON` |
| `services/ani-gateway/internal/router/kb_grpc_client.go` | 修改 (+52) | 接口 + 实现 + `ListDocumentChunks`/`GetSessionMessages`/`DeleteSession`（callCtx 超时包装） |
| `services/ani-gateway/internal/router/kb_resources_test.go` | 修改 (+691) | 18 条路由注册断言 + 11 个 B2 handler 单测 + citations 增强字段断言；fakeKBClient 扩展 6 方法 |
| `architecture/services-route-baseline.yaml` | 修改 | 移除 5 条 `spec_not_in_code` 豁免（B1 2 + B2 3 契约闭环） |
| `services/ani-gateway/internal/authz/zz_generated_core_policies.go` | 修改（生成） | admin 路由变化系合并 upstream 后 v1.yaml 的合法再生成（operationId 一一对应已核实，非本批次功能） |
| `services/kb-service/tests/e2e/test_issue046_e2e.py` | 新增（未跟踪） | E2E 115/115 五接口全链路 + gRPC warmup；**必须随批次提交** |
| `services/kb-service/tests/e2e/show_issue046_api.py` | 新增（未跟踪） | 五接口输入输出展示脚本（双 KB 运行） |

> 同 PR 的 kb-service 侧改动（5 个 B2 servicer + 5 个 repository 函数 + cursor.py + migration×3 + pytest 95）属 issue-045，记录见 `kb-api-b2-issue-045-kb-service-chunks-sessions-citations.md`。

## 设计决策（Design Decisions）

### D1：limit 越界（>100）400 在 Gateway 本地拒绝，limit ≤0 回落默认值而非报错
- **模糊点：** SPEC §4.3 只写 "limit 1–100"，未规定越界是 Gateway 本地 400 还是 kb-service INVALID_ARGUMENT 映射；也未规定 0/负值语义。
- **选择：** `>100` → Gateway 本地 400（不发 RPC）；`≤0/缺失` → 回落默认值（chunks 50 / messages 100，对齐 v1.yaml default）。
- **理由：** ① 越界必失败，本地拒绝省一次 gRPC 往返与 kb-service 事务开销（沿 B1 #044 D1 模式）；② kb-service 侧 1–100 校验保留为纵深防御（其他直连 gRPC 调用方兜底）；③ 0/负值回落默认值对齐 OpenAPI default 语义（客户端传 0 = "我要默认页大小"），不与显式越界错误混叠。
- chunk_type 白名单不在 Gateway 预检：v1.yaml enum 与 kb-service `allowed_chunk_types` 同源，kb-service INVALID_ARGUMENT → 400 映射已覆盖，避免两份白名单漂移。

### D2：JSONB 透传统一 `jsonbToRaw` helper，三态语义单点定义
- **模糊点：** SPEC §3.2/§7.2 规定 proto string → REST object/array，但未规定 empty/null/invalid 三态的边界行为归属。
- **选择：** 单 helper 承载：empty/`"null"` → `nil, nil`（字段级 omit-or-null 由各 JSON struct tag 决定）；invalid → error → handler 500 INTERNAL。
- **理由：** ① 三态语义单点定义，custom_metadata 与 sources 两处复用零漂移；② invalid JSONB 意味着 kb-service 写入路径 bug（DB 侧 jsonb 列天然合法，坏数据只能来自上游序列化缺陷），fail-fast 500 优于静默 null 掩盖；③ 测试三态各有专项用例（object 断言/Empty omitted/Invalid 500）。
- 字段级差异：`custom_metadata` 用 omitempty（契约非 nullable，空省略）；`sources` 无 omitempty（契约 `nullable: true`，user 消息显式 `null`——对齐 KBQueryResponse.sources 先例）。

### D3：`protoTimestampToRFC3339` 双判空（`IsZero` || epoch-0）
- **模糊点：** proto3 Timestamp 的"无值"在 nil 与 epoch-0 之间存在歧义（部分生成物/写入路径落 0 值而非 nil）。
- **选择：** `ts == nil` 与 `t.IsZero() || t.Equal(time.Unix(0,0).UTC())` 双条件都返回空串。
- **理由：** ① epoch-0（1970-01-01T00:00:00Z）在 KB 域语义上不可能为真实业务时间（KB 功能 2026 年上线）；② kb-service 侧 `_ts`/`_cursor_ts` 对空值输出空串，双端配合下 REST 侧永不出现伪 1970 时间戳；③ 双判空是廉价防御（一次比较），消除对生成物 optional 语义的依赖。

### D4：DeleteSession 的 204 幂等语义完全委托 kb-service，Gateway 不做存在性预判
- **模糊点：** SPEC §4.3 #18 写 "204（幂等）"，未规定 Gateway 是否先查询 session 存在性。
- **选择：** Gateway 单次 RPC 调用 `DeleteSession` → 成功即 204；不存在 session / 重复删除均由 kb-service 返回成功（幂等），仅 KB 缺失 404。
- **理由：** ① 先查后删引入竞态窗口（查存在 → 删）且双倍 RPC；② 幂等判定单点在 kb-service（`delete_session` 返回 bool → Empty 映射），Gateway 预判会造成两份语义漂移；③ E2E 覆盖「删除后再删仍 204」与「跨 KB session 404」验证委托正确。

## 偏差（Deviations vs PRD/UX/SPEC）

### DEV1：`architecture/services-route-baseline.yaml` 豁免移除超出 Issue Scope（`repo/services/ani-gateway/` only）
- **Spec 说：** Issue Scope 明确 "Code paths allowed: `repo/services/ani-gateway/` only"。
- **实现：** 同 PR 修改 `architecture/services-route-baseline.yaml`（移除 5 条 `spec_not_in_code` 豁免）。
- **理由：** 与 B1 #044 同构：该文件是 route-contract 门禁登记处，豁免移除是 AC「`make validate-services` 通过」的必要组成（SPEC §4.2 契约与路由同 PR 硬约束），非功能越界；豁免存留一天门禁就晚绿一天（沿 #044 D4 决策）。
- 本次移除的 5 条中 2 条属 B1（issue-040 登记、原定 #044 解除，因 #044 批次时 v1.yaml 契约 PR 与本实现 PR 的合流顺序，实际随本 PR 一并解除）、3 条属 B2（issue-041 登记），全部对应路由已注册，移除后 `make validate-services-route-contract` 0 错误。

其余无偏差——handler 错误映射（400/404/503/500）、路由形式（`svc.METHOD` 直调）、测试矩阵（注册断言 + 成功/越界/404/nil-client + 序列化专项）均按 SPEC §4.3/§5.4/§9.2 与 Issue AC 逐条落地。

## 权衡（Tradeoffs）

### T1：invalid JSONB → 500 中断整页，而非跳过该 item
- **备选 A（采纳）：** `json.Valid` 失败 → 500 INTERNAL，不返回部分结果。
  - 优点：坏 JSONB 意味着 kb-service 序列化 bug（DB jsonb 列数据必然合法 JSON），静默跳过/null 会掩盖上游缺陷；500 + request_id 可观测可归因。
  - 缺点：单 item 坏数据导致整页不可用——但 SPEC §5.4「单条坏数据不炸整页」针对的是 kb-service citations 展开阶段（score/page 强转，已在 #045 DEV2 落地），透传层的 JSONB 本身不可能"部分坏"（kb-service 侧 json.dumps 产物或 DB 合法 jsonb）。
- **备选 B（弃用）：** 跳过坏 item 继续返回其余。
  - 弃用理由：制造"分页静默丢行"——客户端无法感知缺页，键集分页下丢行比报错更危险；且该场景在正常链路下不可达，防御性跳过属于投机性容错。

### T2：fakeKBClient 扩展 6 方法（而非引入 mock 框架或另立 fake）
- **备选 A（采纳）：** 既有 `fakeKBClient` struct 追加方法字段。
  - 优点：与 `kb_resources_test.go` 全文件既有模式零摩擦；编译器保证接口完整（缺方法即编译失败）。
  - 缺点：fake struct 随接口增长膨胀（现 20+ 方法）。
- **备选 B（弃用）：** gomock/mockgen 生成 mock。
  - 弃用理由：引入新依赖与新代码生成链，与仓库测试惯例（手写 fake）冲突；B2 用例均为直白 stub 断言，手写更直接。

### T3：E2E 交付物继续落 `services/kb-service/tests/e2e/`（沿 B1 #044 T3）
- **备选 A（采纳）：** 与 `test_issue044_e2e.py` 同目录，复用起服务/等待就绪/清理惯例。
  - 优点：工具链同构（子进程 go build、端口探测、`_kill_stale_processes`）；B3 reparse E2E 可继续复用。
  - 缺点：跨服务 E2E（测 gateway 入口）住 kb-service 目录，归属别扭——接受沿先例。
- **备选 B（弃用）：** 迁至 gateway 侧或仓库根 `tests/e2e/`。
  - 弃用理由：B1 已定调（Core 域 e2e 与 Services 域 e2e 启动方式不同，混放误导）；批次间迁移制造无谓 churn。

## 开放问题（Open Questions）

### OQ1：`.run/gomodcache/` 误扫 `validate-architecture`（沿承 #043/#044/#045 OQ1，本次收口定案）
本批次 review-it 已完成收口归因并物理处置：`.run/gomodcache` 为本地 e2e 运行时落点的 Go 模块缓存（无任何仓库脚本引用；标准缓存在 `E:\GoModCache`），已移出仓库（`ANI/gomodcache-review-tmp`，可删），门禁恢复绿色。CI 唯一 workflow `build-image.yml` 不跑 validate-architecture 且全新 checkout 无 `.run/`，不受影响。**follow-up issue 建议维持**（#044 OQ1 已提出）：`validate_component_imports.py` 排除表加 `/.run/` + `.gitignore` 补 `repo/.run/`——属工具缺口非本批次引入，按 review-it 契约拒绝在本批次顺手修。

### OQ2：`zz_generated_core_policies.go` 随本 PR 进入（沿 #044 OQ2）
admin 路由变化（available-tenants / tenants/{id}/roles / users/batch 新增，transfer-ownership / changeable-roles 移除）系合并 upstream/main 后 v1.yaml 的合法再生成产物，与本批次 KB 端点无关，但生成物与 v1.yaml 必须同 PR 同步（否则 CI 生成物校验红）。**建议 PR 描述中说明来源**，避免 reviewer 误判 scope。

### OQ3：e2e 日志工件与临时产物的提交边界（沿 #044 OQ4）
`test_issue046_e2e.py` / `show_issue046_api.py` 是可重复运行的脚本（提交）；`e2e_issue046_result.log`、`show_issue046_api_output.log`、`gateway_issue046.stdout.log`、`kb-service_issue046.stdout.log` 为本次运行工件——**建议不提交**。另 `sdks/` + `docs/api/` 在 `git status` 中呈 M 状态但 `git diff` 0 行内容（CRLF 行尾噪音），提交时无实际变更入链，无需处理。

### OQ4：未跟踪交付物必须随批次提交（沿 #045 OQ5）
kb-service 侧 `repositories/cursor.py`、`tests/test_b2_chunks_sessions_citations.py`、`tests/test_update_kb.py`、migration `20260903000300_kb_sessions_kb_id_index.sql`（#045 交付）与 gateway 侧 `tests/e2e/test_issue046_e2e.py`、`tests/e2e/show_issue046_api.py`（本批次交付）均为未跟踪文件，**必须随批次提交**，勿被 `git add` 范围裁剪遗漏。

### OQ5：KB-API 计划剩余批次
issue-046 收口后 B1+B2 全链（契约 → kb-service → gateway）闭环，剩 B3（issue-042 契约已就绪：ReparseDocument RPC + proto；后续 kb-service servicer + gateway handler 未排期）。route-baseline 中 reparse 的 `spec_not_in_code` 条目（L56–61）按 SPEC §2.4 由 B3 批次解除，当前保留属预期中间态。

### OQ6：B3 #048 收口时 B2 e2e 重跑的两项修复（2026-09-07，随 #048 批次 diff）
#048 批次将六幂等入口收严为 `uuid.Parse` 镜像校验后重跑 B2 e2e，两次失败均已定位修复、终态 115/115 全绿（L10 时点结论不变）：
1. **幂等键 400（测试缺陷非产品缺陷）：** `test_issue046_e2e.py` 两处前置 CreateKB 用字符串键 `idem-create-kb1-...`，收严后前置即 400——已改 `str(uuid.uuid4())`，修复属 #048 批次 diff（同源结论见 #048 记录 D1）。
2. **PG 连接槽满（环境问题非代码问题）：** 服务器 PG 连接槽被历史残留会话占满（84/84），`_free_pg_slots.py` 释放至 39 后重跑成功。

## 验证命令（已运行）

```
cd services/ani-gateway
go test ./internal/router/...                                  # ok 1.605s（18 路由注册 + 11 B2 handler 单测全绿）
cd ../kb-service
python -m pytest tests/test_b2_chunks_sessions_citations.py tests/test_grpc_server.py tests/test_grpc_wiring.py tests/test_update_kb.py -q   # 95 passed
python tests/e2e/test_issue046_e2e.py                          # 115/115 全绿（五接口全链路，含种子/分页/幂等/序列化）
cd ../../
make validate-architecture                                     # 通过（.run/gomodcache 移出后：component import guard passed）
make validate-services-route-contract                          # 通过（31 条已登记豁免、0 错误；B2 5 条 spec_not_in_code 已移除）
```

> 注：E2E 需 `go build` 生成 `repo/bin/ani-gateway.exe` 并占用 8080/8002/50053 端口；本机三服务仅本地测试用，数据库/Redis 用服务器已部署组件（用户既定约束）。人工核验项：v1.yaml 五路径 limit 默认值（50/20/20/100/20）与两侧实现一致；chunk_type enum 三方一致；DeleteSession 204 幂等契约 ↔ kb-service bool→Empty 映射；proto message 全量已定义。
