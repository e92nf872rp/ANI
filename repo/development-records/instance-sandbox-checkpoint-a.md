# INSTANCE-SANDBOX-CHECKPOINT-A

> 日期：2026-08-02
> 范围：ANI Core / Kubernetes Sandbox / Rook-Ceph PVC / CSI VolumeSnapshot
> 状态：live passed

## 契约依据

- 复用已确认的 Core v1 checkpoint create/list/restore/clone 契约，不修改 `api/openapi/v1.yaml`、SDK、CLI 或 Console。
- 只提供 filesystem checkpoint；`keep_memory=true` 返回 422。
- 新 Sandbox 使用 PVC；历史 `emptyDir` Sandbox 返回 422，不做在线迁移。
- Services 不在本批范围；真实调用保持 default 网络，不依赖尚未打通的私有 VPC。

## 实现

- 新 Sandbox 渲染 5Gi `ReadWriteOnce` workspace PVC，Deployment 的 `/workspace` 改挂 PVC；resource refs 同时持久化 PVC 和 Deployment。
- checkpoint 创建先将 Deployment 缩到 0，等待 Pod 消失，再创建带 tenant/instance/checkpoint 标签的 CSI `VolumeSnapshot`，ready 后恢复原运行状态。
- list 以 VolumeSnapshot 为 provider source of truth，Gateway 重启后仍可查询。
- restore 删除并从 snapshot 重建同名 PVC；原 running 实例恢复到 1 副本，paused/stopped 保持 0。
- clone 继承源 Sandbox 镜像，并通过内部 snapshot ref 为新 PVC 设置 `dataSource`。
- v1 没有 checkpoint DELETE；删除源 Sandbox 时按 tenant + instance 标签级联清理其 managed VolumeSnapshot。
- 新增 `INSTANCE-SANDBOX-CHECKPOINT-LIVE-GATE-A`、脱敏 evidence validator，以及 ceph-csi snapshot status 最小 RBAC 清单。

## 真实验证

Gateway 镜像：

```text
docker.changqingyun.cn/ani/ani-gateway:instance-sandbox-checkpoint-20260802-v1
sha256:e6ca3965932cd0ca092a195b401f87829d08b8f27bef5c5c234d7656f81c20e8
```

default 网络真实顺序已通过：

- 新 Sandbox 的 5Gi RBD PVC `Bound`，Deployment 使用 `sandbox-kata` 并挂载 workspace PVC；
- 写入 `version-1`，checkpoint 达到 `readyToUse`；覆盖为 `version-2` 后 rollout restart Gateway；
- 重启后 list 仍返回 provider checkpoint，create AsyncTask 仍可查询；
- restore 后 code-run 读回 `version-1`，restore AsyncTask 为 completed；
- clone 获得不同 UUID、继承源镜像，snapshot-backed PVC `Bound`，code-run 读回 `version-1`；
- `keep_memory=true` 和历史 Deployment-only Sandbox 均返回 422；
- 删除 clone 与源实例后 Deployment、Pod、PVC、managed VolumeSnapshot 全部归零；实例删除后 create/restore task 仍可查询。

首次 live 暴露 ceph-csi ServiceAccount 缺少 `volumesnapshotcontents/status` patch 权限；已用独立最小 ClusterRole/Binding 固化到 `deploy/real-k8s-lab/instance-sandbox-checkpoint-csi-rbac.yaml`。

脱敏证据：`development-records/live-evidence/instance-sandbox-checkpoint-live-20260802.json`。

## 能力边界

- checkpoint 是 crash-consistent filesystem snapshot，不包含内存、进程或连接状态。
- 历史 `emptyDir` Sandbox 必须重建为 PVC-backed Sandbox 后才能使用 checkpoint。
- 单次 provider 等待预算为 60 秒；CSI controller leader 接管或底座故障超过预算时返回失败，原幂等 key 重放原失败结果，新逻辑操作必须使用新 key。
- 本批证明 default 网络下真实 provider 闭环，不宣称私有 VPC 已打通，也不外推为 full platform production ready。
