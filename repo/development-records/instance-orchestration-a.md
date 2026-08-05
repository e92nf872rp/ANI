# INSTANCE-ORCHESTRATION-A

> 日期：2026-08-01  
> 范围：ANI Core / Instance Management / Container create-time 编排（Registry + Network + Storage）

## 目标

补齐 Container 创建路径上的跨资源编排最小闭环：

- Harbor / Registry 镜像引用经 resolver 准入
- VPC / Subnet / Security Group 解析后写入 Pod `ovn.kubernetes.io/logical_switch` 等网络注解
- Volume 或 Filesystem 挂载解析为 Storage PVC claim，create 后调用 `MountVolume` / `MountFilesystem`
- operation timeline 增加 `resolve_resources` / `network_binding` / `storage_mount`
- create → stop → start → delete 仍走 Core `/api/v1/instances`

清除既有 container E2E evidence 中的：

- `instance_registry_admission`
- `instance_network_selection`
- `instance_storage_attachment_orchestration`

## 边界

- 仅 Container；不含 GPU Container、Sandbox、Console、Exec WS、scale/update_image live、配额
- Storage 证明 volume **或** filesystem 其一即可（本 live 使用 volume + `ani-rbd-ssd`）
- WaitForFirstConsumer PVC 在 create 前可为 `pending`（resolver 接受 pending）
- 不等于 full platform production ready

## 实现要点

- `dryrun_renderer`：OVN/network 注解；volume/filesystem → `shared_pvc` claim（`vol-*` / `fs-*`）
- `instance_resource_resolver`：充实 `Spec.Storage`；pending volume 可挂载
- `LocalInstanceService`：`WithInstanceStorageService`；create 后 bind storage；timeline 扩展
- Gateway：`SharedNetworkService` / `SharedStorageService` / `SharedImageRegistry` 注入 Instance runtime，避免 `/networks`/`/volumes` 与 `/instances` 各用一套内存 store
- Gateway Harbor：`REGISTRY_TLS_INSECURE` 透传到 `HarborImageRegistry.InsecureSkipVerify`

## 验证

```bash
cd repo
go test ./pkg/adapters/runtime/ ./pkg/bootstrap/ -count=1
(cd services/ani-gateway && GOWORK=off go test . -count=1)
make validate-instance-orchestration-live-gate
```

真实 live（2026-08-01）：

```bash
cd repo
python3 scripts/validate_instance_orchestration_live_gate.py --live \
  --gateway-url http://<node>:30080/api/v1 \
  --ani-bearer-token '<token>' \
  --tenant-id 11111111-1111-1111-1111-111111111111 \
  --image-ref docker.kubercon.local/<tenant>/nginx:alpine \
  --vpc-id <vpc> --subnet-id <subnet> --volume-id <volume> \
  --evidence-output development-records/live-evidence/instance-orchestration-container-live-20260801.json
```

结果：`status=passed`  
evidence：`development-records/live-evidence/instance-orchestration-container-live-20260801.json`  
Gateway：`docker.changqingyun.cn/ani/ani-gateway:instance-orchestration-20260801-v3`
