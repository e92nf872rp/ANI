# INSTANCE-SANDBOX-CODERUN-A

> 日期：2026-08-01  
> 范围：ANI Core / Instance Management / Sandbox code-run real provider（最小闭环）

## 目标

在 `INSTANCE-SANDBOX-ADAPTER-A` 已闭环 create/lifecycle 的基础上，把 `POST /instances/{id}/sandbox/code-runs` 接到真实 Pod 内执行：

- create sandbox（Kata RuntimeClass）→ Pod Ready → code-run（python/javascript）→ delete
- 任务仍返回 `202 AsyncTask`；result 携带 `status/stdout/stderr/exit_code/truncated`

## 边界

- token / ports / files / checkpoint **仍为 local-session**
- javascript 需要镜像内有 `node`；当前 live 默认镜像只保证 `python3`
- 出网 NetworkPolicy、template catalog、Console 不在本批次
- 不等于 full platform production ready

## 实现要点

- `ports.SandboxCodeRunResult` 补齐 `stdout/stderr/exit_code/completed_at`
- `LocalSandboxRuntime` 支持可选 `SandboxCodeRunner`；无 runner 时保持 `accepted` stub
- `KubernetesSandboxRuntime` 在 apply 启用时注入 runner：REST 查找 Ready Pod + `kubectl exec` 执行 `python3 -c` / `node -e`
- Gateway `ani-gateway` 镜像增加 `kubectl`（与既有 helm/vcluster 工具层一致）
- Gateway handler 把 stdout/stderr/exit_code/completed_at 写入 AsyncTask result
- live gate 增加 `core-instance-sandbox-code-run`；默认镜像改为 tenant `sandbox-python:3.12-alpine`

## 验证

```bash
cd repo
go test ./pkg/adapters/runtime/ -run 'TestKubernetesSandboxRuntime' -count=1
cd services/ani-gateway && GOWORK=off go test ./internal/router/ -run 'TestCreateSandboxCodeRun' -count=1
python3 scripts/validate_sandbox_live_gate.py
```

真实 live（2026-08-01）：

```bash
cd repo
python3 scripts/validate_sandbox_live_gate.py --live \
  --gateway-url http://<node>:30080 \
  --ani-bearer-token '<token>' \
  --tenant-id 11111111-1111-1111-1111-111111111111 \
  --name ani-sandbox-coderun-live \
  --image-ref docker.kubercon.local/11111111-1111-1111-1111-111111111111/sandbox-python:3.12 \
  --evidence-output development-records/live-evidence/instance-sandbox-coderun-live-20260801.json
```

结果：`status=passed`，`code_run_status=succeeded`  
evidence：`development-records/live-evidence/instance-sandbox-coderun-live-20260801.json`  
Gateway：`docker.changqingyun.cn/ani/ani-gateway:instance-sandbox-coderun-20260801-v1`
