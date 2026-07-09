# Cluster Foundation（集群底座）

由 `python3 deploy/isolated/deploy.py deploy --only foundation` 安装。

## 顺序

```text
KubeVirt → CDI → Rook operator → ceph-osd-prep → CephCluster → ani-rbd-ssd → Harbor(PVC)
```

CDI 固定为 `v1.65.0`，在 KubeVirt 后安装。operator manifest 中的
CDI 组件镜像已切到 `docker.changqingyun.cn/mirror/kubevirt/*:v1.65.0`，
并通过 `cdi-uploadproxy-nodeport` 把 uploadproxy selector 暴露为 NodePort
`31001`，供后续 ISO 直传到 CDI。

Ceph 在每个节点准备 50Gi OSD block device：优先从 `ubuntu-vg` 创建 `rook-osd`
逻辑卷；如果新集群节点没有该卷组，则回退为 `/var/lib/rook/osd-backing.img`
file-backed loop device。两种路径都会把实际设备写入
`/var/lib/rook/osd-block-device`，供 `deploy.py` 注入 CephCluster。

## 配置

| 文件 | 用途 |
|---|---|
| `yaml/06-cdi-operator.yaml` | CDI v1.65.0 operator / CRD 官方 release manifest（镜像改为本地 mirror） |
| `yaml/07-cdi-cr.yaml` | CDI CR，固定 `ani-rbd-ssd` scratch StorageClass；镜像 pin 由 operator manifest 承载 |
| `yaml/08-cdi-uploadproxy-nodeport.yaml` | `cdi-uploadproxy-nodeport` NodePort `31001` |
| `ceph-osd-prep.yaml` | 节点 OSD block device 预备（LVM 优先，loop fallback） |
| `rook-ceph-cluster.yaml` | BlockPool + StorageClass 模板 |
| `rook-ceph-install-values.yaml` | Rook operator 厂库镜像 |
| `harbor-install-values.yaml` | Harbor PVC（`ani-rbd-ssd`） |

完整流程见 [README.md](../README.md)。
