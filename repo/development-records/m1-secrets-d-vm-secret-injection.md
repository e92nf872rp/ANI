# M1-SECRETS-D — VM Secret Binding Manifest Boundary

完成日期：2026-05-24
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认 KubeVirt VM manifest 不渲染 `WorkloadSecretBinding`；GREEN 后 targeted Go tests、`make validate-doc-entrypoints`、`python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml`、`make validate-real-k8s-profile`、`make validate-vcluster-live-gate`、`make validate-architecture`、`make test` 和 `git diff --check` 均通过。

## 实现了什么

把 Secret binding manifest 注入从容器/Job 扩展到 VM：`KubernetesDryRunRenderer` 渲染 KubeVirt `VirtualMachine` 时，会把 `WorkloadSecretBinding` 映射为 KubeVirt Secret volume，并在 `domain.devices.disks` 中追加只读磁盘引用。渲染结果同时写入 `ani.kubercloud.io/vm-secret-mounts` annotation，用于记录 Secret 与期望 guest mount path 的绑定意图，供后续 live 验证和控制器实现使用。

该批次不是 live VM 注入验收，不代表 KubeVirt VM 已启动，也不代表 guest 内部已经可见 Secret 文件；它只完成 VM Secret binding 的 manifest 代码边界。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/adapters/runtime/dryrun_renderer.go` | 修改 | VM manifest 新增 `domain.devices.disks`，并为 Secret binding 渲染 Secret volume、只读 disk 和 mount annotation |
| `pkg/adapters/runtime/dryrun_renderer_test.go` | 修改 | 覆盖 VM Secret binding 渲染为 KubeVirt Secret volume/disk |
| `repo/CURRENT-SPRINT.md` | 修改 | 标记 M1-SECRETS-D 代码边界，live 验证仍未完成 |
| `ANI-06-开发计划.md` | 修改 | 同步 Sprint 5 状态和未完成边界 |
| `ANI-DOCS-INDEX.md` | 修改 | 同步当前结论 |
| `repo/development-records/README.md` | 修改 | 追加批次归档索引 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：VM manifest 缺少 Secret volume/disk
- [x] KubeVirt VM manifest 可渲染 Secret volume
- [x] KubeVirt VM manifest 可渲染对应只读 disk
- [x] VM manifest annotation 保留 SecretID 到期望 guest mount path 的绑定意图
- [ ] Kubernetes Secret live 写入验证
- [ ] KubeVirt VM 启动后 guest 内 Secret 可见性验证

## 备注

KubeVirt Secret volume 只证明 manifest 层已表达 Secret 注入意图。后续 REAL-K8S-LAB-A live 验证需要创建 Secret、启动 VM，并在 guest 内确认 Secret 数据可见。
