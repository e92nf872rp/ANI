# Core 受管迁移编排（迁移 kb-service 7 表 DDL 到 Core）— Issue #027

完成日期：2026-08-11
对应 Issue：#027 core-managed-migration-kb-tables（Phase A）
依赖：#024（数据面契约），#025（port/adapter），#026（handler + 安全）
SPEC：`design-kb-persistence-to-core-datapipe` §3.2 迁移编排, §4.4
验证结果：`git diff --check` PASS，`validate_component_imports.py` PASS，`go test ./services/ani-gateway/... ./pkg/adapters/postgres/...` PASS，E2E 41/41 PASS（真实 PG 17.10）

## 实现了什么

将 kb-service 私有的 3 个建表迁移（`001_pg_trgm_extension`、`002_kb_chunks`、`003_kb_retrieval_mode`）收口为 Core 受管迁移 `deploy/migrations/20260811_001_kb_tables_managed.sql`，使 7 张 KB 业务表的 schema 由 Core 统一管控。kb-service 侧 `migrations/` 目录删除，启动不再自助建表。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `deploy/migrations/20260811_001_kb_tables_managed.sql` | 新增 | Core 受管迁移：pg_trgm 扩展 + kb_chunks 表（含 FK + 索引 + RLS）+ retrieval_mode 列 |
| `services/kb-service/migrations/001_pg_trgm_extension.sql` | 删除 | DDL 已收口到 Core 迁移 |
| `services/kb-service/migrations/002_kb_chunks.sql` | 删除 | DDL 已收口到 Core 迁移 |
| `services/kb-service/migrations/003_kb_retrieval_mode.sql` | 删除 | DDL 已收口到 Core 迁移 |

## 完工标准达成

- [x] [SPEC §3.2] pg_trgm 扩展迁移纳入 Core 受管迁移（`CREATE EXTENSION IF NOT EXISTS pg_trgm`）
- [x] [SPEC §4.4] kb_chunks / kb_retrieval_mode 等 7 表 DDL 纳入 Core 受管迁移（6 表已在 `20260501_001_init_schema.sql`，本迁移补齐 kb_chunks + retrieval_mode）
- [x] [SPEC §3.2] 迁移走 Core 受管迁移编排；`/data/tables` 保留用于后续 7 表受管变更（CREATE EXTENSION 被 handler 破坏性语句过滤器拦截，故放在 deploy migration 由 migrator 角色执行）
- [x] [SPEC §4.4] kb-service `migrations/` 目录移除，启动不再自助建表
- [x] 迁移可重复执行/幂等（全量 `IF NOT EXISTS` / `DO $$ EXCEPTION` / `DROP POLICY IF EXISTS`）

---

## 1. Design Decisions

### 1.1 CREATE EXTENSION 放在 deploy migration 而非 /data/tables

**歧义：** SPEC §3.2 要求迁移走 `POST /data/tables`（受管 DDL），但 issue #026 的 handler 破坏性语句过滤器拦截 `CREATE EXTENSION`。

**选择：** 将 `CREATE EXTENSION IF NOT EXISTS pg_trgm` 放在 Core deploy migration（由 migrator 角色执行），而非通过 `/data/tables` HTTP 端点。

**理由：** `CREATE EXTENSION` 是 superuser 操作，在受管 PG 环境中不应通过运行时 HTTP 端点执行。deploy migration 由 `ani_migrator` 角色执行，具备必要权限。`/data/tables` 端点保留用于后续 7 表的受管 DDL 变更（CREATE TABLE / ALTER TABLE / CREATE INDEX / CREATE POLICY 均不被过滤器拦截）。迁移文件头部注释明确记录了此决策。

### 1.2 kb_chunks 添加外键约束（FK）

**歧义：** 原 kb-service `002_kb_chunks.sql` 未添加任何 FK 约束。SPEC 未明确要求添加 FK。

**选择：** 在 Core 迁移中为 kb_chunks 添加 3 个 FK 约束，与 init_schema 中 `kb_documents`/`kb_sessions` 的约定对齐。

**理由：** init_schema 中 `kb_documents.kb_id` 和 `kb_sessions.kb_id` 均有 `REFERENCES knowledge_bases(id) ON DELETE CASCADE`。kb_chunks 作为同层子表应遵循相同约定，否则删除 KB 时 kb_documents 级联删除但 kb_chunks 成为孤儿行。添加的 3 个 FK：
- `kb_id → knowledge_bases(id) ON DELETE CASCADE`
- `doc_id → kb_documents(id) ON DELETE CASCADE`
- `parent_chunk_id → kb_chunks(id) ON DELETE CASCADE`（自引用）

`tenant_id` 不加 FK（与 `kb_documents`/`kb_sessions` 一致，租户隔离由 RLS 强制）。

### 1.3 双路径 FK 定义（inline REFERENCES + DO $$ ADD CONSTRAINT）

**歧义：** `CREATE TABLE IF NOT EXISTS` 不会修改已有表。brownfield 升级（表已存在但无 FK）需要额外的 `ALTER TABLE ADD CONSTRAINT`，但 PostgreSQL 不支持 `ADD CONSTRAINT IF NOT EXISTS`。

**选择：** 在 `CREATE TABLE` 内嵌 `REFERENCES`（覆盖 greenfield 全新安装）+ 3 个 `DO $$ ... EXCEPTION WHEN duplicate_object $$` 块（覆盖 brownfield 已有表升级）。

**理由：** greenfield 下 inline REFERENCES 创建 FK（自动命名与显式命名一致），DO 块抛出 `duplicate_object` 被异常处理器捕获（无害）。brownfield 下 `CREATE TABLE IF NOT EXISTS` 跳过表创建，DO 块添加缺失的 FK。两种场景均安全且幂等。

---

## 2. Deviations

### 2.1 无 BEGIN/COMMIT 事务包装

**SPEC 说：** `20260501_001_init_schema.sql` 使用 `BEGIN;`/`COMMIT;` 包裹全部 DDL。

**实现：** 本迁移未使用显式 `BEGIN`/`COMMIT` 包装。

**原因：** `20260802_001_async_tasks.sql` 同样无包装（既有先例）。全量幂等语句使部分失败可通过重跑恢复。`CREATE EXTENSION` 在 PG17 中可在事务内执行，但受管 PG 环境中通常不建议将扩展创建包在显式事务内。迁移文件注释已说明。

---

## 3. Tradeoffs

### 3.1 GIN trigram 索引 vs pg_trgm 全文检索

**备选方案：**
- A) GIN trigram 索引（`USING GIN (content gin_trgm_ops)`）— 支持 `ILIKE '%query%'` 模糊检索
- B) PostgreSQL 内置全文检索（`tsvector` + `GIN`）— 支持 `to_tsquery` 词素检索

**选择：** 方案 A（从原 `002_kb_chunks.sql` 原样继承）。

**理由：** kb-service 的 `keyword_search` 查询使用 `content ILIKE '%' || $1 || '%'` 和 `similarity(content, $1)`，依赖 pg_trgm 扩展。切换到 tsvector 会改变检索语义（词素 vs 子串匹配），超出 issue 027 的 scope（仅迁移，不改变 schema 语义）。E2E 测试验证了 GIN trigram 索引在 `SET enable_seqscan = off` 下被查询计划使用。

### 3.2 brownfield FK 添加方式：DO $$ EXCEPTION vs 纯 ALTER TABLE

**备选方案：**
- A) `DO $$ BEGIN ALTER TABLE ... ADD CONSTRAINT ... EXCEPTION WHEN duplicate_object THEN NULL; END $$` — 幂等
- B) 纯 `ALTER TABLE ... ADD CONSTRAINT` — 非幂等（重跑报错）

**选择：** 方案 A。

**理由：** issue 027 AC-5 要求迁移可重复执行/幂等。PostgreSQL 不支持 `ADD CONSTRAINT IF NOT EXISTS`，DO 块 + `duplicate_object` 异常处理是唯一幂等方案。`DO $$` 语法会被数据面 handler 的破坏性语句过滤器拦截，但本迁移通过 `psql` 执行（不走 HTTP 端点），不受影响。

---

## 4. Open Questions

### 4.1 rag-engine chunks.py docstring 引用旧迁移名

`ai/rag-engine/app/repositories/chunks.py` 第 3-4 行 docstring 仍引用 `002_kb_chunks.sql` 和 "owned by kb-service"。该文件不在 issue 027 的 code paths allowed 内（`repo/deploy/migrations/`、`repo/services/kb-service/migrations/` only）。按 Karpathy 原则三未改动，留待后续 issue（031 kb-assembly-simplify）清理。

### 4.2 `make validate-services` 完整运行的 .venv 环境问题

`make validate-services` 在本机失败，根因是 `ai/rag-engine/.venv/` 内第三方 `joblib` 测试文件含非 UTF-8 字节（gitignore 已忽略）。CI 干净环境无此问题。是否需要在 `validate_services_boundary.py` 中添加 `.venv` 排除逻辑？留待后续评估。

### 4.3 JSONB 参数编码验证

rag-engine `chunks.py` 的 `_INSERT_SQL` 对 `custom_metadata` 列传递 JSON 编码字符串（`json.dumps(...)`），依赖 asyncpg JSONB codec 注册。E2E 测试验证了 schema 兼容性，但未验证运行时 asyncpg 参数绑定。需在 issue 028/029（kb-service 数据面扩展）中确认 codec 已注册。

---

## 5. 验证命令记录

```bash
# 架构门禁
python scripts/validate_component_imports.py --root .          # PASS

# Go 测试
$env:GOCACHE="..."; go test ./services/ani-gateway/... ./pkg/adapters/postgres/... -timeout 120s  # PASS

# git diff 空白检查
git diff --check                                                 # PASS (exit 0)

# E2E 测试（真实 PG 17.10, 10.10.1.66:30945）
# 41/41 PASS — 迁移 3 次幂等执行 + 14 列 schema + 5 索引 + 3 FK + RLS + GRANT + pg_trgm 功能 + GIN 索引可用性

# review-it 审查
# 无 critical/major 发现；2 个 minor（FK 冗余 + 无事务包装）均为有意设计；3 个 false positive
```
