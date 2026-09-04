# m1-sandbox-kata — Kata RuntimeClass lab prep

为 ANI `sandbox_config.runtime_class=sandbox-kata` 准备真实 RuntimeClass。

## 状态（lab，2026-09-03）

- 安装方式：Helm `kata-deploy` chart `4.0.0`
- RuntimeClass：`sandbox-kata`（handler=`kata-qemu`），另有 `kata-qemu`
- 节点标签：`katacontainers.io/kata-runtime=true`（当前 3 个合格节点）
- DaemonSet 镜像：`docker.changqingyun.cn/kubercon/kata-deploy:4.0.0`（由 `quay.io/kata-containers/kata-deploy:4.0.0` 镜像）
- 私有仓库 amd64 manifest digest：`sha256:460128eea49aee30fd023f1eabd94dc75fd694dff9a09ed82daeba5c126b2b0e`
- DaemonSet：3/3 Ready/Available
- 冒烟：`runtimeClassName: sandbox-kata` Pod 已成功跑通，guest kernel `6.18.35`（guest kernel ≠ host）

本目录只覆盖 **RuntimeClass/Kata 底座就绪**，不代表 Sandbox real-provider、子资源或 production ready。

## 存储前置条件

- Sandbox workspace 固定为 5Gi `ReadWriteOnce` PVC，挂载到 `/workspace`。
- PVC 显式使用 ANI 块存储类 `ani-block`；该 StorageClass 必须由目标环境预先提供。
- checkpoint/restore 使用 CSI `VolumeSnapshot`，其 snapshot driver 必须与 `ani-block` 的 RBD CSI driver 一致。
- `WaitForFirstConsumer` 模式下，PVC 会在 Sandbox Pod 完成调度后绑定；不能把创建瞬间的 `Pending` 单独视为失败。

## 安装 / 升级

```bash
helm upgrade --install kata-deploy \
  oci://ghcr.io/kata-containers/kata-deploy-charts/kata-deploy \
  --version 4.0.0 \
  --namespace kube-system \
  --values deploy/manifests/m1-sandbox-kata/kata-deploy-values.yaml \
  --timeout 25m
```

## 验收

```bash
kubectl get runtimeclass sandbox-kata
kubectl get nodes -L katacontainers.io/kata-runtime
kubectl get pods -n kube-system -l name=kata-deploy
kubectl get storageclass ani-block
```

冒烟时使用集群可达的小镜像，并设置 `spec.runtimeClassName: sandbox-kata`。Sandbox 实例验收还应确认 workspace PVC 为 `Bound`、StorageClass 为 `ani-block`，且容器内 `/workspace` 可读写。
