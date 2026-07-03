# Cluster Foundation（集群底座）

由 `python3 deploy/isolated/deploy.py deploy --only foundation` 安装。

## 顺序

```text
KubeVirt → Rook operator → ceph-osd-prep → CephCluster → ani-rbd-ssd → Harbor(PVC)
```

Ceph 在每个节点从 `ubuntu-vg` 创建 `rook-osd` 逻辑卷（50Gi），无需裸盘。

## 配置

| 文件 | 用途 |
|---|---|
| `ceph-osd-prep.yaml` | 节点 OSD LVM 预备 |
| `rook-ceph-cluster.yaml` | BlockPool + StorageClass 模板 |
| `rook-ceph-install-values.yaml` | Rook operator 厂库镜像 |
| `harbor-install-values.yaml` | Harbor PVC（`ani-rbd-ssd`） |

完整流程见 [README.md](../README.md)。
