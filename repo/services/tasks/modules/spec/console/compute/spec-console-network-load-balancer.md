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
- 后端组保存 `round_robin/weighted_round_robin` 算法和健康检查配置。
- 后端成员保存实例/IP 目标、端口和权重，并返回只读 `health_status`。
- 写操作使用 `idempotency_key`；冲突返回 `409`。
- 公网 LB 在平台无 EIP 能力时返回 `422 EIPNotAvailable`。

## 4. References

- `docs/console-modules/compute/network/load-balancer.md`
