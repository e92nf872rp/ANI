# INSTANCE-EXEC-KUBERNETES-STREAM-A — Kubernetes container exec streaming adapter

完成日期：2026-07-08
对应 Sprint：Sprint 13（Core real provider 与 live gate 收敛）
验证结果：local/logic verified（未连接集群验证）

> 边界声明：本批次只补 ANI Core 真实容器 exec 的 adapter 数据面与 Gateway 委托路径；不修改 Console 前端，不新增 Services 路径，不放宽 `scope:instances:exec` 鉴权。当前集群机器暂不可连，因此不声明 live passed、runtime ready 或 production ready。

## 背景

`INSTANCE-EXEC-WS-A` 已完成浏览器 WebSocket 契约、短期 token、Gateway 101 握手与 local profile shell，但真实 Kubernetes Pod exec streaming 仍停留在后续项。Console 已按 KubeCloud TerminalMessage JSON 发送 stdin/resize；后端需要在 adapter 边界把该协议转换为 Kubernetes exec channel 协议，而不是继续落到 Gateway 本地 shell。

## 实现了什么

1. **port 边界**
   - 新增 `InstanceExecSessionConnector` 可选接口，用于 observability adapter 承接已鉴权的 exec session。
   - 新增 `InstanceExecTerminalStream`，表达浏览器侧终端消息流，不泄漏 Kubernetes SDK 或 channel 细节到 Gateway handler。
2. **Gateway 委托**
   - `GET /instances/{id}/exec/{session}` 仍由 Gateway 完成 token 校验和 WebSocket 101。
   - 当 `InstanceObservability` 实现 `InstanceExecSessionConnector` 时，Gateway 将浏览器 TerminalMessage 转成 port stream 并委托 adapter；否则保留 local shell profile。
   - provider exec 路径保留原始 stdin 字节，不使用 local shell 的 `\r -> \n` 兼容转换；resize 保留 `Cols/Rows`。
3. **Kubernetes adapter**
   - `PrometheusInstanceObservability.ConnectExecSession` 解析目标 Pod 后打开 Kubernetes `/api/v1/namespaces/{ns}/pods/{pod}/exec` WebSocket。
   - Kubernetes upstream 使用 `v4.channel.k8s.io`，带 Bearer token 与已解析的 Kubernetes TLS CA 配置。
   - 前端 stdin 映射到 channel `0`；stdout/stderr channel `1/2` 统一映射回 `{"Op":"stdout","Data":...}`；resize 映射到 channel `4` JSON `{"Width":cols,"Height":rows}`。

## 单测覆盖

- Prometheus/Kubernetes observability connector 会先用 label selector 解析 Running/Ready Pod。
- exec query 包含 command、stdin/stdout/stderr/tty 与 container 参数语义。
- stdin/resize 转成 Kubernetes channel `0/4` 帧。
- Kubernetes stdout/stderr channel `1/2` 解包成浏览器 TerminalMessage `Op=stdout`。
- Gateway provider exec helper 保留原始 stdin `\r`，resize 保留 rows/cols；local shell 兼容转换仍只用于 local profile。

## 验证命令

```bash
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'TestPrometheusInstanceObservability|TestKubernetesInstanceOpsExecCreatesSession'
GOCACHE=/tmp/ani-go-cache go test ./services/ani-gateway/internal/router -run 'TestDemoInstanceExecWebSocketRejectsMissingToken|TestExecTerminal|TestDemoInstanceObservability'
```

结果：上述本地/逻辑测试通过。未执行集群连接、kubectl 或 live gate。

## 已知边界

- 本批次不证明真实集群可达；后续需在集群恢复后补 live evidence。
- Kubernetes exec 使用 WebSocket `v4.channel.k8s.io`；若目标集群只允许旧 SPDY exec，需要再补 SPDY fallback。
