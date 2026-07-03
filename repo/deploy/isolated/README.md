# Isolated Deploy — 新环境一键部署

不依赖 `deploy/real-k8s-lab`，适用于全新 Kubernetes 集群（已装 Kube-OVN）。

**唯一入口：** `deploy/isolated/deploy.py`

## 前置条件

| 项 | 要求 |
|---|---|
| 集群 | K8s 1.28+，节点可访问 `docker.changqingyun.cn` |
| 工具 | `kubectl`、`helm`、`oras`、`openssl`（只有 `--build` 时需要 `docker`） |
| 凭据 | `docker login docker.changqingyun.cn` |
| 节点磁盘 | 每节点至少 50Gi 可用空间（用于 Ceph OSD loop 文件，无裸盘时自动创建） |

## 部署顺序（有依赖）

```text
render → mirror → foundation:
         KubeVirt → Rook operator → Ceph OSD 预备 → CephCluster → StorageClass ani-rbd-ssd → Harbor(PVC)
       → base-infra(Postgres PVC) → business → verify
```

**必须先有 `ani-rbd-ssd` StorageClass**，才会部署 Harbor 和 Postgres 等业务组件。

## 一键部署

```bash
cd repo
docker login docker.changqingyun.cn
python3 deploy/isolated/deploy.py deploy dev
```

```bash
# 基础/底座镜像已同步到厂库时
python3 deploy/isolated/deploy.py deploy dev --skip-mirror
```

```bash
# 需要现场重新构建并推送 ANI 业务镜像时
python3 deploy/isolated/deploy.py deploy dev --build
```

### 子命令

```bash
python3 deploy/isolated/deploy.py cleanup
python3 deploy/isolated/deploy.py verify
python3 deploy/isolated/deploy.py render
python3 deploy/isolated/deploy.py mirror
python3 deploy/isolated/deploy.py deploy --only foundation
```

## 存储

| 组件 | 方式 |
|---|---|
| Ceph OSD | 每节点 `/var/lib/rook/osd-backing.img`（50Gi loop，由 `ceph-osd-prep` 创建） |
| StorageClass | `ani-rbd-ssd`（Rook RBD） |
| Postgres | PVC 5Gi，`storageClassName: ani-rbd-ssd` |
| Harbor | PVC，`ani-rbd-ssd` |
| MinIO/Milvus 等 | 仍为 emptyDir（dev 可接受；需持久化可后续改 PVC） |

## 访问地址

| 服务 | 地址 |
|---|---|
| Gateway | `http://<node-ip>:30080` |
| MinIO | `http://<node-ip>:30900` |
| Prometheus | `http://<node-ip>:31990` |
| Dex | `http://console.example.local:30556/dex`（外部 issuer） / `http://<node-ip>:30556`（直连探针） |
| Attu | `http://<node-ip>:30300` |
| Harbor | `http://<node-ip>:30002`（admin / `ani-harbor-admin-dev`） |

## 目录结构

```text
deploy/isolated/
├── deploy.py
├── base-infra.yaml
├── business-stack.yaml
└── cluster-foundation/
    ├── ceph-osd-prep.yaml
    ├── rook-ceph-cluster.yaml
    ├── harbor-install-values.yaml
    └── yaml/
```
