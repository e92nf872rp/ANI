# REGISTRY-P0-CLOSURE-A

> 日期：2026-08-03
> 范围：ANI Core / Registry P0 闭环（Harbor purpose/scan/引用/删除门禁）

## 目标

在现有 Sprint13 Harbor live gate 之上，把镜像 P0 闭环固定为可复跑 gate：

- project / push-instructions / pull-secret
- 真实 artifact（push 或预置 repository/tag）
- purpose 过滤与 scan-result 漏洞摘要
- 创建实例后 tag references
- 仍有引用时 delete tag 返回 409

## 边界

- 不改 OpenAPI v1
- 不声明 BOSS quota/GC、Console 或 full platform production ready
- evidence 只保存状态、计数、响应哈希和资源 ID，不含 Token/密码/dockerconfig

## 契约与脚本

- `deploy/real-k8s-lab/registry-harbor-live-gate.yaml` profile=`REGISTRY-P0-CLOSURE-A`
- `scripts/validate_registry_harbor_live_gate.py` / `_test.py`
- `make validate-registry-harbor-live-gate`

## 验证

```bash
cd repo
make validate-registry-harbor-live-gate
```

真实 live（需人工确认；Gateway 需已部署 A2/A3 镜像门禁）：

```bash
cd repo
python3 scripts/validate_registry_harbor_live_gate.py --live --production-shaped --cleanup \
  --gateway-url http://<node>:30080 \
  --ani-bearer-token '<token>' \
  --tenant-id 11111111-1111-1111-1111-111111111111 \
  --project 11111111-1111-1111-1111-111111111111 \
  --repository <repo> \
  --tag <tag> \
  --purpose container \
  --namespace ani-tenant-11111111-1111-1111-1111-111111111111 \
  --pull-secret-name ani-registry-p0-pull \
  --evidence-output development-records/live-evidence/registry-p0-closure-live-20260803.json
```

可选：`--source-image <local-or-remote>` + 环境变量 `HARBOR_USERNAME`/`HARBOR_PASSWORD` 执行真实 push。

## 当前状态

- 契约 / 单测：已通过
- 真实 live：2026-08-03 passed（production-shaped；scan terminal=`complete`）
  - evidence：`live-evidence/registry-p0-closure-live-20260803.json`
  - 预置 artifact：`nginx:latest`（仓库名推导 purpose=`container`）
  - scan-result：`complete`（Harbor Trivy Success + nested summary 可解码）
  - instance create `201` → references=`1` → delete tag `409` → cleanup deleted
  - Gateway：`ani-gateway:registry-p0-closure-20260803-v2`；`ANI_INSTANCE_IMAGE_VULN_GATE=observe`
  - Harbor Trivy（node1 docker-compose v2.14.4）：`SCANNER_TRIVY_DB_REPOSITORY=docker.kubercon.local/library/trivy-db`，`INSECURE=True`（原 `mirror.gcr.io` 超时导致 Running→Error）
  - 不含 BOSS quota/GC / Console / full platform production ready
