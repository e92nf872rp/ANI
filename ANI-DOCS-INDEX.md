# KuberCloud ANI · 文档导航与一致性矩阵

> 最后更新：2026-05-25
> 目的：让人类开发者和 AI 工具在 5 分钟内判断当前开发阶段、文档职责、下一步入口和闭环规则。

---

## 当前结论

```text
当前阶段：Phase 1 / Sprint 5 收敛中
当前不是 Phase 2：Phase 2 指 2026-10 以后延期能力
当前优先级：继续补齐 Sprint 5 真实 provider 主链路，并通过 REAL-K8S-LAB-A / make validate-real-k8s-profile 强制并行引入 K8s/Kube-OVN/KubeVirt/vCluster/KMS/SM4 真实底座验证环境
刚完成：Sprint 5 K8s CRUD+kubeconfig+proxy local profile、vCluster Helm provider 代码边界、vCluster kubeconfig provider 代码边界、K8s cluster upgrade API/provider 代码边界、K8s node pool CRUD local profile、M1-K8S-G Cluster API node pool provider 代码边界、M1-K8S-LIVE-A vCluster live 验证门禁（validate-vcluster-live-gate）、M1-K8S-LIVE-D vCluster live evidence JSON 输出、M1-K8S-LIVE-B Cluster API node pool live 验证门禁（validate-k8s-node-pool-live-gate，覆盖 GPU 调度检查步骤）、M1-K8S-LIVE-F node pool evidence JSON 输出、M1-K8S-LIVE-C vCluster upgrade live 验证门禁（validate-vcluster-upgrade-live-gate，覆盖 `controlPlane.distro.k8s.version` 检查步骤）、M1-K8S-LIVE-E vCluster upgrade evidence JSON 输出、M1-NETWORK-LIVE-A Kube-OVN `Vpc/Subnet` live 验证门禁（validate-kubeovn-network-live-gate）、M1-NETWORK-LIVE-B Kube-OVN network evidence JSON 输出、M1-KUBEVIRT-LIVE-A KubeVirt VM live 验证门禁（validate-kubevirt-vm-live-gate，覆盖 stop 与 console/VNC 检查步骤）、M1-KUBEVIRT-LIVE-B KubeVirt VM evidence JSON 输出、K8s proxy forwarding adapter 与 target resolver/store/metadata 持久化/Gateway router 注入接线/forwarding_static 与 forwarding_metadata runtime 选择、Encryption keys+seal/unseal-token/rotate/revoke、KMS/SM4 HTTP provider 代码边界、对象内容 SM4-GCM 流式加解密代码边界、M1-ENCRYPT-LIVE-A KMS/SM4 provider streaming live 验证门禁（validate-kms-sm4-live-gate）、M1-ENCRYPT-LIVE-B KMS/SM4 evidence JSON 输出、Secret CRUD+bindings local profile、Kubernetes Secret provider 写入代码边界、Workload Secret binding env/file manifest 注入代码边界、VM Secret binding volume manifest 注入代码边界、M1-SECRETS-LIVE-A Kubernetes Secret env/file/VM live 验证门禁（validate-secrets-live-gate）、M1-SECRETS-LIVE-B Kubernetes Secret evidence JSON 输出、WorkloadReconcileController bootstrap opt-in 运行剖面、目标级失败退避、计数快照、`/metrics` Prometheus text 指标导出、独立 worker 进程形态、metadata-backed leader election 代码边界、M1-RECONCILE-LIVE-A controller HA live 验证门禁（validate-reconcile-ha-live-gate，覆盖 `control_plane_leases` holder 切换与 HA failover 检查步骤）、M1-RECONCILE-LIVE-B controller HA evidence JSON 输出、M1-REAL-LAB-B REAL-K8S-LAB-A 组件级 contract gate 索引、M1-REAL-LAB-C `--live` evidence JSON 输出、M1-REAL-LAB-D component live runner、M1-REAL-LAB-E component live 聚合失败摘要、M1-REAL-LAB-F required env preflight、M1-REAL-LAB-G component env template、M1-REAL-LAB-H component env file loader、M1-REAL-LAB-I component preflight-only mode、M1-REAL-LAB-J component gate selector、M1-REAL-LAB-K component summary report、M1-REAL-LAB-L component report stale summary guard、M1-REAL-LAB-M component report diagnostic details、M1-REAL-LAB-N component evidence integrity guard、M1-REAL-LAB-O component evidence content guard、M1-REAL-LAB-P component report passed-evidence audit、M1-REAL-LAB-Q component report unresolved exit guard 和 M1-REAL-LAB-R component report overall status（2026-05-25）
下一步入口：repo/CURRENT-SPRINT.md（继续 Sprint 5，不能直接切换 Sprint 6）
```

本地真实代码显示：Sprint 4 API/SDK/Mock/Docs 收尾批次已归档；Sprint 5 目前完成可验证的 Core dev/local profile 主链路切片：`/api/v1/k8s-clusters` create/get/list/delete + kubeconfig + proxy + node-pools，`/api/v1/encryption/keys` create/get/list/delete + seal + unseal-token + rotate + revoke，以及 `/api/v1/secrets` CRUD + bindings；`M1-K8S-C/D/E/F/G` 已完成 vCluster Helm provider 代码边界、vCluster kubeconfig provider 代码边界、K8s cluster upgrade API/provider 代码边界、K8s node pool CRUD local profile、Cluster API node pool provider 代码边界和 Gateway `K8S_CLUSTER_PROVIDER_MODE=vcluster_helm` / `K8S_CLUSTER_NODE_POOL_PROVIDER_MODE=clusterapi_kubernetes_rest` runtime 选择；`M1-K8S-LIVE-A` 已提供 vCluster live Helm/kubeconfig/live proxy 验证门禁 `validate-vcluster-live-gate`，`M1-K8S-LIVE-B` 已提供 Cluster API node pool live 验证门禁 `validate-k8s-node-pool-live-gate` 且支持 evidence JSON 输出，`M1-K8S-LIVE-C` 已提供 vCluster upgrade live 验证门禁 `validate-vcluster-upgrade-live-gate`，覆盖 Core upgrade API、Helm `controlPlane.distro.k8s.version`、升级后 kubeconfig 和 proxy 检查；`M1-NETWORK-LIVE-A` 已提供 Kube-OVN `Vpc/Subnet` live 验证门禁 `validate-kubeovn-network-live-gate`，覆盖 Kube-OVN CRD、Vpc/Subnet、NetworkPolicy 和 Service/LB 检查，且 `M1-NETWORK-LIVE-B` 已补充 evidence JSON 输出；`M1-KUBEVIRT-LIVE-A` 已提供 KubeVirt VM live 验证门禁 `validate-kubevirt-vm-live-gate`，覆盖 KubeVirt CRD/control plane、VM start/stop lifecycle、console/VNC 和 delete 检查，且 `M1-KUBEVIRT-LIVE-B` 已补充 evidence JSON 输出，但尚未执行真实 lab live 模式；`M1-K8S-PROXY-A/B/C/D/E/F` 已完成可注入 resolver 的 proxy forwarding adapter、本地 per-cluster target resolver/store、metadata 持久化 store、Gateway router 注入接线和 Gateway `forwarding_static` / `forwarding_metadata` runtime 选择；`M1-ENCRYPT-C/D` 已完成 `ports.EncryptionProvider`、KMS/SM4 HTTP provider adapter、provider-backed key/seal/token evidence、Gateway `ENCRYPTION_PROVIDER_MODE=kms_sm4_http` runtime 选择，以及对象内容 SM4-GCM 流式加解密代码边界；`M1-ENCRYPT-LIVE-A/B` 已提供 KMS/SM4 provider streaming 与对象存储 round trip live 验证门禁 `validate-kms-sm4-live-gate`，并支持 `--evidence-output` 归档 JSON 证据，但尚未执行真实 lab live 模式；`M1-SECRETS-B/C/D` 已完成 Kubernetes Secret provider 写入代码边界、Gateway `SECRET_PROVIDER_MODE=kubernetes_rest` runtime 选择、容器/Job Secret binding env/file manifest 注入代码边界和 VM Secret binding volume manifest 注入代码边界；`M1-SECRETS-LIVE-A` 已提供 Kubernetes Secret env/file/VM live 验证门禁 `validate-secrets-live-gate`，但尚未执行真实 lab live 模式；`M1-RECONCILE-A/B/C/D/E` 已完成 controller adapter/capability、默认关闭的 bootstrap opt-in 后台运行剖面、目标级失败退避、计数快照、`/metrics` Prometheus text 指标导出、独立 worker 进程形态和 metadata-backed leader election 代码边界；`M1-RECONCILE-LIVE-A/B` 已提供 controller HA live 验证门禁 `validate-reconcile-ha-live-gate`，覆盖 `control_plane_leases` active holder、删除 leader pod、follower 接管和 HA failover 后 metrics 检查，并支持 `--evidence-output` 归档 JSON 证据，但尚未执行真实 lab live 模式。vCluster live Helm 安装验证、真实 kubeconfig 可用性、live proxy 验证、live vCluster 升级验证真实执行结果、真实 Kube-OVN Vpc/Subnet 与 NetworkPolicy/Service LB 验证结果、KubeVirt VM lifecycle 与 console/VNC live 验证结果、真实节点池 live 扩缩容、GPU 节点池真实调度验证、controller 多副本 live HA failover 验证真实执行结果、KMS/SM4 live backend 验证、对象存储 + KMS/SM4 provider streaming 端到端验收、Kubernetes Secret live 写入验证和实例 Secret env/file/VM volume live 注入验证尚未由真实执行证明完成。

`M1-SECRETS-LIVE-B` 已补充 `validate-secrets-live-gate --live --evidence-output` 证据归档能力，可记录 tenant、Gateway 地址、secret_id、namespace、Pod 和 VM，且不归档 bearer token 或 Secret 明文；该能力不代表真实 lab live 模式已执行成功。

`M1-REAL-LAB-D/E/F/G/H/I/J/K/L/M/N/O/P/Q/R` 已补充 `validate_real_k8s_profile.py --component-live --component-evidence-dir ... --evidence-output ...`，可按 `contract_gates` 启动所有组件级 `--live --evidence-output` validator，生成包含 total/passed/failed、失败 returncode 和 error 的汇总 JSON；每个 `contract_gates[]` 已声明 `required_env`，缺少必需环境变量时会先写出 `component_live_preflight_failed` summary 并返回非零状态；`--component-live` 在组件 validator 返回 0 后会确认对应 evidence JSON 文件存在，并解析 evidence JSON，只有 JSON object 标记 `status=passed` 或 `passed=true` 时才把 gate 计为 passed，缺失、malformed 或 non-passing evidence 都会计为 failed；`--component-env-template-output` 可生成不含密钥值的 shell env 模板，`--component-env-file` 可在不 shell source 的前提下加载填充后的模板并传递给组件 validator 子进程，`--component-preflight` 可只检查必需 env 而不启动 live validators，`--component-gate <id>` 可把 preflight 或 live run 限定到一个或多个 indexed gate，`--component-report <summary.json>` 可从 component summary 分类 failed/blocked gate、生成 selected preflight/live 复跑命令、拒绝引用当前 profile 未知 gate id 的 stale summary，并在 `gate_details` 保留 failed/blocked gate 的 missing_env、returncode 和 error；对 summary 中 passed gate 声明的 `evidence_output`，`--component-report` 会审计文件是否存在且 evidence JSON 是否 passing，缺失、malformed 或 non-passing evidence 会在 report 中归为 failed；report 写出后会包含 passed 与 unresolved_gates，若仍有 failed/blocked gate，CLI 会以非零状态退出。该能力不代表组件级真实 lab live 模式已执行成功。

因此当前入口仍停留在 Sprint 5 收敛与后续切片，不进入 Sprint 6。从 Sprint 5 起，K8s、Kube-OVN、KubeVirt、vCluster、KMS/SM4、K8s Secret 注入等真实底座组件必须并行建设验证环境；local profile 只能证明 API/SDK/状态机/调用边界，不能证明真实组件已经跑通。`REAL-K8S-LAB-A` 是当前真实底座验证批次，默认通过 `make validate-real-k8s-profile` 校验门禁定义和 `contract_gates` 组件级 gate 索引；总入口 `--live` 支持 `--evidence-output` 写出 JSON 证据文件；`M1-NETWORK-LIVE-A/B` / `make validate-kubeovn-network-live-gate` 是 Kube-OVN `Vpc/Subnet`、NetworkPolicy 和 Service/LB 的固定验证入口，且支持 `--evidence-output` 归档 JSON 证据；`M1-KUBEVIRT-LIVE-A/B` / `make validate-kubevirt-vm-live-gate` 是 KubeVirt VM lifecycle 与 console/VNC 的固定验证入口，且支持 `--evidence-output` 归档 JSON 证据；`M1-K8S-LIVE-A` / `make validate-vcluster-live-gate` 是 vCluster Helm/kubeconfig/live proxy 的固定验证入口，且支持 `--evidence-output` 归档 JSON 证据；`M1-K8S-LIVE-C` / `make validate-vcluster-upgrade-live-gate` 是 vCluster upgrade live 验证固定入口，且支持 `--evidence-output` 归档 JSON 证据；`M1-K8S-LIVE-B` / `make validate-k8s-node-pool-live-gate` 是 Cluster API node pool 扩缩容与 GPU 调度 live 验证固定入口，且支持 `--evidence-output` 归档 JSON 证据；`M1-RECONCILE-LIVE-A/B` / `make validate-reconcile-ha-live-gate` 是 controller 多副本 HA failover live 验证固定入口，且支持 `--evidence-output` 归档 JSON 证据；`M1-ENCRYPT-LIVE-A/B` / `make validate-kms-sm4-live-gate` 是 KMS/SM4 provider streaming 与对象存储 round trip live 验证固定入口，且支持 `--evidence-output` 归档 JSON 证据；`M1-SECRETS-LIVE-A` / `make validate-secrets-live-gate` 是 Kubernetes Secret env/file/VM 注入 live 验证固定入口。三台云 VM 就绪后必须使用 live 模式形成真实验证记录。后续文档更新必须以真实代码、OpenAPI 契约、测试和真实环境验证记录共同落地为准。

`M1-SECRETS-LIVE-A/B` / `make validate-secrets-live-gate` 当前支持 `--evidence-output` 归档 JSON 证据；三台云 VM 就绪后仍需执行真实 Secret live gate 并归档 evidence。

`M1-REAL-LAB-D/E/F/G/H/I/J/K/L/M/N/O/P/Q/R` 提供组件级 live gate 的统一启动入口、失败聚合摘要、required env preflight、env template、env file loader、preflight-only mode、component gate selector、component summary report、stale summary guard、report diagnostic details、component evidence integrity guard、component evidence content guard、component report passed-evidence audit、component report unresolved exit guard 和 component report overall status；三台云 VM 和组件依赖就绪后仍需先生成并填充 env template，再通过 `--component-preflight --component-env-file ...` 完成配置检查，必要时用 `--component-gate <id>` 只检查或重跑单个 gate，最后执行 `validate_real_k8s_profile.py --component-live --component-env-file ...` 并归档每个组件 gate 的 evidence 与汇总 JSON；如果 summary 中存在 failed/blocked gate，或 passed gate 的 `evidence_output` 缺失、malformed、non-passing，用 `--component-report <summary.json>` 生成下一轮 selected preflight/live 复跑命令并查看 `gate_details` 的缺失 env、返回码和错误摘要；只要 report 仍有 failed/blocked gate，CLI 会非零退出。

`CORE-DEV-PROFILE-A` 是 Core dev/local profile 与 Services 业务 mock 的稳定边界名称：Core 可以提供合同兼容的本地开发剖面，但不得承载 Services 业务 mock。

`SPEC-SPLIT-A` 已完成：`/models`、`/inference-services`、`/knowledge-bases` 只保留在 Services API，Core API 和 Core SDK 不再承载这些业务路径。

`SPEC-CORE-BETA` 已完成首个切片：`repo/api/core-beta-readiness.yaml` 和 `make validate-core-beta` 用于持续校验 Core P0 path/schema、分页、幂等、状态机、RBAC scope 和 Core/Services 关联边界。

`SPEC-COMPAT-A` 已完成首个切片：`repo/api/core-v1-compatibility-baseline.yaml` 和 `make validate-core-api-compatibility` 用于持续保护 Core API v1 的 path/method/operationId/参数/响应/schema 字段，允许新增可选能力但阻止破坏性变更。

`SDK-BETA-A` 已完成首个切片：四语言 SDK 已生成 `idempotency_key` helper，并通过 `make validate-sdk-beta` 持续校验。

`SDK-BETA-B` 已完成首个切片：四语言 SDK 已生成 cursor 分页 helper，并在 SDK metadata 中标出支持 `limit/cursor` 的 Core 列表操作。

`SDK-BETA-C` 已完成首个切片：四语言 SDK 已生成统一 API error helper，并在 SDK metadata 中标出 API 契约声明的标准错误码。

`SDK-BETA-D` 已完成首个切片：四语言 SDK 已生成 basic example，覆盖 client 初始化、幂等、cursor 分页和 API error helper 的组合用法。

`SDK-MOCK-SMOKE-A` 已完成首个切片：Core Python SDK 已提供标准库 HTTP `request()` 能力，并通过 `make validate-sdk-mock-smoke` 调用由 API 契约驱动的 Core Mock Server。

`SDK-MOCK-SMOKE-B` 已完成首个切片：Core TypeScript SDK 已提供基于 `fetch` 的 `request()` 能力，并通过 `make validate-sdk-mock-smoke` 调用同一个 Core Mock Server。

`SDK-MOCK-SMOKE-C` 已完成首个切片：Core Go SDK 已提供基于 `net/http` 的 `Request()` 能力，并通过 `make validate-sdk-mock-smoke` 调用同一个 Core Mock Server。

`SDK-MOCK-SMOKE-D` 已完成首个切片：Core Java SDK 已提供基于 `java.net.http.HttpClient` 的 `request()` 能力；有 JDK 时调用同一个 Core Mock Server，无 JDK 时执行 source smoke。

`MOCK-A` 已完成首个切片：Core Mock Server 由 `repo/api/openapi/v1.yaml` 驱动，`make validate-mock-a` 校验全量 Core path 可 mock。

`DOC-API-A` 已完成首个切片：Core/Services 静态 API 文档由 API 契约生成到 `repo/docs/api/`，`make validate-doc-api` 校验 operation 和 schema 覆盖。

`SPRINT4-CLOSURE-A` 已完成首个切片：`make validate-sprint4-closure` 统一校验 Sprint 4 API/SDK/Mock/Docs/Records 关联性闭环。

---

## 唯一真实来源矩阵

| 问题 | 先看哪里 | 说明 |
|---|---|---|
| 当前做什么 | `repo/CURRENT-SPRINT.md` | 当前 Sprint 的执行入口，状态、任务、验收命令以它为准 |
| 全局开发节奏 | `ANI-06-开发计划.md` | Sprint 计划、Services 解锁门禁、延期项以它为准 |
| 产品功能边界 | `ANI-02-产品功能设计.md` | Core/Services 分层、v1.0.0 P0 能力边界以它为准 |
| 系统架构图和模块边界 | `ANI-05-系统架构设计.md` | Core/Services、API/SDK、ports/adapters、local profile/real provider 的结构图以它为准 |
| 路线图阶段 | `ANI-03-产品路线图.md` | Phase 1/2/3 与版本号关系以它为准 |
| 工程约定和 AI 工作规则 | `CLAUDE.md` | AI/人类开发前必须先读；只维护稳定规则和入口，不维护批次流水账 |
| API 契约 | `repo/api/openapi/v1.yaml` | Core OpenAPI REST API 与 Core/Services 跨层控制面契约的唯一真实来源 |
| API Beta 准备矩阵 | `repo/api/core-beta-readiness.yaml` | Core P0 Beta 审查、兼容性和自动校验矩阵 |
| API 兼容性基线 | `repo/api/core-v1-compatibility-baseline.yaml` | Core API v1 已交付 path/schema 的防破坏基线 |
| Services API 契约 | `repo/api/openapi/services/v1.yaml` | Services 层业务 API 契约 |
| 已完成批次 | `repo/development-records/README.md` | 历史完成记录索引，不作为当前任务清单 |
| 单批次细节 | `repo/development-records/*.md` | 追溯实现、验证和关键文件时再读 |
| 审查提示词模板 | `ANI-10-GPT审查提示词集.md` | 只作为审查问题模板；内置示例不得作为当前事实来源 |

---

## 推荐阅读路径

### 人类开发者

1. `ANI-DOCS-INDEX.md`
2. `CLAUDE.md` 的 5 分钟快速上手
3. `repo/CURRENT-SPRINT.md`
4. `ANI-06-开发计划.md` Section 零和当前 Sprint
5. `ANI-05-系统架构设计.md`
6. `repo/api/openapi/v1.yaml` + `repo/api/openapi/services/v1.yaml` + 相关代码入口

### AI 编码工具

1. 必须先读 `CLAUDE.md`
2. 再读 `repo/CURRENT-SPRINT.md`
3. 开发前检查 `ANI-06-开发计划.md` Section 零
4. 涉及架构边界时检查 `ANI-05-系统架构设计.md`
5. 涉及接口时先改 `repo/api/openapi/v1.yaml` 或 `repo/api/openapi/services/v1.yaml`
6. 完成后按 `CLAUDE.md` 的进度更新规约闭环

---

## 当前开发门禁

| 日期 | 门禁 | 当前影响 |
|---|---|---|
| 2026-05-31 | P0 依赖矩阵冻结 | 已完成历史批次归档，后续只按当前 Sprint 补缺口 |
| 2026-06-10 | Core API Alpha Freeze | 已完成 instances 等核心路径冻结；新增能力必须保持兼容性 |
| 2026-06-20 | SDK Alpha | 四语言 Core/Services SDK 已可生成，并由 SDK Beta/Mock smoke 持续校验 |
| 2026-06-30 | Core Dev Profile Ready | Core dev/local profile 边界已建立；Sprint 5 继续补真实 provider |
| 2026-07-31 | Core Real Path Beta | 当前关键门禁：K8s/Kube-OVN/KubeVirt/vCluster 真实底座验证和真实 provider 主链路 |
| 2026-09-30 | v1.0.0 Final Delivery | ANI Core v1.0.0 + ANI Services P0 |

---

## 文档维护规则

1. 当前阶段变更时，必须同步 `ANI-DOCS-INDEX.md`、`ANI-06-开发计划.md` 和 `repo/CURRENT-SPRINT.md`。
2. 批次完成时，必须新增或更新 `repo/development-records/{批次名}.md`，并更新 `repo/development-records/README.md`。
3. 历史归档文档允许保留当时日期和上下文，不反向改写为当前态。
4. 若 `CLAUDE.md` 与其它文档冲突，以 `CLAUDE.md` 的工程规则为准；若是进度状态冲突，以 `ANI-06-开发计划.md` Section 零和 `repo/CURRENT-SPRINT.md` 为准。
5. `CLAUDE.md` 只保留稳定强制规则、读取顺序、架构边界、提交门禁和 Karpathy 五条开发原则；禁止写入单批次完成清单、API path 长列表、文件级变更清单和每日开发流水账。
6. 动态进度只维护在 `repo/CURRENT-SPRINT.md`、`ANI-06-开发计划.md` Section 零和 `repo/development-records/*.md`；入口文档只保留当前状态、下一步和链接。
7. 更换 AI 模型或工具时，必须先重新读取本文件、`CLAUDE.md` 和 `repo/CURRENT-SPRINT.md`，不得依赖上一个会话的记忆。
8. 修改文档入口后必须运行 `make validate-doc-entrypoints`，确认 `CLAUDE.md` 没有重新承担动态进度记录职责。
