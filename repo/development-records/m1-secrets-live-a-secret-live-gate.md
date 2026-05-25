# M1-SECRETS-LIVE-A — Secret Live Validation Gate

完成日期：2026-05-24
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认缺少 `validate_secrets_live_gate` 模块和 Secret live gate 定义；GREEN 后 `python scripts/validate_secrets_live_gate.py`、`python scripts/validate_secrets_live_gate_test.py`、`make validate-secrets-live-gate`、文档/API/SDK/Mock/真实底座 contract gate、架构校验、全量 `make test` 和 `git diff --check` 均通过。

## 实现了什么

新增 Kubernetes Secret live 写入与工作负载 env/file/VM 注入验证的固定门禁：`deploy/real-k8s-lab/secrets-live-gate.yaml` 定义 Core Secret provider-backed 创建、绑定记录、kubectl 读取 Secret、Pod env/file 可见性和 KubeVirt VM Secret volume/disk server-side 接受性检查。

新增 `scripts/validate_secrets_live_gate.py`，默认 contract 模式校验 gate 和文档闭环；`--live` 模式通过 `ANI_GATEWAY_URL`、`ANI_BEARER_TOKEN` 和 `KUBECONFIG` 执行真实 Gateway/Kubernetes/KubeVirt 检查。后续 `M1-SECRETS-LIVE-B` 已补充 `--live --evidence-output` 证据归档能力，输出不包含 bearer token 或 Secret 明文。

该批次不是 Kubernetes Secret live 写入或实例 Secret env/file/VM volume live 注入的完成结果；它只建立固定入口和可执行校验逻辑，真实 lab 就绪后仍需用 `--live --evidence-output` 形成验证记录。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `deploy/real-k8s-lab/secrets-live-gate.yaml` | 新增 | Kubernetes Secret 写入、Pod env/file 和 KubeVirt VM Secret volume gate 定义 |
| `scripts/validate_secrets_live_gate.py` | 新增 | contract/live 校验脚本 |
| `scripts/validate_secrets_live_gate_test.py` | 新增 | validator 单元测试和 fake live round trip |
| `Makefile` | 修改 | 新增 `validate-secrets-live-gate` 入口 |
| `deploy/real-k8s-lab/profile.yaml` | 修改 | REAL-K8S-LAB-A 可选 Secret live gate 检查入口 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：Secret live gate validator 不存在
- [x] Gate 定义包含 Core Kubernetes Secret 创建、Core binding、kubectl read、Pod env/file 和 KubeVirt VM Secret volume 检查
- [x] Contract 模式校验 gate 与文档闭环
- [x] Live 模式支持真实 Gateway、Kubernetes Secret、Pod env/file 可见性和 KubeVirt VM Secret volume 接受性检查
- [ ] 真实 Kubernetes Secret live 写入验证
- [ ] 真实实例 Secret env/file/VM volume live 注入结果记录

## 备注

VM 检查当前以 KubeVirt server-side dry-run 接受 Secret volume/disk manifest 为 live gate 起点；完整 guest 内可见性仍需要后续真实 VM 启动和 guest probe 记录证明。
