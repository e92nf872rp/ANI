# ANI 数据库迁移管理

ANI 共享 PostgreSQL 数据库使用 [Atlas](https://atlasgo.io/) 的版本化迁移管理。
本目录是数据库结构变更的唯一执行入口，迁移文件和执行记录共同构成数据库的真实历史。

## 一、目录内容

- `*.sql`：按版本顺序执行的迁移文件。
- `atlas.sum`：迁移文件校验和，用于检测文件是否被意外修改。
- `atlas.hcl`：位于仓库 `repo/atlas.hcl`，定义 `local` 和 `test` 环境。
- Atlas 执行记录：保存在目标数据库的 Atlas revision 表中，用于记录哪些迁移已经执行。

## 二、迁移文件规范

### 文件命名

文件名必须使用唯一的 14 位数字版本号，并以一个下划线连接描述：

```text
YYYYMMDDHHMMSS_description.sql
```

示例：

```text
20260827000200_add_model_indexes.sql
```

要求：

- 版本号必须唯一，并且能确定执行顺序；
- 描述使用小写英文和下划线，简要说明变更内容；
- 一个迁移文件尽量只处理一个相关的结构变更；
- 迁移文件一旦在共享环境执行，不得修改、重命名或删除；
- 修复已执行迁移的问题，必须新增一个更高版本的迁移文件。

### 事务

Atlas 默认会为每个迁移文件创建独立事务。迁移文件中不要手写：

```sql
BEGIN;
COMMIT;
ROLLBACK;
```

否则可能和 Atlas 的事务管理冲突，导致 `unexpected transaction status idle` 等错误。

需要使用不可事务化语句时，必须单独评估，并使用 Atlas 支持的事务模式配置。

## 三、日常使用方式

以下命令均在 `repo/` 目录执行。

### 1. 校验迁移文件

```shell
make db-validate
```

也可以直接使用 Atlas：

```shell
atlas migrate validate --dir file://deploy/migrations
```

### 2. 查看迁移文件列表

```shell
make db-list
```

或：

```shell
atlas migrate ls --dir file://deploy/migrations
```

### 3. 查看数据库执行状态

```shell
make db-status
```

`local` 环境读取 `DATABASE_URL`，`test` 环境读取 `TEST_DATABASE_URL`：

```shell
make db-status DB_ENV=test
```

正常完成时应看到：

```text
Migration Status: OK
Next Version:    Already at latest version
Pending Files:   0
```

### 4. 预览迁移

预览不会修改数据库，只打印 Atlas 准备执行的 SQL：

```shell
make db-migrate-dry-run DB_ENV=test
```

或：

```shell
atlas migrate apply --env test --dry-run
```

### 5. 执行迁移

确认 dry-run 输出后再执行：

```shell
make db-migrate DB_ENV=test
```

或：

```shell
atlas migrate apply --env test
```

首次执行或排查问题时，可以限制只执行一个待处理文件：

```shell
atlas migrate apply 1 --env test --dry-run
atlas migrate apply 1 --env test
```

## 四、环境变量和凭据

Atlas 配置通过环境变量读取数据库连接信息：

- `DATABASE_URL`：本地环境；
- `TEST_DATABASE_URL`：测试环境。

注意：

- 不要把数据库 URL、密码、Token 提交到 Git；
- 不要把密码直接写入 migration SQL；
- PostgreSQL 角色属于实例级资源，不只属于某个数据库；
- 应用角色密码应通过 Secret、部署系统或独立的凭据初始化流程管理；
- 执行迁移的账号需要具备创建表、索引、策略、函数以及必要数据库角色的权限。

## 五、首次接管已有数据库

已有数据库不能直接假设“迁移文件没执行过”就从第一个文件开始执行。必须先确认：

1. 目标数据库和环境确实正确；
2. 数据库中是否已有业务数据；
3. 当前表、索引、约束、RLS 策略和角色与迁移目录的差异；
4. 数据库备份和恢复方案已经验证；
5. 团队已经审核基线迁移方案。

如果数据库已有数据，应先生成并审核 baseline，再使用 Atlas 的 `--baseline`。不要为了让状态显示为“已完成”而盲目 baseline；baseline 只记录版本，不会执行其中的 SQL。

如果是专用、可丢弃的测试数据库且数据不需要，可以清理后从头执行：

```shell
atlas schema clean --env test --dry-run
atlas schema clean --env test --auto-approve
atlas migrate apply --env test --dry-run
atlas migrate apply --env test
```

`schema clean` 会删除目标数据库中的 schema 对象和数据，只允许对确认过的专用测试库使用，禁止对生产库或共享数据库使用。

## 六、SQL 编写注意事项

### 分区表主键

按某列分区的表，主键或唯一约束必须包含全部分区键。例如：

```sql
CREATE TABLE audit_logs (
    id         UUID NOT NULL DEFAULT gen_random_uuid(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);
```

不能在按 `created_at` 分区的表上只声明 `PRIMARY KEY (id)`。

### CHECK 约束

PostgreSQL 不允许在 `CHECK` 约束中使用子查询。需要逐项检查 JSON 数组时，应使用不可变函数，再由 `CHECK` 调用该函数。

### 角色和授权

角色创建必须可重复执行，不能使用会因角色已存在而失败的裸 `CREATE ROLE`。不要在 SQL 中写入明文密码；角色创建和凭据设置应分离。

### 数据变更

迁移中的 `INSERT`、`UPDATE`、`DELETE` 必须说明用途，并考虑：

- 重复执行或重试是否安全；
- 是否会影响已有租户或用户数据；
- 是否需要先检查旧数据格式；
- 是否需要分批执行或设置超时；
- 是否需要配套的回滚/修复迁移。

## 七、失败处理

迁移失败后不要盲目重复执行，先查看状态：

```shell
atlas migrate status --env test
```

然后根据失败位置判断：

- 如果文件事务已回滚，修正未执行的 migration 后重新运行；
- 如果迁移文件已经执行但 revision 没有记录，先检查数据库结构，再决定清理或补偿；
- 如果前面的 migration 已成功，不能重写前面已经执行的文件；
- 如果失败发生在共享环境，必须通过新增 migration 修复，不能修改历史文件。

修复迁移文件后重新生成并校验校验和：

```shell
atlas migrate hash --dir file://deploy/migrations
atlas migrate validate --dir file://deploy/migrations
```

`migrate hash` 只能用于尚未在共享环境执行的历史文件，不能用来掩盖已执行文件被修改的问题。

## 八、提交前检查清单

- [ ] 文件名版本号唯一且顺序正确；
- [ ] 没有手写 `BEGIN/COMMIT/ROLLBACK`；
- [ ] 已执行 `atlas migrate validate`；
- [ ] 已执行 `atlas migrate hash` 并提交最新 `atlas.sum`；
- [ ] 已用测试数据库执行 dry-run；
- [ ] 已在测试数据库实际执行并确认 `Pending Files: 0`；
- [ ] 已检查权限、RLS、分区、索引和种子数据；
- [ ] 没有提交数据库凭据；
- [ ] 已将 migration、`atlas.hcl`、`atlas.sum` 和相关文档一起提交。
