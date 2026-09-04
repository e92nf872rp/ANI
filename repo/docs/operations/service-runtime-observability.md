# ANI 七服务 Prometheus 状态对接与部署指南

本文面向需要读取 ANI 组件运行状态的后端调用方和测试环境维护者，说明如何通过 Core API 或 Prometheus 获取七个服务的监控可达性，以及如何把该能力合并到现有 Kubernetes Deployment。

## 1. 能力边界

当前覆盖以下七个服务：

| 服务 | 管理端口 |
|---|---:|
| `ani-gateway` | 9200 |
| `auth-service` | 9201 |
| `model-service` | 9203 |
| `task-service` | 9204 |
| `inference-service` | 9204 |
| `tenant-service` | 9205 |
| `metering-service` | 9210 |

这里的“状态”仅表示 Prometheus 能否抓取服务的 `/metrics`，即 `signal=prometheus_scrape`。它不表示：

- 业务请求一定成功；
- 数据库、Redis、NATS 等依赖一定健康；
- Pod 已满足 Kubernetes readiness；
- 全平台所有组件均已被覆盖。

调用方不得把该结果显示或传播为“业务健康”或“整个平台健康”。

## 2. 推荐对接方式：Core 聚合接口

业务调用方优先使用 Core API，不要自行复制 Prometheus 聚合算法：

```http
GET /api/v1/platform/services/health
Authorization: Bearer <access-token>
```

- OpenAPI `operationId`：`getPlatformServiceHealth`
- 权限：`scope:observability:read`
- 成功：HTTP 200，固定返回七个组件
- 未启用 provider：HTTP 503，错误码 `OBSERVABILITY_NOT_CONFIGURED`
- Prometheus 不可用、超时、响应非法或指标身份契约失败：HTTP 503，错误码 `OBSERVABILITY_UNAVAILABLE`

调用示例：

```bash
curl -fsS \
  -H "Authorization: Bearer ${ANI_ACCESS_TOKEN}" \
  "${ANI_GATEWAY_BASE_URL}/api/v1/platform/services/health"
```

成功响应的固定包络如下：

```json
{
  "scope": "ani_services",
  "coverage": "partial",
  "signal": "prometheus_scrape",
  "observed_at": "2026-09-04T10:00:00Z",
  "source_status": "ok",
  "components": [
    {
      "service_name": "ani-gateway",
      "scrape_status": "reachable",
      "observed_replicas": 1,
      "reachable_replicas": 1,
      "versions": ["v0.1.0-test.1"],
      "sample_age_seconds": 7.2
    }
  ]
}
```

示例只展示一个数组元素；真实 200 响应始终包含固定七个服务。

字段语义：

| 字段 | 语义 |
|---|---|
| `scrape_status` | `reachable`、`unreachable` 或 `unknown` |
| `observed_replicas` | 45 秒新鲜度窗口内存在 `up` 样本的目标数 |
| `reachable_replicas` | 新鲜 `up=1` 且 OTel `target_info` 身份校验通过的目标数 |
| `versions` | K8s 标签与 `target_info` 交叉校验后得到的版本集合 |
| `sample_age_seconds` | 被纳入聚合的 fresh `up` 样本中，最旧样本的年龄；没有 fresh target 时为 `null` |

组件状态判定：

| 条件 | `scrape_status` |
|---|---|
| 至少一个目标的 `up=1`，且对应 `target_info` 唯一、fresh、身份有效 | `reachable` |
| 存在 fresh `up` 目标，且所有目标均为 `up=0` | `unreachable` |
| 没有 fresh `up` 目标，包括目标未发现、样本缺失或样本超过 45 秒 | `unknown` |

HTTP 503 表示整个数据源或契约不可用，不等价于七个组件均为 `unknown`。调用方应保留上次成功值并明确标记其已过期，或直接显示“监控数据源不可用”；不能把 503 降级成正常 200。

## 3. 直接从 Prometheus 取数

仅基础设施集成、排障工具或无法调用 Core API 的内部程序需要直接查询 Prometheus。直接对接必须完整实现下面的四查询和聚合规则，否则容易把旧样本或身份冲突误判为健康。

### 3.1 发现契约

Prometheus 使用单一 job：

```yaml
job_name: ani-components
scrape_interval: 15s
scrape_timeout: 5s
metrics_path: /metrics
```

Kubernetes Pod 同时满足以下条件才会被发现：

- Pod label `ani.dev/metrics-scrape="true"`；
- 容器端口名为 `health`；
- Pod phase 为 `Running`；
- Pod label `ani.dev/service-name` 是固定七服务之一。

发现后必须保留这些 target label：

- `ani_service_name`
- `k8s_service_version`
- `kubernetes_namespace`
- `pod`

### 3.2 固定四查询

四个 instant query 必须使用同一个显式求值时间 `T`：

```promql
up{job="ani-components"}
timestamp(up{job="ani-components"})
target_info{job="ani-components"}
timestamp(target_info{job="ani-components"})
```

命令行示例：

```bash
EVALUATION_TIME="$(date +%s)"

curl -fsS -G "${PROMETHEUS_BASE_URL}/api/v1/query" \
  --data-urlencode 'query=up{job="ani-components"}' \
  --data-urlencode "time=${EVALUATION_TIME}"

curl -fsS -G "${PROMETHEUS_BASE_URL}/api/v1/query" \
  --data-urlencode 'query=timestamp(up{job="ani-components"})' \
  --data-urlencode "time=${EVALUATION_TIME}"

curl -fsS -G "${PROMETHEUS_BASE_URL}/api/v1/query" \
  --data-urlencode 'query=target_info{job="ani-components"}' \
  --data-urlencode "time=${EVALUATION_TIME}"

curl -fsS -G "${PROMETHEUS_BASE_URL}/api/v1/query" \
  --data-urlencode 'query=timestamp(target_info{job="ani-components"})' \
  --data-urlencode "time=${EVALUATION_TIME}"
```

Prometheus HTTP API 的 instant-vector 样本形如：

```json
{
  "metric": {
    "job": "ani-components",
    "instance": "10.0.0.8:9201",
    "ani_service_name": "auth-service",
    "k8s_service_version": "v0.1.0-test.1",
    "kubernetes_namespace": "ani-system",
    "pod": "auth-service-7c9c8b9d8d-abcde"
  },
  "value": [1788487200, "1"]
}
```

`value[0]` 是本次 HTTP 查询的求值时间，`timestamp(...)` 查询中的 `value[1]` 才是指标样本自身的时间。新鲜度必须按 `T - value[1]` 计算，窗口固定为 45 秒。

### 3.3 聚合规则

1. 分别把 `up` 与 `timestamp(up)`、`target_info` 与 `timestamp(target_info)` 按完整 label set 配对；`__name__` 不参与配对。
2. 任一侧出现重复、缺失或多余 series，视为数据契约失败。
3. 仅保留 `0 <= T - sample_timestamp <= 45s` 的样本；未来时间戳或非有限数视为失败。
4. 对每个 fresh `up=1`，使用 `(job, instance, kubernetes_namespace, pod)` 唯一连接一个 fresh `target_info`。
5. 连接后的 `target_info` 必须满足：`service_namespace="ani"`、`service_name=ani_service_name`、`service_instance_id` 非空。
6. `k8s_service_version` 与 `target_info.service_version` 同时存在但不一致时，该目标仍可计为 reachable，但不把冲突版本加入 `versions`。
7. 预期服务集合必须由调用方固定为七个服务，不能从 Prometheus 当前返回的 series 动态推导，否则完全缺失的服务会从结果中消失。
8. 最后按上一节状态表逐服务聚合；任何源查询或身份契约错误均整体 fail closed。

## 4. 合并到现有 Kubernetes 部署

只更新七个镜像不足以启用这条链路。镜像、Pod 元数据、管理端口、Prometheus discovery、网络策略和 Gateway provider 配置必须一起落地。

### 4.1 七个 Deployment

在每个现有 Deployment 的 `spec.template` 上合并以下元数据和 Downward API；保留原有业务环境变量、Secret、资源配额、ServiceAccount 和调度配置：

```yaml
spec:
  template:
    metadata:
      labels:
        ani.dev/metrics-scrape: "true"
        ani.dev/service-name: auth-service
        app.kubernetes.io/version: v0.1.0-test.1
    spec:
      containers:
        - name: auth-service
          ports:
            - name: health
              containerPort: 9201
              protocol: TCP
          env:
            - name: ANI_SERVICE_NAME
              valueFrom:
                fieldRef:
                  apiVersion: v1
                  fieldPath: metadata.labels['ani.dev/service-name']
            - name: ANI_SERVICE_VERSION
              valueFrom:
                fieldRef:
                  apiVersion: v1
                  fieldPath: metadata.labels['app.kubernetes.io/version']
            - name: POD_UID
              valueFrom:
                fieldRef:
                  apiVersion: v1
                  fieldPath: metadata.uid
            - name: HEALTH_PORT
              value: "9201"
```

对每个服务替换 canonical service name 和管理端口。注意：

- `ani.dev/service-name` 必须与二进制内的 canonical name 完全一致；
- `app.kubernetes.io/version` 必须是实际构建版本，不能长期使用 `latest` 或 `(devel)`；
- Kubernetes 环境中 `POD_UID` 是启动必需项，缺失时 runtime admin 会 fail closed，进程启动失败；
- 继续沿用现有 readiness/liveness probe。本阶段没有要求把 Kubernetes `readinessProbe` 改到 `/readyz`；
- 管理端口只用于集群内部探测和抓取，不通过 Ingress 或 NodePort 暴露。

现有 ClusterIP Service 也应补充同名内部端口：

```yaml
spec:
  ports:
    - name: health
      port: 9201
      targetPort: health
      protocol: TCP
```

Prometheus 的当前 job 直接发现并抓取 Pod；ClusterIP 的 `health` 端口用于部署契约一致性和集群内排障，不改变 Pod discovery 行为。

### 4.2 Prometheus、RBAC 与网络策略

将 `ani-components` job 合并到环境实际使用的 Prometheus 配置，并把 discovery namespace 改成七服务所在 namespace。仓库基线见 [`sprint13-instance-observability-prometheus-live.yaml`](../../deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml)。

同时确认：

- Prometheus ServiceAccount 对目标 namespace 中的 Pods 具备 `get/list/watch`；
- NetworkPolicy 允许 Prometheus Pod 访问七服务对应的管理端口；
- Prometheus 配置 reload 成功，`/targets` 中出现 `job="ani-components"`；
- 不对管理端口配置公网入口。

### 4.3 Gateway 聚合 provider

在 `ani-gateway` Deployment 中启用：

```yaml
env:
  - name: PLATFORM_SERVICE_HEALTH_ENABLED
    value: "true"
  - name: PLATFORM_SERVICE_HEALTH_PROMETHEUS_URL
    value: http://<prometheus-service>.<namespace>.svc:9090
  - name: PLATFORM_SERVICE_HEALTH_QUERY_TIMEOUT
    value: 3s
```

Prometheus URL 必须是 `http` 或 `https` 的绝对 base origin，不能携带路径、userinfo、query 或 fragment。超时必须大于 0 且不超过 5 秒。启用 provider 但漏配或错配 URL 会使 Gateway 启动失败。

### 4.4 升级顺序

1. 从准备合入的同一代码提交构建七个服务镜像，推送后使用 digest 固定引用。
2. 更新 Prometheus RBAC、NetworkPolicy 和 `ani-components` scrape job，并确认配置 reload 成功。
3. 把标签、`health` 端口和三个 Downward API 环境变量合并到七个现有 Deployment；不要覆盖现有依赖、Secret、probe 和资源设置。
4. 滚动更新七个服务，逐个确认 Deployment rollout 和 `/metrics`。
5. 最后启用 Gateway provider，并调用 Core 聚合接口验收。

建议的验收命令：

```bash
kubectl -n <ani-namespace> rollout status deployment/<service-name>

curl -fsS -G "${PROMETHEUS_BASE_URL}/api/v1/query" \
  --data-urlencode 'query=up{job="ani-components"}'

curl -fsS \
  -H "Authorization: Bearer ${ANI_ACCESS_TOKEN}" \
  "${ANI_GATEWAY_BASE_URL}/api/v1/platform/services/health"
```

验收时应确认：固定七服务全部出现、目标副本数符合当前 Deployment、预期运行目标为 `reachable`、版本为本次构建版本，并且 `sample_age_seconds <= 45`。

### 4.5 不要直接应用隔离 fixture

[`service-runtime-observability-p0.yaml`](../../deploy/real-k8s-lab/service-runtime-observability-p0.yaml) 是隔离 L3 验证的自定义 inventory，不是可以直接 `kubectl apply` 的生产部署清单。对应 renderer 只接受 `ani-service-observability-e2e-*` namespace，并会生成一套独立的 Deployment、Service、共享 Secret 依赖和测试选择器。

升级已有测试环境时，只参考其中的服务名、端口和环境变量契约，合并到环境现有 Deployment；不要把隔离验证使用的 30 个 fixture 对象部署到已有 namespace。

## 5. 回滚

- 保留每个 Deployment 的上一版 digest 和部署清单；异常时按现有发布流程回滚镜像和 Pod template。
- Prometheus job 可保留；旧镜像没有 `/metrics` 时只会显示 `up=0`，不会影响业务端口。
- 如需先关闭聚合接口，把 `PLATFORM_SERVICE_HEALTH_ENABLED` 设为 `false`；接口将返回 `503 OBSERVABILITY_NOT_CONFIGURED`，调用方必须按数据源未配置处理。
- 回滚后再次检查原有 readiness/liveness、业务接口和依赖连接，不能只以 Prometheus `up` 作为回滚成功证据。

## 6. 契约来源

- Core API：[`api/openapi/v1.yaml`](../../api/openapi/v1.yaml)
- Prometheus 聚合实现：[`prometheus_platform_service_health_reader.go`](../../pkg/adapters/runtime/prometheus_platform_service_health_reader.go)
- Gateway provider 配置：[`platform_service_health_runtime.go`](../../services/ani-gateway/platform_service_health_runtime.go)
- P0 验证记录：[`service-runtime-observability-p0.md`](../../development-records/service-runtime-observability-p0.md)
