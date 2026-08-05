# INSTANCE-SANDBOX-PORTS-A

> 日期：2026-08-01
> 范围：ANI Core / Instance Management / Sandbox preview ports real-provider

## 目标

把 `POST/DELETE …/sandbox/ports` 接到真实 Kubernetes：

- 创建 `Service type=NodePort`（契约明确不做产品语义 Ingress）
- `preview_url` 使用 Pod `hostIP` + 分配的 `nodePort`
- delete 删除对应 Service；sandbox delete 时清理全部 preview Service

## 边界

- 不改 OpenAPI v1
- token / checkpoint 仍 local-session
- 不含 GPU、Console、Ingress/Gateway 产品路由
- Gateway 镜像：`instance-sandbox-ports-20260801-v1`

## 实现要点

- `LocalSandboxRuntime` 增加 `portOpener` / `portCloser`
- `KubernetesSandboxRuntime` apply 启用时注入 NodePort Service 后端
- preview host 优先 `SANDBOX_PREVIEW_HOST`，否则用 Ready Pod `status.hostIP`（避免要求 nodes RBAC）
- live gate 增补 `core-instance-sandbox-ports`：Pod 内起 `http.server` → create port → 观测 Service → curl preview_url → delete port

## 验证

```bash
cd repo
go test ./pkg/adapters/runtime/ -run 'TestKubernetesSandboxRuntime' -count=1
python3 scripts/validate_sandbox_live_gate.py
```

真实 live（2026-08-02）：

```bash
cd repo
python3 scripts/validate_sandbox_live_gate.py --live \
  --gateway-url http://<node>:30080 \
  --ani-bearer-token '<token>' \
  --tenant-id 11111111-1111-1111-1111-111111111111 \
  --name ani-sandbox-ports-live4 \
  --image-ref docker.kubercon.local/11111111-1111-1111-1111-111111111111/sandbox-python:3.12 \
  --evidence-output development-records/live-evidence/instance-sandbox-ports-live-20260801.json
```

结果：`status=passed`  
evidence：`development-records/live-evidence/instance-sandbox-ports-live-20260801.json`  
Gateway：`docker.changqingyun.cn/ani/ani-gateway:instance-sandbox-ports-20260801-v1`  
`ports_preview_url` 为真实 NodePort；可达性以 Endpoints + Pod 内 HTTP 为准（Kata 不兼容 port-forward，租户 VPC 阻外部 NodePort）。  
subresources：`code-run-real; files-real; ports-real; token/checkpoint local-session-deferred`
