# PR-M5 — Metering Service 部署清单 + Live Gate 修复

完成日期：2026-08-14
对应 Sprint：以 repo/CURRENT-SPRINT.md 为准
批次类型：Feature batch（新增计量采集产品能力 + live gate 缺陷修复）
依赖批次：PR-M4

> **说明：** 本文件记录 Issue 011 部署清单的实现笔记，以及部署到真实 K8s 集群后发现并修复的系列缺陷。批次全部完成后一次性更新 README.md、CURRENT-SPRINT.md、ANI-06-开发计划.md。

---

## Issue 011: 部署清单 metering-service-live-deps.yaml

完成日期：2026-08-14
验证结果：部署清单 9 个 AC 全部满足；`make validate-architecture` 通过；`git diff --check` 通过

### 实现了什么

新增 `repo/deploy/real-k8s-lab/metering-service-live-deps.yaml`，包含 ServiceAccount + Deployment（replicas:1）+ Service，并在文件头部注释中记录 secret 创建命令。参照 `sprint13-production-auth-dex.yaml` auth-service 部署段格式。

### 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `deploy/real-k8s-lab/metering-service-live-deps.yaml` | 新增 | ServiceAccount + Deployment(replicas:1) + Service(9210) + secret 创建命令注释 |

### Design Decisions

1. **secret 创建命令写入 YAML 注释**
   - 模糊性：Issue 011 AC 未说明 secret 的创建方式文档放在哪里。
   - 选择：在 YAML 文件头部注释中记录完整的 `kubectl create secret generic ani-metering-runtime` 命令，包含三个 key（database_url / nats_url / redis_url）和真实连接地址模板。
   - 理由：部署者打开 YAML 即可看到前置步骤，无需翻阅外部文档。注释中的地址是 dev 环境模板，部署时替换为实际值。

2. **Prometheus URL 用明文 env 而非 secret**
   - 模糊性：Issue 011 AC 指定 `METERING_PROMETHEUS_URL` 为明文，但未说明原因。
   - 选择：按 AC 要求用 `value` 明文，不用 secret。
   - 理由：Prometheus 在集群内部，URL 不含凭据（`http://sprint13-prometheus.ani-s07-observability.svc.cluster.local:9090`），与 secret 级别的 DB/NATS/Redis 连接串不同。

### Deviations

None — 实现完全遵循 Issue 011 AC。

### Tradeoffs

None — 部署清单格式参照已有 auth-service 范式，无替代方案需要权衡。

### Open Questions

None。

### AC 对照

| AC | 证据 |
|---|---|
| 新增 `repo/deploy/real-k8s-lab/metering-service-live-deps.yaml` | 文件已创建 |
| ServiceAccount（name: ani-metering-service, namespace: ani-system） | YAML L17-24 |
| Deployment（replicas: 1, 强制单副本） | YAML L35 `replicas: 1` |
| Service（port: 9210 health） | YAML L115-129 |
| container.command: /opt/ani/bin/metering-service, hostPath /opt/ani/bin | YAML L56-57, L109-113 |
| env: DATABASE_URL / NATS_URL / METERING_PROMETHEUS_URL / INTERVAL=60 / HEALTH_PORT=9210 | YAML L62-77 |
| readinessProbe/livenessProbe: tcpSocket port health | YAML L83-92 |
| resources: requests 100m/128Mi, limits 1/512Mi | YAML L93-99 |
| securityContext 全部约束 | YAML L46-51, L100-104 |
| 参照 sprint13-production-auth-dex.yaml 格式 | 结构一致 |
| Typecheck/lint 通过 | make validate-architecture 通过 |

---

## Live Gate 修复：部署后真实环境缺陷修复

完成日期：2026-08-14
验证结果：metering-service 在真实 K8s 集群中成功采集并写入数据；NATS 事件监听验证通过；4 个测试包 52 个测试全部 PASS

### 实现了什么

将 metering-service 部署到真实 K8s 集群后，发现并修复了 4 个阻断性缺陷：

1. **PromQL pod 匹配失败** — collector 用 `instance_id`（`inst_xxx-uuid`）做 pod 正则匹配，但 Prometheus `pod` 标签值是 `{name}-{hash}-{hash}`，匹配失败返回 "no samples"
2. **CPU 多副本只取第一个 pod** — `rate()` 对多副本返回多条向量，`queryPrometheusScalar` 只取 `Result[0]`
3. **写入到错误 schema** — `ani_app_user` 的 `search_path` 为 `_e2e_issue025, public`，无 schema 限定的 INSERT 写入 `_e2e_issue025.metering_usage_records`
4. **RLS 阻止写入** — 修复 search_path 后，`ani_app_user` 受 RLS 约束，`app.current_tenant_id` 未设置时 INSERT 被拒绝

### 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/metering.go` | 修改 | CollectionSpec 新增 WorkloadName 字段（K8s 资源名，用于 PromQL pod 匹配） |
| `pkg/ports/instance_events.go` | 修改 | InstanceLifecycleEvent 新增 Name 字段 |
| `services/metering-service/internal/spec.go` | 修改 | buildSpec 增加 name 参数，WorkloadName = name, ResourceRef = instanceID |
| `services/metering-service/internal/consumer.go` | 修改 | 使用 event.Name 传入 buildSpec |
| `services/metering-service/internal/rebuilder.go` | 修改 | rebuilder 查询增加 name 列，传入 buildSpec |
| `pkg/adapters/metering/collectors.go` | 修改 | CPU/Mem collector 用 spec.WorkloadName 做 pod 正则匹配；CPU 查询加外层 sum() 聚合多副本 |
| `services/metering-service/internal/service/metering_collection_service.go` | 修改 | persistRecords 用 SET ROLE ani_metering_writer 绕过 RLS；清理调试日志 |
| `deploy/migrations/20260731000100_metering_usage.sql` | 修改 | 补充 GRANT ani_metering_writer TO ani_app_user |
| `deploy/real-k8s-lab/metering-service-live-deps.yaml` | 修改 | 补充 secret 创建命令注释 |

### Design Decisions

1. **CollectionSpec 新增 WorkloadName 字段区分 ResourceRef 和 Pod 匹配名**
   - 模糊性：SPEC/PRD 未预见到 Prometheus `pod` 标签值与 ANI `instance_id` 的格式差异。原始设计用 `ResourceRef`（instance_id）做 PromQL pod 匹配。
   - 选择：新增 `WorkloadName` 字段存储 K8s 资源名（Deployment/VM 的 `metadata.name`），collector 用 `WorkloadName` 做 `pod=~"^<name>(-.*)?$"` 正则匹配；`ResourceRef` 仍用 `instance_id` 做 ticker 去重和 DB 写入标识。
   - 理由：`instance_id` 是 ANI 内部 UUID（`inst_xxx-uuid`），Prometheus `pod` 标签值是 `{name}-{hash}-{hash}`，两者格式完全不同。K8s 资源名 `name` 是两者的桥梁：Pod 名以 `name` 为前缀，PromQL 正则 `^name(-.*)?$` 可匹配所有副本。

2. **CPU 查询加外层 sum() 聚合多副本**
   - 模糊性：SPEC/PRD 未指定多副本 Pod 的 CPU 聚合策略。原始 `rate(container_cpu_usage_seconds_total{...})` 对多副本返回多条向量，`queryPrometheusScalar` 只取 `Result[0]` 导致只计第一个 Pod。
   - 选择：CPU 查询加外层 `sum(rate(...))` 聚合所有匹配 pod 的 CPU 核数。
   - 理由：多副本 Pod 各自消耗 CPU，计量应聚合全部副本。Mem 查询已用 `sum()`，CPU 对齐。

3. **persistRecords 用 SET ROLE ani_metering_writer 绕过 RLS**
   - 模糊性：SPEC §3.1 和 PRD US-001 描述了 `ani_metering_writer` 角色的 BYPASSRLS 属性和 GRANT 表权限，但未明确 persistRecords 如何在运行时切换到该角色。原始实现直接用 `ani_app_user` 连接写入，受 RLS 约束。
   - 选择：在 persistRecords 事务内执行 `SET ROLE ani_metering_writer` → INSERT → `RESET ROLE`。事务提交后连接归还连接池，`RESET ROLE` 确保后续查询不受影响。
   - 理由：`SET ROLE` 后当前会话身份变为 `ani_metering_writer`，RLS 检查的是当前用户，BYPASSRLS 属性生效。这完全符合 migration 设计意图——`ani_metering_writer` 角色 BYPASSRLS 用于采集写侧跨租户写入，`ani_app_user` RLS 用于读侧租户隔离。

4. **migration 补充 GRANT ani_metering_writer TO ani_app_user**
   - 模糊性：PRD US-001 AC 列出了 `CREATE ROLE ani_metering_writer BYPASSRLS NOLOGIN` 和 `GRANT SELECT/INSERT/UPDATE/DELETE ON metering_usage_records TO ani_metering_writer`，但未提及 `GRANT ani_metering_writer TO ani_app_user`（让 ani_app_user 成为角色成员）。
   - 选择：在 migration 文件 `CREATE ROLE` 之后补充 `GRANT ani_metering_writer TO ani_app_user`，使全新环境跑 migration 时 `SET ROLE` 能直接生效。
   - 理由：`SET ROLE ani_metering_writer` 要求当前用户是该角色的成员。手动在 DB 上执行了这条 GRANT，migration 文件必须同步补充，否则全新环境部署会复现同样的 RLS 拒绝写入问题。

### Deviations

1. **InstanceLifecycleEvent 新增 Name 字段（PRD 未规划）**
   - PRD 规定：US-002 AC 列出的 `InstanceLifecycleEvent` 结构包含 InstanceID/TenantID/WorkloadKind/NewStatus/EventSeq/GPUSpec/ErrorMsg，无 Name 字段。
   - 实际实现：新增 `Name string` 字段，用于 PromQL pod 匹配。
   - 理由：live gate 发现 Prometheus `pod` 标签值与 `instance_id` 格式不匹配，需要 K8s 资源名做正则匹配。这是 live gate 发现的真实缺陷修复，非设计阶段可预见。新增字段为 additive 变更，不影响既有消费者。

2. **CollectionSpec 新增 WorkloadName 字段（PRD 未规划）**
   - PRD 规定：US-002 AC 列出的 `CollectionSpec` 结构包含 ResourceRef/TenantID/WorkloadKind/Dimensions/IntervalSec/StartedAt/GPUSpec，无 WorkloadName 字段。
   - 实际实现：新增 `WorkloadName string` 字段。
   - 理由：同上，live gate 发现的真实缺陷修复。新增字段为 additive 变更。

### Tradeoffs

1. **SET ROLE 绕过 RLS vs SET LOCAL app.current_tenant_id**
   - 考虑过的替代方案：在 persistRecords 中用 `SET LOCAL app.current_tenant_id = '<tenant_id>'` 设置 RLS 上下文，让 `ani_app_user` 在 RLS 允许范围内写入。
   - 优点：不需要创建 `ani_metering_writer` 角色和 GRANT 成员关系，写入身份仍为 `ani_app_user`。
   - 缺点：每条记录的 tenant_id 必须与 `app.current_tenant_id` 一致，但 persistRecords 接收的 records 可能包含不同租户的数据（虽然当前实现按单租户批量写入，但语义上限制了跨租户能力）；且 `SET LOCAL` 只在事务内生效，与 `SET ROLE` 的事务隔离语义一致但更脆弱（依赖 GUC 而非角色属性）。
   - 选择理由：`SET ROLE ani_metering_writer` 直接利用 migration 已设计的 BYPASSRLS 角色，语义清晰——采集写侧绕过 RLS 跨租户写入，读侧 RLS 正常生效。与 migration 设计意图完全一致，不引入新的 GUC 依赖。

2. **WorkloadName 字段 vs 用 instance_id 推导 pod 名**
   - 考虑过的替代方案：不新增 `WorkloadName` 字段，在 collector 中用 `instance_id` 查 DB/K8s API 推导 pod 名。
   - 优点：不修改 port 结构。
   - 缺点：collector 引入 DB/K8s API 依赖，增加网络调用和耦合度；且 `instance_id` 到 pod 名的映射关系不确定（取决于命名规则）。
   - 选择理由：事件 payload 已包含 `name`（K8s 资源名），直接传入 collector 做 PromQL 匹配最简单。新增一个 additive 字段比引入运行时推导更符合奥卡姆剃刀。

### Open Questions

1. **collectFullLifetime 的 CPU/Mem 维度何时完善**
   - 继承自 PR-M4 的 open question，本次未解决。`collectFullLifetime` 只处理 GPU 维度，CPU/Mem 分支注释标注"PR-M2 接入后完善"。
   - 建议：后续迭代中完善 `collectFullLifetime` 的 CPU/Mem 分支。

### 验证命令

```bash
# 单元测试（禁用缓存）
cd repo
go test -count=1 ./services/metering-service/internal/...          # 4 包 52 测试 PASS
go test -count=1 ./pkg/adapters/metering/...                         # PASS

# 架构校验
python scripts/validate_component_imports.py --root .               # 通过
git diff --check                                                     # 通过

# 真实环境验证（手动）
# 1. 部署到 K8s 集群
# 2. 确认 metering_usage_records 表有数据写入（public schema）
# 3. 用 nats-box 发布测试事件，确认 consumer 监听到并启动采集
# 4. kubectl logs 无错误
```

### AC 对照（Live Gate 修复部分）

| 缺陷 | 根因 | 修复 | 验证 |
|---|---|---|---|
| PromQL 返回 "no samples" | collector 用 instance_id 做 pod 正则匹配，Prometheus pod 标签值是 {name}-{hash}-{hash} | CollectionSpec 新增 WorkloadName，collector 用 WorkloadName 做正则匹配 | kubectl logs 无 "no samples" 错误 |
| CPU 多副本只取第一个 pod | rate() 返回多向量，queryPrometheusScalar 只取 Result[0] | CPU 查询加外层 sum() 聚合 | 多副本实例 CPU 数据正确聚合 |
| 写入到错误 schema (_e2e_issue025) | ani_app_user 的 search_path 为 _e2e_issue025, public | ALTER ROLE ani_app_user SET search_path TO public（手动执行） | 数据写入 public.metering_usage_records |
| RLS 阻止写入 | ani_app_user 受 RLS 约束，app.current_tenant_id 未设置 | persistRecords 用 SET ROLE ani_metering_writer 绕过 RLS + GRANT ani_metering_writer TO ani_app_user | INSERT 成功，无 RLS 错误 |

---

## NATS 事件监听验证

完成日期：2026-08-14
验证结果：用 nats-box 发布测试 instance.created 事件，metering-service 成功收到并启动采集

### 验证方式

在 K8s 集群中部署 nats-box pod，使用 `nats pub` 命令向 `ani.events.instance.created` subject 发布一条测试事件（JSON payload + tenant-id header），确认 metering-service 日志中收到事件并调用 StartCollection。

### 验证结论

- NATS JetStream 订阅链路正常（Subscribe → handleEvent → StartCollection）
- seenSeq 乱序过滤机制正常
- 租户上下文校验（header tenant-id vs payload tenant_id）正常
