# m1-sandbox-kata — Kata RuntimeClass lab prep

为 ANI `sandbox_config.runtime_class=sandbox-kata` 准备真实 RuntimeClass。

## 状态（lab）

- 安装方式：Helm `kata-deploy` chart `4.0.0`
- RuntimeClass：`sandbox-kata`（handler=`kata-qemu`），另有 `kata-qemu`
- 节点标签：`katacontainers.io/kata-runtime=true`（node1/node2）
- DaemonSet 镜像：`docker.kubercon.local/common/kata-deploy:4.0.0`（由 `quay.io/kata-containers/kata-deploy:4.0.0` 镜像）
- 冒烟：`runtimeClassName: sandbox-kata` Pod 已成功跑通（guest kernel ≠ host）

本目录只覆盖 **RuntimeClass/Kata 底座就绪**，不代表 Sandbox real-provider、子资源或 production ready。

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
```

冒烟时使用集群可达的小镜像，并设置 `spec.runtimeClassName: sandbox-kata`。
