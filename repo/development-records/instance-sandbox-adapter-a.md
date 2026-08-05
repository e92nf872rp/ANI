# INSTANCE-SANDBOX-ADAPTER-A / INSTANCE-SANDBOX-LIVE-GATE-A

> 日期：2026-08-01  
> 范围：ANI Core / Instance Management / Sandbox real provider（create/lifecycle）+ live gate

## 目标

把 `kind=sandbox` 从 local-only 推进到真实 Kata RuntimeClass 写路径：

- 集群安装 Kata（kata-deploy 4.0.0），提供 `RuntimeClass/sandbox-kata`（handler=`kata-qemu`）
- 新增 `KubernetesSandboxRuntime`：Create 渲染/Apply Deployment（`runtimeClassName=sandbox-kata`）；pause/resume scale；delete 清理资源
- Core 写路径仍为 `/api/v1/instances`
- live gate：create → Deployment 观测 → pause → resume → delete

## 边界

- token / ports / files / checkpoint / code-run **仍为 local-session**，本批次不宣称子资源 real-provider
- 出网 NetworkPolicy 自动下发、template catalog live、Console Sandbox 页不在本批次
- 不等于 full platform production ready

## 实现要点

- `pkg/adapters/runtime/kubernetes_sandbox_runtime.go`：apply 启用时由 bootstrap 注入；否则保持 Local
- `createSandbox` 写入真实 `ResourceRefs`（`kubernetes/Deployment/...`）与 `dev_profile.real_provider=true`
- 修复 `MetadataWorkloadIdentityService`：`pgx.ErrNoRows` 映射为 `ports.ErrNotFound`，无 api_key 时 delete revoke 幂等成功
- lab 清单：`deploy/manifests/m1-sandbox-kata/`（kata-deploy values + README）
- live 脚本：`scripts/validate_sandbox_live_gate.py`；`make validate-sandbox-live-gate`

## 验证

```bash
cd repo
go test ./pkg/adapters/runtime/ -run 'TestKubernetesSandboxRuntime|TestLocalSandbox' -count=1
make validate-sandbox-live-gate
```

真实 live（2026-08-01）：

```bash
cd repo
python3 scripts/validate_sandbox_live_gate.py --live \
  --gateway-url http://<node>:30080 \
  --ani-bearer-token '<token>' \
  --tenant-id 11111111-1111-1111-1111-111111111111 \
  --name ani-sandbox-live-b \
  --image-ref docker.kubercon.local/11111111-1111-1111-1111-111111111111/sandbox-busybox:1.36 \
  --evidence-output development-records/live-evidence/instance-sandbox-live-20260801.json
```

结果：`status=passed`；evidence：`development-records/live-evidence/instance-sandbox-live-20260801.json`  
Gateway 镜像：`docker.changqingyun.cn/ani/ani-gateway:instance-sandbox-live-20260801-v2`
