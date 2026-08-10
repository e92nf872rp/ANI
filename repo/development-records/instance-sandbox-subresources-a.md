# INSTANCE-SANDBOX-SUBRESOURCES-A

> 日期：2026-08-01
> 范围：ANI Core / Instance Management / Sandbox files real-provider

## 目标

在 code-run 已闭环的基础上，把 Sandbox workspace 文件 API 接到 Ready Pod 真实 IO：

- create sandbox → write file → list → code-run 读回校验 → delete file → lifecycle delete
- 文件落在容器 `/workspace/<relative-path>`

## 边界

- **本批只做 files**；token / ports / checkpoint 仍 local-session
- 不改 OpenAPI v1；`upload_id` 继续 unsupported；单文件上限 1 MiB → 413
- 不含 GPU、Console、NetworkPolicy、template catalog
- 不等于 full platform production ready

## 实现要点

- `LocalSandboxRuntime` 增加可选 `fileLister` / `fileWriter` / `fileDeleter`（与 codeRunner 同模式）
- `KubernetesSandboxRuntime` apply 启用时注入 Pod exec 后端（python3 + kubectl exec）
- Gateway `writeSandboxRuntimeError` 映射 `ports.ErrPayloadTooLarge` → 413
- live gate 新增 `core-instance-sandbox-files`

## 验证

```bash
cd repo
go test ./pkg/adapters/runtime/ -run 'TestKubernetesSandboxRuntime' -count=1
python3 scripts/validate_sandbox_live_gate.py
PATH=/tmp/ani-pybin:$PATH make validate-architecture
```

真实 live（2026-08-01）：

```bash
cd repo
python3 scripts/validate_sandbox_live_gate.py --live \
  --gateway-url http://<node>:30080 \
  --ani-bearer-token '<token>' \
  --tenant-id 11111111-1111-1111-1111-111111111111 \
  --name ani-sandbox-files-live \
  --image-ref docker.kubercon.local/11111111-1111-1111-1111-111111111111/sandbox-python:3.12 \
  --evidence-output development-records/live-evidence/instance-sandbox-files-live-20260801.json
```

结果：`status=passed`
evidence：`development-records/live-evidence/instance-sandbox-files-live-20260801.json`
Gateway：`docker.changqingyun.cn/ani/ani-gateway:instance-sandbox-files-20260801-v1`
subresources：`code-run-real; files-real; token/port/checkpoint local-session-deferred`
