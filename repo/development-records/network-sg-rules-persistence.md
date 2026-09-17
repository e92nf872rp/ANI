# IN-NETWORK-SG-RULES-PERSISTENCE-A · 安全组规则明细持久化 + 删除收口

- 日期：2026-09-17
- 分支：`fix/network-sg-vpc-and-instance-filter`
- 来源：用户前端实测发现两处——① 安全组详情页「安全组规则加载失败」，`GET /api/v1/networks/security-groups/sg_5a811a20-b701-4f08-abf8-7b5da5f04ee3/rules?limit=100` 返回 404 `capability resource not found`；② 删除安全组报错，`DELETE /api/v1/networks/security-groups/sg_0d98ac92-add6-4b2c-8700-98bc73447141` 返回 404 同错误
- 类型：Feature batch（DB schema 变更 + 持久化能力补齐）
- 镜像：`dev-20260917-sgrules`（规则持久化）→ `dev-20260917-sgdel`（删除收口）

## 背景与根因

安全组-3/4 批次（IN-NETWORK-SG-VPC-AND-INSTANCE-FILTER-A）把安全组本体的 List/Get store 化后，详情页头部能正常打开，但规则列表仍 404。排查确认三层事实：

1. **规则明细从未持久化**：无 `network_security_group_rules` 表，规则 CRUD（List/Get/Create/Update/Delete）五个 service 方法全部只操作网关内存 map `s.securityGroupRules`；`network_security_groups.rules` JSONB 仅存不含 `rule_id` 的摘要（Priority/Direction/Protocol/PortRange/CIDR/Action 六字段）。
2. **存在性校验 memory-only**：`ListSecurityGroupRules` 经 `securityGroupExistsLocked` 只查内存 `s.securityGroup` map，网关重启后为空 → 对任意安全组的规则查询一律 404。
3. **写路径同样受损**：重启后 `CreateSecurityGroupRule` 因内存无该安全组直接 404，规则无法新建。同源的还有 `DeleteSecurityGroup`（存在性校验同样 memory-only，重启后删除历史安全组 404，为安全组-3/4 批次记录过的存量问题，本批一并收口）。

触发条件：部署 VPC-4 修复（`dev-20260916-vpc4`）重启网关后，DB 历史安全组（如 `sg_5a811a20`，rules JSON 有 3 条摘要）的规则列表即报"安全组加载失败"；删除历史安全组同样 404。

## 方案

与安全组-3/4、VPC-4 同模式 store 化：

- 新表 `network_security_group_rules` 承载规则明细（含 rule_id/description/时间戳），RLS/GRANT 对齐既有 network_* 表；
- 迁移内从 `network_security_groups.rules` JSONB **回填存量规则明细**（摘要无 rule_id，统一重新生成 `sgr_<uuid>`；以安全组为粒度 NOT EXISTS 防重，幂等）；
- service 五个方法双分支：store 模式走持久层（存在性校验复用 `resolveSecurityGroupExists`），内存模式保持原逻辑；
- 规则变更后从明细重建摘要回写 `network_security_groups.rules` 并同步内存缓存，保持 Get/ListSecurityGroup 返回的 `record.Rules` 一致；
- `DeleteSecurityGroup` store 化：`store.GetSecurityGroup` 存在性校验 → **级联清理规则明细**（新 `DeleteSecurityGroupRules`，按 SG 全量 DELETE，防孤儿累积——内存时代规则重启即消失无此问题，明细持久化后必须补上）→ 置 deleted 落库（摘要同步清空）→ 同步内存缓存。

## 实现

| 文件 | 变更 |
|---|---|
| `deploy/migrations/20260917010000_network_security_group_rules.sql` | 新建：建表（PK `(tenant_id, rule_id)`，索引 `(tenant_id, security_group_id, priority)`）+ GRANT ani_app + RLS RESTRICTIVE `tenant_isolation` + 存量回填（`jsonb_array_elements` 展开、`gen_random_uuid` 重生成 rule_id、NOT EXISTS 防重） |
| `pkg/ports/network_resources.go` | `NetworkResourceStore` 新增 `UpsertSecurityGroupRule`/`GetSecurityGroupRule`/`ListSecurityGroupRules`/`DeleteSecurityGroupRule`/`DeleteSecurityGroupRules` |
| `pkg/adapters/runtime/network_store.go` | 五方法 SQL 实现：明细表 SELECT/INSERT ON CONFLICT/DELETE（单条）/DELETE（按 SG 级联）；List 按 `priority, created_at` 排序 |
| `pkg/adapters/runtime/network_service.go` | ① `ListSecurityGroupRules`：store 分支 `resolveSecurityGroupExists` + 明细表查询 + direction/protocol 过滤保持（handler 支持该 query 参数）；② `CreateSecurityGroupRule`：store 分支 `store.GetSecurityGroup` 校验存在（修复重启后新建 404）→ 落明细 → 更新内存缓存/幂等 map → `syncSecurityGroupRulesStore`；③ `GetSecurityGroupRule`：store 分支存在性 + 明细查询；④ `UpdateSecurityGroupRule`：store 分支查旧值 → `applySecurityGroupRuleUpdate` 合并校验（提取为纯函数，内存/store 共用）→ 落库 → 同步；⑤ `DeleteSecurityGroupRule`：store 分支查旧值 → DELETE → 清缓存 → 同步；⑥ 新增 `syncSecurityGroupRulesStore`：从明细重建摘要 → UpsertSecurityGroup 持久化 JSON → 同步内存缓存（若存在）；⑦ `DeleteSecurityGroup`：store 分支存在性校验 → 级联清理明细 → 置 deleted 落库 → 同步内存缓存（修复重启后删除历史安全组 404） |
| 测试 | `fakeMetadataTx` 增 `rowBySQL`（按 SQL 关键字路由 QueryRow 行，服务一次事务查多表场景）；`assignScanValues` 补 `*int` case；新增 5 测试：store 层 Upsert/Delete SQL 断言、service 层 store 模式规则列表（含过滤）、规则删除（明细 DELETE + 摘要回写断言）、安全组删除（级联清理 + deleted 落库断言）、删除不存在安全组（404 且零写操作） |

无 OpenAPI 契约变更（rules CRUD 端点已存在且行为不变，仅实现从内存改为持久层）；无 handler 变更。

## 测试

- gofmt 触碰文件干净；`go build ./pkg/... ./services/ani-gateway/...` 通过
- runtime 包全量单测：新增 3 测试全绿；仅余 2 个与本批无关的存量 Windows symlink 失败（`TestSandboxFileScriptsRejectSymlinks`、`TestSandboxFileScriptsAllowWorkspaceOperations`，历批基线一致）
- gateway 全部包单测通过；`git diff --check` 通过

## 迁移执行与回填验证（ani-system）

迁移 `20260917010000_network_security_group_rules.sql` 于部署前经 `psql -1`（单事务）执行：

- 建表/索引/GRANT/RLS 完成
- 回填 `INSERT 0 35`（12 个存活安全组的规则明细合计 35 条）
- 指定验证：`sg_5a811a20-b701-4f08-abf8-7b5da5f04ee3` 回填 3 条，与其 rules JSON 摘要 3 条一致
- 后续补丁：初版 RLS 仅 RESTRICTIVE 导致 Live 验证 0 行，已在迁移文件中补齐两条 PERMISSIVE 并在线上库执行（`DROP/CREATE POLICY` 幂等），迁移文件对全新环境单文件可重放

## Live 验证（ani-system 镜像 dev-20260917-sgrules）

### 二次缺陷：RLS 仅 RESTRICTIVE 导致明细全 deny（已在 Live 验证中捕获并修复）

首轮部署后规则列表 200 但 0 行（DB 直查 3 行、psql 以 `SET LOCAL ROLE ani_app + set_config` 模拟同样 0 行）。对比 `pg_policies` 确认：既有 `network_security_groups` 是**三段 policy 模式**（`platform_bypass` PERMISSIVE + `self` PERMISSIVE + `tenant_isolation` RESTRICTIVE），而新表初版只建了 RESTRICTIVE 一条。PostgreSQL 行可见性规则是"**至少一条 PERMISSIVE 放行 AND 所有 RESTRICTIVE 通过**"——仅有 RESTRICTIVE 时对普通角色全部 deny（表 owner `ani` 为 superuser 绕过 RLS，psql 直查可见，掩盖了问题）。修复：迁移文件补齐两条 PERMISSIVE（`network_security_group_rules_platform_bypass`/`_self`，表达式与族内既有表逐字一致），线上库同步执行后 0 行 → 3 行。

### 实测结果（tenant-a，目标 `sg_5a811a20-b701-4f08-abf8-7b5da5f04ee3`，修复前 404）

| 步骤 | 结果 |
|---|---|
| `GET /rules?limit=100` | **200，3 条回填明细**（22/3389/icmp，回填生成 rule_id；修复前 404） |
| `GET /rules?protocol=icmp` | 200，过滤后 1 条 |
| `POST /rules`（body 带 `idempotency_key`） | 201，`sgr_0e01448d`；DB count 4，SG 摘要 `jsonb_array_length(rules)=4`（同步） |
| 同 key 重放 | 201 返回同一条 `sgr_0e01448d`（幂等语义保持） |
| `GET /rules/{id}` | 200 返回该条 |
| `PUT /rules/{id}`（priority 320→310、description 更新） | 200，字段生效 |
| `DELETE /rules/{id}` | 200 返回删除前记录 |
| 删除后 `GET /rules/{id}` | 404 `capability resource not found` |
| 清理测试规则后 | DB count=3，摘要长度=3（一致） |

healthz 200，Pod `ani-gateway-67f7d9f85d-csckx` 1/1 Running。

### 删除收口实测（镜像 dev-20260917-sgdel，目标 `sg_0d98ac92-add6-4b2c-8700-98bc73447141`）

| 步骤 | 结果 |
|---|---|
| `GET /security-groups/{id}`（删除前） | 200，state=available，rules 摘要 3 条（store 模式可读） |
| `DELETE /security-groups/{id}` | **200** 返回 deleted 记录（修复前 404 `capability resource not found`），`rules` 摘要清空为 `[]` |
| `GET /security-groups/{id}`（删除后） | 200 + deleted 记录（软删语义，与实例「显式 state=deleted 仍可查」一致；非 404） |
| `GET /security-groups?limit=100` | 10 条存活安全组，已删除的 `sg_0d98ac92` **不在列表**（List 过滤 deleted 生效） |

healthz 200，Pod `ani-gateway-cdd8bcc84-jl74w` 1/1 Running。

## Live 验证（ani-test2 镜像 test2-20260917-a）

ani-test2（`NETWORK_PROVIDER=kubeovn_rest`，NodePort 30083）部署同批代码（源码与 ani-system 的 `dev-20260917-sgdel` 相同；Docker 层缓存全命中，构建秒级完成，与 ani-system 镜像仅 tag 不同）。独立 PG（`ani-test2-postgres/ani` 库）按「先迁移后换镜像」纪律执行迁移：`20260916120000` vpc_id 列（test2 库此前缺失，已补）+ `20260917010000` 规则明细表（建表/GRANT/三段 RLS 成功；回填 `INSERT 0 0`——test2 库无存量安全组，属正常）。kubeovn_rest 分支同样构造 `NewLocalNetworkService` + `WithNetworkResourceStore`（`network_runtime.go`），本批修复对该模式生效。

实测（tenant-a，冒烟 SG `sg_2fc5e491`，验证后已清理）：

| 步骤 | 结果 |
|---|---|
| POST 建安全组 | 201 available |
| POST 规则（幂等 key） | 201，`sgr_13731832` 落明细 |
| GET 规则列表 | 200 返回 1 条（明细持久化读回） |
| GET 安全组列表 | 200，rules 摘要与明细一致（1 条） |
| DELETE 安全组 | 200 deleted 且摘要清空 |
| 删除后单查 / 列表 | 200+deleted（软删）；列表 0 条不再显示 |

部署后 ani-system 镜像校验未被误动（7.5）。

## 补充修复：创建安全组携带的预设规则不落明细（2026-09-17 第三处，镜像 test2-20260917-b）

- **现象**：前端创建安全组并选择「常用远程端口（22/3389/ICMP）」模板，创建成功后详情页入站规则为空。
- **根因**：`CreateSecurityGroup` 的内存模式会把请求携带的 `request.Rules` 逐条生成 `sgr_` 写内存明细（L665-680 旧行为），但 store 模式只把摘要写入 SG JSONB，**遗漏明细表写入**。本批迁移回填只覆盖部署前存量，部署后新建带模板规则的 SG 即出现「摘要 JSONB 有、明细表无」断层，而详情页规则列表读明细表 → 空。安全组-7 同源缺陷的最后一块写路径。
- **修复**：store 模式下 `CreateSecurityGroup` 落库 SG 摘要后，将携带规则逐条 `store.UpsertSecurityGroupRule`（priority 默认 1000，与内存行为一致）。单测新增 `TestLocalNetworkServiceCreateSecurityGroupWithRulesPersistsRuleDetails`（fake 捕获 `ruleUpsertArgs` 断言 SQL/参数），累计单测 6 新增。
- **存量数据修复**：两库重跑幂等回填（NOT EXISTS 防重）——ani-system `INSERT 0 3`、ani-test2 `INSERT 0 3`；verify 确认两库所有非 deleted SG 摘要 count = 明细 count（ani-system 7 个、ani-test2 1 个含规则 SG 全部对齐）。
- **实测（ani-test2，镜像 test2-20260917-b，构建秒级缓存命中；用户确认仅部署 test2）**：创建带 3 条模板规则的 SG → 201；**创建后立即 `GET /rules` 200 返回 3 条明细**（修复前为空）；清理 DELETE 200。ani-system 未部署该修复（仍为 `kb-20260917`，并行会话所置）——**在 ani-system 下次 gateway 部署前，ani-system 上新建带模板规则的 SG 仍会产生明细断层**，需部署后重跑回填。
- **部署教训（已入 project_memory）**：源码未变时 Docker 构建秒级（层缓存全命中），不同环境部署同一份代码直接 `docker tag` retag 即可；rollout 完成后 healthz 可能短暂 000（服务未开始监听），需轮询至 200 再实测。

> **已收口（2026-09-17）**：ani-system 已部署含 merge `origin/main 143c4fe` 的镜像 `dev-20260917-objstore2`，该修复随之生效；部署后重跑幂等回填 `INSERT 0 0`（ani-system 无断层），ani-test2 补齐 3 行，两库摘要=明细全对齐。实测创建带 3 条预设规则的安全组后 `GET /rules` 立即返回 3 条且明细落库，删除安全组后明细级联清零（回归 19 PASS / 0 FAIL）。

## 已知边界

- 规则创建幂等 map（`securityRuleIdem`）仍为进程内存：网关重启后同一 idempotency key 重试会生成新规则（与既有 VPC/子网等内存幂等 map 同一 trade-off，未扩大范围）
- 规则摘要回写为「明细全量重建 + UpsertSecurityGroup」非事务，极端并发下摘要可能短暂滞后于明细；规则操作低频，接受
- 存量回填的 rule_id 为重新生成值，与重启前内存中的旧 rule_id 不同（旧值本就随进程消亡，前端无从引用）
