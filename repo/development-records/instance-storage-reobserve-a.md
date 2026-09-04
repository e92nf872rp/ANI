# INSTANCE-STORAGE-REOBSERVE-A — 存储 pending 状态 re-observe 与 WFFC 挂载放行

完成日期：2026-09-03
对应 Sprint：Sprint 13/14 之间的实例链路热修复（hotfix/network-store-read）
验证结果：`go test ./pkg/adapters/runtime/` 目标回归测试全通（仅 Windows 本机环境必挂的 sandbox symlink 用例失败，与本次改动无关）；`go build ./...`（pkg + ani-gateway）通过；gofmt 清洁。**live 验证通过**（镜像 `dev-20260903-reobserve`，2026-09-03）：挂载 Pending 文件系统的容器实例创建 201（`inst_a60c1092-c55b-4937-bbe7-180826baea35`，real provider），Pod 成为 WFFC 首消费者后 `fs_306220e7`（NFS）经 re-observe 由 pending 转 available。

## 实现了什么

修复租户卷/文件系统状态永远停在 `pending`、文件系统永远无法被实例挂载的问题：

1. **存储状态观测链路**：卷/文件系统的 provider 观测只在创建 apply 后执行一次，WaitForFirstConsumer PVC 在那一刻必然是 Pending，之后再无任何 re-observe 途径——即使 PVC 后续绑定，控制面状态也永远停在 pending。`GetVolume`/`GetFilesystem`（store 路径与内存路径）现对 pending 记录发起 provider Observe 并把新状态持久化到 store 与内存 map（与实例状态 observe-on-read 同一架构约定），同一资源 30 秒节流一次防 UI 轮询打爆 K8s API。
2. **resolver 文件系统门禁**：文件系统原来要求 `Available` 才允许在实例创建时挂载，与 WFFC 语义死锁（PVC 等第一个消费者、消费者等 Available）。对齐卷的语义：`Pending` 放行（挂载本身即第一个消费者），`Failed/Deleting/Deleted` 仍拒绝。
3. **MountFilesystem 的 Creating mount target**：provider 模式下 mount target 创建后为 `Creating`（后端 PVC 未绑定），原门禁只认 `Available`。Pod 实际通过共享 PVC（CSI）挂载而非合成 IP，故 `Creating` 放行，`Error/Deleting` 仍拒绝。

## 根因（真实环境排查，10.10.1.66）

- 卷 `vol_0b7dbc7f`（40Gi）PVC 事件 `ProvisioningFailed ... storageclass "ani-block" not found` ×11724：helm values 声明 `defaultStorageClass: ani-block`（values-only chart，无 templates），但集群部署规格从未创建该 SC。已按既有 `ani-rbd-ssd` 参数在集群手工创建 `ani-block`（WFFC/Retain/可扩容）→ PVC 绑定；清单落地防漂移为独立事项。
- 文件系统 PVC（nfs/cephfs SC）事件 `WaitForFirstConsumer ... waiting for first consumer`：用户界面创建的 `filesystem-NFS-dongjm`/`filesystem-CephFS-dongjm` 从未被任何 Pod 挂载，Pending 为 WFFC 正常语义，叠加第 2/3 条门禁形成死锁。
- 观测断裂：`executeStorageProvider` 是唯一 Observe 调用点且仅在创建时执行；`StorageStatus`/`StorageReconcile` 能力与 `LocalStorageStatusReconciler` 已注入/实现但无调用方。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/adapters/runtime/storage_service.go` | 修改 | 新增 `reobserveStorageState`（合成 ApplyResult 调 Observe，30s 节流，失败降级为 warn）与 `reobserveVolumeState`/`reobserveVolumeStateMemory`/`reobserveFilesystemState`（状态刷新 + store/内存双写）；`GetVolume`/`GetFilesystem` 两条路径接入；`MountFilesystem` mount target 门禁放行 `Creating` |
| `pkg/adapters/runtime/instance_resource_resolver.go` | 修改 | 文件系统门禁 `Available` → `Available 或 Pending`，注释说明 WFFC 首消费者语义 |
| `pkg/adapters/runtime/storage_service_reobserve_test.go` | 新增 | 4 个回归测试：store 回路 pending 卷/文件系统经 Get 转可用并持久化（含 Observe 请求 ApplyResult/refs 形状与节流断言）、resolver Pending/Failed 门禁、Creating mount target 挂载 |

## 完工标准达成

- [x] `go test ./pkg/adapters/runtime/` 目标回归测试全通（含新增 4 个）
- [x] `go build ./...`（pkg 与 ani-gateway module）通过
- [x] `gofmt -l` 清洁
- [x] **live 验证通过（2026-09-03，镜像 `dev-20260903-reobserve`）**：创建挂载 `fs_306220e7` 的容器实例 → 201（`inst_a60c1092`，Pending 放行生效）→ Pod 成为 WFFC 首消费者 → PVC 绑定 → GET 文件系统经 re-observe 转 available
- [x] `git diff --check` 通过

## 备注

- live 验证当天发现并排除了两个环境干扰：① Harbor 上 `rocky:10` 被外加 `ani-purpose-system` 全局标签（当日 03:21 创建），触发 `ImagePurposeMismatch` 拦截容器创建——删除该标签后按命名启发式判定为 container；② **另一个部署管道在我部署后数分钟内用 digest 固定镜像（sha256:72d1af13）覆盖了 gateway Deployment**，导致旧二进制短暂接管流量并复现已修复的 pending 拒绝——已重新 `set image` 到 `dev-20260903-reobserve` 并完成验证。多会话/多管道并发部署 gateway 需要协调（与 2026-09-02 `model-repository-live-20260902` 覆盖事件同模式）。

- mount target 的 `Creating → Available` 状态翻转暂无 re-observe（纯展示问题，不影响挂载），如需要可在后续 reconcile 批次补。
- `ani-block` StorageClass 目前是集群手工创建，yaml 存于仓库外（`ANI/ani-block-storageclass.yaml`），需落 `deploy/real-k8s-lab/` 部署规格并考虑与 helm values `defaultStorageClass` 的一致性校验。
- 幂等语义不变：修改请求体重新提交必须使用新 `idempotency_key`。
