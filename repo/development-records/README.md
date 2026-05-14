# ANI Development Records — 批次归档索引

> 本文件是所有已完成开发批次的**唯一归档索引**。
> 进度追踪三层结构：
> - **全局状态快照** → `ANI-06-开发计划.md` Section 零（30秒定位）
> - **当前冲刺任务** → `repo/CURRENT-SPRINT.md`（每冲刺更新）
> - **已完成批次详情** → 本文件（每批次完成后追加）

---

## 已完成批次（按完成时间排列）

### V8 架构重规划（2026-05-14~15）

| 批次 | 内容摘要 |
|---|---|
| V8-ARCH | Core/Services 分层、ANI-02/06 重写、CLAUDE.md 强制约定 |
| AWS-HARDENING | /healthz /readyz、idempotency_key port、ReconcileController port、operations DB 表、permissions schema |

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

---

## 批次完工的更新流程

```
1. 新建 repo/development-records/{批次名}.md
2. 在本文件追加一行
3. 更新 ANI-06 Section 零 状态快照（✅/🔄/⏳）
4. 更新 repo/CURRENT-SPRINT.md 当前任务状态
5. make test 全通后 git commit
```
