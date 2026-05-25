# M1-REAL-LAB-G — REAL-K8S-LAB Component Env Template

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：TDD RED 已确认 `validate_real_k8s_profile.py` 没有 component env template 函数和 `--component-env-template-output` CLI；GREEN 后目标测试通过。

## 实现了什么

`scripts/validate_real_k8s_profile.py` 现在可以从 `deploy/real-k8s-lab/profile.yaml` 的 `contract_gates[].required_env` 生成组件级 live 执行前的 shell 环境模板：

```bash
python scripts/validate_real_k8s_profile.py \
  --component-env-template-output repo/development-records/live/real-k8s-component-live.env
```

模板只包含空值占位，不写入 bearer token、KMS token、presigned URL 或其它 secret value。模板同时包含每个 gate 对应的 env mapping，便于真实 lab 执行前确认 vCluster、node pool、Kube-OVN、KubeVirt、reconcile HA、KMS/SM4 和 Secrets gate 的输入是否齐全。

本批次只提升真实 lab 执行前的配置交接能力，不代表任何 component live gate 已在真实 lab 执行成功。

## 使用顺序

```bash
python scripts/validate_real_k8s_profile.py \
  --component-env-template-output repo/development-records/live/real-k8s-component-live.env

python scripts/validate_real_k8s_profile.py --component-live \
  --component-env-file repo/development-records/live/real-k8s-component-live.env \
  --component-evidence-dir repo/development-records/live/component-gates \
  --evidence-output repo/development-records/live/real-k8s-component-summary.json
```

后续 `M1-REAL-LAB-H` 已新增 `--component-env-file`，可直接解析填充后的模板并传递给组件 validator 子进程，无需 shell source。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_real_k8s_profile.py` | 修改 | 新增 `component_env_template()`、`write_component_env_template()` 和 `--component-env-template-output` |
| `scripts/validate_real_k8s_profile_test.py` | 修改 | 新增 env template 内容和 CLI 输出 RED-GREEN 测试 |
| `repo/development-records/m1-real-lab-g-component-env-template.md` | 新增 | 记录本批次边界、用法和验证命令 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：缺少 `component_env_template()` 和 CLI 参数
- [x] env template 去重列出所有 `contract_gates[].required_env`
- [x] env template 不包含真实 token 或 secret value
- [x] env template 记录每个 component gate 的 env mapping
- [x] CLI 可通过 `--component-env-template-output` 写出模板文件
- [x] 文档明确该能力不等同于真实 lab live 执行成功
- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上填充模板后执行 `--component-live` 并归档真实组件 evidence 与汇总 JSON

## 验证命令

```bash
python scripts/validate_real_k8s_profile_test.py
python scripts/validate_real_k8s_profile.py --component-env-template-output /tmp/ani-real-k8s-component-live.env
make validate-real-k8s-profile
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make validate-architecture
make test
git diff --check
```
