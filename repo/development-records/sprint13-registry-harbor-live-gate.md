# SPRINT13-REGISTRY-HARBOR-LIVE-A — Registry Harbor live gate

完成日期：2026-07-27
对应 Sprint：Sprint 13 / Registry Console Flow 增量补证
验证结果：已通过；契约门禁 `make validate-registry-harbor-live-gate` 通过，真实 Gateway → Harbor → Kubernetes pull secret live gate 已通过并写入 `development-records/live-evidence/sprint13-registry-harbor-live-evidence.json`。

## 实现了什么

补齐镜像仓库模块缺失的 Harbor-backed live gate，使 Registry Console Flow 不再只停留在 local profile / 后端单测：

- 新增 `deploy/real-k8s-lab/registry-harbor-live-gate.yaml`，定义 Gateway Registry API、Harbor API 和 Kubernetes Secret API 的真实验证范围。
- 新增 `scripts/validate_registry_harbor_live_gate.py`，支持通过真实 Gateway 调用 Harbor-backed registry API，并输出脱敏 evidence JSON。
- 新增 `scripts/validate_registry_harbor_live_gate_test.py`，覆盖 gate contract、production-shaped URL 约束和 evidence 脱敏。
- Gateway registry runtime 只读取 registry 契约定义的 canonical env：`REGISTRY_PROVIDER_MODE`、`HARBOR_*`、`REGISTRY_PULL_SECRET_FIELD_MANAGER`，不再从旧 `REGISTRY_*` 或通用 Kubernetes field manager env 做 alias fallback。
- Harbor robot account 创建兼容真实 Harbor 返回的 `secret` 字段和旧测试中的 `token` 字段；live gate 使用唯一 pull-secret 名称避免重复运行时 Harbor robot 名称冲突。

## Live gate 范围

`--live` 模式验证：

- `POST /registry/projects` 通过 Gateway 创建或确认 Harbor 项目。
- `GET /registry/projects` 返回 Harbor real-provider `dev_profile`，并包含租户项目。
- `GET /registry/projects/{project}/push-instructions` 返回真实 registry host 与 push 命令。
- `POST /registry/projects/{project}/pull-secret` 通过 Harbor robot credential 创建 Kubernetes `kubernetes.io/dockerconfigjson` Secret。
- `GET /registry/projects/{project}/scan-report` 回读 Harbor/Trivy project scan report 或空项目真实 provider report。
- 当提供 `--repository` 和 `--tag` 时，额外验证 repository、artifact、`/registry/images?purpose=` 真实 artifact 回读。

## 当前边界

- 不把该 gate 等同于 full platform production ready；它只证明 Gateway → Harbor provider → Kubernetes pull secret 的真实链路。
- 如果不提供已推送的 `repository/tag`，artifact/purpose probe 会在 evidence 中标记为 `skipped`，不声明镜像 artifact 维度通过。
- 不新增 BOSS、权限、配额、GC 或 Console 前端 E2E。
- `POST /instances` 镜像门禁 422 仍属于实例创建切片，不由本 gate 补齐。

## 复跑命令

```bash
cd repo
make validate-registry-harbor-live-gate
python scripts/validate_registry_harbor_live_gate.py --live --production-shaped --cleanup \
  --gateway-url <gateway-url> \
  --ani-bearer-token <redacted> \
  --tenant-id <tenant> \
  --project <tenant-project> \
  --namespace <tenant-namespace> \
  --pull-secret-name <unique-live-secret-name> \
  --evidence-output development-records/live-evidence/sprint13-registry-harbor-live-evidence.json
```
