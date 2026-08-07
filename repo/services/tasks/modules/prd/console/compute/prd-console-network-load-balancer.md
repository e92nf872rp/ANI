# PRD: Console 网络负载均衡

> Revised: 2026-08-05
> 详文：`docs/console-modules/compute/network/load-balancer.md`

## 1. Overview

负载均衡创建、查询和删除；创建后独立管理 listeners、后端组、后端成员和健康检查。

## 2. Goals

- scheme internal/public
- vpc_id 必填创建
- listener 必须关联后端组
- 后端支持实例/IP、端口、权重和健康状态

## 3. Frozen resource semantics

- `listener.port` 是对外监听端口。
- typed backend group 模式下，后端实际服务端口唯一来自每个 backend member 的 `port`。
- `target_port` 是 legacy listener 兼容字段，标记为 deprecated；当 `backend_group_id` 存在时 provider 必须忽略它。没有 `backend_group_id` 的 legacy listener 才使用 `target_port`。
- 新客户端创建 LB 使用固定顺序：空 LB → backend groups → members → listeners。父资源 create 的 `listeners[]` 只保留 legacy 兼容输入，不支持创建 backend group/member 关联；携带 `backend_group_id` 时显式返回 `400`。

## 4. User Stories

US-001 列表；US-002 创建；US-003 删除；US-004 配置监听器；US-005 管理后端组和成员；US-006 查看后端健康状态。

## 5. Non-Goals

- UDP listener、HTTPS 证书、静态 VIP 和独立 EIP 资源
- 负载均衡主资源更新

## 6. Implementation boundary

LB 控制面由 Network Service 和 PostgreSQL authoritative Store 管理资源关系、租户隔离、幂等键以及 `pending/apply/observe` 状态；Gateway handler 不直接创建 Kubernetes 对象。`NetworkLoadBalancerProvider` port 接收 ANI 产品意图，Kube-OVN adapter 将 internal LB 渲染并 apply 为稳定命名的 Kubernetes/Kube-OVN 资源，provider observe 再把 VIP、listener/backend member 健康状态回写控制面。公网 LB 在没有 EIP provider 能力时返回 `422 EIPNotAvailable`，不伪造公网实现。

## 7. References

- 详文、SPEC
