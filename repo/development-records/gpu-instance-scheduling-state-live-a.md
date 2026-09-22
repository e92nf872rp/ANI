# GPU-INSTANCE-SCHEDULING-STATE-LIVE-A

> 日期：2026-09-20
> 范围：ANI Gateway 实例列表 / 状态过滤（GPU 容器 `gpu.scheduling_state`）
> 状态：live passed（K8s 测试环境，namespace `ani-system` 与 `ani-test2`，镜像 `ani-gateway:test2-20260920-gpustatelive`）

## 问题背景

Console 的 GPU 容器实例列表有「全部 / 排队中 / 已调度 / 运行中 / 已停止 / 异常」状态筛选，
前端把选中值发给契约声明的 `scheduling_state` 查询参数（语义与前端标签一致，参数名无误）。
现象是：筛选**看着没效果**，且结果错位——

```text
scheduling_state=running  -> 只返回 1 条，且是那条实际 failed 的实例
scheduling_state=stopped  -> 0 条（实际应有 1 条）
scheduling_state=pending  -> 6 条（实际是 5 running + 1 stopped）
```

## 根因（后端字段链路两处断裂）

契约 `api/openapi/v1.yaml` 已声明 `scheduling_state`（enum：pending/queued/scheduled/running/failed）
与响应字段 `gpu.scheduling_state`，前端用法正确。缺陷在实现：

1. **响应根本没这个字段**：DTO `instanceGPUResponse` 只有 7 个键，未声明 `SchedulingState`，
   `gpuResponseFromRecord` 自然也不填；实测所有记录返回的 `gpu` 键集合里都没有 `scheduling_state`。
2. **过滤读的是创建时冻结的陈旧快照**：`GPU.SchedulingState` 全仓唯一写入点是
   `gpuStatusInfo`（记录物质化时调用一次）。列表期读修复 `refreshOneStoreStatus` 只更新
   `Status.State/Reason`、`Container.RolloutStatus`、`NodeName`、`PodIP`、`ExecAvailable`，
   **完全不碰 GPU**。于是过滤命中旧值：已 stopped 的实例永远命中 `pending`，
   已 failed 的实例命中 `running`。

另发现两处相邻缺陷：

3. **孤儿实例路径丢弃大部分过滤**：`instanceAPI.list` 合并 live Kubernetes 孤儿时只做
   `kind / state / network` 三项判断，`scheduling_state`、`rollout_status`、`gpu_model`、
   `queue_name`、`template_id`、`session_state` 被静默跳过（存在孤儿的集群上这些筛选失效）。
4. **孤儿 Deployment 相位映射缺 stopped**：`observeOrphan` 只按 `availableReplicas/replicas`
   分档，`spec.replicas=0`（生命周期停止）与 `Progressing=False`（真实失败）都被压成
   Pending/Provisioning，`orphanState` 也不认识 stopped。

`ani-system` 上的记录全部走 store 路径，所以只表现前两处；`ani-test2` 有孤儿记录，四处叠加。

## 实现

- `pkg/adapters/runtime/instance_orchestrator.go`：`gpuSchedulingState` 导出为
  `GPUSchedulingState`，并补文档说明"必须由 live status 派生，不得读物质化快照"。
- `pkg/adapters/runtime/instance_service.go`：`matchesInstanceList` 导出为
  `MatchesInstanceList`（供 router 层合并孤儿时复用同一套过滤语义）；`scheduling_state`
  过滤改为「kind 必须是 `gpu_container` + `GPUSchedulingState(record.Status)` 现算比对」。
- `services/ani-gateway/internal/router/instances.go`：
  ① `instanceGPUResponse` 补 `SchedulingState`（契约已声明、实现漏掉）；
  ② `gpuResponseFromRecord` 用 `runtimeadapter.GPUSchedulingState(record.Status)` 填充，
     与过滤条件同源，避免"响应与过滤各说各话"；
  ③ `list()` 合并孤儿改用 `MatchesInstanceList`，替换原来只做 kind/state/network 的三连判断；
  ④ `observeOrphan` 增加 `spec.replicas=0 → Stopped`、`Progressing=False → Failed` 分支；
  ⑤ `orphanState` 认 `stopped`。
- 测试：`instance_service_test.go` 新增
  `TestMatchesInstanceListSchedulingStateDerivesFromLiveStatus`（快照与 live status 故意不一致：
  stopped 记录的快照写 `pending`、failed 记录的快照写 `running`，钉死"过滤不看快照"）；
  `instances_test.go` 新增 `TestObserveOrphanStateMapping`（httptest 假 Deployment，覆盖
  running/stopped/failed/provisioning/pending 五档相位与状态映射）；既有调用点改名。

未改契约与生成物：`api/openapi/v1.yaml` 的 `scheduling_state` enum（缺 `stopped`，
且三端 `queued` 不一致）受兼容性基线治理，属独立变更；运行时无 enum 校验，传 `stopped`
返回 200 而非 400，因此该漂移目前只是文档问题。

## 真实验证

镜像 `ani-gateway:test2-20260920-gpustatelive` 滚动到 `ani-system`（NodePort 30080）与
`ani-test2`（NodePort 30083），两个 deployment 均 `successfully rolled out`，`/healthz` 200。

部署后实测（`kind=gpu_container`，两环境各 7 条）：

| 检查 | `ani-system` | `ani-test2` |
|---|---|---|
| 响应出现 `gpu.scheduling_state` | 是（此前无此键） | 是 |
| `gpu.scheduling_state` 与 live state 推导值一致 | 7/7 | 7/7 |
| `scheduling_state=running` | 5 条，集合与真值一致 | 6 条，集合与真值一致 |
| `scheduling_state=stopped` | 1 条（`test-gpu-inst-create3`） | 1 条（同左，孤儿记录） |
| `scheduling_state=failed` | 1 条（`123`） | 0 条 |
| `scheduling_state=pending/scheduled` | 0 / 0 | 0 / 0 |
| `state=running/stopped/failed`（回归） | 5 / 1 / 1 全对 | 6 / 1 / 0 全对 |

修复前对照：`scheduling_state=running` 只回 1 条且是 failed 实例；`stopped` 回 0；`pending` 回 6。

## 能力边界

- 本批只修「状态字段与过滤链路的一致性」，不改契约 enum、不新增筛选控件。
- 顺带发现但**未修**（超出本批范围）：`GET /instances/{id}` 对孤儿记录（id == name）
  返回 `state=null` / `gpu=null`，列表有值、详情没有，属独立缺陷。
- 本地 Windows 环境下 `pkg/adapters/runtime` 的 `TestSandboxFileScripts*` 两个用例失败
  （exit code 9009，测试通过 `runSandboxPythonScript` exec 外部解释器，本机无该可执行文件）；
  已用 stash 基线对照确认该失败在本批改动前即存在，与本批无关。

## 验证命令

```bash
# 代码
cd repo/pkg && go build ./... && go vet ./adapters/runtime/ && go test ./adapters/runtime/ -count=1
cd repo/services/ani-gateway && go build ./... && go test ./internal/router/ -count=1
gofmt -l <changed files>            # 无输出
git diff --check                    # 无输出

# 仓库门禁
python scripts/validate_component_imports.py --root .
python scripts/validate_instance_contracts.py
python scripts/validate_doc_entrypoints.py
python scripts/validate_services_boundary.py --root .   # passed with 7 accepted baseline warning(s)

# live（NodePort 30080=ani-system / 30083=ani-test2）
curl -s 'http://<env>/api/v1/instances?kind=gpu_container&scheduling_state=stopped' -H "Authorization: Bearer $TOK"
```
