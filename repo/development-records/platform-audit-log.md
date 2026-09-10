# 批次：PLATFORM-AUDIT-LOG — BOSS 平台审计日志读取接口

> 目标：在 Core 层新增只读平台审计日志查询接口 `GET /api/v1/platform/audit-logs`，数据源为 kube-apiserver Metadata 级 write 审计日志，经 fluent-bit 接流存入 Loki，接口按方案 v2 §8 的语义返回时间倒序 + 游标分页 + 近似总量 + 多 label 过滤 + 单源降级。
> 分支：`feat/platform-audit-log`（未 push）；状态：`LOCAL_VERIFIED` + K8s 测试环境实测（local 降级路径，real_provider=false）。
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

### 6.1 启用 kube-apiserver 审计（master 10.10.1.66）
目标：写 `/etc/kubernetes/audit/audit-policy.yaml`（`level: Metadata`，verbs create/update/patch/delete 放最前，中间忽略 events/leases/coordination.k8s.io 与探活路径，末尾 `level: None` 兜底压读操作），改 `kube-apiserver.yaml` static pod manifest 增加 `--audit-policy-file` / `--audit-log-path` / `--audit-log-maxsize=100` / `--audit-log-maxbackup=10` 及同名 volume + hostPath 挂载，kubelet 感知变化自动重启 apiserver。

**实测受阻与结论**：master 节点 kubelet 对 `kube-apiserver.yaml` static pod manifest 的编辑不敏感——多次改写 manifest（保持规范、触 touch、重启 kubelet、删 mirror pod）后，kubelet 仍沿用原始 apiserver spec，`/etc/kubernetes/audit/audit.log` 未产生写审计行。判定为本环境对 apiserver manifest 编辑存在限制，无法在真实集群侧验证 `real_provider=true` 的端到端接流。

**回退**：manifest 改动已撤销、备份还原，`/etc/kubernetes/audit` 目录未残留非法配置；apiserver 未变更、已确认无 audit flag 注入，集群不受影响。

**影响与兜底**：由于无法启用真实控制面审计接流，真实环境实测回退到 **local 降级路径**（`AUDIT_LOG_PROVIDER` 未配置 → local adapter，`real_provider=false`）。接口契约、过滤、分页、降级、鉴权语义已在真实 gateway + local adapter 下验证。这是「遗留风险」，见底部。

### 6.2 fluent-bit（未执行，因 6.1 受阻）
方案 §6.2 的 hostPath 挂载 + `extract_audit_labels.lua` + 独立 loki OUTPUT 流水线，因 6.1 无法产出审计数据而未改动共享 `ani-fluent-bit` DaemonSet/ConfigMap，避免影响他人。回退无需操作。

### 6.3 部署 gateway 到 ani-test2（禁动 ani-system）
- 构建镜像 tag `test2-20260910-auditlog2`（修复 total 语义后重建；首次 `test2-20260910-auditlog` 已弃）。
- `kubectl set image deployment/ani-gateway ani-gateway=...:test2-20260910-auditlog2 -n ani-test2` → rollout 120s，pod `ani-gateway-77885f57db-*` Running。
- ani-test2 已验证：`GET /api/v1/platform/audit-logs` 走 local 降级（未配 `AUDIT_LOG_PROVIDER`），实测通过（见测试报告）。

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
3. 真实环境因 apiserver manifest 编辑受限，无法启用真实控制面审计接流 → 真实接口实测走 local 降级（real_provider=false），此为环境阻塞非实现缺失。
4. 租户 token 403 在真实环境因 ani-test2 无 `/auth/tenant/password/login` 端点无法端到端复现，由单测 + middleware 红线锁定。

---

## 9. 遗留风险 / 后续

- **真实控制面审计接流未验证**：需在 APIServer manifest 可编辑的集群（或修复 kubelet 对 static pod manifest 的响应）启用 audit-policy.yaml + fluent-bit 审计流水线，然后以 `AUDIT_LOG_PROVIDER=loki` 重新部署并验证 `real_provider=true` 端到端（T-2 接流、T-7 脱敏、T-8 稳定性）。
- audit.lua 脱敏与 fluent-bit audit pipeline 未接线（随 6.1 受阻一同待后续）。
- total_approx 为近似量，前端不应依赖精确值做分页计数。