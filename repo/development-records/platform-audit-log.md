# 批次：PLATFORM-AUDIT-LOG — BOSS 平台审计日志读取接口

> 目标：在 Core 层新增只读平台审计日志查询接口 `GET /api/v1/platform/audit-logs`，数据源为 kube-apiserver Metadata 级 write 审计日志，经 fluent-bit 接流存入 Loki，接口按方案 v2 §8 的语义返回时间倒序 + 游标分页 + 近似总量 + 多 label 过滤 + 单源降级。
> 分支：`feat/platform-audit-log`（未 push）；状态：`LOCAL_VERIFIED` + K8s 测试环境实测（local 降级路径通过；**2026-09-10 真实接流已启用并端到端实测通过，`real_provider=true`**，见 §6.1/§6.2 修订；**同日 auditlog3 重构：默认直连 Loki、删除 `AUDIT_LOG_PROVIDER` 分派与 local 降级 adapter、Loki 失败直接报错，已部署 ani-test2 实测通过**，见 §10）。
> 批次类型：Feature batch（四件套已同步）；命名风格沿用 `platform-component-status-a.md`。

---

## 1. 范围（对照执行方案提示词）

只动 Core 层：OpenAPI（`repo/api/openapi/v1.yaml`）、`pkg/ports`、`pkg/adapters/runtime`、`services/ani-gateway`（router + main + 装配）、生成物（authz 注册表 + Core SDK 各语言）。**不碰数据库、frontends/boss、auth-service、Services 层。**

数据源口径（方案 §3 决策 D-1~D-8）：
- 数据源 = kube-apiserver 审计日志（Metadata 级，仅 write verb：create/update/patch/delete）。
- 操作者 = K8s 原生身份（`system:serviceaccount:*` 等），不映射 ANI 用户；不含 gateway 业务责任人台账。
- fluent-bit 与 Loki 复用 `ani-s07-observability` 命名空间既有 `ani-fluent-bit` DaemonSet + `ani-loki`，独立 `stream="kubernetes-audit"`。
- total_approx 为近似量，缺失字段给 0/空 + `dev_profile.real_provider=false`，单源失败不阻塞 200。

本批次不做（方案 §4 明确排除）：审计变更告警、合规导出/API Key 审计、gateway 业务责任人台账、多集群聚合、缓存、前端代码。

---

## 2. 契约（OpenAPI，先契约后实现）

新增 `GET /platform/audit-logs`：

| 项 | 值 |
|---|---|
| operationId | `getPlatformAuditLogs` |
| 路径 | `GET /api/v1/platform/audit-logs` |
| x-ani-rbac-scope | `scope:audit-log:read` |
| x-ani-authz | `{version: v1, resource: audit-log, action: read, boundary: platform, principal_kinds: [user]}` |
| 必填参数 | `time_from`、`time_to`（date-time，必须成对且 from≤to，否则 400） |
| 可选参数 | `user`(audit_user)、`verb`、`resource_type`(audit_resource)、`namespace`、`after`(游标)、`page_size`(默认 20 上限 100 钳制)、`keyword`(可选全文，限时间窗) |
| 200 响应 | `PlatformAuditLogResponse`：`items[]`、`next_after`、`total_approx`、`dev_profile` |
| item schema | `PlatformAuditLogItem`：`audit_id/timestamp/verb/user/resource/response_code/detail`；`PlatformAuditUser`(username/groups)、`PlatformAuditResource`(namespace/resource/name) |
| 鉴权语义 | 401 无凭证/无效 token；403 租户 token 或无权角色访问 |

未新增 path 内嵌字段；沿用 `/platform/capacity` 的 x-ani-authz 严格 5 字段（`scripts/generate_gateway_authz.py` 缺一即生成失败），并补齐 `x-ani-handler/x-ani-owner/x-ani-auth-classification/x-ani-authn` 标注（dp2 缺一失败）。

---

## 3. 代码实现（Karpathy 最小）

### 3.1 ports（`repo/pkg/ports/platform_audit.go`）
- `PlatformAuditService` 接口：`QueryAuditLogs(ctx, PlatformAuditLogQuery) (PlatformAuditLogResult, error)`
- `PlatformAuditLogQuery`：`TimeFrom/TimeTo *time.Time`、`User/Verb/ResourceType/Namespace/After/Keyword string`、`PageSize int`
- `PlatformAuditLogResult`：`Items []PlatformAuditLogItem`、`NextAfter string`、`TotalApprox int64`、`DevProfile DevProfileInfo`（`DevProfileInfo` 复用 `pkg/ports/sandbox_runtime.go`）

### 3.2 real adapter（`repo/pkg/adapters/runtime/loki_platform_audit.go`）
- 复刻 `loki_log_store.go` 的 HTTP 基建（baseURL、`/loki/api/v1/query_range`、`decodeLokiResponse`、stream 解析），LogQL/分页/过滤语义重写。
- **LogQL**：`{stream="kubernetes-audit"} | json` + 按需 label 过滤（`audit_user/audit_verb/audit_resource/audit_namespace` 精确/正则；keyword `|~` 限时间窗）。不含 node 维度（跨多 master 一次拉全）。
- **分页/排序**：`query_range` 设 `start=time_from`、`end=time_to 或 after.timestamp`、`limit=page_size+1`（多取 1 判断下页）；各 stream 结果按 `timestamp(+auditID)` 全局倒序合并后截取一页；返回末条 `(timestamp, auditID)` 作为 `after`。下页 `end=after.timestamp` 并排除该 timestamp 下已取过的 `auditID`，天然去重。
- **total_approx**：`count_over_time` 近似量。
- **降级**：构造失败/超时/Loki 非 200 → 200 + `real_provider=false` + reason，不报错。

### 3.3 local adapter（`repo/pkg/adapters/runtime/local_platform_audit.go`）
- 确定性的 2 条 write 审计假数据，按时间窗 + label 过滤裁剪；`TotalApprox` 随过滤裁剪（反映过滤后条数，与真实语义一致）；`DevProfile` Mode=local、RealProvider=false。

### 3.4 router（`repo/services/ani-gateway/internal/router/platform_audit.go`）
- handler `getPlatformAuditLogs`：解析参数（404→400 语义、time_from>time_to→400、page_size 钳制）、调用 service、透传 dev_profile。
- `registerPlatformAudit(v1, options.PlatformAuditService)`；注入为 nil 时回退 local。
- `RegisterOptions` 增加 `PlatformAuditService`（`router.go`），在 `RegisterWithOptions` 紧跟 `registerPlatformCapacity` 注册。
- 响应 `dev_profile` 复用 `coreDevProfileResponse`。

### 3.5 装配（`repo/services/ani-gateway/platform_audit_runtime.go` + `main.go`）
- `newGatewayPlatformAuditService(cfg)`：env `AUDIT_LOG_PROVIDER` 取值 `""/local/not_configured` → nil（router 回退 local）；`loki` → real adapter；`AUDIT_LOG_LOKI_URL` 默认 `http://ani-loki.ani-s07-observability:3100`。装配错误 → `ErrUnsupported`。
- 只编译进 gateway 二进制，部署只需更新 gateway。

---

## 4. 单测（本批次新增）

| 文件 | 用例数 | 覆盖要点 |
|---|---|---|
| `pkg/ports/platform_audit_test.go` | 结构断言、Query 默认值 | 类型/字段、默认语义 |
| `pkg/adapters/runtime/loki_platform_audit_test.go` | 全局倒序合并、page_size+1 下页判定、游标 timestamp+auditID 去重、LogQL 组装、total_approx、Loki 不可用/非 200 降级、非法游标/时间窗 | 真实语义与降级 |
| `pkg/adapters/runtime/local_platform_audit_test.go` | 确定性值、时间窗裁剪、total_approx 随过滤 | local 语义 |
| `services/ani-gateway/internal/router/platform_audit_test.go` | 平台 token 200、tenant token 403、无凭证 401、dev_profile 透传、time 校验 400、page_size 钳制 | 鉴权与边界 |
| `services/ani-gateway/internal/middleware/auth_test.go`（增量） | 平台 token 放行 / 租户 token 拒绝 | 平台/租户隔离红线 |

全部 PASS（详见测试报告）。`pkg/adapters/runtime` 包仅有的失败为既有 Windows 特有 sandbox 符号链接单测（`TestSandboxFileScriptsRejectSymlinks` / `TestSandboxFileScriptsAllowWorkspaceOperations`），在 clean main 同样失败，与本批次无关（Windows 无 `O_DIRECTORY`/symlink 特权）。

---

## 5. 生成物（零漂移）

- `make gen-gateway-authz` / `make gen-core-sdk`：`zz_generated_core_policies.go` + audit-log 策略（+12）、Core SDK 各语言 client.py/client.go/ApiClient.java/index.ts/index.mjs（+7）与 sdk-metadata.json（+11）。`validate_gateway_authz_drift.py` 全绿。
- **修复**：`scripts/generate_gateway_authz.py` 单行改动（`write_text(..., newline="\n")`）——原在 Windows 文本模式下生成器把 `\n` 写为 `\r\n`，导致生成产物行尾与仓库 LF 冲突、校验全文件漂移；显式 `newline="\n"` 后生成物稳定为 LF，校验收敛到真实内容差异。

---

## 6. K8s 基建实录（方案 §6）与回退

### 6.0 etcd 预检
按 `kjs-study/更新K8s测试环境的auth-service和gateway操作步骤.md` 第六节 6.0 预检 `kube-system/etcd-kubercloud` dbSize/配额，逼近 2 GiB 时先 `compact` + `defrag` 修复，再执行任何 `kubectl set image`。

### 6.1 启用 kube-apiserver 审计（master 10.10.1.66）——**2026-09-10 已启用**

目标：写 `/etc/kubernetes/audit/audit-policy.yaml`（`level: Metadata`，verbs create/update/patch/delete 放最前，中间忽略 events/leases/coordination.k8s.io 与探活路径，末尾 `level: None` 兜底压读操作），改 `kube-apiserver.yaml` static pod manifest 增加 `--audit-policy-file` / `--audit-log-path` / `--audit-log-maxsize=100` / `--audit-log-maxbackup=10` 及同名 volume + hostPath 挂载，kubelet 感知变化自动重启 apiserver。

**首轮受阻与误判**：改 manifest 后 kubelet 重建的容器始终不带 audit 参数（8 种手段无效），一度判定为「kubelet 对 manifest 编辑不敏感 / 影子 manifest 机制」。

**真根因（2026-09-10 下午定位）**：manifests 目录里遗留的 `kube-apiserver.yaml.bak-20260910091653` 备份文件不以 `.` 开头，被 kubelet `extractFromDir`（glob `[^.]*`）当作独立 manifest 解析，与主 manifest 解析出同名 pod `kube-apiserver-kubercloud`，两个 spec 在每轮 sync 中竞态，最终收敛到 bak 里的无 audit 旧 spec。

**修复**：`mv /etc/kubernetes/manifests/*.bak* /root/kube-manifest-backups/`（备份文件移出 manifests 目录），50 秒内 kubelet 重建新容器 `4e07a1b5c7c7` 带 8 个 audit flag，`/etc/kubernetes/audit/audit.log` 开始持续写入（当日 30 万+ 字节），apiserver `readyz=ok`。

**教训**：备份 static pod manifest 绝不能留在 manifests 目录里（会命中 kubelet glob 被当独立 manifest 解析）；必须移到目录外或命名 `.` 开头。

**回退**：把主 manifest 还原为原始无 audit 版（或从 `/root/kube-manifest-backups/` 恢复），kubelet 下个周期自动重建容器（约 20s~1min），确认 readyz=ok。

**端到端验证**：`kubectl create configmap ani-audit-e2e-probe` 探针事件 3 条命中 audit.log；带平台 token 调 gateway `GET /api/v1/platform/audit-logs` 返回 200 + `real_provider=true` + items 非空（详细见测试报告 §3.2）。

### 6.2 fluent-bit 审计流水线（2026-09-10 已接线）

改动 `ani-s07-observability/ani-fluent-bit`（改前备份至 `/root/ani-audit-fluentbit-backup/`）：
- ConfigMap `ani-fluent-bit-config`：`fluent-bit.conf` 追加审计 [INPUT]（tail `/etc/kubernetes/audit/audit.log`）+ [FILTER]（Lua `audit_labels` 提取 `audit_user/audit_verb/audit_resource/audit_namespace/audit_name/audit_code` 到 label）+ [OUTPUT]（Loki，`stream="kubernetes-audit"`，Labels 带脱敏维度）；`extract_labels.lua` 追加 `audit_labels` 函数（requestURI 敏感 query 置 `***`）。
- **两处 fluent-bit 3.x 配置适配**：① `[PARSER]` 段不允许出现在主配置，新增独立 key `audit-parsers.conf` + [SERVICE] 段 `Parsers_File /etc/fluent-bit/audit-parsers.conf`；② audit 时间戳带微秒精度（如 `...T12:44:31.865175Z`），`%Y-%m-%dT%H:%M:%SZ` 解析报错 → parser 简化为纯 JSON（去掉 Time_Key/Time_Format）。
- DaemonSet：新增 `k8saudit` hostPath（`/etc/kubernetes/audit`，readOnly）+ volumeMount + `securityContext.runAsUser: 0`（audit.log 仅 root 可读）。

**验证**：fluent-bit 干净启动无报错；Loki `{stream="kubernetes-audit"}` 查到带 label 审计事件；`audit_resource="configmaps"` 过滤命中探针事件。

**回退**：`kubectl apply -f /root/ani-audit-fluentbit-backup/ani-fluent-bit-config.20260910123829.yaml` + `ani-fluent-bit-ds.20260910123829.yaml`。

### 6.3 部署 gateway 到 ani-test2（禁动 ani-system）
- 构建镜像 tag `test2-20260910-auditlog2`（修复 total 语义后重建；首次 `test2-20260910-auditlog` 已弃）。
- `kubectl set image deployment/ani-gateway ani-gateway=...:test2-20260910-auditlog2 -n ani-test2` → rollout 120s，pod `ani-gateway-77885f57db-*` Running。
- **补 `AUDIT_LOG_PROVIDER=loki`（2026-09-10）**：`kubectl set env deployment/ani-gateway -n ani-test2 AUDIT_LOG_PROVIDER=loki` → 滚动成功（新 pod `ani-gateway-5678df866-fwgps`），gateway 走 loki real adapter。回退：`kubectl set env deployment/ani-gateway -n ani-test2 AUDIT_LOG_PROVIDER-`。
- ani-test2 双路径验证：local 降级（未配 env 时 T-3~T-5 通过）+ **real 接流（`real_provider=true`、T-2/T-7/T-8 通过，见测试报告 §3.2）**。

---

## 7. 验证（门禁退出码）

- `go test ./pkg/ports/...` → ok；`go test ./pkg/adapters/runtime/`（audit 用例）→ 全 PASS；`go test ./services/ani-gateway/...` → ok（middleware/router/main）。
- `python scripts/generate_gateway_authz.py`（+go fmt 后）+ `python scripts/validate_gateway_authz_drift.py` → `gateway authz registry: no drift`。
- `python scripts/gen_sdk_alpha.py` → `SDK Alpha artifacts generated`，内容零漂移。
- `git diff --check` → 无空白错误；新增/改动 go 文件全部 `gofmt` 干净。
- `make validate-architecture` 走 `scripts/run_architecture_validate.py`（Windows 直接跑脚本）→ 见测试报告。

---

## 8. 推理/差异点（简，详情见 implementation-diff 文档）

1. 生成器 `newline="\n"`（§5 修复，方案未涉及）。
2. local adapter `TotalApprox=len(items)` 随过滤裁剪，对齐真实 `count_over_time` 语义（方案未明说 local 值）。
3. **已解决（2026-09-10）**：首轮真实环境因误判「apiserver manifest 编辑受限」走 local 降级实测；后定位真根因为 manifests 目录 bak 备份文件污染（见 §6.1），真实接流已启用并以 `AUDIT_LOG_PROVIDER=loki` 验证 `real_provider=true` 端到端（T-2/T-7/T-8 全通过）。
4. 租户 token 403 在真实环境因 ani-test2 无 `/auth/tenant/password/login` 端点无法端到端复现，由单测 + middleware 红线锁定。

---

## 9. 遗留风险 / 后续

- ~~真实控制面审计接流未验证~~（**已解决，2026-09-10**：`real_provider=true` 端到端通过，见 §6.1/§6.2）。
- **运维风险**：audit.log 依赖 `maxsize=100MB/maxbackup=10` 轮转，需在监控中关注 master 节点磁盘；fluent-bit 为共享 DaemonSet，后续升级/他人改动需保留审计流水线配置（备份在 `/root/ani-audit-fluentbit-backup/`）。
- **操作规范**：备份 static pod manifest 不得留在 manifests 目录内（kubelet glob `[^.]*` 会把它当独立 manifest 解析产生同名 pod 竞态），详见 `kjs-study/平台审计日志/kube-apiserver审计接流排查记录.md`。
- total_approx 为近似量，前端不应依赖精确值做分页计数。

---

## 10. auditlog3 重构（2026-09-10）：默认直连 Loki、不降级、失败报错

> 用户决策：当前方案为过渡方案（后续将由独立审计服务替代），简化优先——**不再配置 `AUDIT_LOG_PROVIDER`，默认即查 Loki；Loki 不可用直接报错（"没有就没有了"），不再返回 local 假数据降级**。

### 10.1 代码改动
- **删除** `pkg/adapters/runtime/local_platform_audit.go` + `local_platform_audit_test.go`（local 降级 adapter 整体移除）。
- `pkg/adapters/runtime/loki_platform_audit.go`：`fetchAuditItems` 失败改为直接 `return err`（原 `degradedPlatformAudit` 降级构造函数删除）；`QueryAuditLogs` 注释同步为「失败即报错，handler 映射 5xx」。
- `pkg/ports/platform_audit.go`：删除 `ErrPlatformAuditUnsupported`（无 provider 分派后无使用方）；`PlatformAuditLogResult.DevProfile` 注释更新（real adapter 恒 `real_provider=true`）。
- `services/ani-gateway/platform_audit_runtime.go`：删除 `AUDIT_LOG_PROVIDER` switch 分派与 `LokiPlatformAuditConfig.Provider` 字段，`newGatewayPlatformAuditService()` 无参直连 `NewLokiPlatformAudit`（`AUDIT_LOG_LOKI_URL` 仍可覆盖地址，默认 `http://ani-loki.ani-s07-observability:3100`）。
- `services/ani-gateway/main.go`：装配调用简化为无参。
- `services/ani-gateway/internal/router/platform_audit.go`：删除 `service == nil → NewLocalPlatformAudit()` 回退（nil 不再回退假数据）；`router.go` 的 `RegisterOptions.PlatformAuditService` 注释更新。
- 测试反转：`TestPlatformAuditDegradedWhenLokiUnavailable/Non200` → `TestPlatformAuditErrorWhenLokiUnavailable/Non200`（断言"必须返回 error"）；router 测试删除 local 回退/过滤用例，改用 fake 注入透传断言。
- OpenAPI `repo/api/openapi/v1.yaml`：`/platform/audit-logs` description 改为「Loki 不可用或查询失败时直接返回错误（503/500，不降级不返回假数据）」；重跑 `gen_sdk_alpha.py` + `generate_api_docs.py`，`validate_generated_idempotence.py` 幂等零漂移。

### 10.2 验证
- `go test ./pkg/adapters/runtime/ -run TestPlatformAudit` 全 PASS（含反转断言）；`go test ./pkg/ports/... ./services/ani-gateway/...` ok；改动文件 gofmt 干净。`pkg/adapters/runtime` 仅剩的 2 项 sandbox 失败为既有 Windows 环境限制（`os.O_DIRECTORY` 缺失），与本批次无关。
- 构建部署：tar（pkg + services/ani-gateway）→ 构建机 192.168.18.35 解压后**显式 `rm` 已删除的 local adapter 文件**（tar 解压不删除旧文件）→ docker build/push `test2-20260910-auditlog3`。
- 部署：`kubectl set image ...:test2-20260910-auditlog3` + `kubectl set env deployment/ani-gateway -n ani-test2 AUDIT_LOG_PROVIDER-`（移除 env）→ rollout 成功，新 pod `ani-gateway-7887ff6b7c-xq9g5` Running，deployment env 校验 `AUDIT env names: []`。
- 实测（T-9a）：无任何 AUDIT env，平台 token 查询返回 `dev_profile={mode:real, provider:loki, real_provider:true}`、`total_approx=95296`、5 条真实审计 items（kube-scheduler/capk/capi/kubevirt leases update），`next_after` 正常。详见测试报告 §3.3。

### 10.3 语义变化（前端/调用方需知）
- `AUDIT_LOG_PROVIDER` 环境变量废止（读不到也不影响启动）。
- Loki 失败从「200 + `real_provider=false` + reason」改为 **500 `PLATFORM_AUDIT_FAILED`**；前端需按 5xx 展示「审计服务暂不可用」，不再需要处理 `real_provider=false` 分支。
- `dev_profile` 字段保留（契约不破坏），但恒为 `real/loki/true/""`。