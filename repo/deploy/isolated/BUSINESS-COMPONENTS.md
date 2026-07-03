# ANI Isolated 部署配置（业务组件）

本文件描述业务组件（Core 业务服务）部署配置，按组件拆分，并与基础组件解耦说明。

## 1. 范围

业务组件包含：

- `ani-gateway`
- `ani-auth-service`
- `model-service`
- `task-service`
- `reconcile-worker`

## 2. 镜像使用

默认部署直接使用厂库中已存在的 5 个组件镜像：

```bash
cd repo
python3 deploy/isolated/deploy.py deploy dev --skip-mirror
```

需要现场重新构建并推送时，显式加 `--build`：

```bash
python3 deploy/isolated/deploy.py deploy dev --build
```

对应镜像名：

- `docker.changqingyun.cn/ani/ani-gateway:dev`
- `docker.changqingyun.cn/ani/ani-auth-service:dev`
- `docker.changqingyun.cn/ani/model-service:dev`
- `docker.changqingyun.cn/ani/task-service:dev`
- `docker.changqingyun.cn/ani/reconcile-worker:dev`

## 3. 当前 Isolated 部署入口

`business-stack.yaml` 已覆盖全部 5 个业务组件（gateway / auth / model / task / reconcile）与 gateway RBAC；`ani-services-runtime` 由部署脚本生成。

入口：

- 清单：`deploy/isolated/business-stack.yaml`

```bash
python3 deploy/isolated/deploy.py deploy dev --only business
```

## 4. model/task/reconcile 组件配置

## 4.1 统一运行时 Secret

`deploy.py` business 阶段会创建 `ani-services-runtime`（默认 namespace `ani-system`），包含：

- `database_url`
- `nats_url`
- `redis_url`
- `oidc_*`
- `auth_jwt_issuer`
- `jwt_private_key_pem`
- `jwt_public_key_pem`

业务组件都通过 `secretKeyRef` 读取该 Secret。

## 4.2 model-service

- 工作负载：`Deployment/model-service`
- 服务：`Service/model-service`
- 端口：`9103`（grpc）、`9203`（health）

```bash
kubectl -n ani-system get deploy model-service
kubectl -n ani-system get svc model-service
kubectl -n ani-system rollout status deploy/model-service --timeout=180s
```

## 4.3 task-service

- 工作负载：`Deployment/task-service`
- 服务：`Service/task-service`
- 端口：`9104`（grpc）、`9204`（health）
- outbox 参数：
  - `OUTBOX_ENABLED=true`
  - `OUTBOX_POLL_INTERVAL_MS=500`
  - `OUTBOX_BATCH_SIZE=100`

```bash
kubectl -n ani-system get deploy task-service
kubectl -n ani-system get svc task-service
kubectl -n ani-system rollout status deploy/task-service --timeout=180s
```

## 4.4 reconcile-worker

- 工作负载：`Deployment/reconcile-worker`
- 服务：`Service/reconcile-worker`
- 端口：`9205`（health）
- ServiceAccount：`ani-gateway`

```bash
kubectl -n ani-system get deploy reconcile-worker
kubectl -n ani-system get svc reconcile-worker
kubectl -n ani-system rollout status deploy/reconcile-worker --timeout=180s
```

## 5. 业务组件部署示例

先确保基础组件已就绪（见 `deploy/isolated/BASE-INFRA-COMPONENTS.md`），再执行：

```bash
cd repo
POSTGRES_PASSWORD=ani_dev_password \
REDIS_PASSWORD=ani_dev_password \
NATS_URL=nats://nats.ani-system.svc.cluster.local:4222 \
python3 deploy/isolated/deploy.py deploy dev --only business
```

## 6. 一键入口（当前能力）

`python3 deploy/isolated/deploy.py deploy dev` 当前流程：

1. 渲染/同步依赖镜像
2. 部署 foundation 与 isolated 基础组件
3. 部署业务组件
4. 输出健康检查摘要
