# instance-observability-completion B-2 — Prometheus 新增 DCGM exporter scrape 配置

完成日期：2026-07-20
对应 Sprint：Sprint 15（Console Instance Observability Completion，第二轨 12-issue 计划）
批次：B-2（Prometheus DCGM scrape）
对应 Issue：issue-002-prometheus-dcgm-scrape
对应 PRD US：US-002 / FR-2
对应 SPEC：§5.10 Prometheus scrape 配置（US-002）
验证结果：`python scripts/validate_yaml.py ...` EXIT 0；`make test` EXIT 0；`make validate-architecture` passed；`git diff --check` passed（仅 CRLF 警告）

## 实现了什么

在 `repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml` 的 Prometheus ConfigMap `prometheus.yml` 中、现有 `kubernetes-cadvisor` job 之后，新增 `dcgm-exporter` scrape job，使 `DCGM_FI_DEV_GPU_UTIL`、`DCGM_FI_DEV_FB_USED`、`DCGM_FI_DEV_FB_TOTAL` 等 GPU 指标可被 Prometheus 采集，供 adapter GPU 分支查询。

新增 9 行（含 3 行中文注释），无删除、无对现有配置的修改。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml` | 修改 | 在 `data.prometheus.yml` 的 `scrape_configs` 下追加 `dcgm-exporter` job |

## 完工标准达成

- [x] 修改 `repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml`，在 `scrape_configs` 下新增 `job_name: dcgm-exporter`
- [x] scrape 配置使用 `static_config` 指向 `ani-dcgm-exporter.ani-system:9400`，`metrics_path: /metrics`
- [ ] 部署后 `curl http://prometheus.ani-s07-observability:9090/api/v1/query?query=DCGM_FI_DEV_GPU_UTIL` 返回非空结果 — **需人工部署后验证**（当前环境无集群访问，见「部署后手动验证步骤」）
- [x] 不修改 Prometheus ClusterRole（`static_config` 直接 HTTP 访问 Service，不需要跨 namespace RBAC）
- [x] `make validate-architecture` 通过

## Design Decisions

### 1. 使用 `static_config` 而非 `kubernetes_sd_configs`

- **歧义点**：SPEC §5.10 给出的参考配置使用 `static_config` 指向固定 Service DNS；但 Prometheus 也有 `kubernetes_sd_configs`（服务发现）方式，可自动发现带特定 label 的 pod。
- **选择**：采用 `static_config` + `targets: ['ani-dcgm-exporter.ani-system:9400']`，与 SPEC §5.10 参考完全一致。
- **理由**：
  1. DCGM exporter 是 `ani-system` namespace 中的固定单实例 Service，DNS 名 `ani-dcgm-exporter.ani-system:9400` 稳定不变，不需要服务发现。
  2. `static_config` 通过 CoreDNS 解析 Service DNS 走普通 HTTP，**不需要 API server 访问权限**，因此不需要修改 Prometheus ClusterRole（满足 AC #4）。
  3. `kubernetes_sd_configs` 需要 Prometheus 拥有 `pods` 资源的 `get/list/watch` 权限，会引入跨 namespace RBAC 变更，违反 Issue「不修改 ClusterRole」的硬约束。
  4. 与 Sprint 13 已完成的 `sprint13-gpu-inventory-dcgm-readiness.md` 记录的 DCGM exporter 部署地址完全对齐，单一真实来源。

### 2. 添加 `component: 'dcgm-exporter'` label

- **歧义点**：SPEC §5.10 参考配置中为 `static_configs` 添加了 `labels.component: 'dcgm-exporter'`，但 Issue AC 未明确要求 label。
- **选择**：按 SPEC §5.10 参考保留 `component: 'dcgm-exporter'` label。
- **理由**：
  1. 该 label 在 Prometheus target 列表和 `up` 指标中可用作 job 内子分类，便于运维排查 DCGM exporter 健康状态。
  2. 与 SPEC §5.10 参考配置完全对齐，避免与同批后续 issue-008（KubeVirt virt-handler scrape）的 relabel 风格产生不必要的差异。
  3. label 是纯添加，不影响 adapter 端 PromQL 查询（adapter 查询 `DCGM_FI_DEV_GPU_UTIL` 不依赖此 label）。

### 3. 不修改 ClusterRole / ClusterRoleBinding

- **歧义点**：Issue AC #4 明确「不修改 Prometheus ClusterRole」，但需要确认 `static_config` 真的不需要 RBAC。
- **选择**：完全不触碰 yaml 中的 `sprint13-prometheus-cadvisor-reader` ClusterRole 与 ClusterRoleBinding。
- **理由**：`static_config` 是 Prometheus 在自身进程内直接 HTTP GET 目标地址，不经过 Kubernetes API server，不涉及 `nodes`、`pods`、`nodes/metrics`、`/metrics/cadvisor` 等资源/非资源 URL 的鉴权。现有 ClusterRole 仅授权 cadvisor 所需权限，不扩展，符合最小权限原则。

## Deviations

None — 实现严格遵循 SPEC §5.10 §847-857 给出的参考 yaml 与 Issue AC 全部 5 项。SPEC 参考中的 `job_name`、`metrics_path`、`static_configs`、`targets`、`labels.component` 五个字段全部按原样落地，未做任何偏离。SPEC §5.10 同时描述了 US-008（KubeVirt virt-handler scrape），但该部分属 Issue #008 / B-5 范围，本批次不实现。

## Tradeoffs

### 1. `static_config` vs `kubernetes_sd_configs`（同 Design Decisions #1）

- **备选方案 A**（采用）：`static_config` 固定 DNS。
- **备选方案 B**：`kubernetes_sd_configs` role: pod + namespace `ani-system` + label 过滤。
- **取舍**：
  - 方案 A 优点：零 RBAC 变更，满足 AC #4；配置最小；与 SPEC 参考一致。
  - 方案 A 缺点：DCGM exporter 实例迁移或 DNS 变更需手动改 yaml（但 Service DNS 本身稳定，此场景罕见）。
  - 方案 B 优点：自动发现新 pod，但对单实例 Service 过度设计。
  - 方案 B 缺点：必须扩展 ClusterRole 授权 `pods get/list/watch`（可能需 ClusterRoleBinding 更新），违反 AC #4。
  - 选 A：最小改动 + 满足硬约束 + 与 SPEC 对齐。

### 2. 仅实现 US-002（DCGM），不一次性实现 US-008（KubeVirt）

- **备选方案 A**（采用）：本批次仅新增 `dcgm-exporter` job，不实现 `kubevirt-virt-handler` job。
- **备选方案 B**：一次性实现 §5.10 中描述的两个 scrape job（DCGM + KubeVirt）。
- **取舍**：
  - 方案 A 优点：严格遵守 Issue `## Scope` 范围（仅 US-002）和 Karpathy 原则三「只触碰必须改动的部分」；US-008 有独立 Issue #008 / B-5，由对应批次实现可独立验证 evidence。
  - 方案 A 缺点：yaml 需二次修改（但 Issue 拆分本身就是这么设计的）。
  - 方案 B 优点：一次改完，少一次 PR。
  - 方案 B 缺点：越界实现 Issue #008 范围；US-008 端到端验证依赖 `issue-009-getmetrics-vm-branch`，过早添加 scrape 会让 DCGM 的 live gate 验证与 KubeVirt 验证耦合，违反 Issue 依赖隔离原则。
  - 选 A：严格按 Issue 拆分实现，保持每个 Issue 可独立验证。

## Open Questions

1. **DCGM exporter 真实可达性需部署后 live 验证**：本批次仅在 yaml 层面完成配置，`make validate-architecture` 与 `python scripts/validate_yaml.py` 只能证明语法和架构边界正确，不能证明 Prometheus reload 后 `dcgm-exporter` target 状态为 `up` 或 `DCGM_FI_DEV_GPU_UTIL` 查询返回非空。需在真实 k8s lab 中执行「部署后手动验证步骤」中的 5 步命令并归档 evidence（建议写入 `repo/development-records/live-evidence/sprint13-instance-observability-prometheus-live-evidence.json` 的 dcgm 字段或新建独立 evidence 文件）。

2. **依赖 Issue #001（handler 传 Kind）合入后才能端到端验证 GPU 分支触发**：本批次使 DCGM 指标可被 Prometheus 采集，但 Console 指标 Tab 的 GPU 字段非 null 还依赖 `demo_instances.go` 的 `getMetrics` handler 透传 `record.Kind`（Issue #001 / B-1）。Issue #001 的代码改动当前已存在于工作区（pre-existing），`make test` 已含 `TestDemoInstanceGetMetricsHandlerPassesRecordKind` 并通过，但未提交。两个 issue 应一起 ship 或按依赖顺序 ship，以确保合入后 GPU 分支端到端生效。US-003（issue-003-gpu-adapter-e2e-verify）负责该端到端验证。

3. **DCGM exporter 是否已实际部署在 `ani-system` namespace**：Issue Description 引用 `repo/development-records/sprint13-gpu-inventory-dcgm-readiness.md` 声明 DCGM exporter 已部署在 `ani-system` namespace，地址 `ani-dcgm-exporter.ani-system:9400`。本批次未重新盘点 DCGM exporter 部署状态，假设该事实仍然有效。若 lab 中 DCGM exporter 实际未部署或地址变更，`static_config` target 会持续 down，需在 live 验证时确认。

## Verification commands run

```bash
# 1. YAML 语法校验
python scripts/validate_yaml.py deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml
# 结果: EXIT 0, YAML 语法正确

# 2. 全量测试（含依赖 #1 的 handler kind 测试）
make test
# 结果: EXIT 0, 无 FAIL（含 TestDemoInstanceGetMetricsHandlerPassesRecordKind 3/3 subtests PASS）

# 3. 架构边界
make validate-architecture
# 结果: architecture guardrails valid

# 4. 空白检查
git diff --check
# 结果: EXIT 0（仅 CRLF 警告，无空白错误）
```

## 部署后手动验证步骤（当前环境无集群访问，需运维在 real-k8s-lab 执行）

1. 应用更新后的 yaml：
   ```bash
   kubectl apply -f repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml
   ```
2. 触发 Prometheus reload 配置（deployment 已启用 `--web.enable-lifecycle`）：
   ```bash
   curl -X POST http://prometheus.ani-s07-observability:9090/-/reload
   ```
3. 验证 DCGM 指标已采集（AC #3）：
   ```bash
   curl 'http://prometheus.ani-s07-observability:9090/api/v1/query?query=DCGM_FI_DEV_GPU_UTIL'
   ```
   **期望**：返回非空结果（`data.result` 数组非空），证明 scrape job 成功采集 GPU 指标。
4. 验证 target 状态：
   ```bash
   curl 'http://prometheus.ani-s07-observability:9090/api/v1/targets'
   ```
   **期望**：`dcgm-exporter` job 的 endpoint `ani-dcgm-exporter.ani-system:9400` 状态为 `up`。
5. 端到端验证（依赖 Issue #001 合入）：创建 `kind=gpu_container` 实例，调用 `GET /api/v1/instances/{id}/metrics`，确认 GPU 字段非 null（归 Issue #003 / US-003 验证）。

## 备注

- 本次变更未触碰 Core API 契约（`repo/api/openapi/v1.yaml`），仅修改 deploy yaml，无破坏性变更。
- 本次变更未触碰 `pkg/ports/`、`pkg/adapters/`、Gateway handler、OpenAPI、SDK，属纯部署配置批次。
- 未越界实现 US-008（KubeVirt virt-handler scrape，Issue #008 / B-5 范围）或 US-007（Loki + Fluent Bit 部署，Issue #007 / B-3 范围），严格遵守 Issue `## Scope` 限定。
- 工作区存在 Issue #001（handler 传 Kind）的 pre-existing 改动（`demo_instances.go` + `demo_instances_test.go`），属本 issue 的依赖项，不是本批次引入的改动；`make test` 已含其测试并全部通过。
- 未提交，未推送，未创建 PR — 遵循 Issue 约束与 goal 命令约束，等待用户显式 `/ship-it`。
