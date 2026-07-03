# ANI Core 组件化部署配置（Isolated）

本文件用于按“组件维度”整理部署配置，**仅使用 `deploy/isolated` 与 `deploy/isolated/deploy.py`**，不依赖 `deploy/real-k8s-lab`。

如需按类别阅读，请优先查看：

- `deploy/isolated/BASE-INFRA-COMPONENTS.md`
- `deploy/isolated/BUSINESS-COMPONENTS.md`

## 1. 部署入口（仅 Isolated）

- 统一部署入口：`python3 deploy/isolated/deploy.py`
- 依赖层阶段：`deploy --only base-infra`
- 服务层阶段：`deploy --only business`
- 依赖清单：`deploy/isolated/base-infra.yaml`
- 服务清单：`deploy/isolated/business-stack.yaml`

## 2. 组件依赖关系

先部署基础依赖，再部署 services 组件：

1. 数据与消息基础层：PostgreSQL、Redis、NATS
2. 对象/向量/观测依赖层：MinIO（S05）、Milvus（含 etcd/minio）、Prometheus
3. 业务服务层：model-service、task-service、reconcile-worker

## 3. 统一环境变量与 Secret

### 3.1 依赖层（`deploy.py deploy --only base-infra`）

可覆盖环境变量（有默认值）：

- `POSTGRES_PASSWORD`（默认 `ani_dev_password`）
- `REDIS_PASSWORD`（默认 `ani_dev_password`）
- `MINIO_ACCESS_KEY_ID`（默认 `ani-minio-access`）
- `MINIO_SECRET_ACCESS_KEY`（默认 `ani-minio-secret`）
- `MILVUS_MINIO_ACCESS_KEY`（默认 `ani-milvus-access`）
- `MILVUS_MINIO_SECRET_KEY`（默认 `ani-milvus-secret`）

会创建以下 Secret：

- `ani-system/ani-postgres-secret`
- `ani-system/ani-redis-secret`
- `ani-system/ani-s05-minio-root`
- `ani-system/milvus-minio`

### 3.2 服务层（`deploy.py deploy --only business`）

可覆盖环境变量（有默认值）：

- `NAMESPACE`（默认 `ani-system`）
- `POSTGRES_PASSWORD`（默认 `ani_dev_password`）
- `REDIS_PASSWORD`（默认 `ani_dev_password`）
- `NATS_URL`（默认 `nats://nats.ani-system.svc.cluster.local:4222`）

脚本会在目标 namespace 创建 `ani-services-runtime`，包含：

- `database_url`
- `redis_url`
- `nats_url`
- `oidc_*`
- `auth_jwt_issuer`
- `jwt_private_key_pem`
- `jwt_public_key_pem`

`business-stack.yaml` 中业务组件均通过 `secretKeyRef` 读取上述键。

## 4. 按组件配置清单

## 4.1 PostgreSQL（ani-postgres）

- 命名空间：`ani-system`
- 清单来源：`deploy/isolated/base-infra.yaml`
- 工作负载：`StatefulSet/ani-postgres`
- 服务：`Service/ani-postgres`
- 凭据来源：`Secret/ani-postgres-secret`（`POSTGRES_USER`、`POSTGRES_PASSWORD`、`POSTGRES_DB`）

验证：

```bash
kubectl -n ani-system get sts ani-postgres
kubectl -n ani-system get svc ani-postgres
kubectl -n ani-system rollout status sts/ani-postgres --timeout=240s
```

## 4.2 Redis（ani-redis）

- 命名空间：`ani-system`
- 清单来源：`deploy/isolated/base-infra.yaml`
- 工作负载：`Deployment/ani-redis`
- 服务：`Service/ani-redis`
- 凭据来源：`Secret/ani-redis-secret`（`password`）

验证：

```bash
kubectl -n ani-system get deploy ani-redis
kubectl -n ani-system get svc ani-redis
kubectl -n ani-system rollout status deploy/ani-redis --timeout=240s
```

## 4.3 NATS（nats）

- 命名空间：`ani-system`
- 清单来源：`deploy/isolated/base-infra.yaml`
- 工作负载：`Deployment/nats`
- 服务：`Service/nats`

验证：

```bash
kubectl -n ani-system get deploy nats
kubectl -n ani-system get svc nats
kubectl -n ani-system rollout status deploy/nats --timeout=240s
```

## 4.4 Object Store MinIO（ani-s05-minio）

- 命名空间：`ani-system`
- 清单来源：`deploy/isolated/base-infra.yaml`
- 工作负载：`Deployment/ani-s05-minio`
- 服务：`Service/ani-s05-minio`
- 凭据来源：`Secret/ani-s05-minio-root`（`access_key_id`、`secret_access_key`）

验证：

```bash
kubectl -n ani-system get deploy ani-s05-minio
kubectl -n ani-system get svc ani-s05-minio
kubectl -n ani-system rollout status deploy/ani-s05-minio --timeout=240s
```

## 4.5 Vector Store Milvus（milvus）

- 命名空间：`ani-system`
- 清单来源：`deploy/isolated/base-infra.yaml`
- 依赖组件：
  - `Deployment/milvus-etcd` + `Service/milvus-etcd`
  - `Deployment/milvus-minio` + `Service/milvus-minio`
- 主组件：
  - `Deployment/milvus` + `Service/milvus`
- 凭据来源：`Secret/milvus-minio`（`access_key`、`secret_key`）

验证：

```bash
kubectl -n ani-system get deploy milvus-etcd milvus-minio milvus
kubectl -n ani-system get svc milvus-etcd milvus-minio milvus
kubectl -n ani-system rollout status deploy/milvus --timeout=240s
```

## 4.6 Observability Prometheus（prometheus）

- 命名空间：`ani-system`
- 清单来源：`deploy/isolated/base-infra.yaml`
- 工作负载：`Deployment/prometheus`
- 服务：`Service/prometheus`
- 配置：`ConfigMap/prometheus-config`
- 服务账号：`ServiceAccount/prometheus`

验证：

```bash
kubectl -n ani-system get sa prometheus
kubectl -n ani-system get configmap prometheus-config
kubectl -n ani-system get deploy prometheus
kubectl -n ani-system rollout status deploy/prometheus --timeout=240s
```

## 4.7 model-service

- 命名空间：`ani-system`（可由 `NAMESPACE` 覆盖）
- 清单来源：`deploy/isolated/business-stack.yaml`
- 工作负载：`Deployment/model-service`
- 服务：`Service/model-service`
- 端口：`9103(grpc)`、`9203(health)`
- 运行时依赖：`ani-services-runtime` 中 `database_url`、`nats_url`、`redis_url`

验证：

```bash
kubectl -n ani-system get deploy model-service
kubectl -n ani-system get svc model-service
kubectl -n ani-system rollout status deploy/model-service --timeout=180s
```

## 4.8 task-service

- 命名空间：`ani-system`（可由 `NAMESPACE` 覆盖）
- 清单来源：`deploy/isolated/business-stack.yaml`
- 工作负载：`Deployment/task-service`
- 服务：`Service/task-service`
- 端口：`9104(grpc)`、`9204(health)`
- 运行时依赖：`ani-services-runtime` 中 `database_url`、`nats_url`、`redis_url`
- 额外参数：`OUTBOX_ENABLED=true`、`OUTBOX_POLL_INTERVAL_MS=500`、`OUTBOX_BATCH_SIZE=100`

验证：

```bash
kubectl -n ani-system get deploy task-service
kubectl -n ani-system get svc task-service
kubectl -n ani-system rollout status deploy/task-service --timeout=180s
```

## 4.9 reconcile-worker

- 命名空间：`ani-system`（可由 `NAMESPACE` 覆盖）
- 清单来源：`deploy/isolated/business-stack.yaml`
- 工作负载：`Deployment/reconcile-worker`
- 服务：`Service/reconcile-worker`
- 端口：`9205(health)`
- ServiceAccount：`ani-gateway`
- 运行时依赖：`ani-services-runtime` 中 `database_url`、`nats_url`、`redis_url`

验证：

```bash
kubectl -n ani-system get deploy reconcile-worker
kubectl -n ani-system get svc reconcile-worker
kubectl -n ani-system rollout status deploy/reconcile-worker --timeout=180s
```

## 5. 推荐部署顺序（组件化）

```bash
# 1) 部署依赖层
python3 deploy/isolated/deploy.py deploy --only base-infra

# 2) 部署服务层（示例版本 dev）
python3 deploy/isolated/deploy.py deploy dev --only business
```

默认使用厂库已有服务镜像；需要现场重新构建并推送时，显式执行：

```bash
python3 deploy/isolated/deploy.py deploy dev --build
```

## 6. 总体验证命令

```bash
kubectl -n ani-system get pods
kubectl -n ani-system get pods | rg 'model-service|task-service|reconcile-worker'
```

如果仅做组件级部署排障，优先从对应 namespace 的 `describe` 和日志开始：

```bash
kubectl -n <namespace> describe pod <pod-name>
kubectl -n <namespace> logs <pod-name>
```
