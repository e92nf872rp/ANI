# SPEC: Console 网络负载均衡

> Revised: 2026-06-17

## 2. Frozen Facts

| Method | Path | operationId | 成功 | 错误 |
|---|---|---|---|---|
| GET | `/api/v1/networks/load-balancers` | `listNetworkLoadBalancers` | `200 + NetworkLoadBalancerListResponse` | `401`,`403` |
| POST | `/api/v1/networks/load-balancers` | `createNetworkLoadBalancer` | `201 + NetworkLoadBalancer` | `400`,`401`,`403`,`404` |
| GET | `/api/v1/networks/load-balancers/{load_balancer_id}` | `getNetworkLoadBalancer` | `200 + NetworkLoadBalancer` | `401`,`403`,`404` |
| DELETE | `/api/v1/networks/load-balancers/{load_balancer_id}` | `deleteNetworkLoadBalancer` | `200 + NetworkLoadBalancer` | `401`,`403`,`404` |

## 3. TODO-YAML

- listeners CRUD；目标组

## 4. References

- `docs/console-modules/compute/network/load-balancer.md`
