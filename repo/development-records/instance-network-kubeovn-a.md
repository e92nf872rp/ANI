# INSTANCE-NETWORK-KUBEOVN-A — instance create network selection to Kube-OVN subnet

完成日期：2026-07-06
对应 Sprint：Sprint 13（Core real provider 与 live gate 收敛）
验证结果：local/logic verified + real cluster container subnet verification passed

> 边界声明：本批次把 `POST /instances` 的 ANI `network.subnet_id` 选择接到 workload manifest 的 Kube-OVN Pod annotation，并持久化到 `workload_instances`。真实集群验证覆盖 container Deployment -> Pod 落入指定 Kube-OVN subnet；VM/KubeVirt 当前以 manifest/template 单测证明 virt-launcher template annotation 路径，不声明完整 VM live gate。

## 背景

Console 以后只提交 ANI 网络意图：

```json
{
  "network": {
    "vpc_id": "vpc_xxx",
    "subnet_id": "subnet_xxx",
    "private_ip": "10.74.0.10"
  }
}
```

用户不接触 Kube-OVN annotation。Gateway 后端负责校验 tenant 下 subnet/vpc 状态，并把 ANI `subnet_id` 翻译成 provider `Subnet` CR 名称。

## 实现了什么

1. **Core API 契约**
   - `CreateInstanceRequest.network` 新增可选 `vpc_id/subnet_id/private_ip`。
   - `InstanceRecord` 新增可选 `vpc_id/subnet_id/private_ip`。
   - 同步补充可选 `command/args`，用于容器类实例通过 API 覆盖镜像默认入口。
2. **数据库**
   - `workload_instances` 增加 nullable `vpc_id/subnet_id/private_ip`。
   - 新增迁移 `20260706_001_instance_network_selection.sql`。
   - 初始化 SQL 同步补齐字段。
3. **Gateway 校验**
   - `network.subnet_id` 有值时，按 `tenant_id + subnet_id` 查询 subnet。
   - subnet/vpc 必须为 `available`。
   - 请求 `vpc_id` 有值时必须等于 `subnet.vpc_id`。
   - `private_ip` 必须为合法 IPv4、在 subnet CIDR 内且不等于 gateway。
4. **Provider 映射**
   - 复用 Kube-OVN provider name helper：`subnet_xxx` -> `subnet-subnet-xxx`。
   - Deployment/Job pod template 与 KubeVirt VM `spec.template.metadata.annotations` 注入：
     - `ovn.kubernetes.io/logical_switch`
     - `ovn.kubernetes.io/ip_address`（仅 private IP 有值时）
   - 同步注入 ANI 调试 annotation：`ani.kubercloud.io/vpc-id/subnet-id`。
5. **持久化与响应**
   - instance orchestrator/service/store 将选定网络写入 `WorkloadInstanceRecord` 和 PG。
   - list/detail/create response 返回网络字段。

## 关键文件

| 文件 | 说明 |
|---|---|
| `api/openapi/v1.yaml` | CreateInstanceRequest / InstanceRecord 契约 |
| `services/ani-gateway/internal/router/demo_instances.go` | network 入参校验、spec attachment 注入、command/args 映射 |
| `pkg/adapters/runtime/dryrun_renderer.go` | Kube-OVN annotation 注入到 pod template / VM template |
| `pkg/adapters/runtime/kubeovn_network_renderer.go` | Kube-OVN provider name helper |
| `pkg/adapters/runtime/instance_orchestrator.go` | 从 primary tenant_vpc attachment 生成实例记录网络字段 |
| `pkg/adapters/runtime/instance_store.go` | `workload_instances` 持久化与 scan |
| `deploy/migrations/20260706_001_instance_network_selection.sql` | 增量迁移 |

## 单测覆盖

- subnet 不存在 -> 400
- subnet 不属于当前 tenant -> 400
- subnet 非 available -> 400
- vpc_id 和 subnet.vpc_id 不一致 -> 400
- private_ip 不在 subnet.cidr 内 -> 400
- private_ip 等于 gateway -> 400
- valid network -> 创建成功并保存 vpc_id/subnet_id/private_ip
- container provider manifest 包含 `ovn.kubernetes.io/logical_switch` 与 `ovn.kubernetes.io/ip_address`
- VM provider manifest 在 KubeVirt launcher pod template 路径包含 Kube-OVN annotation
- container `command/args` 从 API request 映射到 workload spec

## 真实集群验证

### 初始 10.72 验证与问题

按需求创建：

```bash
POST /api/v1/networks/vpcs
{"name":"test","cidr":"10.72.0.0/24","idempotency_key":"live-vpc-20260706-a"}

POST /api/v1/networks/subnets
{"vpc_id":"vpc_e98fdcbf-113a-4627-a597-35768fca273b","name":"test-subnet","cidr":"10.72.0.0/25","gateway":"10.72.0.1","idempotency_key":"live-subnet-20260706-a"}
```

Gateway response 与 Deployment template 均写入：

```text
subnet_id=subnet_10de4df3-ffba-4b92-a125-0436f73616ae
ovn.kubernetes.io/logical_switch=subnet-subnet-10de4df3-ffba-4b92-a125-0436f73616ae
```

但实际 Pod 被 Kube-OVN 回写到旧重叠 subnet：

```text
ovn.kubernetes.io/logical_switch=subnet-subnet-82d2354a-750b-468d-b68e-bccc0e3581da
ovn.kubernetes.io/cidr=10.72.0.0/27
```

根因：tenant namespace 同时挂载了旧 `10.72.0.0/27` 与新 `10.72.0.0/25` logical switch，CIDR 重叠：

```bash
kubectl get subnet.kubeovn.io -o custom-columns=NAME:.metadata.name,CIDR:.spec.cidrBlock,GATEWAY:.spec.gateway,VPC:.spec.vpc --no-headers
```

关键输出：

```text
subnet-subnet-10de4df3-ffba-4b92-a125-0436f73616ae   10.72.0.0/25   10.72.0.1   vpc-vpc-e98fdcbf-113a-4627-a597-35768fca273b
subnet-subnet-82d2354a-750b-468d-b68e-bccc0e3581da   10.72.0.0/27   10.72.0.1   vpc-vpc-ce0e1f23-9f0e-4a67-b2c2-dbbba3c46396
```

结论：代码和 template annotation 路径正确，但测试环境存在重叠 subnet 干扰。未删除历史资源，改用非重叠网段复验。

### 干净网段复验

创建 VPC/Subnet：

```bash
POST /api/v1/networks/vpcs
{"name":"test-clean","cidr":"10.74.0.0/24","idempotency_key":"live-vpc-clean-20260706-a"}

POST /api/v1/networks/subnets
{"vpc_id":"vpc_a8624e53-abcc-4fc8-884e-a40c83a5a086","name":"test-clean-subnet","cidr":"10.74.0.0/25","gateway":"10.74.0.1","idempotency_key":"live-subnet-clean-20260706-a"}
```

创建两个 container instance：

```bash
POST /api/v1/instances
{"kind":"container","name":"net-clean-a","image":"dockerproxy.net/library/nginx:1.27-alpine","cpu":"100m","memory":"64Mi","idempotency_key":"live-instance-clean-a-20260706-a","network":{"vpc_id":"vpc_a8624e53-abcc-4fc8-884e-a40c83a5a086","subnet_id":"subnet_c3c56d1d-495f-40d3-ae6b-a9d931b8f55c","private_ip":"10.74.0.10"}}

POST /api/v1/instances
{"kind":"container","name":"net-clean-b","image":"dockerproxy.net/library/nginx:1.27-alpine","cpu":"100m","memory":"64Mi","idempotency_key":"live-instance-clean-b-20260706-a","network":{"vpc_id":"vpc_a8624e53-abcc-4fc8-884e-a40c83a5a086","subnet_id":"subnet_c3c56d1d-495f-40d3-ae6b-a9d931b8f55c","private_ip":"10.74.0.11"}}
```

验证 Pod：

```bash
kubectl -n ani-tenant-11111111-1111-1111-1111-111111111111 get pods -l 'ani.kubercloud.io/instance in (net-clean-a,net-clean-b)' -o wide
```

结果：

```text
net-clean-a-86b78d59b9-tf7bn   1/1   Running   10.74.0.10
net-clean-b-689fbff969-bjpvk   1/1   Running   10.74.0.11
```

验证 annotation：

```bash
kubectl -n ani-tenant-11111111-1111-1111-1111-111111111111 get pod net-clean-a-86b78d59b9-tf7bn -o jsonpath='{.metadata.annotations.ovn\.kubernetes\.io/logical_switch}{"\n"}{.metadata.annotations.ovn\.kubernetes\.io/ip_address}{"\n"}{.metadata.annotations.ovn\.kubernetes\.io/cidr}{"\n"}{.status.podIP}{"\n"}'
```

结果：

```text
subnet-subnet-c3c56d1d-495f-40d3-ae6b-a9d931b8f55c
10.74.0.10
10.74.0.0/25
10.74.0.10
```

互 ping：

```bash
kubectl -n ani-tenant-11111111-1111-1111-1111-111111111111 exec net-clean-a-86b78d59b9-tf7bn -- ping -c 3 10.74.0.11
```

结果：

```text
3 packets transmitted, 3 packets received, 0% packet loss
```

现场恢复：

```bash
kubectl -n ani-system set env deploy/ani-gateway ANI_AUTH_MODE=auth_service
kubectl -n ani-system rollout status deploy/ani-gateway --timeout=120s
kubectl -n ani-system delete pod ani-curl --ignore-not-found=true
```

确认 Gateway 已恢复 `ANI_AUTH_MODE=auth_service`。

## 验收命令

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'KubeOVNAnnotations|KubernetesDryRunRenderer|MetadataInstanceStore|LocalInstanceService|LocalInstanceOrchestrator'
GOCACHE=/tmp/ani-go-cache go test ./services/ani-gateway/internal/router -run 'DemoSpecFromRequestMapsContainerCommandAndArgs|DemoInstanceNetwork'
GOCACHE=/tmp/ani-go-cache go test ./services/ani-gateway/internal/router ./services/ani-gateway
python3 scripts/validate_yaml.py api/openapi/v1.yaml
python3 scripts/validate_core_alpha_contract.py
python3 scripts/validate_core_beta_contract.py
python3 scripts/validate_network_alpha_contract.py
python3 scripts/validate_instance_contracts.py
python3 scripts/validate_core_api_compatibility.py
git diff --check
```

已知验证边界：

- `python3 scripts/validate_core_api_compatibility.py` 当前有既有 baseline drift：`GET /branding changed operationId`，与本批次无关。
- `make validate-demo-instances` 在当前沙箱使用 `/private/tmp/...` GOCACHE 不可写；改跑等价 `GOCACHE=/tmp/ani-go-cache go test`。
- `make image-gateway` 重新构建审批被拒；真实集群验证使用已部署的 Gateway network 改动镜像，新增 `command/args` 仅通过本地单测验证。

## 后续建议

- network provider 应拒绝同一 tenant namespace 下重叠 subnet CIDR，或在创建 subnet 前检测 Kube-OVN/metadata 中已有重叠 CIDR，避免 Kube-OVN IPAM 按旧 subnet 回写 logical switch。
- 如果要把 VM 标成 live verified，需要新增 KubeVirt VM 创建真实验证，检查 virt-launcher Pod annotation/IP。
