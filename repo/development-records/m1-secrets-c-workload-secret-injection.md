# M1-SECRETS-C — Workload Secret Injection Manifest Boundary

完成日期：2026-05-24
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认缺少 `WorkloadSpec.SecretBindings` 与 demo request 映射；GREEN 后 targeted Go tests、`make validate-doc-entrypoints`、`make validate-real-k8s-profile`、`make validate-architecture`、`make test`、`git diff --check` 均通过。

## 实现了什么

为实例 spec 增加 Secret binding intent，并让 Kubernetes dry-run renderer 把容器/Job workload 的 Secret binding 渲染为 Kubernetes `envFrom.secretRef` 和只读 Secret volume mount。Gateway demo instance 创建请求可接收 `secret_bindings` 并映射到 `ports.WorkloadSpec`。

该批次是实例环境变量/文件挂载注入的代码边界，不代表 Kubernetes Secret live 写入、真实 Pod 启动验证或 VM 注入已经完成。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/workload_runtime.go` | 修改 | 新增 `WorkloadSecretBinding` 和 `WorkloadSpec.SecretBindings` |
| `pkg/adapters/runtime/dryrun_renderer.go` | 修改 | 渲染 `envFrom.secretRef`、Secret volume 和只读 volumeMount |
| `pkg/adapters/runtime/dryrun_renderer_test.go` | 修改 | 覆盖 Secret binding env/file manifest 注入 |
| `services/ani-gateway/internal/router/demo_instances.go` | 修改 | `secret_bindings` 请求字段映射到 workload spec |
| `services/ani-gateway/internal/router/demo_instances_test.go` | 修改 | 覆盖 demo create request 到 Secret binding spec 的映射 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：renderer 缺少 `WorkloadSpec.SecretBindings`；router 缺少 `demoCreateInstanceRequest.SecretBindings`
- [x] 容器/Job manifest 可引用 Kubernetes Secret 作为 env prefix 和只读文件挂载
- [x] demo instance 创建请求可携带 `secret_bindings` 并进入 `WorkloadSpec`
- [x] `make test`、`make validate-architecture`、`make validate-doc-entrypoints`、`make validate-real-k8s-profile` 和 `git diff --check` 通过
- [ ] Kubernetes Secret live 写入与 Pod 启动后 env/file 可见性验证
- [ ] VM workload Secret 注入

## 备注

Kubernetes Secret 名称当前沿用 Secret provider 写入边界的 `SecretID`。后续真实 provider 验证若引入 provider-specific Secret name，需要把 provider ref 解析纳入 binding 到 workload spec 的链路。
