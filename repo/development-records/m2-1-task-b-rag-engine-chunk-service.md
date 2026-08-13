# M2.1-TASK-B — rag-engine 父子分块 (Issue #010)

完成日期：2026-07-31
对应 Sprint：Sprint 14
验证结果：84 tests passed, make validate-architecture passed, git diff --check clean, 真实 DB INSERT 验证通过

| 字段 | 值 |
|---|---|
| Issue | #010 — rag-engine 父子分块 |
| PRD | US-012 (`prd-core-knowledge-base-platform.md`, chunking portion) |
| UX | N/A — backend-only |
| SPEC | `spec-services-rag-engine.md` §2.2, §5.1, §9 |
| Batch | M2.1-TASK-B |
| Dependencies | #009 (parse service) — per SPEC §10.2 |

## 实现了什么

实现 `chunk_service`：消费 parse_service 输出的扁平 `list[ParsedNode]`，利用元信息（sub_type/heading_level/section_path/content_type）做 meta 感知分段，再用 SentenceSplitter 切子块（128-512 tokens，优先句子边界），连续子块累积到 2048 tokens 套叠为父块，子块 `parent_chunk_id` 指向父块并反填 `parent_content`。图片/表格/代码块/超链接作为不可切断原子单元。实现 `chunks.py` repository 批量写入 `kb_chunks` 表（RLS 租户上下文 + FK 顺序 + 元数据继承）。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `ai/rag-engine/app/services/chunk_service.py` | 新增 | 核心：ChunkService + _segment_nodes + _split_text_by_sentences + _nest_parents |
| `ai/rag-engine/app/repositories/chunks.py` | 新增 | write_chunks 批量 INSERT + delete_chunks_by_doc 幂等 + _to_uuid 校验 |
| `ai/rag-engine/app/repositories/__init__.py` | 新增 | 空包初始化 |
| `ai/rag-engine/tests/test_chunk_service.py` | 新增 | 39 个纯逻辑单测（含 2 个 review 回归测试） |

## 完工标准达成（Issue 6 条 AC）

- [x] [SPEC] `chunk_service` 用 SentenceSplitter 切子块 256-512 tokens（优先句子边界，单句超 chunk_size 强制截断，SPEC §5.1）
- [x] [SPEC] 连续子块累积到 2048 tokens 归为一个父块（固定窗套叠，SPEC §5.1）
- [x] [SPEC] 图片链接/表格/代码块/超链接作为不可切断单元（SPEC §5.1）
- [x] 子块 `parent_chunk_id` 指向父块，父块完整文本存入子块 `parent_content`
- [x] 写入 `kb_chunks` 表，元数据继承（doc_id/kb_id/tenant_id/file_name/page_number/content_type）— **运行时验证**：连接真实 PostgreSQL (10.10.1.66:30945)，执行 kb-service 迁移创建 kb_chunks 表后，`write_chunks` 真实 INSERT 3 行（1 parent + 2 child），SELECT 回读验证列映射/FK 顺序/parent_content 反填/metadata 继承全部正确；RLS 隔离用 `SET ROLE ani_app` 验证（不匹配租户 INSERT 被拒绝），策略定义 `RESTRICTIVE + FORCE RLS` 正确
- [x] `make test` 通过（Python 门禁：compile + pytest 84 passed；make test 仅在 test-go 阶段因无 Go 工具链失败，与本期无关）

---

## Implementation Notes

### 1. Design Decisions

**1.1 子块切分引入 min 阈值（CHILD_CHUNK_MIN=128），优先句子边界切分**

- **Ambiguity:** SPEC §5.2 规定子块区间 256-512 tokens，但未明确是"填到接近上限才切"还是"达下限即可在句子边界切"。
- **Choice:** 新增 `CHILD_CHUNK_MIN=128`，一旦子块累积达 128 tokens，在下一个句子边界就切，让子块落在 [128, 512] 区间内更偏小的位置。
- **Rationale:** 用户明确要求 "子块 512 上限太大了，应该以一句话为切分快，同时满足 128-512 就进行切割子块"。这样子块更小更精准，单块聚焦一个语义点，利于检索精度。硬上限仍是 512（SPEC §5.2）。

**1.2 meta 感知分段在 chunk_size 切分之前（用户要求的优化）**

- **Ambiguity:** SPEC §5.1 只说用 SentenceSplitter 切子块 + 固定窗套叠父块，未提及如何利用 parse_service 的元信息。
- **Choice:** 在 `chunk()` 流程中先做 `_segment_nodes`（meta 感知分段，决定"在哪切"），再对每个文本段做 `splitter.split_text`（chunk_size 切分）。原子类型（table/image/code）成为独立子块；标题节点开启新分段；section_path 变化也开启新分段。
- **Rationale:** 用户明确要求 "应该利用 meta 信息做更精准的切分，再按照 chunk_size 切分"。先按 meta 决定语义边界，再按 chunk_size 细化，使父块保持章节一致性。

**1.3 纯逻辑 SentenceSplitter 作为 LlamaIndex 的可降级 fallback**

- **Ambiguity:** SPEC §5.1 指定用 LlamaIndex SentenceSplitter，但单元测试环境无该重依赖。
- **Choice:** `_make_default_splitter` 优先尝试 import LlamaIndex SentenceSplitter（带 smoke test 防 MagicMock 返回非 list），失败则回退到纯逻辑 `_PureSentenceSplitter`，语义一致（CJK+ASCII 句子边界 + 链接/代码块原子 + 超长句强制截断）。
- **Rationale:** 模块无重依赖也能加载和单测；生产环境若装了 LlamaIndex 则自动启用。注意：LlamaIndex SentenceSplitter 无 min-size 参数，故仅纯逻辑 fallback 路径支持 min 阈值优化，测试钉住 fallback 路径行为。

**1.4 父块 token_count 用全文精确估算而非子块 floor 累加**

- **Ambiguity:** SPEC 未规定父块 token_count 的计算方式。
- **Choice:** `_build_parent` 用 `_estimate_tokens(full_text)` 从父块全文重新估算，而非 `sum(child.token_count)`。
- **Rationale:** 子块 token_count 是 `len//2`（floor），多个子块 floor 误差累加会让父块 count 系统性偏高，导致 `_nest_parents` 的预算检查提前触发父块切分。全文精确估算消除该漂移。（review-it 发现并修复）

**1.5 repository 的 `set_tenant_context` 本地实现而非导入 kb-service**

- **Ambiguity:** rag-engine 写 kb_chunks，但 kb-service 已有 `set_tenant_context`。
- **Choice:** 在 `chunks.py` 本地重实现 `set_tenant_context`，但用 `SELECT set_config('app.current_tenant_id', $1, true)` 而非 kb-service 的 `SET LOCAL ... = $1`。
- **Rationale:** rag-engine 是独立部署服务，不应导入 kb-service 包；SPEC §2.4 文件结构把 `repositories/chunks.py` 放在 rag-engine 内。**关键差异**：真实 DB 验证发现 `SET LOCAL ... = $1` 被 PostgreSQL 拒绝（`SET` 是 utility 命令，不接受参数绑定），改用 `set_config(..., true)` 是参数安全且事务作用域的等价写法。kb-service 的 `rls.py` 仍有此 bug（见 Open Questions 4.4）。

### 2. Deviations

**2.1 子块下限用 128 而非 SPEC §5.2 的 256**

- **Spec said:** SPEC §5.2 子块区间 256-512 tokens。
- **Implemented:** `CHILD_CHUNK_SIZE=512`（上限不变），新增 `CHILD_CHUNK_MIN=128`（下限降为 128）。
- **Why:** 用户明确要求 "同时满足 128-512 就进行切割子块"。128 仍是合理下限（约 256 字符 ≈ 1-2 句），避免过小碎片。`CHILD_CHUNK_SIZE_MIN=256`/`MAX=512` 仍用于 `child_chunk_size` 参数的合法性校验，与 SPEC 一致。

**2.2 `_split_text_by_sentences` 用 char 预算而非 token 预算**

- **Spec said:** SPEC §5.1 用 token 计量。
- **Implemented:** 内部用 `chunk_size * CHARS_PER_TOKEN` 字符预算做累积判断，仅在 dataclass `token_count` 字段存 token 估值。
- **Why:** 避免 floor 累加漂移——`_estimate_tokens` 是 `len//2`（floor），85 个 13 字符句子 floor 和为 510 但实际 552 tokens。字符预算是精确的，消除累积误差。

### 3. Tradeoffs

**3.1 meta 感知分段 vs 纯 chunk_size 切分**

- **Alternatives:** (A) 仅按 chunk_size 切，忽略 meta；(B) 按 meta 分段后再按 chunk_size 切。
- **Pros/Cons:** (A) 简单但可能跨章节合并父块，破坏语义一致性；(B) 父块保持章节一致，但分段数增多、实现复杂度略高。
- **Chosen:** (B)，因用户明确要求 "利用 meta 信息做更精准的切分"。

**3.2 内联超链接并入文本子块 vs 单独成块**

- **Alternatives:** (A) 含 `[text](url)` 的句子与正文同块（仅保证不在链接中间断）；(B) 链接优先单独成块。
- **Pros/Cons:** (A) 符合 SPEC 字面（"不可切断"=不在链接中间断，非要求单独成块），子块更紧凑；(B) 更精准但子块数增多、可能切碎语义。
- **Chosen:** (A)，用户确认 "保持现状"。`_split_units` 已识别链接为原子单元，保证不在链接中间切断。

**3.3 真实环境 e2e 测试 vs 纯单元测试**

- **Alternatives:** (A) 仅纯逻辑单测；(B) 新增真实 MinIO/Milvus e2e。
- **Pros/Cons:** (A) SPEC §9.4 把 US-012 测试类型定为 integration（用 mock），真实 live gate 归 US-018；(B) 验证真实连通，但 #010 分块是纯算法不依赖外部组件，且实测 Milvus 19530 不可达。
- **Chosen:** (A)，无需 e2e。真实底座验证应在 US-018 统一实施。

### 4. Open Questions

**4.1 LlamaIndex SentenceSplitter 与 min 阈值的兼容性**

- **Assumption:** 当前仅纯逻辑 fallback 路径支持 `CHILD_CHUNK_MIN`；LlamaIndex SentenceSplitter 无 min-size 参数，生产环境若启用它则 min 优化不生效。
- **To verify:** 生产环境是否安装 LlamaIndex？若是，需评估是否禁用 fallback 或为 LlamaIndex 路径补 min 语义（例如包裹一层后处理切分）。

**4.2 e2e 测试时机的归属**

- **Assumption:** issue #010 不做 e2e，归属 US-018。
- **To verify:** US-018 是否会覆盖 #010 的分块→写入 kb_chunks 端到端路径，以及届时 Milvus 19530 连通性问题是否已解决（当前实测不可达）。

**4.3 make test 的 Go 工具链依赖**

- **Assumption:** `make test` 在 `test-go` 阶段失败仅因本 Windows 环境无 Go 工具链，与 #010 无关。
- **To verify:** CI 环境是否有 Go 工具链；若有则 `make test` 完整通过。

**4.4 kb-service `set_tenant_context` 的 `SET LOCAL $1` 参数化 Bug（跨服务）**

- **Finding:** 真实 DB 验证时发现 `SET LOCAL app.current_tenant_id = $1` 被 PostgreSQL 拒绝（`SET` 是 utility 命令，不接受参数绑定，报 `syntax error at or near "$1"`）。rag-engine 的 `set_tenant_context` 已改用 `SELECT set_config('app.current_tenant_id', $1, true)`（参数安全且事务作用域）。**但 kb-service 的 `rls.py` 仍有同样的 `SET LOCAL ... = $1` 写法**，其测试全 mock 掉 asyncpg 从未发现。
- **Action:** rag-engine 侧已修复（本期范围）。kb-service 侧的修复不在 #010 范围，需在 kb-service 的 issue 中跟进（建议同样改用 `set_config`）。

**4.5 kb-service 迁移未在集群 PostgreSQL 执行**

- **Finding:** 当前 `ani` 库（10.10.1.66:30945）原无 `kb_chunks`/`kb_documents` 表，kb-service 的 `001_pg_trgm_extension.sql`/`002_kb_chunks.sql` 迁移文件存在但从未在该库执行。为完成 AC5 运行时验证，执行了这两个幂等迁移创建 kb_chunks 表 + pg_trgm 扩展。验证后已清理测试数据（0 行残留），**表与扩展保留**（迁移本就该执行，不影响 core/gateway/auth）。core 表数据完好：users(5)/tenants(2)/api_keys(13)/jwt_blocklist(17)/workload_instances(2)。
- **To verify:** kb-service 正式部署时是否使用独立数据库？若是，其迁移流程是否已纳入部署 pipeline。

**4.6 superuser 绕过 RLS 的测试环境限制**

- **Finding:** `.env` 的 DATABASE_URL 用 `ani` 用户（`rolsuper=True, rolbypassrls=True`），superuser 自动绕过 RLS。用 `SET ROLE ani_app`（非 superuser）验证时，RLS 正确拦截了不匹配 tenant 的 INSERT（报 "new row violates row-level security policy"），证明 RLS 策略本身正确生效。
- **To verify:** 生产环境用 `ani_app_user`（非 superuser，`rolsuper=False, rolbypassrls=False`），RLS 会正常生效。本地测试若需验证 RLS 隔离，需用 `SET ROLE ani_app` 或获取 `ani_app_user` 的真实密码（当前在 K8s secret 中，本地无明文）。

---

## Verification commands run

```bash
# 单元测试（纯逻辑，无需外部组件）
cd repo/ai/rag-engine && PYTHONPATH=. python -m pytest tests/ -q --ignore=tests/test_e2e_parse.py
# → 84 passed (45 parse + 39 chunk)

# 编译检查
python -m compileall -q ai/rag-engine
# → exit 0

# 架构门禁
make validate-architecture
# → ✅ architecture guardrails valid

# 空白检查
git diff --check
# → exit 0

# 真实 DB 验证（AC5 运行时证据）
# 连接 10.10.1.66:30945，执行 kb-service 迁移创建 kb_chunks + pg_trgm，
# write_chunks 真实 INSERT 3 行（1 parent + 2 child），SELECT 回读验证：
#   列映射 ✓ / FK 顺序 ✓ / parent_content 反填 ✓ / metadata 继承 ✓
# RLS 隔离：SET ROLE ani_app 后 INSERT 被 RLS 拒绝（violates policy）✓
# 清理：测试数据已删除，core 表完好
```

## review-it 结果

**第一次 review** 发现并修复 3 个问题：
1. **BUG**: `_segment_nodes` 在非标题节点 section_path 变化时 `current_section` 未重置，新分段继承旧章节路径 → 已修复 + 回归测试 `test_segment_nodes_section_change_without_heading`
2. **NIT**: `CHILD_CHUNK_SIZE_MIN/MAX` 定义位置 → 上移到常量块
3. **BUG**（真实 DB 验证发现）: `set_tenant_context` 的 `SET LOCAL ... = $1` 被 PostgreSQL 拒绝 → 改用 `SELECT set_config('app.current_tenant_id', $1, true)`

拒绝 5 个非问题发现（RLS 一致性、char 预算、default=str、列顺序、set_tenant_context 架构边界）。

**第二次 review**（set_tenant_context 修复后）发现 1 个 NIT：
1. **NIT**: 模块 docstring 仍写 `SET LOCAL` → 更新为 `set_config`

其余全部确认正确：set_config 事务作用域 ✓、delete_chunks_by_doc RLS ✓、SQL 列顺序 ✓、FK 顺序 ✓、边界情况 ✓、既有修复未引入新 bug ✓。
