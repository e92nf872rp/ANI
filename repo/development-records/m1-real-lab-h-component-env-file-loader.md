# M1-REAL-LAB-H — REAL-K8S-LAB Component Env File Loader

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：TDD RED 已确认 `validate_real_k8s_profile.py` 缺少 component env file loader 和 `--component-env-file` CLI；GREEN 后目标测试通过。

## 实现了什么

`scripts/validate_real_k8s_profile.py` 现在支持在组件级 live 汇总执行时直接加载填充后的 env 模板：

```bash
python scripts/validate_real_k8s_profile.py --component-live \
  --component-env-file repo/development-records/live/real-k8s-component-live.env \
  --component-evidence-dir repo/development-records/live/component-gates \
  --evidence-output repo/development-records/live/real-k8s-component-summary.json
```

env file loader 只接受空行、注释、`NAME=value` 和 `export NAME=value` 形式。value 使用 shell quote 规则解析为单个 token，但不会 source 文件、不会执行 shell 命令，也不会把 env 文件内容写入 evidence。加载后的 env 会覆盖当前进程同名 env，并传递给每个组件 validator 子进程。

本批次只提升真实 lab 组件级 live 执行的配置加载能力，不代表任何 component live gate 已在真实 lab 执行成功。

## 使用顺序

```bash
python scripts/validate_real_k8s_profile.py \
  --component-env-template-output repo/development-records/live/real-k8s-component-live.env

# 人工填充 repo/development-records/live/real-k8s-component-live.env 后：
python scripts/validate_real_k8s_profile.py --component-preflight \
  --component-env-file repo/development-records/live/real-k8s-component-live.env \
  --evidence-output repo/development-records/live/real-k8s-component-preflight.json

python scripts/validate_real_k8s_profile.py --component-live \
  --component-env-file repo/development-records/live/real-k8s-component-live.env \
  --component-evidence-dir repo/development-records/live/component-gates \
  --evidence-output repo/development-records/live/real-k8s-component-summary.json
```

后续 `M1-REAL-LAB-I` 已补充 `--component-preflight`，可在不启动 live validators 的前提下先检查 env file 完整性。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_real_k8s_profile.py` | 修改 | 新增 `load_component_env_file()`、安全 assignment parser、`--component-env-file` 和子进程 env 传递 |
| `scripts/validate_real_k8s_profile_test.py` | 修改 | 新增 env file 解析、非法行拒绝和 main 合并 env 的 RED-GREEN 测试 |
| `repo/development-records/m1-real-lab-h-component-env-file-loader.md` | 新增 | 记录本批次边界、用法和验证命令 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：缺少 `load_component_env_file()` 和 CLI 参数
- [x] env file loader 支持 `export NAME="value"` 和 `NAME='value'`
- [x] env file loader 拒绝非 assignment 行，避免 shell source 语义
- [x] `--component-env-file` 只允许配合 `--component-live` 使用
- [x] env file 值会覆盖当前进程 env 并传递给组件 validator 子进程
- [x] 文档明确该能力不等同于真实 lab live 执行成功
- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上填充 env file 后执行 `--component-live` 并归档真实组件 evidence 与汇总 JSON

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
