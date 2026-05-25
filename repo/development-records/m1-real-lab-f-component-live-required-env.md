# M1-REAL-LAB-F — REAL-K8S-LAB Component Live Required Env Preflight

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：TDD RED 已确认 `profile.yaml` 的 component gates 未声明必需环境变量，contract validation 不校验 `required_env`，且 `--component-live` 会在环境缺失时直接启动 validators；GREEN 后目标测试通过。

## 实现了什么

`deploy/real-k8s-lab/profile.yaml` 的每个 `contract_gates[]` 现在声明 `required_env`，覆盖真实 lab 执行组件 gate 前必须具备的环境输入，例如：

- `KUBECONFIG`
- `ANI_GATEWAY_URL`
- `ANI_BEARER_TOKEN`
- `DATABASE_URL`
- `RECONCILE_WORKER_METRICS_URL`
- `KMS_PROVIDER_BASE_URL`
- `KMS_PROVIDER_BEARER_TOKEN`
- `OBJECTSTORE_LIVE_PUT_URL`
- `OBJECTSTORE_LIVE_GET_URL`

`scripts/validate_real_k8s_profile.py --component-live` 会在启动组件 validators 前执行 preflight：

- 任一 gate 缺少 `required_env` 时，不启动任何 validator。
- summary status 为 `component_live_preflight_failed`。
- 每个 gate 记录 `required_env` 和 `missing_env`。
- summary 记录 `total/passed/failed/blocked`。
- 如果传入 `--evidence-output`，先写出 preflight summary，再以非零状态退出。

本批次只提升真实 lab 执行前的配置诊断能力，不代表任何 component live gate 已经在真实 lab 执行成功。

## 使用示例

```bash
python scripts/validate_real_k8s_profile.py --component-live \
  --component-evidence-dir repo/development-records/live/component-gates \
  --evidence-output repo/development-records/live/real-k8s-component-summary.json
```

如果返回 `component_live_preflight_failed`，先补齐 summary 中各 gate 的 `missing_env`，再重复执行。

后续 `M1-REAL-LAB-G` 已补充 `--component-env-template-output`，可先生成不含密钥值的 shell env 模板，再填充并执行 `--component-live`。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `deploy/real-k8s-lab/profile.yaml` | 修改 | 为每个 `contract_gates[]` 增加 `required_env` |
| `scripts/validate_real_k8s_profile.py` | 修改 | contract validation 校验 `required_env`；`--component-live` 在启动 validators 前执行 required env preflight |
| `scripts/validate_real_k8s_profile_test.py` | 修改 | 新增 required env 索引、contract validation 和 preflight RED-GREEN 测试 |
| `repo/development-records/m1-real-lab-f-component-live-required-env.md` | 新增 | 记录本批次边界、用法和验证命令 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：profile 无 `required_env`，contract 不校验，runner 不 preflight
- [x] 每个 component contract gate 声明 `required_env`
- [x] contract validation 拒绝缺失或空的 `required_env`
- [x] `--component-live` 缺少 env 时不启动 validators
- [x] preflight summary 记录 `required_env` / `missing_env` 和 blocked 计数
- [x] 文档明确该能力不等同于真实 lab live 执行成功
- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上补齐 required env 后执行 `--component-live` 并归档真实组件 evidence 与汇总 JSON

## 验证命令

```bash
python scripts/validate_real_k8s_profile_test.py
make validate-real-k8s-profile
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make validate-architecture
make test
git diff --check
```
