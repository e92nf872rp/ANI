# M1-WKID-A — Workload Identity P0

完成日期：2026-05-20
对应 Sprint：Sprint 3（2026-05-20 提前启动；计划窗口 2026-06-16 ~ 06-30）
验证结果：make validate-workload-identity EXIT:0，make validate-sdk-alpha EXIT:0，python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml EXIT:0，make validate-architecture EXIT:0，make test EXIT:0，git diff --check EXIT:0

## 实现了什么

补齐实例级 Workload Identity P0：实例创建时绑定 lifecycle-bound scoped API key，Kubernetes workload 通过 Secret 引用注入 `ANI_WORKLOAD_TOKEN`，实例删除时自动 revoke 对应 key。绑定和撤销都写入 operation timeline，API 只返回 key 摘要，不返回明文。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/workload_runtime.go` | 修改 | 新增 `WorkloadIdentityService` port、绑定结果和实例身份摘要 |
| `pkg/adapters/runtime/workload_identity.go` | 新增 | 本地/metadata Workload Identity 实现 |
| `pkg/adapters/runtime/instance_orchestrator.go` | 修改 | provider render 前绑定 scoped key，并把身份上下文交给 renderer |
| `pkg/adapters/runtime/dryrun_renderer.go` | 修改 | 通过 Secret 引用注入 `ANI_WORKLOAD_TOKEN`，避免明文进入 manifest |
| `pkg/adapters/runtime/instance_service.go` | 修改 | create/delete operation timeline 记录身份绑定与撤销 |
| `deploy/migrations/20260520000700_workload_identity_api_keys.sql` | 新增 | 对齐 `api_keys.instance_id` 与 ANI instance id |
| `scripts/validate_workload_identity_contract.py` | 新增 | Workload Identity 合同守卫 |
| `api/openapi/v1.yaml` | 修改 | `InstanceRecord` 增加 `workload_identity` 摘要，不暴露 key 明文 |

## 完工标准达成

- [x] 实例创建时生成 lifecycle-bound scoped API key
- [x] Kubernetes workload 通过 `ANI_WORKLOAD_TOKEN` Secret 引用获得工作负载身份
- [x] 实例删除时 revoke 对应 key
- [x] operation timeline 记录 `workload_identity_bind` 与 `workload_identity_revoke`
- [x] API 响应只返回 key prefix/scopes/active 等摘要，不返回 key 明文
- [x] `make validate-workload-identity` 通过
- [x] `make test`、`make validate-architecture`、`git diff --check` 通过

## 备注

P0 使用 lifecycle-bound scoped API key；P1 可在同一 port 后升级为短期 token / IRSA 风格能力。当前 renderer 使用 Secret 引用表达注入意图，真实 Secret 写入可在后续 real provider 加固中由身份 adapter 或 controller 承接。
