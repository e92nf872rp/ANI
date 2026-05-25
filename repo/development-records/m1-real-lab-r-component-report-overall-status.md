# M1-REAL-LAB-R · Component Report Overall Status

## 目标

强化 REAL-K8S-LAB-A 的组件级 report JSON：`--component-report <summary.json>` 除了列出 failed/blocked gate 与复跑命令，还必须提供机器可读的整体状态，便于 CI 和人工归档流程直接判断 report 是否已清零。

## 已完成

- `component_summary_report()` 输出 `passed`，当 `blocked_gates` 与 `failed_gates` 都为空时为 `true`。
- `component_summary_report()` 输出 `unresolved_gates`，按 blocked gate、failed gate 的顺序去重，和 `next_commands` 的复跑目标一致。
- `main()` 复用 `report["unresolved_gates"]` 决定是否非零退出，保持 M1-REAL-LAB-Q 的退出语义。

## 验证

- TDD RED：先在 component report 测试中断言 `passed` 和 `unresolved_gates`，初始失败为 `KeyError: 'passed'`。
- GREEN：补充 report 字段后，`python scripts/validate_real_k8s_profile_test.py` 通过。

## 事实边界

该批次只增强 component report 的机器可读整体状态，不代表任何 component live gate 已在真实 lab 执行成功，也不代表三台云 VM、K8s/Kube-OVN/KubeVirt/vCluster/KMS/SM4/Secret 注入链路已经跑通。
