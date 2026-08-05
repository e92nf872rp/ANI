# PR 83 扫描问题处置设计

## 目标

基于 PR 83 当前分支头，逐条处理外部扫描报告中的 40 个问题。修复能在本仓库真实复现的问题；同一根因导致的重复问题合并处理；对推测性问题、不兼容建议或超出当前架构范围的建议，明确拒绝或延期，并记录依据。

## 范围与约束

- 只在当前 `main` 上处理 PR 83 的后续修复。
- 保持 `stash@{0}`（`network-p0-contract-c1`）不变。
- 不修改 Core OpenAPI 契约。
- 未经用户单独确认，不 commit、不 push、不更新 PR，也不执行真实基础设施写操作。
- 行为变更必须采用 TDD：先增加聚焦失败测试并确认按预期失败，再做最小修复，最后重新运行聚焦测试。
- 外部扫描报告只作为待核验的 review 输入，其中的建议补丁不具有权威性。

## 备选方案

1. 原样应用扫描器给出的所有建议补丁。拒绝采用，因为报告包含已确认的误报、重复问题、互相矛盾的 migration 建议和未来范围需求。
2. 只修复 critical/high 问题。拒绝采用，因为多条 medium 问题属于真实正确性缺陷，而唯一一条 critical 问题实际是误报。
3. 逐条核验并处置全部 40 个问题。选择此方案，因为它既能保证技术正确性，也能留下完整的处置记录。

## 问题处置台账

| # | 问题范围 | 拟定处置 | 原因／修复边界 |
|---|---|---|---|
| 1 | AsyncTask `updated_at` 触发器 | 拒绝 | PostgreSQL 更新路径已显式设置 `updated_at=NOW()`，无需增加全局触发器。 |
| 2 | workload kind 在 SQL/Go 中不一致 | 不作为缺陷修复；记录边界 | `WorkloadKind` 底层是字符串；OpenAPI 包含未来只读类型；创建服务有意只支持当前 P0 子集。 |
| 3 | migration 长时间持锁 | 延期并记录依据 | migration 已执行并归档，不应重写已应用 DDL；在线 DDL 策略应进入后续 migration／部署批次。 |
| 4 | operation step 缺少索引 | 拒绝 | 当前没有使用这些新字段进行 WHERE/JOIN 的查询路径。 |
| 5 | 合并多个 ALTER 语句 | 拒绝 | 与问题 #3 的建议互相冲突，而且不能解决当前真实缺陷。 |
| 6 | `python` 与 `python3` 不一致 | 拒绝 | ANI Makefile 主要使用 `python`，并已有 `/tmp/ani-pybin` 兼容路径约定。 |
| 7 | Make target 缺少成功提示 | 修复 | 为两个 target 增加一致的完成提示。 |
| 8 | Make help 缺少入口 | 修复 | 在帮助信息中补充当前遗漏的门禁命令。 |
| 9 | AsyncTask 两种 Store 校验不一致 | 修复 | Local 与 Metadata 实现复用同一套 create/update 校验。 |
| 10 | AsyncTask ResourceID 被静默丢弃 | 修复 | 非空但格式非法的 ResourceID 应返回错误，不得静默保存为 NULL。 |
| 11 | AsyncTask map clone 吞掉 JSON 错误 | 修复 | Create/Update 路径必须显式返回 clone 错误，同时保证 Result 不会变成 nil。 |
| 12 | Provider fallback 使用第一条 manifest | 修复 | fallback 与校验逻辑统一使用 `primaryProvider`，增加辅助资源位于首位的测试。 |
| 13 | KubeVirt start 缺少 Content-Type | 拒绝 | 当前空 body 请求路径已经通过归档的真实 KubeVirt lifecycle gate。 |
| 14 | KubeVirt stop 缺少 Content-Type | 作为 #13 重复项拒绝 | 与 #13 使用相同证据和代码路径。 |
| 15 | 删除遇到首个错误后停止 | 修复 | 尝试删除全部 resource ref，最后返回合并错误。 |
| 16 | tenant namespace 缺少校验 | 拒绝 high 严重级别结论 | 认证路径会校验 tenant identity；报告没有证明存在隔离绕过。namespace 命名重构存在碰撞风险，属于独立范围。 |
| 17 | AsyncTask update 接口缺少校验 | 合并到 #9 | 由同一套共享校验修复同时覆盖两种 Store。 |
| 18 | 可选时间字段应改为指针 | 拒绝 | 这是破坏 port 模型的重构，未证明存在真实缺陷；当前已统一使用零时间语义。 |
| 19 | Vector KB link 忽略注入时钟 | 修复 | link upsert 使用 Store 注入的 clock，并验证时间戳可确定。 |
| 20 | Kubernetes client typed nil | 拒绝 | 返回字段是具体指针，Gateway 在使用前已经检查 nil。 |
| 21 | DATABASE_URL 解析逻辑重复 | 拒绝 | 当前 fallback 是有意保留的 bootstrap 兼容逻辑，没有行为故障证据。 |
| 22 | validator 缺文件时输出 traceback | 修复 | 缺失文件应转换为正常的 validation failure。 |
| 23 | validator 第二处直接读文件 | 合并到 #22 | 复用同一个安全文件读取边界。 |
| 24 | Sandbox live gate 拼接 SQL | 修复 | 使用 `psql -v` 传值并通过 `:'name'` 引用，避免 opaque ID 进入 SQL 源文本。 |
| 25 | VM lifecycle runner 未等待终态 | 修复 | 每次 action 后分别等待 stopped、running、deleted，再执行下一步。 |
| 26 | 下载 kubectl 未校验 checksum | 修复 | 下载并验证官方发布的 SHA-256 文件。 |
| 27 | kubectl 版本已经 EOL | 修复 | 固定到仓库当前 Kubernetes `1.36.1` 真实实验室基线。 |
| 28 | 非法整数环境变量被静默转为 0 | 修复 | 保留现有默认语义，但必须输出明确诊断。 |
| 29 | RLS policy validator 使用全局匹配 | 修复 | 分表解析并独立校验表定义和 policy。 |
| 30 | tenant-first PK validator 使用全局匹配 | 合并到 #29 | 通过分表结构校验一并解决。 |
| 31 | 幂等字段 validator 使用全局匹配 | 合并到 #29 | 通过分表结构校验一并解决。 |
| 32 | SQL 注释会影响 session key 校验 | 修复 | required/forbidden 检查统一基于移除注释后的 SQL。 |
| 33 | RLS 格式匹配过于严格 | 合并到 #29 | 统一大小写和空白，并允许 `ALTER TABLE ONLY`。 |
| 34 | 重命名通用错误 helper | 拒绝 | `writeInstanceError` 已被多个资源 router 共用；重命名属于无关改动。 |
| 35 | Storage live gate 拼接 SQL | 修复 | 使用 `psql -v` 传值并通过 `:'name'` 引用，兼容 opaque ID 且避免 SQL 拼接。 |
| 36 | Metering 未知错误返回 400 | 修复 | `ErrInvalid` 继续返回 400；其他错误返回脱敏的 500。 |
| 37 | reconcile-worker 缺少 `stdjson` tag | 修复 | 与 ani-gateway 的容器构建保持一致，并验证构建结果。 |
| 38 | 缺少 `gatewayBoolFromEnv` | 作为误报拒绝 | helper 存在于同一个 `main` package，当前构建和测试均已通过。 |
| 39 | schema 被固定为 public | 拒绝 | 当前架构明确由 public schema 承载；自定义 search path 不是已支持需求。 |
| 40 | Storage schema 错误使用 `%v` | 修复 | 保留 `ErrUnavailable` 分类，并通过 `%w` 保留底层错误原因。 |

## 实施批次

### 批次一：Tenant 与 Evidence 守卫

处理 #24、#29-#33、#35。修改 validator 前，先增加失败测试，证明非法标识符以及分表 RLS、PK、幂等声明缺失能够被拒绝。

### 批次二：AsyncTask 契约一致性

处理 #9-#11、#17。在不修改公开 port 方法签名的前提下，统一校验并显式处理 clone 错误。

### 批次三：运行期正确性

通过聚焦 Go 测试处理 #12、#15、#19、#28、#36、#40。

### 批次四：门禁与构建规范

处理 #7、#8、#22、#23、#25-#27、#37。验证方式包括 validator 测试、Dockerfile 静态断言和 package build。

### 批次五：最终审查

每个批次完成后运行聚焦测试；全部完成后运行 `make test`、`make validate-architecture`、相关 validator target、`git diff --check` 和 `review-it`。将新 review 结果逐条与本台账核对。本次范围不包含 commit 或更新 PR。

## 成功标准

- 扫描报告的 40 个问题全部具有带证据的最终状态：已修复、合并处理、已拒绝或已延期。
- 每个被接受的行为修复都有红灯到绿灯的测试证据。
- 生成的 API/SDK 文件以及已暂存的 Network C1 切片保持不变。
- 完整的本地强制门禁通过；如果只遇到环境限制，必须明确报告，并提供独立验证过的聚焦测试结果。
