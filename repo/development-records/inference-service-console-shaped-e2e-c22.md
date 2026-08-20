# INFERENCE-SERVICE-CONSOLE-SHAPED-E2E-C22

> 日期：2026-08-17
> 状态：live passed（HTTP 进现有 `ani-console` nginx `/api/`，生产 `ani-gateway`，`ANI_AUTH_MODE=auth_service`）
> 方案依据：`services/docs/console-modules/inference/inference-service.md`
> 前置：`INFERENCE-SERVICE-INCLUSTER-E2E-C21`

## 完成范围

- 用租户 Bearer 模拟 Console 请求：进入现有 `ani-console` nginx `/api/`，转发到生产 `ani-gateway`，再走 inference-service gRPC → Core `platform-workloads`。
- 请求只带 `Authorization: Bearer`、`Content-Type: application/json`、`Origin`；不发 `X-Dev-Tenant-ID`。
- 覆盖仪表盘 list，以及 Console SDK 形状的 create / get / stop / start / delete。
- 不删除已有租户 Namespace，不部署第二条 Gateway，不改 `ANI_AUTH_MODE`。
- Core platform-workload Pod 钉到集群 default overlay（`ovn-default` / `ovn-cluster`），不继承租户 Namespace 上的私有 VPC。
- 未改 OpenAPI。Console 推理创建页仍未落地，本批次只模拟其 HTTP。
- 事后清理：删除测试残留 `ani-vllm-cpu-smoke`（含模型 PVC）。后续 C22 不再要求该 Namespace；租户侧需已有 `vllm-model` PVC。

## Design Decisions

- 入口是 `ani-console`，不是 kubectl port-forward 到 Gateway。
- Core `platform-workloads` 仍用 service JWT；按会话 token 的 `tid` 刷新 `CORE_SERVICE_TOKEN`，不把租户 token 用于 Core。
- 模型 PVC 如租户 Namespace 中不存在 `vllm-model`，才从 smoke 快照恢复；跑完只删本次克隆，不删租户其它资源。
- 已有 Console `default` 租户 Namespace 绑了多条私有 VPC subnet。PlatformWorkload 契约没有 VPC/subnet 字段，却会继承 Namespace 网络；kubelet 从节点网探活私有 VPC 是 `no route to host`，不是 NetworkPolicy REJECT。渲染器显式钉 `ovn-default`，租户实例 VPC 不动。
- 本次 live 使用该已有租户的 `tenant-admin` Bearer（经 `ani-console` nginx）。Console `roles=user` 仍不能 DELETE。

## 验证证据

```text
cd /root/kubercon/ANI/repo
python3 scripts/run_inference_console_shaped_e2e.py --kubeconfig /root/.kube/config --token-file "$CONSOLE_TOKEN_FILE"
PATH=/tmp/ani-pybin:$PATH make validate-inference-console-shaped-e2e-live-gate
```

2026-08-17 经 `ani-console` nginx `/api/` 实跑：list `200`、create `202`、CPU vLLM `running`、stop、start、delete `404`。Pod 落在 `ovn-default`，未进租户私有 VPC。未删已有租户 Namespace。生产 Gateway overlay `pw-ovn-default-20260817`，`ANI_AUTH_MODE` 保持 `auth_service`。gate `status: live`。事后删除测试残留 `ani-vllm-cpu-smoke`。

## 明确未完成

- Console 推理创建/详情页面本身未实现。
- Console `roles=user` 不能 DELETE；完整删除需要 `tenant-admin`（或等价 scope）。
- 未接真实 ModelCatalog / auth-service `IssueServiceToken` mint。
- 无 GPU / LWS runtime，不得标记 GPU / LWS / full platform ready。
- `ani-vllm-cpu-smoke` 已删；后续 CPU live 需要租户已有模型 PVC 或新的模型来源。
