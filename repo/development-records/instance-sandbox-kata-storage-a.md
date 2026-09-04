# INSTANCE-SANDBOX-KATA-STORAGE-A

> 日期：2026-09-03
> 范围：Kata lab 部署基线与 Sandbox workspace PVC StorageClass 修复
> 状态：部署底座 live verified；代码 local/logic verified

## 问题

- ANI 仓库仍引用旧的 `docker.kubercon.local` Kata 镜像地址，与当前私有仓库不一致。
- Sandbox 创建和 checkpoint restore 重建 workspace PVC 时没有写 `storageClassName`；目标集群没有默认 StorageClass 时，PVC 会持续 `Pending`，Pod 无法启动。

## 修改

- `deploy/manifests/m1-sandbox-kata/kata-deploy-values.yaml` 改用 `docker.changqingyun.cn/kubercon/kata-deploy:4.0.0`。
- Kata lab README 同步 3 节点、镜像 digest、RuntimeClass 和存储验收条件。
- Sandbox 新建、clone 和 restore 的 5Gi `ReadWriteOnce` workspace PVC 显式使用 `ani-block`。
- 单元测试覆盖普通创建、从 checkpoint 创建和 restore 重建 PVC 三条路径。

## 验证边界

- 目标集群 `kata-deploy` DaemonSet 为 3/3 Ready/Available，`sandbox-kata` 冒烟 Pod 成功，guest kernel 为 `6.18.35`。
- 现有 `sandbox-workspace` PVC 手工设置 `ani-block` 后达到 `Bound`，Sandbox Pod 使用 `sandbox-kata` 运行且 `/workspace` 可见 5Gi 文件系统。
- 本批代码在合并和 Gateway rollout 前只称为 local/logic verified；现有 PVC 的手工修复不能替代新代码的 rollout 后 E2E。
- 按本次任务范围，不把节点 sysctl 配置写入 ANI 仓库。
