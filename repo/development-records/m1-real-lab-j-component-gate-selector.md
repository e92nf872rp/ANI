# M1-REAL-LAB-J — REAL-K8S-LAB Component Gate Selector

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：TDD RED 已确认 `validate_real_k8s_profile.py` 缺少 `select_component_contract_gates()` 和 `--component-gate` CLI；GREEN 后目标测试通过。

## 实现了什么

`scripts/validate_real_k8s_profile.py` 现在支持把组件级 preflight 或 live run 限定到一个或多个 indexed contract gate：

```bash
python scripts/validate_real_k8s_profile.py --component-preflight \
  --component-gate secrets-live-gate \
  --component-env-file repo/development-records/live/real-k8s-component-live.env \
  --evidence-output repo/development-records/live/secrets-live-gate-preflight.json

python scripts/validate_real_k8s_profile.py --component-live \
  --component-gate secrets-live-gate \
  --component-env-file repo/development-records/live/real-k8s-component-live.env \
  --component-evidence-dir repo/development-records/live/component-gates \
  --evidence-output repo/development-records/live/secrets-live-gate-summary.json
```

`--component-gate` 可重复传入多个 gate id。脚本仍会先校验完整 `profile.yaml` 的 contract gate 定义，再对 selected profile 执行 preflight 或 live runner；未知 gate id 会直接失败。该能力用于首轮 `--component-live` 汇总后只重查或重跑失败项。

本批次只提升真实 lab 组件级 live 执行的定位和重跑能力，不代表任何 component live gate 已在真实 lab 执行成功。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_real_k8s_profile.py` | 修改 | 新增 `select_component_contract_gates()` 和 `--component-gate` CLI |
| `scripts/validate_real_k8s_profile_test.py` | 修改 | 新增 selector helper、未知 gate 拒绝和 CLI selected preflight 的 RED-GREEN 测试 |
| `repo/development-records/m1-real-lab-j-component-gate-selector.md` | 新增 | 记录本批次边界、用法和验证命令 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：缺少 selector helper 和 CLI 参数
- [x] `--component-gate <id>` 会筛选 `contract_gates`，不修改原 profile
- [x] 未知 gate id 会以非零状态失败
- [x] selected preflight 只检查被选中的 gate
- [x] `--component-gate` 只允许配合 `--component-preflight` 或 `--component-live`
- [x] 文档明确该能力不等同于真实 lab live 执行成功
- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上用 `--component-gate` 重跑真实失败 gate 并归档 evidence

## 验证命令

```bash
python scripts/validate_real_k8s_profile_test.py
python scripts/validate_real_k8s_profile.py --component-env-template-output /tmp/ani-real-k8s-component-live.env
# 填充 /tmp/ani-real-k8s-component-live.env 后：
python scripts/validate_real_k8s_profile.py --component-preflight --component-gate secrets-live-gate --component-env-file /tmp/ani-real-k8s-component-live.env --evidence-output /tmp/ani-real-k8s-secrets-preflight.json
make validate-real-k8s-profile
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make validate-architecture
make test
git diff --check
```
