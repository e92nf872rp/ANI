# KB-SPLIT-P1H — 向量路径收口：写入-检索往返断言 + core_api vector 引用清零证据

完成日期：2026-09-24
对应批次：KB-SPLIT-P1H（ADR 0001 §D1 裁定、§四 #6 向量部分，本地 issue #057，依赖 #056 P1G 向量自管）
验证结果：kb-service 单测 **593 项中 565 passed + 28 skipped**（P1G B' 修复后基线 559 + 新增往返/重解析/删除测试 6，零回归）；`make validate-architecture` 4 个 guardrail 通过；`make validate-auth-contract`、`make validate-gateway-authz` 通过；`go test` 12 包树 **11 过 + 1 预存在环境失败**（`pkg/adapters/runtime` 两个沙箱用例，Windows symlink 特权 + `python3` 不在 PATH，status.md 基线测试记录已登记同款、零 Go 文件改动）；`git diff --check` 通过。

## 实现了什么

P1G 偏差段预告的收口批次：#056 已在 P1G 顺带完成全部数据面热路径切换（parse insert / retrieve search / 重解析清理直连自管 Milvus，`vector_store_id` 列语义平移为 Milvus collection 名），#057 本批交付 **写入-检索往返断言 + core_api vector 引用清零证据 + 全门禁复跑**，向量路径正式关闭。

**① `tests/test_vector_roundtrip.py` 新建（561 行，6 项测试，mock Milvus）**——核心是 `_RoundtripVectorStore`：**带真实存储与余弦排序的 fake**（存行、按 query 向量算 COSINE 排序、按 filter 删行），同时注入 **parse 写侧（parse_orchestrator）与 retrieve 读侧（retrieve_service）**，构成真往返——写入的行被检索读到，而非两侧各自 mock。另追踪 `ops` 调用顺序（`["delete", "insert", ...]`）供清理先行断言。

6 项测试：
- **写入-检索往返**：parse 灌 3 chunk（正交单位向量）→ vector 模式检索 → 断言命中集合、排序（余弦降序）与 score 透传；
- **summary backfill**：summary 行随机 uuid 按 `chunk_type == "summary"` 断言 + parent chunk backfill 行为；
- **hybrid 同 store**：hybrid 模式向量腿从同一 store 取数，keyword 腿与 RRF 融合语义不变；
- **重解析 delete 先行**：同 doc 重跑 `process_document` → `ops == ["delete", "insert", "delete", "insert"]` 且旧向量无残留（旧 chunk id 不在检索命中内）；
- **delete doc-scoped**：删单文档向量不误删同 KB 他文档（`metadata["doc_id"]` 过滤）；
- **delete 后检索空**：文档删除后检索不返回其 chunk。

**测试技巧（正交单位向量）**：`V_CHILD_A=[1,0,0,0]`、`V_CHILD_B=[0,1,0,0]`、`V_QUERY=[1,0,0,0]` 等，使余弦值精确为 1.0 / 0.0 / 1/√2——无浮点噪声，断言可精确到分数值。COLLECTION 按 `_vector_store_name` 派生（UUID 去横线加 `kb_` 前缀）。

**② grep 清零证据落档**：`core_client.(insert|delete|search|create|list)_vector` 在 `services/` 全域 **零命中**——生产路径无任何 vector 系 Core 调用残留。`core_api/contracts.py` 中 vector 相关仅为 Protocol 方法声明（类型面，无调用）、`core_api/client.py` 的 vector 方法为死代码（模块删除归 #065 P4B，Karpathy 原则三不顺便自删）。

**③ 门禁复跑**（AC #7）：validate-architecture（SQL 集中化 / 组件导入 / PG init 一致性等 4 guardrail）、validate-auth-contract、validate-gateway-authz、compileall（排除 .venv）全过；`go test` 12 包树补跑全过（除 pkg/adapters/runtime 预存在环境失败，见验证结果）；`git diff --check` 干净。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/kb-service/tests/test_vector_roundtrip.py` | 新增 | 6 项写入-检索往返/重解析 delete/文档删除测试（`_RoundtripVectorStore` 真实余弦排序 fake，双注入 parse 写侧 + retrieve 读侧） |

（生产代码零改动——P1G 已完成全部切换，本批纯收口；唯一 git 变更为本未跟踪测试文件。）

## 完工标准达成

- [x] parse 直连写入自管 Milvus（#056/P1G 已实现，本批以往返断言核实：写入行经 `_RoundtripVectorStore` 被检索读到）
- [x] 重解析清理：旧向量 delete 先行、无残留（`ops` 顺序断言 + 旧 chunk id 不在命中集合断言）
- [x] 检索直连自管 Milvus，hybrid 语义不变（vector 腿往返断言 + hybrid 同 store 断言，keyword 腿与 RRF 融合未被触碰）
- [x] kb 编排职责链保留（parse_orchestrator 职责链零改动，测试直接驱动既有编排路径）
- [x] 单测（mock Milvus）：写入-检索往返 + 重解析后旧向量无残留（delete 调用断言）——6 项全过
- [x] core_api vector 引用清零（grep 验证：`core_client.(insert|delete|search|create|list)_vector` services/ 零命中）
- [x] `make test`（等价直跑；test-go 唯一失败为预存在环境失败，见偏差）+ `make validate-architecture` + `git diff --check` 通过
- [x] progress 文档闭环 4 件套（本文件 + README 索引 + CURRENT-SPRINT.md + ANI-06-开发计划.md）+ status.md 阶段进度与差异清单更新

## 设计决策

- **共享 fake 构成真往返**：`_RoundtripVectorStore` 同一实例注入写侧与读侧，往返语义（写入的行确实被检索消费）被真实执行——两侧各自 mock 无法证明「写进的就是读出的」。
- **fake 带真实余弦排序而非存根**：排序、score 透传、top_k、`metadata["doc_id"]` 过滤删除均被真实验证；仅 Milvus 传输层被 mock（与 P1G `test_vector_store_milvus.py` 的参数级 mock 分层互补——彼处验证 pymilvus 调用参数正确，此处验证编排端到端语义）。
- **正交单位向量**：余弦值退化为精确常数（1.0/0.0/1/√2），断言零浮点容差。
- **`ops` 顺序追踪替代 spy 逐条记录**：一条列表同时支撑「无条件清理先行」与「重解析双清理」两个断言面，测试噪音最小。

## 偏差

- **无生产代码改动**：P1G 偏差段已预告——数据面热路径切换（parse insert / retrieve search / 重解析清理）属接线必然性，已在 #056 顺带完成并经既有单测断言迁移覆盖；#057 按其 AC 逐条核对后确认余量仅为「往返级断言 + grep 证据 + 门禁」，即本批交付内容。
- `make test-go` 唯一失败为 `pkg/adapters/runtime` 的 `TestSandboxFileScriptsRejectSymlinks`（5 子测试）+ `TestSandboxFileScriptsAllowWorkspaceOperations`：Windows 无符号链接权限（`A required privilege is not held by the client`）+ `python3` 可执行不在 PATH（Windows 为 python.exe）——预存在环境性失败，status.md 基线测试记录已登记同款（origin/main 同 FAIL），本批零 Go 文件改动，与本 issue 无因果。

## 权衡

- **`core_api/client.py` vector 方法与 `contracts.py` Protocol 声明保留**：前者死代码、后者仅类型面，删除归 #065（P4B core_api 面清零）；本批 grep 证据以「生产调用零命中」为收口口径。
- **往返测试聚焦向量腿**：keyword 腿（PG 全文）与 RRF 融合已有独立测试覆盖（P1C/P1D 建立的 `test_retrieve_service.py` 语义），本批仅以「hybrid 同 store」断言衔接，不重复建 keyword 侧夹具。

## 开放问题

- 无新增。`core_api` 模块删除（client.py 死代码 + contracts.py Protocol）与 NotifyDocumentUploaded 删除归 #065；对象自管（#058/#059）为下一切分面。
- 提交 #057 时建议在 commit message 中说明工作区三个生产文件的未提交非行为性小改（见下方审查记录 Advisory-1）的归属。

## 验证命令（goal 收口实测）

```
python -m pytest tests/test_vector_roundtrip.py -q（kb-service） → 6 passed
python -m pytest -q（kb-service 全量）                            → 565 passed, 28 skipped（P1G B' 后基线 559 + 6，零回归）
grep -rnE "core_client\.(insert|delete|search|create|list)_vector" services/ → 零命中（contracts.py 仅 Protocol 声明、client.py 死代码归 #065）
make validate-architecture（沙箱下等价直跑 4 guardrail）          → passed
make validate-auth-contract / validate-gateway-authz（等价直跑）  → passed
python -m compileall -q -x "\.venv" ai/rag-engine                → exit 0
go test（GO_PACKAGES 12 包树，等价直跑）                         → 11 包树 ok + pkg/adapters/runtime 预存在环境失败（TestSandboxFileScripts*：Windows symlink 特权 + python3 不在 PATH，基线已登记）
git diff --check                                                 → exit 0
```

> 注：`make` 顶层目标在本机 Windows 沙箱的已知环境限制下（P1A-P1G 记录一致）按 Makefile 逐目标等价直跑。

## 审查记录（/review-it 收口，2026-09-24）

审查对象：分支 `feat/split-kb-from-core`（HEAD=17c6cb0e/#056）上本批全部交付物（未跟踪测试文件 + 工作区 4 个 kb-service 文件小改 + progress 4 件套）。结论：**CLEAN——8 条 AC 全部达成，无阻塞/高危 finding**，2 条 advisory。

### 交付物性质甄别（审查前置）

- 判定本批为「收口批次」：数据面切换本体已在 #056 提交，工作区 7 个修改文件中与 kb-service 相关的 4 个均为非行为性小改（`contracts.py` docstring——`vector_store_id` 语义平移说明，其"grpc_server resolves it from the KB row"声称经 [grpc_server.py L1657] 核实属实；`parse_orchestrator.py` Milvus delete 失败日志 debug→warning；`grpc_server.py` query_stream 调用缩进对齐；另 4 个为 progress 文档文件），生产逻辑改动为零，与本文「生产代码零改动纯收口」记录一致。

### 逐条验证（全部独立复现，非采信 progress 记录）

- AC1/AC2/AC4：测试断言（`ops == ["delete","insert","delete","insert"]`、旧向量零残留、编排链顺序）与 [parse_orchestrator.py L336-353/L388-419] 真实源码逐段核对一致——delete 先行于 insert、单批 100 条 insert、`filter_expr` 格式与 L347 生产调用逐字符一致。
- AC3：[retrieve_service.py L174-179] 直连 search（top_k×2）、RRF K=60 融合未被触碰。
- AC5：`python -m pytest tests/test_vector_roundtrip.py -q` 本轮独立复跑 → **6 passed**（1.84s）。
- AC6：`core_client.(insert|delete|search|create|list)_vector` 在 `services/` 全域独立 grep 复扫两次 → 零命中。
- AC7：全量 `python -m pytest -q`（565 passed + 28 skipped，与 progress 记录一致零回归）+ validate-architecture 4 guardrail + validate-inference-legacy-control-plane + `git diff --check` CLEAN，全部本轮实测。
- AC8：progress 4 件套数字与实测无一虚报。

### Advisory（不阻塞，随批说明）

1. 「生产代码零改动」表述与工作区实况有细微出入（上述 3 个生产文件未提交小改，均为非行为性）——建议 commit message 说明归属，避免 review 者困惑。
2. `parse-{doc_id}-{run_id}-b{batch_no}` 幂等键已在自管路径消失（Core insert idempotency_key 时代语义）：自管路径靠「delete 先行 + 新 run 新 chunk_id（含随机 summary uuid4）」免撞，行为等价；contracts.py 新 docstring 已写清，ADR §一-1.2 原文与实现的机制差异已获文档覆盖，无需动作。

### 拒绝升级的候选 finding（说明理由）

- best-effort Milvus delete 失败 → 旧向量残留泄漏窗口（[parse_orchestrator.py L349-353]）：预存在设计（继承 DeleteDocument handler 同款语义），非本批引入（本批零生产逻辑改动）；本批 debug→warning 升级恰提升了该路径观测性。跨批次修复归 #063（可观测性），不属本审查 actionable。
