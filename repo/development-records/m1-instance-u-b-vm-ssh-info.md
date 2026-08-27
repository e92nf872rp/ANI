# M1-INSTANCE-U-B — VM SSH Connection Info

完成日期：2026-05-19
对应 Sprint：Sprint 2（2026-05-19 提前启动；计划窗口 2026-06-01 ~ 06-15）
验证结果：make test EXIT:0，make validate-core-alpha EXIT:0，make validate-instance-store EXIT:0，make validate-demo-instances EXIT:0，make validate-architecture EXIT:0，git diff --check EXIT:0

## 实现了什么

完成 M1-INSTANCE-U 的第二个可验证切片：VM 创建后生成 SSH 连接元数据（username、host、port、key_ref、ready、reason），Gateway `/api/v1/instances` dev/local profile 返回该字段，Metadata store 持久化 `ssh_connection`。该字段只保存连接元数据和 key 引用，不返回、不存储私钥内容。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/workload_runtime.go` | 修改 | 新增 `VMSSHConnectionInfo`，VM spec 支持 `SSHUsername`，实例记录支持 `SSH` |
| `pkg/adapters/runtime/instance_orchestrator.go` | 修改 | VM create 时生成 SSH connection info |
| `pkg/adapters/runtime/instance_orchestrator_test.go` | 修改 | 覆盖 VM SSH 元数据生成 |
| `pkg/adapters/runtime/instance_store.go` | 修改 | 持久化/读取 `ssh_connection` |
| `pkg/adapters/runtime/instance_store_test.go` | 修改 | 覆盖 SSH key ref 进入持久化参数 |
| `services/ani-gateway/internal/router/demo_instances.go` | 修改 | create request 支持 `ssh_username`/`ssh_key_ref`，response 返回 `ssh` |
| `api/openapi/v1.yaml` | 修改 | `CreateInstanceRequest` 与 `InstanceRecord.ssh` schema 补齐 SSH 字段 |
| `deploy/migrations/20260519000400_instance_u_vm_protection.sql` | 修改 | 同一 M1-INSTANCE-U migration 增加 `ssh_connection` JSONB |
| `scripts/validate_core_alpha_contract.py` | 修改 | 守卫 SSH request/response schema 和 migration token |

## 完工标准达成

- [x] VM 实例记录包含 SSH 连接元数据
- [x] Gateway dev/local profile 返回 `ssh.username/host/port/key_ref/ready/reason`
- [x] SSH 信息进入持久化路径，且不包含私钥内容
- [x] `make test` 通过
- [x] `make validate-architecture` 通过

## 备注

本切片只覆盖连接信息元数据。M1-INSTANCE-U 后续的 console/VNC session、快照和磁盘绑定已分别由 U-C、U-D、U-E 闭环。
