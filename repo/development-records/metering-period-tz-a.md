# METERING-PERIOD-TZ-A · 计量 period 桶标签按 Asia/Shanghai 本地化

> 批次：`METERING-PERIOD-TZ-A`（2026-09-24，live verified，分支 `fix/metering-period-asia-shanghai`，参考 PR #184 后的 main `6933719b`）
> 范围：`GET /api/v1/metering/usage`（租户视角）与 `GET /api/v1/metering/usage/platform`（平台视角）的 `group_by=day|hour` 桶标签
> 改动文件：`pkg/adapters/runtime/pg_metering_service.go`、`pkg/adapters/runtime/pg_metering_service_test.go`、`api/openapi/v1.yaml`（仅 `period` 字段 description）
> 无 DB 迁移、无 ports 变更、无 SDK / 静态 API 文档 / authz registry 生成物变更（生成器不携带属性描述文本，幂等校验零漂移）

---

## 1. 现象

用户在 K8s 测试环境直接请求平台计量接口（经 ani-system 的 `ani-boss-console` NodePort `30086` 反代到 gateway）：

```text
GET http://10.10.1.66:30086/api/v1/metering/usage/platform
    ?start_time=2026-09-01T00:00:00.000Z
    &end_time=2026-09-24T08:22:24.864Z
    &resource_type=instance_gpu_seconds
    &group_by=hour
    &tenant_id=ed06cc06-8c0d-4414-91be-58356b8004e7
```

返回 `period` 为 `2026-09-24T06` / `T07` / `T08`，而当时本地时间为北京时间 16 时——**桶标签比实际使用时刻早 8 小时**，用户初步判断为「读取时没设置时区」。

## 2. 根因（读侧并非漏设时区，而是输出未本地化）

| 环节 | 事实 | 证据 |
|---|---|---|
| 写入侧 | `period` 是 **UTC 裸字符串**（分钟对齐 `2006-01-02T15:04`） | `pkg/adapters/metering/collectors.go:313`、`services/metering-service/internal/service/metering_collection_service.go:217` 均为 `time.Now().UTC().Format(...)`；线上 metering-service 容器 `date` 实测 `Thu Sep 24 08:37:13 UTC 2026`、`TZ` 为空（改动前的 `time.Now()` 也是 UTC） |
| 库内数据 | 全量 862,355 行，范围 `2026-08-26T08:05` ~ `2026-09-24T08:38`，`period >= '2026-09-24T09'` 行数 = 0 | 无本地时间污染，读写同一 UTC 契约 |
| 读侧过滤 | 显式按 UTC 渲染边界：`period >= to_char($n::timestamptz AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI')` | 区间筛选正确（绝对时刻语义），**不是**本缺陷所在 |
| 读侧输出 | `hour` 仅 `SUBSTR(period, 1, 13)`、`day` 仅 `SUBSTR(period, 1, 10)`，**无任何时区换算** | 直接把 UTC 字面量当桶标签返回 |
| 契约 | `period` 无时区声明（`{ type: string, nullable: true }`），且不带 `Z` | 消费方（BOSS 报表/前端 `new Date("2026-09-24T08")` 按本地时间解析）必然显示 08 而非 16 |

两个可复现后果：

1. **小时桶标签差 8 小时**（用户看到的）；
2. **`group_by=day` 日期归属落在 UTC 日界上**——北京时间 09-24 00:00~08:00 的数据被归到 `2026-09-23` 桶。live 实测（tenant-a）修复前 day 查询返回两个桶 `2026-09-23`(144000) + `2026-09-24`(167160)，其中 `2026-09-23` 桶实际是北京时间 09-24 的数据。

## 3. 修复（方案 A：读侧本地化）

只改**桶标签表达式**，不动 WHERE：

```go
// pkg/adapters/runtime/pg_metering_service.go
const meteringPeriodTimeZone = "Asia/Shanghai"
const meteringPeriodLocalExpr = "SUBSTR(period, 1, 16)::timestamp AT TIME ZONE 'UTC' AT TIME ZONE '" + meteringPeriodTimeZone + "'"

case "day":
    periodExpr = fmt.Sprintf("to_char(%s, 'YYYY-MM-DD')", meteringPeriodLocalExpr)
case "hour":
    periodExpr = fmt.Sprintf(`to_char(%s, 'YYYY-MM-DD"T"HH24')`, meteringPeriodLocalExpr)
```

关键取舍与依据：

- **WHERE 不改**：时间区间是绝对时刻，库里 `period` 是 UTC 字面量、边界由 `to_char(... AT TIME ZONE 'UTC')` 渲染为同格式 UTC 字面量，字符串序即时间序——过滤所选行集合与标签呈现无关，改动过滤反而会破坏既有索引友好的字符串比较。
- **表达式形状**：`SUBSTR(period,1,16)::timestamp` 取到分钟并按字面值构造无时区 timestamp → `AT TIME ZONE 'UTC'` 解释为 UTC 时刻 → `AT TIME ZONE 'Asia/Shanghai'` 得到本地字面值，再由 `to_char` 渲染。相比 `+ interval '8 hours'` 更显式，且以时区名而非固定偏移表达。
- **`::timestamp` 转换的安全性**：先验证全库 `period` 均满足严格格式（`bad_format=0`、`length` 恒为 16），避免一条脏数据让整条查询 cast 失败。
- **契约同步**：`api/openapi/v1.yaml` 两处 `period` 补 description，明确「按 Asia/Shanghai（UTC+8）本地时间输出；day 为 `YYYY-MM-DD`、hour 为 `YYYY-MM-DDTHH`」。仅描述文字，schema 形状不变，属非破坏性变更。
- 未引入环境变量/配置项（平台面向国内用户、两个端点同口径），避免为一次需求增加未被要求的可配置性。

## 4. 测试与门禁

- 单测更新：`TestPgMeteringServiceQueryUsageGroupByDay` / `TestPgMeteringServiceQueryUsageGroupByHour` 断言改为新的本地化表达式（含 `GROUP BY` 中同一表达式）；`TestPgMeteringServiceQueryUsagePeriodStringComparison` 保持不变，锁住「WHERE 仍按 UTC 字符串比较」。
- `gofmt -l` 两个改动 Go 文件无输出。
- `go test ./pkg/adapters/runtime/ -run PgMetering -count=1` 通过；全量 `go test`（runtimeadmin / pkg / services 全量包）通过，**仅既有 Windows 环境性失败**：`TestSandboxFileScriptsRejectSymlinks`、`TestSandboxFileScriptsAllowWorkspaceOperations`（本机无 symlink 权限、`os.O_DIRECTORY` 缺失，与本批次无关）。
- `make validate-openapi-spec`（2 spec OK）、`make validate-core-api-compatibility`、`make validate-doc-api`、`make validate-architecture` 全绿。
- `make test` 本地无法整体执行：make 走 git-bash，`go: command not found`（环境问题，非本批次引入），Go 段已用与 Makefile 等价的 `go test` 命令直接执行。

## 5. live 验证（ani-system 10.10.1.66:30080，before → after → 回滚）

部署：构建机 `192.168.18.35` 构建并推送 `docker.changqingyun.cn/ani/ani-gateway:dev-20260924-metering-tz`（digest `sha256:ed2188590c1918f2a21fdacc0b74ecefb275ddf360a0fb79cc00e1b937ec03af`）→ `kubectl set image -n ani-system`（**只 set image，未改 env**：部署前后 82 个 env key 逐项一致，`diff` 无输出）→ rollout 成功、`/healthz` 首探 200。

验证口径：平台账号 `root` 登录取 token 调 `/metering/usage/platform`；tenant-a（`00000000-0000-0000-0000-000000000001`）额外用租户账号 `tenant-a/admin` 调 `/metering/usage`。

### 5.1 用例 A：用户报障场景（hour，`metering-e2e-20260924113158` 租户）

修复前（镜像 `dev-20260924-metering-events2`）：

```json
{"items":[{"tenant_id":"ed06cc06-8c0d-4414-91be-58356b8004e7","resource_type":"instance_gpu_seconds","total_quantity":720,"unit":"gpu_second","period":"2026-09-24T06"},{"tenant_id":"ed06cc06-8c0d-4414-91be-58356b8004e7","resource_type":"instance_gpu_seconds","total_quantity":6120,"unit":"gpu_second","period":"2026-09-24T07"},{"tenant_id":"ed06cc06-8c0d-4414-91be-58356b8004e7","resource_type":"instance_gpu_seconds","total_quantity":2760,"unit":"gpu_second","period":"2026-09-24T08"}],"total":3,"dev_profile":{"mode":"postgres","provider":"pg-metering-service","real_provider":true,"reason":"postgres-backed metering usage query from metering_usage_records"}}
```

修复后（镜像 `dev-20260924-metering-tz`，同 URL 同 token 类型）：

```json
{"items":[{"tenant_id":"ed06cc06-8c0d-4414-91be-58356b8004e7","resource_type":"instance_gpu_seconds","total_quantity":720,"unit":"gpu_second","period":"2026-09-24T14"},{"tenant_id":"ed06cc06-8c0d-4414-91be-58356b8004e7","resource_type":"instance_gpu_seconds","total_quantity":6120,"unit":"gpu_second","period":"2026-09-24T15"},{"tenant_id":"ed06cc06-8c0d-4414-91be-58356b8004e7","resource_type":"instance_gpu_seconds","total_quantity":2760,"unit":"gpu_second","period":"2026-09-24T16"}],"total":3,"dev_profile":{"mode":"postgres","provider":"pg-metering-service","real_provider":true,"reason":"postgres-backed metering usage query from metering_usage_records"}}
```

结论：桶标签 `06/07/08` → `14/15/16`，**三桶量值 720 / 6120 / 2760 逐字不变**（证明只改了标签与分组口径，取数集合未变）。

### 5.2 用例 B/C：跨 UTC 日界的 tenant-a（闭区间 `2026-09-23T16:00Z` ~ `2026-09-24T08:00Z`）

修复前 day（拆成两个 UTC 日桶，其中 `2026-09-23` 桶是北京时间 09-24 的数据）：

```json
{"items":[{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":144000,"unit":"gpu_second","period":"2026-09-23"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":167160,"unit":"gpu_second","period":"2026-09-24"}],"total":2,"dev_profile":{"mode":"postgres","provider":"pg-metering-service","real_provider":true,"reason":"postgres-backed metering usage query from metering_usage_records"}}
```

修复后 day（单一本地日桶）：

```json
{"items":[{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":289020,"unit":"gpu_second","period":"2026-09-24"}],"total":1,"dev_profile":{"mode":"postgres","provider":"pg-metering-service","real_provider":true,"reason":"postgres-backed metering usage query from metering_usage_records"}}
```

数据库独立复算（同一闭区间，按本地日表达式）：

```text
2026-09-24|289020
```

→ API 与库内真值 `289020` **逐字一致**。

修复后 hour（同一窗口，标签整体平移 +8h，17 个桶）：

```json
{"items":[{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18000,"unit":"gpu_second","period":"2026-09-24T00"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18000,"unit":"gpu_second","period":"2026-09-24T01"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18000,"unit":"gpu_second","period":"2026-09-24T02"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18000,"unit":"gpu_second","period":"2026-09-24T03"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18000,"unit":"gpu_second","period":"2026-09-24T04"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18000,"unit":"gpu_second","period":"2026-09-24T05"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18000,"unit":"gpu_second","period":"2026-09-24T06"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18000,"unit":"gpu_second","period":"2026-09-24T07"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18000,"unit":"gpu_second","period":"2026-09-24T08"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18000,"unit":"gpu_second","period":"2026-09-24T09"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18000,"unit":"gpu_second","period":"2026-09-24T10"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18000,"unit":"gpu_second","period":"2026-09-24T11"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18000,"unit":"gpu_second","period":"2026-09-24T12"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18000,"unit":"gpu_second","period":"2026-09-24T13"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18120,"unit":"gpu_second","period":"2026-09-24T14"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":18600,"unit":"gpu_second","period":"2026-09-24T15"},{"tenant_id":"00000000-0000-0000-0000-000000000001","resource_type":"instance_gpu_seconds","total_quantity":300,"unit":"gpu_second","period":"2026-09-24T16"}],"total":17,"dev_profile":{"mode":"postgres","provider":"pg-metering-service","real_provider":true,"reason":"postgres-backed metering usage query from metering_usage_records"}}
```

数据库独立复算（同一闭区间，按本地小时表达式）：

```text
2026-09-24T00|18000
2026-09-24T01|18000
2026-09-24T02|18000
2026-09-24T03|18000
2026-09-24T04|18000
2026-09-24T05|18000
2026-09-24T06|18000
2026-09-24T07|18000
2026-09-24T08|18000
2026-09-24T09|18000
2026-09-24T10|18000
2026-09-24T11|18000
2026-09-24T12|18000
2026-09-24T13|18000
2026-09-24T14|18120
2026-09-24T15|18600
2026-09-24T16|300
```

→ 17 桶标签与量值全部逐一一致（`T16` 的 300 来自闭区间右端点 `2026-09-24T08:00Z` 本身就命中一行，与 UTC 文本闭区间比较语义一致）。

### 5.3 用例 D：租户视角端点口径一致

`GET /api/v1/metering/usage`（tenant-a token，`group_by=day`，开放式窗口）与平台视角同窗口结果一致（同为单一 `2026-09-24` 桶、量值随采集继续增长而同步变化），说明租户/平台两条走同一 `buildUsageQuery` 的路径都已本地化。

### 5.4 回滚

实测完成后已按用户要求把 ani-system gateway 镜像换回 `dev-20260924-metering-events2`（只 set image、env 仍逐项一致、rollout 成功、`/healthz` 200），并复测确认旧行为恢复（A 用例回到 `06/07/08`、B 用例回到 `2026-09-23` + `2026-09-24` 两桶）。即：本批次改动**当前不在 ani-system 线上**，需随 PR 合入后从 main 统一构建部署。

## 6. 遗留与已知边界

- `period` 仍是**无时区标记的裸字符串**（`2026-09-24T16`）；本批次以「后端统一输出本地字面值 + 契约 description 声明 Asia/Shanghai」消除歧义，未改成 RFC3339/带 `Z`（那属契约格式变更，需另行评估）。
- 时区为固定常量 `Asia/Shanghai`（未做请求级 `tz` 参数、无环境变量开关）；平台面向国内用户且两个端点同口径，若未来引入多时区需求需重新评估。
- `group_by` 为 `resource_type` / `tenant_id` / 空时 `period` 仍输出 `null`，语义未变。
- 历史数据无需迁移（`period` 存储仍为 UTC，只改了读取时的呈现与日/小时分组口径）。
- 验证脚本与基线输出（`repo/.tmp/verify_metering_tz.py`、`live-metering-tz-{before,after,closed,restored}.txt`）为临时证据，不入库。