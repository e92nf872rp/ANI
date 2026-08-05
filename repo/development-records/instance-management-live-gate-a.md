# INSTANCE-MANAGEMENT-LIVE-GATE-A

> 日期：2026-07-31（契约）/ 2026-08-01（VM live evidence）
> 范围：ANI Core / Instance Management / VM live gate

## 目标

定义并真实跑通统一实例管理 VM live gate，固定真实验证必须走 Core `/api/v1/instances` 产品路径：

- `POST /api/v1/instances` 创建 `kind=vm`
- `GET /api/v1/instances/{instance_id}` 轮询运行态
- `POST /api/v1/instances/{instance_id}/console` 验证 `vnc` 与 `console`
- `POST /api/v1/instances/{instance_id}/lifecycle` 执行 `stop`、`start`、`delete`
- Kubernetes/KubeVirt 仅允许只读观测 VM/VMI，不允许用 `kubectl apply/patch/delete` 代替 Core API 写路径

## 边界

- VM 与 Sandbox 分开推进；VM 不依赖 Kata RuntimeClass。
- 不修改 Core OpenAPI `v1.yaml`，继续遵循已确认的 v1 契约。
- 本批次证明 VM 基础生命周期真实写路径闭环；不等于完整 Registry/Network/Storage/GPU Spec 关联编排、配额或 full platform production ready。
- GPU Container / Sandbox live gate 仍属后续。

## 实现要点（live 收口）

- Gateway Harbor 使用域名 `https://docker.kubercon.local`；Pod 内通过 `hostAliases` 解析该域名。
- VM 观测脚本按 Core 实例 **name** 读取 KubeVirt 对象，而不是 opaque `instance_id`。
- KubeVirt lifecycle subresource 使用 `PUT`（非 `POST`），Accept 兼容空 body 202；`start` 对 “already running” 409 幂等成功。
- Delete 清理混用的 `kubevirt/*` 与 `kubernetes/Secret/*` resource refs 时按各自 provider 解析。
- VM 渲染：`logSerialConsole=false`；默认 pod 网络；跳过镜像路径占位 root PVC。

## 验证

契约 / 单测：

```bash
cd repo/scripts && python3 -m unittest validate_instance_management_live_gate_test -v
cd repo && make validate-instance-management-live-gate
```

真实 live（2026-08-01）：

```bash
cd repo
python3 scripts/validate_instance_management_live_gate.py --live \
  --gateway-url "<gateway>/api/v1" \
  --ani-bearer-token "<token>" \
  --tenant-id 11111111-1111-1111-1111-111111111111 \
  --image-ref docker.kubercon.local/11111111-1111-1111-1111-111111111111/system-cirros:v1.8.2 \
  --evidence-output development-records/live-evidence/instance-management-vm-live-20260731.json
```

结果：`status=passed`；evidence：`development-records/live-evidence/instance-management-vm-live-20260731.json`  
覆盖 create→running、KubeVirt VM/VMI 只读观测、console(vnc/console)、stop→start→delete。

聚焦单测（lifecycle）：

```bash
cd repo && go test ./pkg/adapters/runtime/ -count=1 -run 'KubernetesLifecycleExecutor'
```
