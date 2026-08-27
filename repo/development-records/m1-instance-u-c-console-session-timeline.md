# M1-INSTANCE-U-C — VM Console Session Timeline

完成日期：2026-05-19
对应 Sprint：Sprint 2（2026-05-19 提前启动；计划窗口 2026-06-01 ~ 06-15）
验证结果：make test EXIT:0，make validate-core-alpha EXIT:0，make validate-demo-instances EXIT:0，make validate-architecture EXIT:0，git diff --check EXIT:0

## 实现了什么

完成 M1-INSTANCE-U 的第三个可验证切片：VM console/VNC/serial session 申请现在返回 `operation_id`、`session_id`、`connect_url`/`url` 和 `expires_at`，并写入 instance operation timeline。`GET /instances/{id}/operations` 可以看到 `console_session` 操作和 `issue_session` step，便于 Console 与审计侧追踪远程访问入口。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/workload_runtime.go` | 修改 | `WorkloadInstanceOpsResult` 增加 `operation_id` 和 `url`，operation enum 增加 `console_session` |
| `pkg/adapters/runtime/instance_service.go` | 修改 | session 类 ops 写入 operation store 和 timeline |
| `pkg/adapters/runtime/instance_ops.go` | 修改 | 本地 session 返回 `url` alias |
| `pkg/adapters/runtime/kubernetes_instance_ops.go` | 修改 | Kubernetes ops session 同步返回 `url` alias |
| `pkg/adapters/runtime/instance_service_test.go` | 修改 | 覆盖 VM VNC session 产生 operation timeline |
| `api/openapi/v1.yaml` | 修改 | `InstanceConsoleSession` schema 增加 `operation_id` 和 `url`，operation enum 增加 `console_session` |
| `deploy/migrations/20260519000400_instance_u_vm_protection.sql` | 修改 | operation CHECK enum 增加 `console_session` |
| `scripts/validate_core_alpha_contract.py` | 修改 | 守卫 console session response 与 migration token |

## 完工标准达成

- [x] VM console/VNC session 返回 `session_id/url/expires_at`
- [x] VM console/VNC session 返回 `operation_id`
- [x] session 申请写入 operation timeline
- [x] `make test` 通过
- [x] `make validate-architecture` 通过

## 备注

本切片完成 console session 的 Core operation 语义；M1-INSTANCE-U 后续的快照和磁盘绑定本地 profile 已分别由 U-D、U-E 闭环。
