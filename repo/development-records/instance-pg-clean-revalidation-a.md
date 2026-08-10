# INSTANCE-PG-CLEAN-REVALIDATION-A

> 日期：2026-08-02
> 范围：ANI Core / Instance Management / PostgreSQL 数据清理与重新验证

## 目标

清除历史 live gate 遗留的实例管理数据，从空 PostgreSQL 基线重跑 Sandbox 真实 provider 门禁，验证新数据与 Kubernetes 资源生命周期一致。

## 清理范围

清理前已备份 PostgreSQL 和 Workload Identity 记录，文件权限为 `0600`：

- `/tmp/ani-instance-pg-backup-20260802.sql`，SHA-256 `98bf850cd71aa269449f8fead56db7cc6e41d89a0591700a5ddaa09da269c78e`
- `/tmp/ani-instance-workload-identities-20260802.csv`，SHA-256 `514d61434e861d0530433b4727ab501034eb690bec3a568eac30e1165651c954`

只在一个事务中删除实例管理数据，不触碰 tenant、user、role、登录密钥或其他业务表：

| 对象 | 删除数 |
|---|---:|
| `workload_instance_operation_steps` | 381 |
| `workload_instance_operations` | 104 |
| `workload_instances` | 26 |
| `instance_plan_audits` | 27 |
| `api_keys WHERE instance_id IS NOT NULL` | 27 |

事务提交后五类数据均为 0；Gateway 公开 API `GET /api/v1/instances?limit=100` 返回 `items=[]` 和 `total=0`。

## 清理后真实验证

- 重启 `ani-gateway` 和 `reconcile-worker`，两个 Deployment 均为 `1/1 Ready`
- 重跑完整 Sandbox live gate，`status=passed`
- create、pause、resume、delete 四个 operation 均为 `succeeded`，8 个 operation step 均为 `succeeded`
- 最终 PG 仅保留本次新鲜审计记录：1 个 `sandbox_1`，`state=deleted`
- Sandbox 当前路径未产生 plan audit 或持久化 Workload Identity，两者均为 0
- Kubernetes 中无对应 Deployment、Pod 或 Service 残留
- 文件安全：`/workspace=emptyDir`，5 个 symlink/hard-link 越界操作均返回 400，外部内容保持不变
- evidence：`development-records/live-evidence/instance-sandbox-post-clean-live-20260802.json`

## 已知边界

- 本批次只清理数据并重新验证，不修改 Core OpenAPI v1、数据库 schema 或 Services。
- 删除状态实例作为审计历史保留是当前设计，不是脏数据。
- provider 资源主动丢失时，Kubernetes REST 404 与 `ports.ErrNotFound` 的 reconcile 错误映射尚未收口；SandboxRuntime 也仍有进程内状态。本次清理不宣称两项问题已修复。

## 验证

```bash
python3 scripts/validate_sandbox_live_gate_test.py
python3 scripts/validate_sandbox_live_gate.py
python3 scripts/validate_yaml.py deploy/real-k8s-lab/instance-sandbox-live-gate.yaml
python3 scripts/validate_sandbox_live_gate.py --live ...
```

本批次不自动 commit，保留人工确认关口。
