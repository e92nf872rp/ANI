# M1-REAL-LAB-I — REAL-K8S-LAB Component Preflight-Only Mode

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：TDD RED 已确认 `validate_real_k8s_profile.py` 缺少 `validate_component_live_preflight()` 和 `--component-preflight` CLI；GREEN 后目标测试通过。

## 实现了什么

`scripts/validate_real_k8s_profile.py` 现在支持只检查组件级 live gate 的必需环境变量，不启动任何组件 validator：

```bash
python scripts/validate_real_k8s_profile.py --component-preflight \
  --component-env-file repo/development-records/live/real-k8s-component-live.env \
  --evidence-output repo/development-records/live/real-k8s-component-preflight.json
```

`--component-preflight` 会读取 `deploy/real-k8s-lab/profile.yaml` 的 `contract_gates[].required_env`，合并当前进程环境和可选 `--component-env-file`，再输出每个 gate 的 `required_env`、`missing_env` 和 `passed`。如果任一 gate 缺少必需 env，CLI 会写出 `component_live_preflight_failed` summary 并以非零状态退出。

本批次只提升真实 lab 组件级 live 执行前的配置检查能力，不代表任何 component live gate 已在真实 lab 执行成功。

## 使用顺序

```bash
python scripts/validate_real_k8s_profile.py \
  --component-env-template-output repo/development-records/live/real-k8s-component-live.env

# 人工填充 repo/development-records/live/real-k8s-component-live.env 后：
python scripts/validate_real_k8s_profile.py --component-preflight \
  --component-env-file repo/development-records/live/real-k8s-component-live.env \
  --evidence-output repo/development-records/live/real-k8s-component-preflight.json

# 后续 M1-REAL-LAB-J 可限定单个 gate：
python scripts/validate_real_k8s_profile.py --component-preflight \
  --component-gate secrets-live-gate \
  --component-env-file repo/development-records/live/real-k8s-component-live.env \
  --evidence-output repo/development-records/live/secrets-live-gate-preflight.json

python scripts/validate_real_k8s_profile.py --component-live \
  --component-env-file repo/development-records/live/real-k8s-component-live.env \
  --component-evidence-dir repo/development-records/live/component-gates \
  --evidence-output repo/development-records/live/real-k8s-component-summary.json
```

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_real_k8s_profile.py` | 修改 | 新增 `validate_component_live_preflight()`、`--component-preflight` CLI 和 evidence 输出 |
| `scripts/validate_real_k8s_profile_test.py` | 修改 | 新增 complete env、missing env 和 CLI env file preflight 的 RED-GREEN 测试 |
| `repo/development-records/m1-real-lab-i-component-preflight-only.md` | 新增 | 记录本批次边界、用法和验证命令 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：缺少 `validate_component_live_preflight()` 和 `--component-preflight`
- [x] complete env 会生成 `component_live_preflight_passed` summary
- [x] incomplete env 会生成 `component_live_preflight_failed` summary，并记录每个 gate 的 `missing_env`
- [x] CLI 支持 `--component-preflight --component-env-file ... --evidence-output ...`
- [x] preflight-only mode 不启动任何 component live validator
- [x] 文档明确该能力不等同于真实 lab live 执行成功
- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上填充 env file 后执行 `--component-preflight`、`--component-live` 并归档真实组件 evidence 与汇总 JSON

## 验证命令

```bash
python scripts/validate_real_k8s_profile_test.py
python scripts/validate_real_k8s_profile.py --component-env-template-output /tmp/ani-real-k8s-component-live.env
# 填充 /tmp/ani-real-k8s-component-live.env 后：
python scripts/validate_real_k8s_profile.py --component-preflight --component-env-file /tmp/ani-real-k8s-component-live.env --evidence-output /tmp/ani-real-k8s-component-preflight.json
make validate-real-k8s-profile
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make validate-architecture
make test
git diff --check
```
