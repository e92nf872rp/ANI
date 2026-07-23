# INSTANCE-OBSERVABILITY-COMPLETION-B5-PROMETHEUS-KUBEVIRT-SCRAPE

> 批次类型：Feature batch（Prometheus 部署 yaml 新增 KubeVirt virt-handler scrape job，新增 ClusterRole pods 读权限）
> 关联 PRD：`services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md`
> 关联 SPEC：`services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md`
> 关联 Issue：`services/tasks/modules/issue/console/compute/instance-observability-completion/issue-008-prometheus-kubevirt-scrape.md`
> 关联批次：B-5（与 b5-loki-range-ca-fix 同批次，均为 instance-observability-completion 收尾）
> 完成日期：2026-07-22

## 背景

Issue-008 要求在 Prometheus 部署 yaml 中新增 `kubevirt-virt-handler` scrape job，采集 `kubevirt_vmi_*` 指标，用于 VM 实例的可观测性分支。该 issue 依赖 #1（handler 传 Kind）合入后才能端到端验证 VM 分支，但 scrape 配置本身的部署不依赖 VM 存在——virt-handler 是 KubeVirt 组件（DaemonSet），只要 KubeVirt 已部署即可被服务发现并采集。

本次改动只触碰 `repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml`，不涉及 Go 代码、OpenAPI、adapter 或前端。

## 1. Design Decisions

### 1.1 ClusterRole 新增 pods 读权限而非新建独立 Role

**Ambiguity:** Issue AC-4 要求"若 Prometheus ClusterRole 无 kubevirt namespace pods 读权限，新增对应权限（或确认已有）"。现有 ClusterRole `sprint13-prometheus-cadvisor-reader` 只有 nodes 相关权限，没有 pods 权限。SPEC §5.10 未明确是新建独立 Role 还是扩展现有 ClusterRole。

**Choice:** 在现有 ClusterRole `sprint13-prometheus-cadvisor-reader` 的 `resources` 中追加 `"pods"`，verbs 复用现有 `["get", "list", "watch"]`。

**Rationale:**
- `kubernetes_sd_configs`（role: pod）需要集群级 list/watch pods 权限才能做服务发现，这是 Prometheus 官方文档明确要求的
- 现有 ClusterRole 已是集群级（ClusterRole + ClusterRoleBinding），与 `kubernetes-cadvisor` job（role: node，需集群级 nodes 读权限）模式一致
- 新建独立 Role + RoleBinding 限定到 kubevirt namespace 看似更窄，但 `kubernetes_sd_configs` 在服务发现阶段需要枚举目标，ClusterRole 是 Prometheus 的标准做法
- 实际 scrape 范围已由 `relabel_configs`（`namespaces.names: ['kubevirt']` + `kubevirt.io=virt-handler` label + 端口 8443）限制到 kubevirt namespace 的 virt-handler pod

### 1.2 relabel_configs label key 转换：kubevirt.io → __meta_kubernetes_pod_label_kubevirt_io

**Ambiguity:** Issue AC-3 要求"relabel_configs 过滤 kubevirt.io=virt-handler label"，但未明确 Prometheus meta label 的具体写法。KubeVirt 的 label key 含点号（`kubevirt.io`），需要确认 Prometheus kubernetes_sd_configs 如何转换。

**Choice:** 使用 `__meta_kubernetes_pod_label_kubevirt_io` 作为 source_labels。

**Rationale:**
- Prometheus 官方文档规定 `__meta_kubernetes_pod_label_<labelname>` 中"any unsupported characters converted to an underscore"，即点号 `.` 转换为下划线 `_`
- KubeVirt 官方 runbook（NoReadyVirtHandler、VirtHandlerRESTErrorsHigh）多处用 `kubectl get pods -l kubevirt.io=virt-handler` 确认 virt-handler pod 确有此 label
- 端口 meta label `__meta_kubernetes_pod_container_port_number` 同样经官方文档确认

## 2. Deviations

None — 实现完全遵循 Issue AC 和 SPEC §5.10。

- AC-1：在 `scrape_configs` 下新增 `job_name: kubevirt-virt-handler` ✅
- AC-2：`kubernetes_sd_configs`（role: pod，namespace: kubevirt）+ `bearer_token_file` + `tls_config.insecure_skip_verify` + `metrics_path: /metrics` ✅
- AC-3：`relabel_configs` 过滤 `kubevirt.io=virt-handler` label 且端口为 8443 ✅
- AC-4：ClusterRole 新增 pods 读权限 ✅
- AC-6：`make validate-architecture` 通过 ✅

唯一无法在代码实现阶段验证的是 AC-5（部署后 curl 返回非空结果），这是部署后端到端验证，依赖真实集群 + VM 运行。

## 3. Tradeoffs

### 3.1 ClusterRole 扩展 vs 新建独立 Role

**Alternatives:**
- A. 扩展现有 ClusterRole 追加 pods 权限（chosen）——与现有 nodes 权限模式一致，改动最小
- B. 新建独立 Role（kubevirt namespace scoped）+ RoleBinding——权限更窄，但 `kubernetes_sd_configs` 服务发现需要集群级枚举权限，Role 限定 namespace 会限制发现能力
- C. 新建独立 ClusterRole + ClusterRoleBinding——职责分离更清晰，但增加 yaml 资源数量，且与现有 cadvisor reader 共享同一个 ServiceAccount

**Pros/Cons:** A 改动最小（一行 resources 追加），与现有模式一致；实际 scrape 范围已由 relabel_configs 限制。B 看似更窄但会破坏服务发现。C 职责更清晰但过度设计。

### 3.2 insecure_skip_verify vs 配置 CA

**Alternatives:**
- A. `insecure_skip_verify: true`（chosen）——与现有 `kubernetes-cadvisor` job 一致，virt-handler 使用自签证书
- B. 配置 CA 证书——需要额外挂载 CA ConfigMap/Secret，增加部署复杂度，且 lab 环境的 virt-handler 证书无固定 CA

**Pros/Cons:** A 是 lab/real-k8s 环境的标准做法，与现有 job 一致；B 在生产环境更安全但当前阶段非目标。

## 4. Open Questions

### 4.1 AC-5 端到端验证需真实部署 + VM 运行

**Assumption:** AC-5（`curl ...kubevirt_vmi_cpu_usage_seconds_total` 返回非空结果）需要真实部署 yaml 到集群 + KubeVirt virt-handler 已部署 + 至少一个 VM 在运行。当前环境无 VM，查询会返回空结果 `{"result":[]}`（正常响应，非报错）。

**Should verify:** Issue Dependencies 标注"#1 — handler 传 Kind（合入后才能端到端验证 VM 分支）"。#1 合入并创建 VM 后，需在真实集群执行 curl 验证 AC-5。

### 4.2 ClusterRole pods 权限范围是否需要进一步收窄

**Assumption:** 当前 ClusterRole 的 pods 权限是集群级（所有 namespace），但实际 scrape 范围已由 relabel_configs 限制到 kubevirt namespace。这是 Prometheus kubernetes_sd_configs 的标准做法。

**Should verify:** 安全审查时是否需要考虑收窄 pods 权限到特定 namespace？当前认为不需要，因为 relabel_configs 已做过滤，且 Prometheus 只读不写。

## 验证命令

```bash
cd repo
# 静态门禁
make validate-architecture
# 结果：✅ architecture guardrails valid (exit 0)

# 全量测试
make test
# 结果：ok (exit 0)

# diff 检查
git diff --check
# 结果：仅 CRLF warning（Windows 环境正常），无错误

# 端到端验证（需真实部署 + VM，当前无法执行）
# curl http://prometheus.ani-s07-observability:9090/api/v1/query?query=kubevirt_vmi_cpu_usage_seconds_total
```

## 涉及文件

| 文件 | 改动 |
|---|---|
| `repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml` | ClusterRole `resources` 追加 `"pods"`；`scrape_configs` 新增 `kubevirt-virt-handler` job（kubernetes_sd_configs role:pod namespace:kubevirt + relabel_configs 过滤 kubevirt.io=virt-handler label + 端口 8443 + bearer_token_file + tls_config.insecure_skip_verify） |
