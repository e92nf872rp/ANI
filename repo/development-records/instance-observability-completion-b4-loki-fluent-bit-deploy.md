# instance-observability-completion B-4 — Loki + Fluent Bit 推荐部署示例 yaml

完成日期：2026-07-21
对应 Sprint：Sprint 15（Console Instance Observability Completion，第二轨 12-issue 计划）
批次：B-4（推荐部署示例 yaml，US-007 / FR-11/12/13/20）
对应 Issue：issue-007-loki-fluent-bit-deploy
对应 PRD US：US-007 / FR-11 / FR-12 / FR-13 / FR-20
对应 SPEC：§5.11 Loki + Fluent Bit 部署 yaml（US-007）
对应 UX：N/A（部署改动）
验证结果：`kubectl apply --dry-run=server -f` 10 个资源全部通过；三节点（dev-phys-02/03、kubercloud）Fluent Bit DaemonSet 全部 Running 0 重启；Loki `/ready` 返回 ready（200）；`/loki/api/v1/labels` 返回 `container/namespace/pod/service_name`；`query_range {namespace="kube-system"}` 返回 ovn-central/hami-scheduler 真实结构化日志（含 time/logtag/container/message/namespace/pod/stream 字段）；临时凭据文件和验证脚本已从本机与服务器清理

## 实现了什么

新增 `repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml`（485 行），包含 10 个 K8s 资源：

1. Namespace `ani-s07-observability`（显式声明，幂等 apply）
2. Secret `ani-loki-s3-creds`（S3 凭据占位值，注释说明替换来源）
3. ConfigMap `ani-loki-config`（Loki 3.6.0 单租户 + schema v13 + tsdb + 30d retention + MinIO S3 后端）
4. Deployment `ani-loki`（grafana/loki:3.6.0，带 `-config.expand-env=true` 启用环境变量展开）
5. Service `ani-loki`（3100/9095）
6. ServiceAccount `ani-fluent-bit`
7. ClusterRole `ani-fluent-bit-reader`（只读 pods/log、nodes/nodes/proxy、/metrics）
8. ClusterRoleBinding `ani-fluent-bit-reader`
9. ConfigMap `ani-fluent-bit-config`（含 fluent-bit.conf + extract_labels.lua Lua 脚本）
10. DaemonSet `ani-fluent-bit`（fluent/fluent-bit:3.2.0，显式 `-c` 指定配置路径）

yaml 头部标注「推荐示例，非必须部署」，注释说明可替换为 ES/OpenSearch（只需新增对应 adapter），并标注前置依赖（MinIO bucket 需先创建、MinIO 数据卷 emptyDir 非持久化风险需运维确认、S3 凭据需在 ani-s07-observability namespace 重建）。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml` | 新增 | Loki + Fluent Bit 完整部署示例（485 行，10 个资源） |
| `repo/development-records/instance-observability-completion-b4-loki-fluent-bit-deploy.md` | 新增 | 本笔记文件 |

## Implementation Notes

### 1. Design Decisions

#### D-1：Loki 3.x storage_config.aws 结构（s3 为 URL 字符串而非嵌套 map）

- **歧义**：Loki 2.x 文档常见 `storage_config.s3` 嵌套 map 写法，但 Loki 3.x 实际期望 `storage_config.aws` 结构，其中 `s3` 是 URL 字符串（含 host:port/bucket，不含凭据），`bucketnames`/`endpoint`/`s3forcepathstyle`/`access_key_id`/`secret_access_key` 是同级字段。SPEC §5.11 未展开具体配置格式。
- **选择**：采用 Loki 3.x 的 `storage_config.aws` 结构，`s3` 为 URL 字符串 `http://ani-s05-minio.ani-s05-objectstore:9000/ani-loki-logs`，凭据用 `${S3_ACCESS_KEY}` / `${S3_SECRET_KEY}` 占位通过 env Secret 注入。
- **理由**：
  1. 服务器实测：嵌套 map 写法报 `cannot unmarshal !!map into string`，确认 Loki 3.6.0 期望 `s3` 为字符串。
  2. 凭据用环境变量展开（`-config.expand-env=true`）而非明文写入 ConfigMap，避免凭据落到 ConfigMap（etcd）明文存储。
  3. 通过 WebSearch 确认 Loki 3.x 官方文档采用此结构。

#### D-2：Fluent Bit Lua 脚本从 Tag 提取 namespace/pod/container（而非依赖 parser）

- **歧义**：Fluent Bit 提取 namespace/pod/container 有两种路径：(a) 用 CRI parser 将日志行解析成结构化 record 字段；(b) 从 tail 的 Tag 路径分段提取。SPEC §5.11 只要求「提取 namespace/pod/container 作为 Loki label」，未明确提取来源。
- **选择**：用 Lua 脚本从 Tag 提取（Tag 形如 `kube.var.log.pods.<namespace>_<pod>_<uid>.<container>.<restart>.log`），不依赖 parser 解析日志行内容。
- **理由**：
  1. Tag 由 tail input 按 Path 自动生成，格式稳定不依赖日志内容。
  2. Loki adapter 的 LogQL 依赖 `{namespace=...,pod=...}` label 过滤实现多租户隔离（见 `loki_log_store.go:137`），label 来源必须可靠。
  3. 即使 parser 解析日志行失败（如非 CRI 格式），namespace/pod label 仍能从 Tag 正确提取，保证多租户隔离不失效。
  4. container label 虽不被 adapter LogQL 使用，但 `mapLokiStreamsToLogEntries` 会读 `stream.Stream["container"]`（`loki_log_store.go:192`），提取它保持链路完整。

#### D-3：Loki `-config.expand-env=true` 启用环境变量展开

- **歧义**：Loki 默认不展开配置文件中的环境变量，`${S3_ACCESS_KEY}` 会被当作字面量字符串。SPEC §5.11 未明确是否需要此参数。
- **选择**：在 Loki Deployment args 显式加 `-config.expand-env=true`。
- **理由**：使 storage_config.aws 中的 `${S3_ACCESS_KEY}` / `${S3_SECRET_KEY}` 能从 env Secret 注入，避免凭据明文写入 ConfigMap。服务器实测：不加此参数会导致凭据未展开，Loki 用字面量 `${S3_ACCESS_KEY}` 连接 MinIO 失败。

#### D-4：Fluent Bit 显式 `-c /etc/fluent-bit/fluent-bit.conf` 指定配置路径

- **歧义**：Fluent Bit 镜像内置默认配置（含 cpu input + stdout output），若不显式指定配置文件路径，会加载默认配置而非挂载的 ConfigMap。SPEC §5.11 未提及此参数。
- **选择**：在 DaemonSet 容器 args 显式加 `-c /etc/fluent-bit/fluent-bit.conf`。
- **理由**：服务器实测：不显式指定时 Fluent Bit 日志全是 `cpu.local` CPU metrics，没有 `kube.*` pod 日志，说明加载了镜像内置默认配置。显式指定后正确加载挂载配置并采集 pod 日志。

### 2. Deviations

#### DEV-1：删除 `varlibcontainers` hostPath 挂载（docker json-file 路径）

- **SPEC 说**：SPEC §5.11 流程图描述 Fluent Bit 采集 `/var/log/pods/*`。初期实现额外挂载了 `/var/lib/docker/containers` hostPath（docker json-file 驱动场景的历史路径）。
- **实现**：删除 `varlibcontainers` volume 和 volumeMount，只保留 `varlog`（`/var/log`）和 `config` 两个 volume。
- **原因**：当前 `[INPUT]` 只采集 `/var/log/pods/*`（CRI 格式），不引用 docker json-file 路径。containerd 节点（实测 dev-phys-02/03）上 `/var/lib/docker/containers` 不存在，该挂载是死代码。属于本次新建文件中的冗余，review-it 发现后按 Karpathy 原则三「只清理你自己制造的脏」清理。服务器实测：清理后三节点 pod 仍全部 Running 0 重启，AC8 查询无回归。

#### DEV-2：compactor 移除 `shared_store: s3`（Loki 3.x 已删除该字段）

- **SPEC 说**：SPEC §5.11 未明确 compactor 配置细节，Loki 2.x 文档常见 `shared_store: s3` 字段。
- **实现**：删除 `shared_store: s3`，保留 `delete_request_store: s3`。
- **原因**：服务器实测报 `field shared_store not found in type compactor.Config`，确认 Loki 3.x 已移除该字段（2.x 遗留）。这是配置层面的版本适配，非 SPEC 意图偏离。

### 3. Tradeoffs

#### T-1：`Inotify_Watcher false` 轮询 vs inotify 监视

- **备选方案 A**：用 inotify 监视 `/var/log/pods/*` 新文件（Fluent Bit 默认行为）。
  - 优点：实时性更好，文件创建即采集。
  - 缺点：每个节点 inotify watch 实例数受限（实测 dev-phys-02 `max_user_instances=128`），pod 重启重扫时触发 `Too many open files (errno=24)` 导致 input 初始化失败、容器 CrashLoopBackOff。
- **备选方案 B**（采用）：`Inotify_Watcher false` 改用轮询，配合 `Refresh_Interval 5`（5s 扫描一次）。
  - 优点：规避 inotify watch 限制，三节点稳定运行 0 重启。
  - 缺点：日志采集延迟最多 5s。
- **胜出原因**：稳定性优先于实时性；5s 延迟对日志查询场景可接受；服务器实测 dev-phys-02 用 inotify 模式崩溃 4 次，轮询模式 0 重启。

#### T-2：`DB /tmp/flb_kube.db` 容器可写层 vs emptyDir 持久化

- **备选方案 A**：新增 emptyDir volume 挂载到 `/tmp` 持久化 DB 文件。
  - 优点：pod 重启后 DB 保留，避免重扫。
  - 缺点：增加一个 volume；emptyDir 随 pod 删除丢失，对 DaemonSet 生命周期收益有限。
- **备选方案 B**（采用）：直接用容器可写层 `/tmp/flb_kube.db`。
  - 优点：零额外 volume；pod 重启重扫一次可接受（有 `Inotify_Watcher false` + `Refresh_Interval 5` 限流，不会触发崩溃）。
  - 缺点：pod 重启后 DB 丢失，从头重扫。
- **胜出原因**：最小复杂度；DaemonSet pod 很少重启；重扫一次的代价（短暂 fd 压力）已被 `Inotify_Watcher false` 限流控制；符合 Karpathy 原则五「如无必要，勿增实体」。

#### T-3：`Parsers_File` 引用镜像内置路径 vs ConfigMap 自定义 cri parser

- **备选方案 A**：在 ConfigMap 里自定义 cri parser，完全自包含不依赖镜像内置文件。
  - 优点：不依赖镜像内置 `parsers.conf` 路径，换镜像版本不会失效。
  - 缺点：增加 ConfigMap 复杂度；需维护 cri parser 正则；与镜像内置 parser 重复。
- **备选方案 B**（采用）：`Parsers_File /fluent-bit/etc/parsers.conf` 加载镜像内置 parsers.conf（含 cri parser）。
  - 优点：零维护成本；cri parser 由 Fluent Bit 官方维护；ConfigMap 更简洁。
  - 缺点：依赖镜像内置文件路径（`/fluent-bit/etc/parsers.conf`，已通过 Dockerfile 确认）。
- **胜出原因**：用能解决问题的最小代码（Karpathy 原则二）；镜像内置路径稳定（Dockerfile 显式 COPY）；ConfigMap 挂载到 `/etc/fluent-bit` 不覆盖 `/fluent-bit/etc`，内置文件仍存在。

### 4. Open Questions

#### OQ-1：MinIO 数据卷 emptyDir 非持久化风险

- **假设**：当前 MinIO 数据卷是 emptyDir（见 `sprint13-objectstore-minio-live.yaml`），MinIO pod 重启会丢数据，Loki chunk 随之丢失。yaml 头部已标注此前置依赖（AC7），但未给出具体迁移方案。
- **待用户确认**：运维是否接受此风险？若不接受，应先改 MinIO 用 PVC（超出本 issue 范围）。
- **可能后续**：由运维决策，可能另开 issue 改 MinIO 持久化。

#### OQ-2：Loki 单副本无高可用

- **假设**：当前 Loki Deployment `replicas: 1`，单点故障会导致日志查询不可用。PRD §7.4 性能假设对齐单体模式。SPEC §5.11 未要求高可用。
- **待用户确认**：是否需要 Loki 多副本高可用？多副本需引入 memberlist + 共享存储协调，复杂度显著增加。
- **可能后续**：若需高可用，另开 issue 评估；当前推荐示例定位为「开箱即用」非生产高可用。

#### OQ-3：S3 凭据跨 namespace 重建的运维流程

- **假设**：K8s Secret 不能跨 namespace 直接引用，需在 `ani-s07-observability` namespace 重建 MinIO 凭据（从 `ani-s05-objectstore/ani-s05-minio-root` 读取）。yaml 头部已说明此前置依赖，但未提供自动化脚本。
- **待用户确认**：是否需要提供 `kubectl get secret ... | kubectl apply -n ...` 自动化重建命令？当前注释只说明来源和必要性。
- **可能后续**：若运维需要，可在同级文档补充重建命令；本 yaml 保持纯部署示例定位。

#### OQ-4：`ani-s07-observability` namespace 自身日志的采集

- **假设**：Fluent Bit DaemonSet 运行在 `ani-s07-observability` namespace，会采集自身和 Loki pod 的日志。实测 `query_range {namespace="ani-s07-observability"}` 返回空（时间窗口内无新日志），但长期会有日志。
- **待用户确认**：是否需要排除 `ani-s07-observability` 自身日志避免循环采集？当前未配 `Exclude_Path`。
- **可能后续**：若循环采集造成问题，可在 `[INPUT]` 加 `Exclude_Path /var/log/pods/ani-s07-observability_*/*.log`。

## 验证命令

```bash
# yaml 语法与服务端 dry-run（需 kubectl 连接到真实集群）
kubectl apply --dry-run=server -f repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml

# 部署后验证（AC8）
kubectl wait deployment/ani-loki -n ani-s07-observability \
  --for=condition=ready --timeout=120s
# 返回 deployment condition met

kubectl run tmp --image=busybox:1.36 --restart=Never --rm -i \
  --namespace=ani-s07-observability --command -- \
  wget -qO- http://ani-loki.ani-s07-observability:3100/ready
# 返回 ready（200）

kubectl run tmp --image=busybox:1.36 --restart=Never --rm -i \
  --namespace=ani-s07-observability --command -- \
  wget -qO- http://ani-loki.ani-s07-observability:3100/loki/api/v1/labels
# 返回 ["container","namespace","pod","service_name"]

# query_range（需带 start/end 时间戳，URL 编码 LogQL）
NOW=$(date +%s)000000000; START=$((NOW-3600000000000))
kubectl run tmp --image=busybox:1.36 --restart=Never --rm -i \
  --namespace=ani-s07-observability --command -- \
  wget -qO- "http://ani-loki.ani-s07-observability:3100/loki/api/v1/query_range?query=%7Bnamespace%3D%22kube-system%22%7D&limit=3&start=$START&end=$NOW"
# 返回 stream 结构化日志（含 namespace/pod/container label + JSON 日志行）

# DaemonSet 状态
kubectl get pods -n ani-s07-observability -l app.kubernetes.io/name=ani-fluent-bit -o wide
# 三节点全部 Running 0 RESTARTS
```

## AC 满足情况（8/8）

- [x] 新增文件 `repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml`，包含 Namespace、Loki Deployment/Service/ConfigMap/Secret、Fluent Bit DaemonSet/ServiceAccount/ClusterRole/ClusterRoleBinding/ConfigMap（10 个资源）
- [x] Loki 镜像 `grafana/loki:3.6.0`，单租户模式（`auth_enabled: false`），后端 MinIO S3（`bucketnames: ani-loki-logs`，`endpoint: ani-s05-minio.ani-s05-objectstore:9000`，`s3forcepathstyle: true`）
- [x] Loki schema v13 + tsdb store，`retention_period: 30d`
- [x] Fluent Bit 镜像 `fluent/fluent-bit:3.2.0`，DaemonSet 部署，采集 `/var/log/pods/*`，提取 namespace/pod/container 作为 Loki label（Lua 脚本从 Tag 提取）
- [x] yaml 头部明确标注「推荐示例，非必须部署」，注释说明可替换为 ES/OpenSearch（只需新增 adapter）
- [x] S3 凭据 Secret `ani-loki-s3-creds` 用占位值 `<REPLACE_WITH_MINIO_ACCESS_KEY>`，注释说明部署前必须替换为 MinIO 真实凭据（来自 `ani-s05-minio-root`）
- [x] yaml 头部标注前置依赖：MinIO bucket `ani-loki-logs` 需先创建、MinIO 数据卷 emptyDir 非持久化风险需运维确认
- [x] 部署后验证命令在 yaml 注释中给出：`kubectl wait ... /ready` 返回 200、`curl /loki/api/v1/labels` 返回 namespace/pod label、`query_range` 可查到指定 pod 日志（服务器实测全部通过）

## 后续依赖

本 Issue 是 B-4 推荐部署示例，与上游 issue 的依赖关系：

- 依赖 issue-005（LokiLogStore adapter）已合入：adapter 通过 LogQL `{namespace=...,pod=...}` 查询，与本 yaml 的 Fluent Bit namespace/pod label 提取对齐
- 依赖 issue-006（runtime 注入）已合入：环境变量 `INSTANCE_OBSERVABILITY_LOG_STORE=loki` + `INSTANCE_OBSERVABILITY_LOKI_URL=http://ani-loki.ani-s07-observability:3100` 与本 yaml 的 Service 名/端口对齐
- 运维前置依赖：MinIO bucket `ani-loki-logs` 需先创建（实测用 `mc mb` 在 MinIO pod 内创建）；S3 凭据需在 `ani-s07-observability` namespace 重建
