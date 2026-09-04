# ANI Platform Helm Chart

`ani-platform` is the umbrella chart entrypoint for the ANI control plane.

Current scope:

- `M1-INFRA-A` chart metadata and values contract.
- Shared infrastructure dependency contract for PostgreSQL, NATS JetStream, Redis, MinIO, Milvus, and Harbor.
- Service image and port values for the initial Go services.
- `M1-INFRA-B` profile contract:
  - `profiles/dev.yaml`
  - `profiles/attach-k8s.yaml`
  - `profiles/offline.yaml`
  - `component-contracts/*.yaml`

Rendering templates will be added after the raw `deploy/manifests/m1-infra-a` baseline is accepted. The raw manifests remain the first validation target because they can be checked with `kubectl --dry-run=client` in environments where Helm is not installed.

The component contracts intentionally do not download public charts. Chart versions are recorded as compatibility targets and must be pinned into a lockfile before any offline installer package is produced.

## 七服务运行时监控部署

当前 chart 只有 metadata、values 和 component contract，还没有渲染七服务工作负载的 Helm templates。因此，不能仅修改本目录的 `values.yaml` 并假定 `helm upgrade` 已经把监控端点部署到现有环境。

升级已有 Kubernetes 环境时，必须在环境实际使用的 Deployment/Service 来源中合并以下内容：

- 七服务镜像使用同一合入提交构建并以 digest 固定；
- Pod template 补 `ani.dev/metrics-scrape`、`ani.dev/service-name`、`app.kubernetes.io/version`；
- 容器补名为 `health` 的管理端口，以及 `ANI_SERVICE_NAME`、`ANI_SERVICE_VERSION`、`POD_UID` Downward API；
- Prometheus 补 `ani-components` Pod discovery、RBAC 和 NetworkPolicy；
- `ani-gateway` 配置内部 Prometheus base URL 并启用聚合 provider；
- 保留环境现有的 Secret、依赖、probe、资源和调度配置。

完整字段、七服务端口、升级顺序和验收方法见[七服务 Prometheus 状态对接与部署指南](../../../docs/operations/service-runtime-observability.md)。`deploy/real-k8s-lab/service-runtime-observability-p0.yaml` 是隔离验证 inventory，不得直接应用到已有 namespace。
