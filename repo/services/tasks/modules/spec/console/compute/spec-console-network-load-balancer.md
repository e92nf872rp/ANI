# SPEC: Console 网络负载均衡

> Revised: 2026-08-05

## 2. Frozen Facts

| Method | Path | operationId | 成功 | 错误 |
|---|---|---|---|---|
| GET | `/api/v1/networks/load-balancers` | `listNetworkLoadBalancers` | `200 + NetworkLoadBalancerListResponse` | `401`,`403` |
| POST | `/api/v1/networks/load-balancers` | `createNetworkLoadBalancer` | `201 + NetworkLoadBalancer` | `400`,`401`,`403`,`404` |
| GET | `/api/v1/networks/load-balancers/{load_balancer_id}` | `getNetworkLoadBalancer` | `200 + NetworkLoadBalancer` | `401`,`403`,`404` |
| DELETE | `/api/v1/networks/load-balancers/{load_balancer_id}` | `deleteNetworkLoadBalancer` | `200 + NetworkLoadBalancer` | `401`,`403`,`404` |
| GET/POST | `/api/v1/networks/load-balancers/{load_balancer_id}/listeners` | `list/createNetworkLoadBalancerListener` | `200/201` | `400`,`401`,`403`,`404`,`409` |
| GET/PUT/DELETE | `/api/v1/networks/load-balancers/{load_balancer_id}/listeners/{listener_id}` | `get/update/deleteNetworkLoadBalancerListener` | `200` | `400`,`401`,`403`,`404`,`409` |
| GET/POST | `/api/v1/networks/load-balancers/{load_balancer_id}/backend-groups` | `list/createNetworkLoadBalancerBackendGroup` | `200/201` | `400`,`401`,`403`,`404`,`409` |
| GET/PUT/DELETE | `/api/v1/networks/load-balancers/{load_balancer_id}/backend-groups/{backend_group_id}` | `get/update/deleteNetworkLoadBalancerBackendGroup` | `200` | `400`,`401`,`403`,`404`,`409` |
| GET/POST | `/api/v1/networks/load-balancers/{load_balancer_id}/backend-groups/{backend_group_id}/members` | `list/createNetworkLoadBalancerBackendMember` | `200/201` | `400`,`401`,`403`,`404`,`409` |
| GET/PUT/DELETE | `/api/v1/networks/load-balancers/{load_balancer_id}/backend-groups/{backend_group_id}/members/{member_id}` | `get/update/deleteNetworkLoadBalancerBackendMember` | `200` | `400`,`401`,`403`,`404`,`409` |

## 3. Schema Decisions

- `CreateNetworkLoadBalancerListenerRequest.backend_group_id` 必填；历史父资源 `listeners[]` 摘要中的同名字段保持可选。
- `listener.port` 是前端监听端口；`NetworkLoadBalancerBackendMember.port` 是后端实际服务端口。typed backend group 模式下，`member.port` 是唯一权威来源。
- `target_port` 在摘要、typed resource 和 create/update request 中均为可选 deprecated 兼容字段。存在 `backend_group_id` 时 provider 必须忽略 `target_port`；无 `backend_group_id` 的 legacy listener 必须提供并使用 `target_port`。
- 新客户端创建顺序冻结为：空 LB → backend groups → members → listeners。父资源 `CreateNetworkLoadBalancerRequest.listeners[]` 仅兼容 legacy listener 输入，不支持 backend group/member 原子关联；item 携带 `backend_group_id` 时 Core 必须返回 `400`，不得静默忽略。
- 后端组保存 `round_robin/weighted_round_robin` 算法和健康检查配置。
- 后端成员保存实例/IP 目标、端口和权重，并返回只读 `health_status`。
- 写操作使用 `idempotency_key`；冲突返回 `409`。
- 公网 LB 在平台无 EIP 能力时返回 `422 EIPNotAvailable`。

## 4. Implementation boundary

- Network Service 负责 listener/backend group/member 的关系校验、端口语义、删除冲突、租户隔离和幂等，并将控制面状态写入 PostgreSQL authoritative Store。
- provider apply/observe 通过 `NetworkLoadBalancerProvider` port 完成；Gateway handler 不直接依赖 Kubernetes SDK，也不拼装 Kube-OVN 对象。
- Kube-OVN adapter 只渲染 ANI 产品意图为稳定命名的 Kubernetes/Kube-OVN 资源；`member.port` 用于后端 Endpoint/Service 端口，listener `port` 用于前端监听，`target_port` 在 typed 模式不参与渲染。
- provider observe 负责回写 VIP 与 member `health_status`；Gateway 内存定时器不得伪造健康状态。公网 LB 缺少 EIP 能力时返回 `422 EIPNotAvailable`。

## 5. References

- `docs/console-modules/compute/network/load-balancer.md`
