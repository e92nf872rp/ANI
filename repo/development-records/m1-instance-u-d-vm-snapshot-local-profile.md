# M1-INSTANCE-U-D — VM Snapshot Local Profile

完成日期：2026-05-19
对应 Sprint：Sprint 2（2026-05-19 提前启动；计划窗口 2026-06-01 ~ 06-15）
验证结果：make test EXIT:0，make validate-core-alpha EXIT:0，make validate-demo-instances EXIT:0，make validate-instance-store EXIT:0，make validate-architecture EXIT:0，git diff --check EXIT:0

## 实现了什么

完成 M1-INSTANCE-U 的第四个可验证切片：VM `snapshot` lifecycle action 在 local/dev profile 中记录快照元数据，不调用 provider executor，不改变实例运行态，并写入 operation timeline。`/api/v1/instances` 响应现在包含 `snapshots[]`，`POST /instances/{id}/lifecycle` 可通过 `snapshot_name` 指定快照名。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/workload_runtime.go` | 修改 | 增加 VM snapshot 记录结构、lifecycle `Snapshot` 方法和 `snapshot_name/volume_id` 请求字段 |
| `pkg/adapters/runtime/instance_service.go` | 修改 | `Snapshot` local profile 生成 ready 快照、保留当前 state、写入 `create_snapshot` timeline |
| `pkg/adapters/runtime/instance_store.go` | 修改 | `workload_instances.snapshots` JSONB 持久化读写 |
| `services/ani-gateway/internal/router/demo_instances.go` | 修改 | lifecycle 支持 `snapshot` action，响应返回 `snapshots[]` |
| `api/openapi/v1.yaml` | 修改 | `InstanceRecord.snapshots` 和 `InstanceLifecycleRequest.snapshot_name` 进入 Core Alpha 契约 |
| `deploy/migrations/20260519000400_instance_u_vm_protection.sql` | 修改 | 增加 `snapshots` JSONB 列 |
| `scripts/validate_core_alpha_contract.py` | 修改 | 合同守卫覆盖 snapshot schema 和迁移 token |
| `pkg/adapters/runtime/instance_service_test.go` | 修改 | 覆盖 VM snapshot 不调用 provider、保持 running、写入 timeline |
| `services/ani-gateway/internal/router/demo_instances_test.go` | 修改 | 覆盖 demo service VM snapshot 响应 |

## 完工标准达成

- [x] `snapshot` lifecycle action 进入 VM local/dev profile
- [x] 快照元数据持久化到 instance record
- [x] 快照操作写入 operation timeline，并与幂等语义保持一致
- [x] Gateway/API response 返回 `snapshots[]`
- [x] `make test` 通过
- [x] `make validate-architecture` 通过

## 备注

本切片完成 VM snapshot 的本地 profile 与 Core API 可依赖元数据；provider-native 快照执行留给 real provider beta 路径继续对齐。
