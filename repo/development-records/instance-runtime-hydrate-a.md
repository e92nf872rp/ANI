# INSTANCE-RUNTIME-HYDRATE-A — 实例运行时信息回填（节点/私网IP/端点/终端可用性）

> 日期：2026-09-01
> 范围：ANI Gateway `instanceAPI` / K8s REST provider 列表与详情回填路径
> 状态：**live passed（10.10.1.66，镜像 `ani-gateway:dev-20260901`）**——运行中 GPU 容器实例列表/详情已回填节点/私网IP/访问端点/终端；`go test ./services/ani-gateway/...` 全通，`make validate-architecture` 通过

## 问题背景

前端对接 GPU 容器实例时反馈：列表页与详情页缺失运行节点信息、私网 IP 访问地址、访问端点，终端不可用；网络 Tab 只有 VPC/子网，无访问地址/端点。

## 根因（Gateway `internal/router/instances.go`）

1. **节点字段错位**：`refreshOneStoreStatus` 把节点写入 `record.Status.NodeName`，但响应序列化 `compute.node_name` 读的是 `record.Compute.NodeName`。刷新路径从不回填 `Compute.NodeName`，导致 `compute.node_name` 恒为空。
2. **网络/终端不回读**：`refreshOneStoreStatus` 只从 Deployment + Pod 刷 state/replicas/node/调度原因，从不回读 PodIP、访问端点、`access.*`。而 `record.Network.PrivateIP` / `.Endpoints`、`record.Status.Endpoint`、`record.Access.ExecAvailable` 只在创建时快照（通常为空）。
3. **详情页不刷新**：`get` 处理器直接 `service.Get` 后序列化，**不调用任何刷新**，详情页状态比列表更陈旧。

## 实现

- `services/ani-gateway/internal/router/instances.go`
  - `refreshOneStoreStatus`：pod 结构体新增读取 `status.podIP` / `status.podIPs`；回填 `record.Compute.NodeName = Status.NodeName`；从已调度 Pod 回填 `Network.PrivateIP`、`Status.Endpoint`、`Network.Endpoints`（未存在时构造 `private` 端点）；运行态 `container`/`gpu_container` 置 `Access.ExecAvailable = true`。
  - `get` 处理器：拿到 store 记录后调用 `refreshOneStoreStatus` 一次再序列化，使详情与列表一致地回填运行时字段。
- `services/ani-gateway/internal/router/instances_test.go`：新增两个单测，用 httptest 注入 K8s REST 客户端，验证 `compute.node_name` / `private_ip` / `endpoint` / `endpoints` 回填与 `exec_available` 归位（gpu_container 启用、vm 不启用）。

## 完工标准达成

- [x] `go build ./services/ani-gateway/...` 通过
- [x] `go test ./services/ani-gateway/...` 全通（router 新增 `TestRefreshOneStoreStatusHydratesRuntimeFields`、`TestRefreshOneStoreStatusDoesNotSetExecForVM`）
- [x] `make validate-architecture` 通过
- [x] `git diff --check` 通过

## 能力边界

- 本批只回读 Deployment/Pod 派生字段，不新增 Service/NodePort/LoadBalancer 探测；`Network.Endpoints` 在真实 Service/外部访问端点存在时不会被覆盖（仅在建端点为空的补给时写入 private 端点）。
- 配置与密钥摘要（`InstanceRecord` 是否对外暴露 env/secret 绑定）不在本批范围，需另行确认产品口径。

## 真实验证（10.10.1.66，镜像 dev-20260901）

成功滚动更新 `deployment/ani-gateway` 至 `docker.changqingyun.cn/ani/ani-gateway:dev-20260901`；新 Pod 1/1 Running 于 `dev-phys-02`；`curl http://10.10.1.66:30080/healthz` 连续 3 次 200。

租户 `tenant-a` 登录后核对实例接口，运行中 GPU 容器实例（`test-gpu-inst-create`，`state=running`）**列表与详情一致回填**：

```text
compute.node_name            = dev-phys-02        # 运行节点 ✓
network.private_ip           = 10.60.0.3          # 私网 IP ✓
endpoint                     = 10.60.0.3          # 访问端点 ✓
network.endpoints            = [{name:private, address:10.60.0.3, protocol:tcp}]  # 访问端点 ✓
access.exec_available        = true               # 终端可用 ✓
```

非运行/已删除/未调度实例（deleted/pending/provisioning、无已调度 Pod）正确地保持空字段，回填按「已调度 Pod + 运行态」门控，不误产数据。

## 验证命令

```bash
cd repo/pkg && go build ./...
cd repo/services/ani-gateway && go build ./...
go test ./internal/router/... -count=1
# 部署后业务验证（NodePort 30080）
# GET /api/v1/instances?kind=gpu_container 与 GET /api/v1/instances/{id} 应返回
#   compute.node_name、network.private_ip、endpoint、network.endpoints[]、access.exec_available=true
```