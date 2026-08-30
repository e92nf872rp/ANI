# M1-INSTANCE-U-A — VM Termination Protection Precheck

完成日期：2026-05-19
对应 Sprint：Sprint 2（2026-05-19 提前启动；计划窗口 2026-06-01 ~ 06-15）
验证结果：make test EXIT:0，make validate-core-alpha EXIT:0，make validate-instance-store EXIT:0，make validate-instance-service EXIT:0，make validate-demo-instances EXIT:0，make validate-architecture EXIT:0，git diff --check EXIT:0

## 实现了什么

完成 M1-INSTANCE-U 的第一个可验证切片：VM 支持 `termination_protection` 生命周期策略，开启后 `stop/delete/rebuild` 等危险操作在 service precheck 阶段失败关闭，不调用 provider、不改实例状态，并写入 failed operation timeline。实例生命周期策略也进入持久化模型，避免真实 DB 路径丢失保护状态。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/workload_runtime.go` | 修改 | 扩展 lifecycle action enum，新增 `TerminationProtection` 和实例记录生命周期策略 |
| `pkg/adapters/runtime/instance_service.go` | 修改 | lifecycle precheck 拦截 VM 危险操作，记录 failed operation、precheck 和 destructive impact |
| `pkg/adapters/runtime/instance_service_test.go` | 修改 | 覆盖 protected VM stop 被拒绝、不调用 provider、不更新实例状态 |
| `pkg/adapters/runtime/instance_store.go` | 修改 | 持久化/读取 `lifecycle_policy` |
| `deploy/migrations/20260519000400_instance_u_vm_protection.sql` | 新增 | 增加 `workload_instances.lifecycle_policy`，扩展 operation CHECK enum |
| `services/ani-gateway/internal/router/demo_instances.go` | 修改 | create request/response 支持 `termination_protection` |
| `scripts/validate_core_alpha_contract.py` | 修改 | 守卫 lifecycle enum、port constants 和 migration 004 |

## 完工标准达成

- [x] 开启 `termination_protection` 后 VM `stop` 被拒绝
- [x] 拒绝发生在 provider 调用前，实例状态不变
- [x] 失败写入 operation precheck/timeline，`failure_reason=termination_protection_enabled`
- [x] 生命周期保护策略可进入持久化路径
- [x] `make test` 通过
- [x] `make validate-architecture` 通过

## 备注

本切片先完成危险操作保护的横切语义；M1-INSTANCE-U 后续的 SSH 信息、console session、快照和磁盘绑定已分别由 U-B、U-C、U-D、U-E 闭环。
