# M1-REAL-LAB-Q · Component Report Unresolved Exit Guard

## 批次目标

强化 REAL-K8S-LAB-A 的组件级 report CLI 行为：`--component-report <summary.json>` 生成诊断 report 后，如果 report 中仍存在 failed 或 blocked gate，命令必须以非零状态退出，避免 CI 或人工流程把 unresolved live gate 误判为通过。

## 本批次完成

- `scripts/validate_real_k8s_profile.py --component-report` 保持先写出 JSON report。
- report 中存在 `failed_gates` 或 `blocked_gates` 时，CLI 在 report 写出后返回非零状态。
- report 中没有 unresolved gate 时，CLI 保持 0 退出。

## 测试与验证

- TDD RED：调整 component report CLI 测试，要求存在 failed gate 时写出 report 后抛出 `SystemExit`；初始失败。
- GREEN：新增 clean report 0 退出测试，并让 `--component-report` 在 unresolved gate 存在时非零退出。

## 事实边界

该批次只加强 component report 的退出语义，不代表任何 component live gate 已在真实 lab 执行成功，也不代表三台云 VM、K8s/Kube-OVN/KubeVirt/vCluster/KMS/SM4/Secret 注入链路已经跑通。
