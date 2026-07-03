# ANI Isolated 部署配置（基础组件）

本文件只描述基础组件（数据、消息、中间件）部署配置，且仅使用 `deploy/isolated` 路径，不依赖 `deploy/real-k8s-lab`。

## 1. 部署入口

- 统一部署：`python3 deploy/isolated/deploy.py deploy --only base-infra`
- 组件清单：`deploy/isolated/base-infra.yaml`

## 2. 组件清单（基础层）

- `ani-postgres`（PostgreSQL）
- `ani-redis`（Redis）
- `nats`（NATS）
- `ani-s05-minio`（Object Store MinIO）
- `milvus`（Milvus，依赖 etcd/minio）
- `prometheus`（Prometheus）
- `ani-dex`（OIDC 认证）
- `attu`（Milvus Web UI）

## 3. 基础组件统一变量与 Secret

`deploy.py` 的 base-infra 阶段支持以下环境变量（有默认值）：

- `POSTGRES_PASSWORD`（默认 `ani_dev_password`）
- `REDIS_PASSWORD`（默认 `ani_dev_password`）
- `MINIO_ACCESS_KEY_ID`（默认 `ani-minio-access`）
- `MINIO_SECRET_ACCESS_KEY`（默认 `ani-minio-secret`）
- `MILVUS_MINIO_ACCESS_KEY`（默认 `ani-milvus-access`）
- `MILVUS_MINIO_SECRET_KEY`（默认 `ani-milvus-secret`）

脚本会创建：

- `ani-system/ani-postgres-secret`
- `ani-system/ani-redis-secret`
- `ani-system/ani-s05-minio-root`
- `ani-system/milvus-minio`

## 4. 按组件配置与验证

## 4.1 PostgreSQL（ani-postgres）

- 命名空间：`ani-system`
- 工作负载：`StatefulSet/ani-postgres`
- 服务：`Service/ani-postgres`
- Secret：`ani-postgres-secret`

```bash
kubectl -n ani-system get sts ani-postgres
kubectl -n ani-system get svc ani-postgres
kubectl -n ani-system rollout status sts/ani-postgres --timeout=240s
```

## 4.2 Redis（ani-redis）

- 命名空间：`ani-system`
- 工作负载：`Deployment/ani-redis`
- 服务：`Service/ani-redis`
- Secret：`ani-redis-secret`

```bash
kubectl -n ani-system get deploy ani-redis
kubectl -n ani-system get svc ani-redis
kubectl -n ani-system rollout status deploy/ani-redis --timeout=240s
```

## 4.3 NATS（nats）

- 命名空间：`ani-system`
- 工作负载：`Deployment/nats`
- 服务：`Service/nats`

```bash
kubectl -n ani-system get deploy nats
kubectl -n ani-system get svc nats
kubectl -n ani-system rollout status deploy/nats --timeout=240s
```

## 4.4 Object Store（ani-s05-minio）

- 命名空间：`ani-system`
- 工作负载：`Deployment/ani-s05-minio`
- 服务：`Service/ani-s05-minio`
- Secret：`ani-s05-minio-root`

```bash
kubectl -n ani-system get deploy ani-s05-minio
kubectl -n ani-system get svc ani-s05-minio
kubectl -n ani-system rollout status deploy/ani-s05-minio --timeout=240s
```

## 4.5 Vector Store（milvus）

- 命名空间：`ani-system`
- 依赖：`milvus-etcd`、`milvus-minio`
- 主组件：`milvus`
- Secret：`milvus-minio`

```bash
kubectl -n ani-system get deploy milvus-etcd milvus-minio milvus
kubectl -n ani-system get svc milvus-etcd milvus-minio milvus
kubectl -n ani-system rollout status deploy/milvus --timeout=240s
```

## 4.6 Observability（prometheus）

- 命名空间：`ani-system`
- 工作负载：`Deployment/prometheus`
- 服务：`Service/prometheus`
- 组件配置：`ConfigMap/prometheus-config`

```bash
kubectl -n ani-system get sa prometheus
kubectl -n ani-system get configmap prometheus-config
kubectl -n ani-system get deploy prometheus
kubectl -n ani-system rollout status deploy/prometheus --timeout=240s
```

## 5. 最小部署示例

```bash
cd repo
POSTGRES_PASSWORD=ani_dev_password \
REDIS_PASSWORD=ani_dev_password \
MINIO_ACCESS_KEY_ID=ani-minio-access \
MINIO_SECRET_ACCESS_KEY=ani-minio-secret \
MILVUS_MINIO_ACCESS_KEY=ani-milvus-access \
MILVUS_MINIO_SECRET_KEY=ani-milvus-secret \
python3 deploy/isolated/deploy.py deploy --only base-infra
```

