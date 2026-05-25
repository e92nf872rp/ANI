# M1-REAL-LAB-P · Component Report Passed-Evidence Audit

## 批次目标

强化 REAL-K8S-LAB-A 的组件级 summary report：当 `--component-report <summary.json>` 读取到 `passed: true` 的 gate 且该 gate 声明了 `evidence_output` 时，不能只信任 summary 的布尔值，还必须审计 evidence 文件本身。

## 本批次完成

- `scripts/validate_real_k8s_profile.py --component-report` 会审计 passed gate 的 `evidence_output`。
- 缺失 evidence 文件会在 report 中归为 `failed_gates`，并在 `gate_details[].error` 中记录 `missing evidence output`。
- malformed evidence JSON 或 non-passing evidence JSON 会在 report 中归为 failed，并生成 selected preflight/live 复跑命令。
- 保持兼容：旧 summary 中没有声明 `evidence_output` 的 passed gate 不会仅因字段缺失被判失败。

## 测试与验证

- TDD RED：新增 passed gate 缺失 evidence、passed gate malformed evidence 两个 report 测试，初始均失败。
- GREEN：`python scripts/validate_real_k8s_profile_test.py` 通过。

## 事实边界

该批次只加强 component report 对既有 evidence 文件的审计，不代表任何 component live gate 已在真实 lab 执行成功，也不代表三台云 VM、K8s/Kube-OVN/KubeVirt/vCluster/KMS/SM4/Secret 注入链路已经跑通。
