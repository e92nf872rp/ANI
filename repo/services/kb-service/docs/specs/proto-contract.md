# proto 契约固化（KB-SPLIT-P2 / issue #060）

> 本文件是 kb-service 拆分事务中 proto 契约的落地说明：proto 源单一真源、三份生成物复制点的 pin 约定、
> Python 代码生成命令与版本锁定、D2/D3 边界。执行期权威裁定见 [ADR 0001](../adr/0001-split-kb-from-core.md)
> §四 #7、§五-阶段2、§D2、§D3。冲突时以 ADR 为准，本文件随之修订。

## 1. 契约边界（ADR §五-阶段2）

| 方向 | 契约 | 归属 |
|---|---|---|
| 对上（前端/Gateway） | `kb.v1`（`kb_service.proto`）+ `common.v1`（`common.proto`） | **归 kb 仓库**；拆分后由 kb-service 持有源与生成 |
| 对 rag-engine | `rag.v1`（`rag.proto`：Parse/Embed/Generate/GenerateStream） | 与 rag-engine **共同 pin 同一版本**（两侧源 byte-identical） |
| 对 inference-service（内部直连） | `inference.internal.v1`（`inference_endpoint_resolver.proto`：`ResolveInternalEndpoint`） | 与 Go 侧 `api/proto/` **共同 pin 同一版本**；模型直连解析唯一路径，见 §6 |
| 对 Core | 无 | **无调用即无契约**（D3），见 §5 |

**阶段边界**：`app/generated/` 散复制件的**删除**属阶段 5（ADR §五-阶段5 不带入清单）；阶段 2 只做
契约化与 pin 分发，本批次**不删除**任何生成物，`app/generated/`（kb.v1 + common.v1）与
`app/rag_engine/rag_pb2*`（rag.v1）保持 tracked 可用。

## 2. 单一真源与 pin 约定

kb-service 侧 proto 源是拆分事务的**单一真源**：

```
services/kb-service/proto/kb/v1/kb_service.proto     # kb.v1（对上）
services/kb-service/proto/common/v1/common.proto     # common.v1（对上）
services/kb-service/proto/rag/v1/rag.proto           # rag.v1（对 rag-engine）
services/kb-service/proto/inference/resolver/v1/inference_endpoint_resolver.proto  # inference.internal.v1（对 inference-service 内部直连）
```

对应镜像（必须与上表逐字节一致，由 `tests/test_proto_contract.py` 断言）：

- `api/proto/kb/v1/kb_service.proto`、`api/proto/common/v1/common.proto`（Go 侧契约源）
- `ai/rag-engine/app/grpc/rag.proto`（rag-engine 侧源）
- `api/proto/inference/resolver/v1/inference_endpoint_resolver.proto`（Go 侧契约源；
  文件目录跟随上游 Go 生成物路径 `resolver/v1`，proto 包名保持
  `inference.internal.v1`，服务 `InferenceEndpointResolver`，wire 契约与包名一致）

**pin = 两侧源 byte-identical（sha256 一致）**。任一侧修改必须同步另一侧，否则契约测试失败。
同意变更流程：改 kb-service 侧源 → 同步镜像 → 重新生成生成物（§3）→ 双侧契约测试通过。

快照基线（`68bf198f`）固定时的 sha256：

| 源 | sha256 |
|---|---|
| `kb/v1/kb_service.proto` | `B4C079D50C23E5A108B3637517F6F54EB044F7B6C9525B01D60F07AFAEB4B6B2` |
| `common/v1/common.proto` | `5FB40A5572A62AA0B38F51BAA0436216EA3327D062BEA69A0C92CD2CB316BC43` |
| `rag/v1/rag.proto` | `CEB710F953347ADA31CAA52E9F56F1FA8C614FC528D4B5853110A4AB3C75C581` |

> sha256 仅作快照登记；契约测试断言「两侧逐字节一致」，不硬编码哈希（避免每次合法变更都改测试）。

## 3. Python 代码生成命令

### 3.1 版本锁定（requirements.txt 已钉死）

```
grpcio==1.83.0
grpcio-tools==1.83.0
protobuf==7.35.1
```

生成物头部标注 `Protobuf Python Version: 7.35.1`，`*_pb2_grpc.py` 标注
`GRPC_GENERATED_VERSION = '1.83.0'`。**升级任一依赖须同步重生成全部生成物**，否则运行时
`ValidateProtobufRuntimeVersion` / gRPC 版本门禁会拒绝加载。

### 3.2 生成命令

在 `services/kb-service/` 目录下（`python` 为锁定版本的解释器，如服务 `.venv`）：

```bash
# kb.v1 + common.v1 → app/generated/
python -m grpc_tools.protoc \
  -I proto \
  --python_out=app/generated --pyi_out=app/generated --grpc_python_out=app/generated \
  proto/kb/v1/kb_service.proto proto/common/v1/common.proto

# rag.v1 → app/rag_engine/
python -m grpc_tools.protoc \
  -I proto/rag/v1 \
  --python_out=app/rag_engine --pyi_out=app/rag_engine --grpc_python_out=app/rag_engine \
  proto/rag/v1/rag.proto

# inference.internal.v1 → app/generated/
python -m grpc_tools.protoc \
  -I proto \
  --python_out=app/generated --pyi_out=app/generated --grpc_python_out=app/generated \
  proto/inference/resolver/v1/inference_endpoint_resolver.proto
```

`-I proto` 使 `common/v1/common.proto` 以 `from common.v1 import common_pb2` 形式生成
（kb/common 生成物为**包内绝对导入**）；`inference.internal.v1`（resolver）同样以
`-I proto` 生成（`from inference.resolver.v1 import ...`，生成物落点
`app/generated/inference/resolver/v1/`——目录跟随文件路径，Python 包路径与上游 Go
生成物路径一致）；`rag.proto` 单独以 `-I proto/rag/v1` 生成，产出裸 `rag_pb2.py`
（**不含包路径**），供 `app/rag_engine/` 包内使用。

### 3.3 生成后修正（rag_pb2_grpc.py 相对导入）

`protoc` 不感知 Python 包结构，`rag_pb2_grpc.py` 默认生成绝对导入
`import rag_pb2 as rag__pb2`，但该文件是 `app.rag_engine` 包的一部分，绝对导入在包被 import
时失败。生成后须将这一行改为相对导入：

```
from . import rag_pb2 as rag__pb2
```

（沿用 `RAG-REFACTOR-STEP-2-CONTRACT.md` §1.4 已确立约定；此修正不影响 proto 契约本身。
`kb.v1` / `common.v1` 生成物用包内绝对导入，无需此修正。）

### 3.4 可复现性

以锁定版本按 §3.2/§3.3 重生成，与仓库现有 tracked 生成物**逐字节一致**（12 份全等：
`kb_service_pb2.py` / `kb_service_pb2.pyi` / `kb_service_pb2_grpc.py`、
`common_pb2.py` / `common_pb2.pyi` / `common_pb2_grpc.py`、
`rag_pb2.py` / `rag_pb2.pyi` / `rag_pb2_grpc.py`、
`inference_endpoint_resolver_pb2.py` / `inference_endpoint_resolver_pb2.pyi` / `inference_endpoint_resolver_pb2_grpc.py`）。该一致性由
`tests/test_proto_contract.py::test_generated_artifacts_match_proto_source` 强制
（改了 proto 源必须重新生成并提交，否则运行时加载的是旧契约）。

## 4. D2：身份契约测试边界（ADR §D2）

身份机制（`TenantContext`、`x-user-id` gRPC metadata 等）**原样保留，零改动**（D2 暂缓）。
契约测试仅覆盖**租户上下文传递正确性**——即 tenant 过滤按请求携带的 `tenant_id` 生效、
metadata 缺失不导致越权——**不针对具体 header 形态/名称做强断言**（形态属演进期议题）。

## 5. D3：对 Core 无契约 + RFC 流程（ADR §D3）

拆分后 kb-service **不再调用 Core**，故**无调用即无契约**，对 Core 方向不设契约测试。
未来若产生新的 Core 依赖，须走 RFC 流程（ADR §D3 line 153-154 原文）：

> kb 提案（端点、动机、兼容性影响）→ Core 评审 → 发新版本 → pin 升级。
> 允许改：新增端点、废弃端点、语义修正；不允许：静默变更。

> 对 rag-engine proto 与对上 gRPC 契约不受此流程影响，照常 pin 版本。

## 6. 对 inference-service：内部 endpoint 解析（直连 svc）

知识库问答/解析需要按租户所选模型直连后端推理服务。**首选路径**（inference.internal.v1）：
kb-service 以 `(tenant_id, served_model_name)` 调用 inference-service 的内部 gRPC

- 地址：`inference-service.ani-system.svc.cluster.local:9104`
- RPC：`inference.internal.v1.InferenceEndpointResolver/ResolveInternalEndpoint`

请求只传 `tenant_id` + `served_model_name`（不经 AI Gateway、不带租户 JWT / 用户 API Key、
不自拼 K8s Service DNS、不传公网 `invocation_url`）。响应返回运行时地址快照：

- `base_url`：OpenAI 客户端 base URL（形如 `http://pw-<svc>.ani-tenant-<tenant>.svc.cluster.local:<port>`）
- `served_model_name`：实际模型名——**后续 OpenAI 请求体的 `model` 必须用它**
- `task`：`generate`（→ POST `{base_url}/chat/completions`）或 `embed`（→ POST `{base_url}/embeddings`）
- `status`：必须为 `running`

`base_url` 是运行时地址快照，**不永久缓存**：kb-service 每次模型调用前重新解析，遇服务重启 /
404 / 503 / endpoint 错误时下一次调用自然重解析。错误映射（gRPC code + details 文本错误码）：

| 错误 | 语义 | kb 侧处理 |
|---|---|---|
| `NOT_FOUND` | 当前租户没有该 served_model_name | `InferenceServiceNotFoundError`，降级 rag-engine 默认并告警 |
| `INFERENCE_SERVICE_NOT_READY` | 服务存在但未 running | 客户端内等待 0.5s 重试一次，仍失败则 `InferenceServiceNotReadyError` |
| `RUNTIME_ENDPOINT_MISSING` / `RUNTIME_ENDPOINT_INVALID` | endpoint 缺失/非法 | `InferenceServiceEndpointError`（重试并记录告警） |
| `INVALID_ARGUMENT` | tenant_id / served_model_name 非法 | `InferenceServiceClientError` |

解析结果（`base_url` + 规范化 `served_model_name`）经 `rag.v1` 的 Embed/Generate 请求
（`runtime_endpoint` + `model` 字段）透传给 rag-engine 直连调用：Embed 落
`{base_url}/embeddings`，Generate/GenerateStream 落 `{base_url}/chat/completions`。

- **模型即租户边界**：`inference_services(tenant_id, served_model_name)` 部分唯一索引保证
  同租户内 active 模型名唯一对应一个服务；kb 侧按模型分别解析（Embed 用 `kb.embedding_model`，
  Generate 用问答模型 / 解析摘要用 `kb.default_inference_service`），每次调用都重新解析（不缓存）。
- **网络前置**：NetworkPolicy 须放行 kb-service → `inference-service:9104` 与
  kb-service → 返回的推理 SVC `:8000`。
- **不外发**：`base_url` 属集群内部地址，inference-service 对租户面
  （`InferenceService.invocation_url`）刻意不暴露；本 RPC 仅供服务间调用。
- **契约来源**：kb-service 侧源
  `services/kb-service/proto/inference/resolver/v1/inference_endpoint_resolver.proto` 与 Go 侧镜像
  `api/proto/inference/resolver/v1/inference_endpoint_resolver.proto` 逐字节一致，由 §1/§2 的 pin 断言强制
  （proto 包名 `inference.internal.v1`，服务 `InferenceEndpointResolver`；上游 #196 起，
  旧 `inference.control.v1 ResolveInferenceServiceEndpoint` 临时兼容路径已从双侧移除）。
