# ANI Development Records — 批次归档索引

> 本文件是所有已完成开发批次的**唯一归档索引**。
> 进度追踪三层结构：
> - **全局状态快照** → `ANI-06-开发计划.md` Section 零（30秒定位）
> - **当前冲刺任务** → `repo/CURRENT-SPRINT.md`（每冲刺更新）
> - **已完成批次详情** → 本文件（每批次完成后追加）

> 当前执行处于 **Sprint 5 收敛中**：本地真实代码已完成 M1-K8S-A/B 的 CRUD+kubeconfig+proxy local profile、M1-K8S-C 的 vCluster Helm provider 代码边界、M1-K8S-D 的 vCluster kubeconfig provider 代码边界、M1-K8S-E 的 K8s cluster upgrade API/provider 代码边界、M1-K8S-F 的 node pool CRUD local profile、M1-K8S-G 的 Cluster API node pool provider 代码边界、M1-K8S-LIVE-A 的 vCluster live 验证 contract gate（`validate-vcluster-live-gate`）、M1-K8S-LIVE-D 的 vCluster live evidence JSON 输出、M1-K8S-LIVE-B 的 Cluster API node pool live 验证 contract gate（`validate-k8s-node-pool-live-gate`）、M1-K8S-LIVE-F 的 node pool evidence JSON 输出、M1-K8S-LIVE-C 的 vCluster upgrade live 验证 contract gate（`validate-vcluster-upgrade-live-gate`，覆盖 `controlPlane.distro.k8s.version` 检查步骤）、M1-K8S-LIVE-E 的 vCluster upgrade evidence JSON 输出、M1-NETWORK-LIVE-A 的 Kube-OVN `Vpc/Subnet` live 验证 contract gate（`validate-kubeovn-network-live-gate`）、M1-NETWORK-LIVE-B 的 Kube-OVN network evidence JSON 输出、M1-KUBEVIRT-LIVE-A 的 KubeVirt VM start/stop lifecycle 与 console/VNC live 验证 contract gate（`validate-kubevirt-vm-live-gate`）、M1-KUBEVIRT-LIVE-B 的 KubeVirt VM evidence JSON 输出、M1-K8S-PROXY-A/B/C/D/E/F 的 proxy forwarding adapter、per-cluster target resolver/store、metadata 持久化 store、Gateway router 注入接线、Gateway `forwarding_static` runtime 选择与 Gateway `forwarding_metadata` metadata resolver 接线、M1-ENCRYPT-A/B 的 keys+seal/unseal-token+rotate+revoke local profile、M1-ENCRYPT-C 的 KMS/SM4 HTTP provider 代码边界、M1-ENCRYPT-D 的对象内容 SM4-GCM 流式加解密代码边界、M1-ENCRYPT-LIVE-A 的 KMS/SM4 provider streaming live 验证 contract gate（`validate-kms-sm4-live-gate`）、M1-ENCRYPT-LIVE-B 的 KMS/SM4 evidence JSON 输出、M1-SECRETS-A 的 Secret CRUD+bindings local profile、M1-SECRETS-B 的 Kubernetes Secret provider 写入代码边界、M1-SECRETS-C 的容器/Job Secret binding env/file manifest 注入代码边界、M1-SECRETS-D 的 VM Secret binding volume manifest 注入代码边界、M1-SECRETS-LIVE-A 的 Kubernetes Secret env/file/VM live 验证 contract gate（`validate-secrets-live-gate`）、M1-SECRETS-LIVE-B 的 Kubernetes Secret evidence JSON 输出、M1-RECONCILE-A 的 background controller adapter/capability 与默认关闭的 bootstrap opt-in 运行剖面、M1-RECONCILE-B 的目标级失败退避和计数快照、M1-RECONCILE-C 的 `/metrics` Prometheus text 指标导出、M1-RECONCILE-D 的独立 reconcile worker 进程形态、M1-RECONCILE-E 的 metadata-backed leader election 代码边界、M1-RECONCILE-LIVE-A 的 controller HA live 验证 contract gate（`validate-reconcile-ha-live-gate`，覆盖 `control_plane_leases` holder 切换与 HA failover 检查步骤）、M1-RECONCILE-LIVE-B 的 controller HA evidence JSON 输出、REAL-K8S-LAB-A 真实底座 contract gate、M1-REAL-LAB-B 总 profile 组件级 contract gate 索引、M1-REAL-LAB-C `--live` evidence JSON 输出、M1-REAL-LAB-D component live runner、M1-REAL-LAB-E component live 聚合失败摘要、M1-REAL-LAB-F required env preflight、M1-REAL-LAB-G component env template、M1-REAL-LAB-H component env file loader、M1-REAL-LAB-I component preflight-only mode、M1-REAL-LAB-J component gate selector、M1-REAL-LAB-K component summary report、M1-REAL-LAB-L component report stale summary guard、M1-REAL-LAB-M component report diagnostic details、M1-REAL-LAB-N component evidence integrity guard、M1-REAL-LAB-O component evidence content guard、M1-REAL-LAB-P component report passed-evidence audit、M1-REAL-LAB-Q component report unresolved exit guard 和 M1-REAL-LAB-R component report overall status；vCluster live Helm 安装验证、真实 kubeconfig 可用性、live proxy 验证、live vCluster 升级验证真实执行结果、真实 Kube-OVN Vpc/Subnet 与 NetworkPolicy/Service LB 验证结果、KubeVirt VM lifecycle 与 console/VNC live 验证结果、真实节点池 live 扩缩容、GPU 节点池真实调度验证、controller 多副本 live HA failover 验证真实执行结果、KMS/SM4 live backend 验证、对象存储 + KMS/SM4 provider streaming 端到端验收、Kubernetes Secret live 写入验证和实例 Secret env/file/VM volume live 注入验证尚未执行完成。本文只做已完成批次归档，不作为当前任务清单使用。
> 2026-05-20 提交前闭环审查：Sprint 2 代码实现、OpenAPI 契约、冻结矩阵、校验脚本和批次记录已对齐；Sprint 3 当前优先项已切换为 `CORE-DEV-PROFILE-A`（原 `MOCK-DEV-A`，已收窄为 Core dev/local profile，不包含 Services 业务 mock）。
> 2026-05-21 Sprint 3 闭环门禁已通过，当前执行切换到 **Sprint 4**；`SPEC-SPLIT-A` 已完成，`SPEC-CORE-BETA` 已完成 Beta 准备矩阵、Core API v1 兼容性基线、SDK/Mock/API 文档加固、四语言 SDK-Mock 联动烟测和提交前审查。当前状态：开发与验收完成，待提交 GitHub；提交完成后再切换下一 Sprint。

---

## 已完成批次（按完成时间排列）

### Sprint 5 Delivery（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| M1-K8S-A | K8s 集群 create/get/list/delete + kubeconfig API + local dev profile + idempotency + tenant isolation；不含 proxy/真实 vCluster provider | m1-k8s-a-core-api-dev-profile.md |
| M1-K8S-B | K8s 集群 proxy Core API 契约 + local dev profile；method/path/query/body 请求边界、幂等 key、路径 allowlist 和 SDK/docs 生成；不含真实 vCluster API 转发 | m1-k8s-b-api-proxy-dev-profile.md |
| M1-K8S-C | vCluster Helm provider 代码边界；新增 provider apply port、Helm adapter、provider evidence、proxy target 注册和 Gateway `K8S_CLUSTER_PROVIDER_MODE=vcluster_helm`；不含 live Helm/kubeconfig/proxy 验证 | m1-k8s-c-vcluster-helm-provider.md |
| M1-K8S-D | vCluster kubeconfig provider 代码边界；real provider cluster 的 kubeconfig 可委托 `vcluster connect --print` adapter，Gateway `vcluster_helm` 同时接入 apply 与 kubeconfig provider；不含 live kubeconfig 可用性验证 | m1-k8s-d-vcluster-kubeconfig-provider.md |
| M1-K8S-E | K8s cluster upgrade API/provider 代码边界；新增 `POST /k8s-clusters/{cluster_id}/upgrade`、upgrade port、local 幂等版本更新、vCluster Helm upgrade intent 和 Gateway provider 接线；不含 live vCluster 升级验证或节点池管理 | m1-k8s-e-cluster-upgrade-boundary.md |
| M1-K8S-F | K8s node pool CRUD local profile；新增 cluster-scoped node-pools API、ports、local runtime、Gateway router 和 SDK/docs 生成；不含真实 provider 节点池扩缩容或 GPU 调度 live 验证 | m1-k8s-f-node-pool-local-profile.md |
| M1-K8S-G | K8s node pool provider 代码边界；新增 `K8sClusterNodePoolProvider` port、Cluster API MachineDeployment adapter 和 Gateway `K8S_CLUSTER_NODE_POOL_PROVIDER_MODE=clusterapi_kubernetes_rest` 接线；不含 live 扩缩容或 GPU 调度验证 | m1-k8s-g-node-pool-provider-boundary.md |
| M1-K8S-LIVE-A | vCluster live 验证门禁；新增 `validate-vcluster-live-gate`，固定 Helm install、vCluster kubeconfig、kubectl `/version` 和 Core live proxy 检查入口；不含真实 lab live 结果 | m1-k8s-live-a-vcluster-live-gate.md |
| M1-K8S-LIVE-D | vCluster live evidence JSON 输出；`validate_vcluster_live_gate.py --live` 支持 `--evidence-output` / `ANI_VCLUSTER_LIVE_EVIDENCE_OUTPUT` 归档 kubeconfig 路径与 Core proxy HTTP 状态；不含真实 lab live 结果 | m1-k8s-live-d-vcluster-evidence-output.md |
| M1-K8S-LIVE-B | Cluster API node pool live 验证门禁；新增 `validate-k8s-node-pool-live-gate`，固定 Core node pool create/update、MachineDeployment 观测和 GPU workload 调度检查入口；不含真实 lab live 结果 | m1-k8s-live-b-node-pool-live-gate.md |
| M1-K8S-LIVE-F | Node pool evidence JSON 输出；`validate_k8s_node_pool_live_gate.py --live` 支持 `--evidence-output` / `ANI_K8S_NODE_POOL_LIVE_EVIDENCE_OUTPUT` 归档 node pool、MachineDeployment、namespace、scaled replicas 与 GPU workload 证据；不含真实 lab live 结果 | m1-k8s-live-f-node-pool-evidence-output.md |
| M1-K8S-LIVE-C | vCluster upgrade live 验证门禁；新增 `validate-vcluster-upgrade-live-gate`，固定 Core upgrade API、Helm `controlPlane.distro.k8s.version`、升级后 kubeconfig、kubectl `/version` 和 Core proxy 检查入口；不含真实 lab live 结果 | m1-k8s-live-c-vcluster-upgrade-live-gate.md |
| M1-K8S-LIVE-E | vCluster upgrade evidence JSON 输出；`validate_vcluster_upgrade_live_gate.py --live` 支持 `--evidence-output` / `ANI_VCLUSTER_UPGRADE_LIVE_EVIDENCE_OUTPUT` 归档 target version、kubeconfig 路径与 Core proxy HTTP 状态；不含真实 lab live 结果 | m1-k8s-live-e-vcluster-upgrade-evidence-output.md |
| M1-NETWORK-LIVE-A | Kube-OVN network live 验证门禁；新增 `validate-kubeovn-network-live-gate`，固定 Kube-OVN `Vpc/Subnet`、NetworkPolicy 和 Service/LB 检查入口；不含真实 lab live 结果 | m1-network-live-a-kubeovn-network-live-gate.md |
| M1-NETWORK-LIVE-B | Kube-OVN network evidence JSON 输出；`validate_kubeovn_network_live_gate.py --live` 支持 `--evidence-output` / `ANI_KUBEOVN_NETWORK_LIVE_EVIDENCE_OUTPUT` 归档 namespace、Vpc、Subnet、NetworkPolicy/security_group 与 Service/load_balancer 证据；不含真实 lab live 结果 | m1-network-live-b-kubeovn-evidence-output.md |
| M1-KUBEVIRT-LIVE-A | KubeVirt VM live 验证门禁；新增 `validate-kubevirt-vm-live-gate`，固定 VM start/stop lifecycle、console/VNC 和 delete 检查入口；不含真实 lab live 结果 | m1-kubevirt-live-a-vm-live-gate.md |
| M1-KUBEVIRT-LIVE-B | KubeVirt VM evidence JSON 输出；`validate_kubevirt_vm_live_gate.py --live` 支持 `--evidence-output` / `ANI_KUBEVIRT_VM_LIVE_EVIDENCE_OUTPUT` 归档 namespace 与 VM 名称证据；不含真实 lab live 结果 | m1-kubevirt-live-b-vm-evidence-output.md |
| M1-K8S-PROXY-A | K8s 集群 proxy forwarding adapter；通过 resolver 将 Core proxy 请求转发到目标 vCluster/K8s API Server；不含真实 vCluster 生命周期或 Gateway 默认生产接线 | m1-k8s-proxy-a-forwarding-adapter.md |
| M1-K8S-PROXY-B | K8s 集群 proxy per-cluster target resolver/store；按 tenant/cluster 注册、解析、删除目标 API Server 和 bearer token；不含 DB 持久化或 Gateway 默认生产接线 | m1-k8s-proxy-b-target-resolver-store.md |
| M1-K8S-PROXY-C | K8s 集群 proxy target metadata 持久化；通过 `ports.MetadataStore` upsert/resolve/delete tenant/cluster 目标 API Server 和 bearer token；不含 Gateway 默认生产接线或 live proxy 验证 | m1-k8s-proxy-c-target-metadata-store.md |
| M1-K8S-PROXY-D | Gateway K8s proxy 注入接线；`RegisterWithOptions` 可接入 forwarding-capable `ports.K8sClusterService`；不含 Gateway main 默认 runtime 选择或 live proxy 验证 | m1-k8s-proxy-d-gateway-injection-wiring.md |
| M1-K8S-PROXY-E | Gateway K8s proxy runtime 选择；`K8S_CLUSTER_PROXY_MODE=forwarding_static` 可在 Gateway main 组合 forwarding adapter 和静态上游 target；不含 per-cluster metadata resolver Gateway 接线或 live proxy 验证 | m1-k8s-proxy-e-gateway-runtime-selection.md |
| M1-K8S-PROXY-F | Gateway K8s proxy metadata runtime；`K8S_CLUSTER_PROXY_MODE=forwarding_metadata` 可通过 `DATABASE_URL` 接入 metadata-backed per-cluster target resolver；不含 vCluster 生命周期或 live proxy 验证 | m1-k8s-proxy-f-gateway-metadata-runtime.md |
| REAL-K8S-LAB-A | 真实底座验证门禁：定义三台云 VM K8s/Kube-OVN/KubeVirt/vCluster lab profile、`make validate-real-k8s-profile` 和 live kubectl 检查入口；不代表真实环境已经部署完成 | real-k8s-lab-a-validation-gate.md |
| M1-ENCRYPT-A | Encryption keys create/get/list/delete + seal + unseal-token API + local dev profile + idempotency + tenant isolation；不含真实 KMS/SM4 provider | m1-encrypt-a-core-api-dev-profile.md |
| M1-ENCRYPT-B | Encryption key rotate/revoke API + local dev profile + idempotency + state guard；不含真实 KMS/SM4 provider 生命周期操作 | m1-encrypt-b-key-rotation-revoke-local-profile.md |
| M1-ENCRYPT-C | KMS/SM4 HTTP provider 代码边界：`ports.EncryptionProvider`、provider-backed key/seal/token evidence、Gateway `ENCRYPTION_PROVIDER_MODE=kms_sm4_http` runtime 选择；不含 live KMS/SM4 backend 验证或对象数据面 provider streaming 验收 | m1-encrypt-c-kms-sm4-provider-boundary.md |
| M1-ENCRYPT-D | 对象内容 SM4-GCM 流式加解密代码边界：reader/writer seal/open port、本地 SM4 block cipher、chunk frame、nonce 和 digest 校验；不含 live KMS/SM4 backend 或真实对象存储 provider streaming 验收 | m1-encrypt-d-sm4-gcm-object-content.md |
| M1-ENCRYPT-LIVE-A | KMS/SM4 live 验证门禁；新增 `validate-kms-sm4-live-gate`，固定 Core key/seal/token、KMS streaming seal/open 和 objectstore sealed content round trip 检查入口；不含真实 lab live 结果 | m1-encrypt-live-a-kms-sm4-live-gate.md |
| M1-ENCRYPT-LIVE-B | KMS/SM4 evidence JSON 输出；`validate_kms_sm4_live_gate.py --live` 支持 `--evidence-output` / `ANI_KMS_SM4_LIVE_EVIDENCE_OUTPUT` 归档 tenant、Gateway/KMS 地址、object URI、provider、key、sealed URI 与 round-trip bytes；不含 bearer token 或 presigned URL；不含真实 lab live 结果 | m1-encrypt-live-b-kms-sm4-evidence-output.md |
| M1-SECRETS-A | Secret create/get/list/delete + bindings API + local dev profile + idempotency + tenant isolation；响应不返回明文，不含真实 K8s Secret 注入 | m1-secrets-a-core-api-dev-profile.md |
| M1-SECRETS-B | Kubernetes Secret provider 写入代码边界；新增 Secret provider port、Kubernetes Secret manifest apply、Gateway `SECRET_PROVIDER_MODE=kubernetes_rest` runtime 选择；不含 live 写入验证或实例环境变量/文件挂载注入 | m1-secrets-b-kubernetes-secret-provider.md |
| M1-SECRETS-C | Workload Secret binding 注入 manifest 边界；容器/Job workload 可渲染 `envFrom.secretRef` 与只读 Secret volume mount；不含 live Pod 验证或 VM 注入 | m1-secrets-c-workload-secret-injection.md |
| M1-SECRETS-D | VM Secret binding 注入 manifest 边界；KubeVirt VM 可渲染 Secret volume、只读 disk 和 guest mount intent annotation；不含 live VM guest 可见性验证 | m1-secrets-d-vm-secret-injection.md |
| M1-SECRETS-LIVE-A | Secret live 验证门禁；新增 `validate-secrets-live-gate`，固定 Core Kubernetes Secret 创建、kubectl read、Pod env/file 和 KubeVirt VM Secret volume 检查入口；不含真实 lab live 结果 | m1-secrets-live-a-secret-live-gate.md |
| M1-SECRETS-LIVE-B | Kubernetes Secret evidence JSON 输出；`validate_secrets_live_gate.py --live` 支持 `--evidence-output` / `ANI_SECRETS_LIVE_EVIDENCE_OUTPUT` 归档 tenant、Gateway 地址、secret_id、namespace、Pod 与 VM；不含 bearer token 或 Secret 明文；不含真实 lab live 结果 | m1-secrets-live-b-evidence-output.md |
| M1-RECONCILE-A | WorkloadReconcileController adapter + bootstrap capability + opt-in 后台运行；扫描 reconcile target、观察 provider 状态、回写 instance 状态；不含 leader election/指标/退避 | m1-reconcile-a-background-controller.md |
| M1-RECONCILE-B | WorkloadReconcileController 目标级失败退避和计数快照；单 target provider 失败不终止整轮扫描；不含 leader election、Prometheus 指标导出或独立 worker 部署形态 | m1-reconcile-b-controller-backoff-metrics.md |
| M1-RECONCILE-C | WorkloadReconcileController Prometheus text 指标导出；probe server `/metrics` 暴露 tick/success/failure/backoff skip counters；不含 leader election 或独立 worker 部署形态 | m1-reconcile-c-prometheus-metrics.md |
| M1-RECONCILE-D | 独立 reconcile worker 进程形态；新增 `services/reconcile-worker` 和 `bootstrap.RunWorkloadReconcileWorker`，不启动 gRPC 即运行 controller/probe/metrics；不含 leader election | m1-reconcile-d-independent-worker.md |
| M1-RECONCILE-E | WorkloadReconcileController metadata-backed leader election；新增 leader elector port、metadata lease adapter、bootstrap 显式配置和 control plane lease 迁移；不含多副本 live HA failover 验证 | m1-reconcile-e-leader-election.md |
| M1-RECONCILE-LIVE-A | Controller HA live 验证门禁；新增 `validate-reconcile-ha-live-gate`，固定两副本 worker、`control_plane_leases` active holder、metrics、删除 leader pod 和 follower 接管 HA failover 检查入口；不含真实 lab live 结果 | m1-reconcile-live-a-ha-live-gate.md |
| M1-RECONCILE-LIVE-B | Controller HA evidence JSON 输出；`validate_reconcile_ha_live_gate.py --live` 支持 `--evidence-output` / `ANI_RECONCILE_HA_LIVE_EVIDENCE_OUTPUT` 归档 namespace、worker selector、lease、metrics URL、holder 和 deleted pod 证据；不含真实 lab live 结果 | m1-reconcile-live-b-ha-evidence-output.md |
| M1-REAL-LAB-B | REAL-K8S-LAB-A 组件级 contract gate 索引；总 profile 固定索引 vCluster、vCluster upgrade、node pool、Kube-OVN、KubeVirt、reconcile HA、KMS/SM4 和 Secrets gate，并校验 Make target/manifest/validator 存在；不含真实 lab live 结果 | m1-real-lab-b-contract-gate-index.md |
| M1-REAL-LAB-C | REAL-K8S-LAB-A `--live` evidence JSON 输出；总 live 入口可归档 profile、minimum_nodes、kubeconfig 标记和每个必需 live check 的 passed 结果；不含真实 lab live 结果 | m1-real-lab-c-live-evidence-output.md |
| M1-REAL-LAB-D | REAL-K8S-LAB-A component live runner；`validate_real_k8s_profile.py --component-live` 会逐个执行 `contract_gates` validator 的 `--live --evidence-output` 并归档汇总 JSON；不含真实 lab live 结果 | m1-real-lab-d-component-live-runner.md |
| M1-REAL-LAB-E | REAL-K8S-LAB-A component live 聚合失败摘要；`--component-live` 执行所有 indexed validators 后汇总 total/passed/failed 和失败 gate error，再以非零状态退出；不含真实 lab live 结果 | m1-real-lab-e-component-live-aggregation.md |
| M1-REAL-LAB-F | REAL-K8S-LAB-A component live required env preflight；`contract_gates[].required_env` 固定每个组件 validator 的必需环境变量，`--component-live` 缺失 env 时写出 preflight summary 并不启动 validators；不含真实 lab live 结果 | m1-real-lab-f-component-live-required-env.md |
| M1-REAL-LAB-G | REAL-K8S-LAB-A component live env template；`--component-env-template-output` 从 `contract_gates[].required_env` 生成不含密钥值的 shell env 模板和 gate mapping；不含真实 lab live 结果 | m1-real-lab-g-component-env-template.md |
| M1-REAL-LAB-H | REAL-K8S-LAB-A component live env file loader；`--component-env-file` 安全解析填充后的 env 模板并把合并 env 传递给组件 validator 子进程；不含真实 lab live 结果 | m1-real-lab-h-component-env-file-loader.md |
| M1-REAL-LAB-I | REAL-K8S-LAB-A component live preflight-only mode；`--component-preflight` 可加载 env file 或当前环境，只检查 `required_env` 并写出 summary JSON，不启动 live validators；不含真实 lab live 结果 | m1-real-lab-i-component-preflight-only.md |
| M1-REAL-LAB-J | REAL-K8S-LAB-A component gate selector；`--component-gate <id>` 可把 component preflight 或 live run 限定到一个或多个 indexed gate，便于只重查或重跑失败项；不含真实 lab live 结果 | m1-real-lab-j-component-gate-selector.md |
| M1-REAL-LAB-K | REAL-K8S-LAB-A component summary report；`--component-report <summary.json>` 可读取 component preflight/live summary，分类 failed/blocked gates 并生成 selected preflight/live 复跑命令；不含真实 lab live 结果 | m1-real-lab-k-component-summary-report.md |
| M1-REAL-LAB-L | REAL-K8S-LAB-A component report stale summary guard；`--component-report` 会拒绝引用当前 profile 未知 gate id 的旧 summary，避免生成无效复跑命令；不含真实 lab live 结果 | m1-real-lab-l-component-report-stale-summary-guard.md |
| M1-REAL-LAB-M | REAL-K8S-LAB-A component report diagnostic details；`--component-report` 会在 `gate_details` 中保留 failed/blocked gate 的 status、missing_env、returncode 和 error，便于真实 lab 首轮失败后定位原因与复跑；不含真实 lab live 结果 | m1-real-lab-m-component-report-diagnostic-details.md |
| M1-REAL-LAB-N | REAL-K8S-LAB-A component evidence integrity guard；`--component-live` 会在 validator 返回 0 后确认 per-gate evidence JSON 已写出，缺失时把 gate 计为 failed；不含真实 lab live 结果 | m1-real-lab-n-component-evidence-integrity-guard.md |
| M1-REAL-LAB-O | REAL-K8S-LAB-A component evidence content guard；`--component-live` 会在 validator 返回 0 且 evidence 文件存在后解析 JSON，只接受 `status=passed` 或 `passed=true` 的 passing evidence；malformed/non-passing evidence 计为 failed；不含真实 lab live 结果 | m1-real-lab-o-component-evidence-content-guard.md |
| M1-REAL-LAB-P | REAL-K8S-LAB-A component report passed-evidence audit；`--component-report` 会审计 passed gate 的 `evidence_output`，缺失、malformed 或 non-passing evidence 会在 report 中归为 failed；不含真实 lab live 结果 | m1-real-lab-p-component-report-passed-evidence-audit.md |
| M1-REAL-LAB-Q | REAL-K8S-LAB-A component report unresolved exit guard；`--component-report` 会先写出 report，再在 failed/blocked gate 仍存在时非零退出；不含真实 lab live 结果 | m1-real-lab-q-component-report-unresolved-exit-guard.md |
| M1-REAL-LAB-R | REAL-K8S-LAB-A component report overall status；`--component-report` 会输出 `passed` 与 `unresolved_gates`，便于 CI 和人工归档读取整体状态；不含真实 lab live 结果 | m1-real-lab-r-component-report-overall-status.md |

### Sprint 5 Kickoff（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPRINT5-KICKOFF-A | Sprint 5 启动：执行入口切换与三份主文档状态对齐 | sprint5-kickoff-a.md |

### Sprint 4 API Beta Preparation（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPEC-SPLIT-A | Core/Services API 分层收口：Services 业务路径迁移到 Services API，Gateway Services stub 改挂 `/api/v1/svc`，SDK metadata 自然分层 | spec-split-a-core-services-api-boundary.md |
| SPEC-CORE-BETA-A | Core API Beta 准备矩阵：P0 path/schema、分页、幂等、状态机、dev_profile、RBAC scope 和 Core/Services 边界守卫 | spec-core-beta-a-readiness-matrix.md |
| SPEC-COMPAT-A | Core API v1 兼容性基线：保护 path/method/operationId/参数/响应/schema 字段，允许新增可选能力但阻止破坏性变更 | spec-compat-a-core-api-v1-baseline.md |
| SDK-BETA-A | 四语言 SDK 幂等 helper：生成 idempotency key、注入请求体、metadata 标出 Core 幂等操作 | sdk-beta-a-idempotency-helper.md |
| SDK-BETA-B | 四语言 SDK cursor 分页 helper：构造 limit/cursor 参数、metadata 标出 Core 分页操作 | sdk-beta-b-cursor-pagination-helper.md |
| SDK-BETA-C | 四语言 SDK 统一 API error helper：错误对象、错误码清单、错误码判断 | sdk-beta-c-api-error-helper.md |
| SDK-BETA-D | 四语言 SDK basic example：client 初始化、幂等、cursor 分页和 API error helper 组合用法 | sdk-beta-d-basic-examples.md |
| SDK-MOCK-SMOKE-A | Core Python SDK 调用 Mock Server 烟测：标准库 HTTP request 能力、分页响应和标准错误响应校验 | sdk-mock-smoke-a-python-sdk-mock-server.md |
| SDK-MOCK-SMOKE-B | Core TypeScript SDK 调用 Mock Server 烟测：fetch request 能力、分页响应和标准错误响应校验 | sdk-mock-smoke-b-typescript-sdk-mock-server.md |
| SDK-MOCK-SMOKE-C | Core Go SDK 调用 Mock Server 烟测：net/http Request 能力、分页响应和标准错误响应校验 | sdk-mock-smoke-c-go-sdk-mock-server.md |
| SDK-MOCK-SMOKE-D | Core Java SDK 调用 Mock Server 烟测：HttpClient request 能力、分页响应和标准错误响应校验 | sdk-mock-smoke-d-java-sdk-mock-server.md |
| MOCK-A | Core Mock Server：由 `api/openapi/v1.yaml` 驱动，覆盖 Core API 成功响应和统一错误结构 | mock-a-core-openapi-mock-server.md |
| DOC-API-A | 静态 API 文档生成：Core/Services API 契约生成 docs/api，并校验 operation/schema 覆盖 | doc-api-a-static-api-docs.md |
| SPRINT4-CLOSURE-A | Sprint 4 关联性闭环门禁：统一校验 API/SDK/Mock/Docs/Records 与 Makefile 入口 | sprint4-closure-a-contract.md |

### Sprint 3 Network / Storage / SDK（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| M1-NETWORK-A | VPC/Subnet/SecurityGroup/LoadBalancer Core API 契约、Gateway dev profile、持久化边界和网络合同守卫 | m1-network-a-core-api-dev-profile.md |
| M1-NETWORK-A | KubeOVN/Kubernetes provider 渲染边界：Vpc/Subnet、NetworkPolicy、Service 清单与 bootstrap capability | m1-network-a-kubeovn-renderer.md |
| M1-NETWORK-A | 网络 provider server-side dry-run、默认关闭 apply gate、KubeOVN/Kubernetes REST path 映射 | m1-network-a-provider-dry-run-apply-gate.md |
| M1-NETWORK-A | 网络 provider 状态读取边界：KubeOVN/Kubernetes 资源状态归一化为 ANI 网络状态与失败原因 | m1-network-a-provider-status-reader.md |
| M1-NETWORK-A | 网络状态 reconcile：provider observation 校验后回写网络资源 state/reason/updated_at | m1-network-a-status-reconcile.md |
| M1-STORAGE-A | volumes/filesystems/objects Core API 契约、Gateway dev profile、租户隔离和存储合同守卫 | m1-storage-a-core-api-dev-profile.md |
| M1-STORAGE-A | storage metadata 持久化边界、RLS 迁移、bootstrap capability 和持久化单元测试 | m1-storage-a-persistence-boundary.md |
| M1-STORAGE-A | 存储 provider 渲染边界：PVC manifest、objectstore metadata intent 和 bootstrap capability | m1-storage-a-provider-renderer.md |
| M1-STORAGE-A | 存储 provider server-side dry-run、默认关闭 apply gate、objectstore 执行边界保留 | m1-storage-a-provider-dry-run-apply-gate.md |
| M1-STORAGE-A | 存储 provider 状态读取和 metadata state/reason 回写闭环 | m1-storage-a-status-reconcile.md |
| M1-VSTORE-A | vector-stores Core API 契约、Gateway dev profile、搜索响应结构和合同守卫 | m1-vstore-a-core-api-dev-profile.md |
| SDK-ALPHA-A | Core/Services 四语言 SDK Alpha 生成、分层隔离和 smoke 门禁 | sdk-alpha-a-generation-smoke.md |
| M1-WKID-A | Workload Identity P0：实例 lifecycle-bound scoped API key、Secret 引用注入和删除 revoke | m1-wkid-a-workload-identity-p0.md |
| CORE-DEV-PROFILE-A | Core P0 API dev/local profile 显式标记、Core/Services mock 边界和合同守卫 | core-dev-profile-a-boundary-contract.md |
| SPRINT3-CLOSURE-A | Sprint 3 闭环审查门禁：批次记录、API/SDK 分层和各批次合同守卫统一校验 | sprint3-closure-a-contract.md |

### Sprint 2 Core API Alpha（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPEC-CORE-ALPHA-A | `/api/v1/instances` Core Alpha path/schema/RBAC scope + Gateway 主路径 + 合同守卫 | spec-core-alpha-a-instance-contract-guard.md |
| SPEC-CORE-ALPHA-B | Core API Alpha 机器可读冻结矩阵，校验 path/schema/error/state/RBAC scope 与 Gateway/runtime 对齐 | spec-core-alpha-b-freeze-matrix.md |
| M1-INSTANCE-U-A | VM `termination_protection` 危险操作 precheck、failed operation timeline 和 lifecycle policy 持久化 | m1-instance-u-a-termination-protection.md |
| M1-INSTANCE-U-B | VM SSH 连接元数据 schema、Gateway dev profile 响应和 `ssh_connection` 持久化 | m1-instance-u-b-vm-ssh-info.md |
| M1-INSTANCE-U-C | VM console/VNC/serial session 返回 `operation_id/url/expires_at` 并写入 operation timeline | m1-instance-u-c-console-session-timeline.md |
| M1-INSTANCE-U-D | VM `snapshot` local profile、`snapshots[]` 响应、operation timeline 和 JSONB 持久化 | m1-instance-u-d-vm-snapshot-local-profile.md |
| M1-INSTANCE-U-E | VM `attach_volume/detach_volume` local profile、`volumes[]` 响应和 operation timeline | m1-instance-u-e-vm-volume-binding-local-profile.md |
| M1-INSTANCE-V-A | Container/GPU Container `replicas/revision/rollout_status/history` 响应和 `container_status` 持久化 | m1-instance-v-a-container-rollout-status.md |
| M1-INSTANCE-V-B | Container/GPU Container `rollback` local profile、revision 回退和 `rollback_revision` operation timeline | m1-instance-v-b-container-rollback-local-profile.md |
| M1-INSTANCE-V-C | GPU Container `vendor/model/count/scheduling_reason/utilization_percent` 响应和 `gpu_status` 持久化 | m1-instance-v-c-gpu-status-local-profile.md |

### V8 架构重规划（2026-05-14~15）

| 批次 | 内容摘要 |
|---|---|
| V8-ARCH | Core/Services 分层、ANI-02/06 重写、CLAUDE.md 强制约定 |
| AWS-HARDENING | /healthz /readyz、idempotency_key port、ReconcileController port、operations DB 表、permissions schema |

### Sprint 1 Foundation（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| M1-HEALTH-A | Gateway/Auth/Model/Task 标准 /healthz 与 /readyz 探针 | m1-health-a-health-endpoints.md |
| M1-IDEM-A | 实例 create/lifecycle 幂等锁、DB 原子冲突回放和 bootstrap 接线 | m1-idem-a-idempotency-wire-up.md |

### M1 基础设施底座（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| M1-INFRA-A | ani-system 命名空间、NetworkPolicy、ServiceAccount 基线 | m1-infra-a-baseline.md |
| M1-INFRA-B | PostgreSQL/NATS/Redis/MinIO/Milvus/Harbor 组件安装 profile | m1-infra-b-component-profiles.md |
| M1-INFRA-C | KubeOVN VPC/Subnet 模板、沙箱出口限制 | m1-infra-c-network-isolation.md |
| M1-INFRA-D | cluster preflight validation profile | m1-infra-d-cluster-preflight.md |
| M1-INFRA-E | GPU scheduling baseline（Volcano/HAMi/DCGM）| m1-infra-e-gpu-scheduling-baseline.md |
| M1-INFRA-F | GPU preflight/e2e hardening | m1-infra-f-gpu-preflight-e2e.md |
| M1-GPU-A | 异构 GPU 发现调度契约（NVIDIA/昇腾/海光/GPUInventory port）| m1-gpu-a-heterogeneous-gpu-contract.md |
| M1-RUNTIME-A | WorkloadRuntime port（全实例类型抽象）| m1-runtime-a-workload-runtime.md |

### M1 Instance Fabric（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| M1-INSTANCE-A | 核心实例对象、生命周期、网络平面、存储附件契约 | m1-instance-a-instance-fabric.md |
| M1-INSTANCE-B | PlanningRuntime 实例规划器 | m1-instance-b-planning-runtime.md |
| M1-INSTANCE-C | K8s/KubeVirt provider dry-run renderer | m1-instance-c-provider-renderer.md |
| M1-INSTANCE-D | 本地 admission guardrail | m1-instance-d-admission-guardrail.md |
| M1-INSTANCE-E | 实例计划/渲染/准入审计持久化 | m1-instance-e-plan-audit.md |
| M1-INSTANCE-F | WorkloadProviderDryRun executor boundary | m1-instance-f-provider-dry-run.md |
| M1-INSTANCE-G | WorkloadProviderApply 执行门控 | m1-instance-g-provider-apply-gate.md |
| M1-INSTANCE-H | WorkloadStatusReconciler 状态回写 | m1-instance-h-status-reconcile.md |
| M1-INSTANCE-I | WorkloadProviderStatusReader + Orchestrator | m1-instance-i-orchestrator.md |
| M1-INSTANCE-J | WorkloadInstanceStore + workload_instances RLS 表 | m1-instance-j-instance-store.md |
| M1-INSTANCE-K | KubernetesProviderAdapter + Client | m1-instance-k-provider-adapter.md |
| M1-INSTANCE-L | WorkloadInstanceService API 层 | m1-instance-l-instance-service.md |
| M1-INSTANCE-M | 生命周期 + 可视化运维 API | m1-instance-m-lifecycle-ops.md |
| M1-INSTANCE-N | Kubernetes provider 执行剖面 | m1-instance-n-kubernetes-provider-execution.md |
| M1-INSTANCE-O | adapter-owned KubernetesRESTClient | m1-instance-o-kubernetes-rest-client.md |
| M1-INSTANCE-P | bootstrap/config provider wiring | m1-instance-p-kubernetes-bootstrap-wiring.md |
| M1-INSTANCE-Q | KubernetesLifecycleExecutor | m1-instance-q-kubernetes-lifecycle-execution.md |
| M1-INSTANCE-R | KubernetesInstanceOps | m1-instance-r-kubernetes-ops-execution.md |
| M1-INSTANCE-S | VM console/VNC/serial remote ops session 边界 | — |
| M1-INSTANCE-T | 操作语义横切基础：operation_id、timeline、幂等回放和操作查询 | m1-instance-t-operation-semantics.md |
| M1-E2E-A | M1 端到端集成剖面 | m1-e2e-a-instance-profile.md |
| M1-E2E-B | M1 real provider integration regression profile | m1-e2e-b-real-provider-profile.md |

### ARCH-ADAPTER 系列（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| ARCH-ADAPTER-A / M1-ARCH-A | 开源组件松耦合适配器架构设计 | m1-arch-a-component-adapter-design.md |
| ARCH-ADAPTER-B | pkg/ports + pkg/adapters + bootstrap.Capabilities 骨架 | arch-adapter-b-ports-adapters-skeleton.md |
| ARCH-ADAPTER-GUARD-A | 组件 SDK 直接导入扫描与 allowlist 护栏 | arch-adapter-guard-a-component-imports.md |
| ARCH-ADAPTER-C | 第一批迁移（CacheStore + MessageBus）| arch-adapter-c-first-migration.md |
| ARCH-ADAPTER-C-2 | pgx/metadata 依赖 bounded_direct 分类 | arch-adapter-c-2-metadata-boundaries.md |

### M2 Gateway / Auth（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| M2.1-TASK-A/B | task-service + transactional outbox | m2-1-task-a-b-task-service-outbox.md |
| M2.1-TASK-C | worker mutation RPCs | m2-1-task-c-worker-mutations.md |
| M2.2-AUTH-A~K | auth-service 完整实现（JWT/OIDC/JWKS/RBAC/API Key）| m2-2-auth-*.md |
| M2.2-AUTH-FINAL | Auth 生产收尾：OIDC/Dex 护栏、Gateway Auth REST、API Key 管理、合同守卫与 Docker Dex smoke | m2-2-auth-final-production-closeout.md |

---

## 批次完工的更新流程

> 完整规约在 `CLAUDE.md` → "📋 开发进度更新规约"，以下是速查版本。

**批次完成时（必须按顺序）：**

```
① make test                              → 全通（零失败）
② 新建 {批次名}.md（用 TEMPLATE.md）    → 填入完成日期/验证结果/关键文件
③ 本文件 README.md                       → 在对应分组表格追加一行
④ repo/CURRENT-SPRINT.md                 → 该批次 🔄→✅，下一批次 ⏳→🔄
⑤ ANI-06-开发计划.md Section 零         → 更新批次/Sprint 状态行
⑥ git commit -m "feat: {批次名} {一句话}"
```

**Sprint 全部完成时，额外：**
```
⑦ ANI-06 Section 零 Sprint 行：🔄→✅（填完成日期）/ 下一Sprint：⏳→🔄
⑧ repo/CURRENT-SPRINT.md 整体重写为下一 Sprint 内容
⑨ git commit -m "sprint: Sprint N completed"
```
