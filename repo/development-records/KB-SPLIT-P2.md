# KB-SPLIT-P2 — proto 契约固化：pin 版本统一分发 + 上传契约入契约测试

完成日期：2026-09-28
对应批次：KB-SPLIT-P2（ADR 0001 §四 #7 proto 三份复制耦合点、§五-阶段2、§D2、§D3，本地 issue #060，依赖 #052 新 RPC 入 proto / #057 #059 向量与对象路径定型）
验证结果：kb-service 单测 **639 passed + 28 skipped**（P1J 基线 615 + 本批新增 22 项契约测试 + 2 项既有生成物同步，零回归）；`make validate-architecture` 4 guardrail 通过；`git diff --check` 通过。

## 实现了什么

ADR 0001 §四 #7 登记的三份 proto 生成物复制耦合点（`app/generated/kb/v1/`、`app/generated/common/v1/`、`app/rag_engine/rag_pb2*`）的**契约固化**——把拆分事务中「proto 缘何单一真源、如何 pin、生成命令与版本锁定、D2/D3 契约边界」沉淀为文档 + 可执行契约测试。

**① 单一真源 + pin 版本统一分发（对上 + 对 rag）**
- 确立 kb-service `proto/` 为 proto 源**单一真源**：`proto/kb/v1/kb_service.proto`（对上 kb.v1）、`proto/common/v1/common.proto`（对上 common.v1）、`proto/rag/v1/rag.proto`（对 rag-engine rag.v1）。
- **pin 约定 = 两侧源 byte-identical（sha256 一致）**，三对镜像：Go 侧 `api/proto/kb/v1/kb_service.proto` + `api/proto/common/v1/common.proto`、rag-engine 侧 `ai/rag-engine/app/grpc/rag.proto`。任一侧漂移即契约测试失败。
- 采用**最小方案**：不引入独立 pip 分发包（最小代码原则），以「真源 + 镜像 + 契约测试断言双侧一致」达成 pin；快照 sha256 仅登记，不硬编码进断言（避免每次合法变更都改测试）。
- 阶段边界：`app/generated/` 散复制件的**删除**属阶段 5（ADR §五-阶段5 不带入清单）；阶段 2 只做契约化/pin 分发，**不删除**生成物，`app/generated/`（kb.v1 + common.v1）与 `app/rag_engine/rag_pb2*`（rag.v1）保持 tracked 可用。

**② 生成命令文档（含 Python 版本锁定）**
- 新增 `docs/specs/proto-contract.md`：契约边界表（对上/对 rag/对 Core）+ 阶段边界声明、单一真源与 pin 约定（三源路径 + 三镜像 + sha256 快照表）、Python 生成命令（版本锁定 protobuf==7.35.1 / grpcio==1.83.0 / grpcio-tools==1.83.0；两条 `grpc_tools.protoc` 命令；`rag_pb2_grpc.py` 相对导入修正约定；可复现性说明）、D2 身份契约测试边界、D3 对 Core 无契约 + RFC 流程引用。
- `docs/specs/README.md` 追加「规格文件」表指向 proto-contract.md。

**③ 契约测试（ADR §五-阶段2 三条 AC 全固化）**
- `tests/test_proto_contract.py` 新建 **22 项**，五组：
  1. **pin（对上 + 对 rag）**：`kb.v1`/`common.v1`/`rag.v1` 三对源 sha256 逐字节一致（parametrize 3）；真源均在 `kb-service/proto/` 下；**生成物↔proto 源一致性**（按 `proto-contract.md` §3.2/§3.3 重生成 9 份 tracked 生成物逐字节比对，防「改源忘重生成」漂移）。
  2. **rag 服务面 + EmbedResponse 扁平数组约定**：`RagEngine` 服务面 = {Parse, Embed, Generate, GenerateStream}；`EmbedResponse` 字段号固化（`vectors_flat=1 / dimension=2 / count=3`）+ `vectors_flat` 为 repeated float（扁平化前提）；wire 往返按 `vectors[i] = flat[i*dim:(i+1)*dim]` 契约切片无损还原。
  3. **上传契约（D1）**：预签名 PUT 时效默认 900s、非正数本地拒绝（不发起 MinIO 往返）；key 规则 `{kb_id}/{doc_id}`（无文件名后缀）；双视角 URL 语义（PUT→public；GET 默认 internal、`view="public"`→public；未知视角拒绝）；servicer 侧完整三步（签发端点 key = `{kb_id}/{doc_id}`、仅 presign 不写 kb_documents）。
  4. **D2（暂缓，零改动）**：租户上下文传递正确性（请求 tenant_id 原样传给仓储查询）；越权防线（以 tenant B 身份请求 tenant A 的 KB → NOT_FOUND、不签发 URL）；无 metadata 不放大可见范围。仅断言「传递正确性」，不对具体 header 形态/名称强断言。
  5. **RPC 面（ADR §一-1.3）**：kb.v1 服务面含 #052/#059 新增（ListOperations/GetOperation/GetObjectURL）且共 **28** 个 RPC；gRPC servicer 覆盖 proto 每个 RPC；servicer 继承生成基类；common.v1.TenantContext 字段号固化。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/kb-service/proto/kb/v1/kb_service.proto` | 新增（tracked 单一真源） | kb.v1 源（对上契约，28 RPC） |
| `services/kb-service/proto/common/v1/common.proto` | 新增（tracked 单一真源） | common.v1 源（TenantContext 等） |
| `services/kb-service/proto/rag/v1/rag.proto` | 新增（tracked 单一真源） | rag.v1 源（对 rag-engine，4 RPC） |
| `services/kb-service/docs/specs/proto-contract.md` | 新增 | 契约边界 + pin 约定 + 生成命令与版本锁定 + D2/D3 |
| `services/kb-service/docs/specs/README.md` | 修改 | 追加「规格文件」表指向 proto-contract.md |
| `services/kb-service/tests/test_proto_contract.py` | 新增 | 22 项契约测试（pin / 生成物↔源一致 / rag EmbedResponse / 上传契约 / D2 / RPC 面） |
| `services/kb-service/docs/execution/status.md` | 修改 | 耦合点 #7 转「已处置」；阶段 2 转「✅ 完成」 |

## 完工标准达成

- [x] [ADR §五-阶段2-对上proto] kb.v1 + common.v1 归 kb 仓库（`services/kb-service/proto/` 单一真源）；停止 `app/generated/` 散复制改 pin 版本分发（真源 + Go 侧镜像 byte-identical 契约测试断言）；生成命令写进 `docs/specs/proto-contract.md`（含 Python 版本锁定 protobuf==7.35.1 / grpcio==1.83.0 / grpcio-tools==1.83.0）
- [x] [ADR §五-阶段2-对rag] rag proto（Parse/Embed/Generate/GenerateStream）与 rag-engine 共同 pin 同一版本（`proto/rag/v1/rag.proto` 与 `ai/rag-engine/app/grpc/rag.proto` byte-identical）；EmbedResponse 扁平 float 数组约定（字段号 + wire 往返切片）列入契约测试
- [x] [ADR §五-阶段2-上传契约] 预签名 PUT URL 签发 + complete 端点（HeadObject 校验 + 登记）契约测试：时效（默认 900s / 非正数本地拒绝）、key 规则（`{kb_id}/{doc_id}`）、双视角 URL 语义（PUT→public / GET internal vs public）固化
- [x] [ADR §D2] 契约测试覆盖租户上下文传递正确性（请求 tenant_id 原样传仓储、越权 NOT_FOUND、无 metadata 不放大），不对具体 header 形态做强断言（D2 暂缓，零改动原则）
- [x] [ADR §D3] 对 Core 不设契约测试（无调用即无契约）；文档登记 RFC 流程（未来新 Core 依赖时启用）
- [x] [ADR §一-1.3] gRPC server 全量 RPC 面与 proto 一致（28 个，含 #052 新增 ListOperations/GetOperation）
- [x] `make test`（等价直跑：kb-service 639 passed + 28 skipped）+ `make validate-architecture` + `git diff --check` 通过
- [x] progress 文档闭环 4 件套（本文件 + README 索引 + CURRENT-SPRINT.md + ANI-06-开发计划.md）+ status.md 阶段进度与耦合点清单更新

## 设计决策

- **pin 采用「真源 + 镜像 + 契约测试」最小方案，不引入独立 pip 分发包**：pin 的实质是「两侧源 byte-identical」，独立分发包会引入发布/版本/CI 复杂度而无额外保障——Karpathy 最小代码原则；后续阶段 5 拆仓时再评估是否升级为独立包。
- **sha256 仅登记快照，不硬编码进测试断言**：契约测试断言「两侧逐字节一致」而非比对固定哈希——避免每次合法 proto 变更都需改测试，同时仍能捕获单侧漂移。
- **阶段 2 不删除 `app/generated/` 生成物**：删除散复制件属阶段 5（ADR §五-阶段5 不带入清单）；阶段 2 只做契约化与 pin，保持生成物 tracked 可用，避免破坏当前运行链路。
- **D2 契约测试只断言「传递正确性」**：身份机制（TenantContext、x-user-id metadata）按 D2 暂缓原样保留零改动——契约测试聚焦 tenant 过滤是否按请求 tenant_id 生效、是否越权，不锁具体 header 形态（形态属演进期议题）。
- **D3 对 Core 无契约 + RFC 流程登记**：拆分后 kb 不再调用 Core，无调用即无契约；未来若产生新 Core 依赖须走 RFC（提案 → Core 评审 → 发版本 → pin 升级），文档登记以防静默变更。

## 偏差

- **`make test` 未整体跑（等价直跑）**：Windows + PowerShell + Makefile 的 POSIX 环境变量前缀语法（`GOCACHE=... go test`）与沙箱对 `C:\dev\null` 的限制（P1A-P1J 记录一致的已知环境问题），按 Makefile 逐目标等价直跑：kb-service pytest 全量 + validate-architecture 逐 guard + `git diff --check`。
- **`tests/test_grpc_server.py` 中 stale 函数命名未修正**：既有 `test_servicer_declares_10_p0_rpcs` / `_3_p1_rpcs` 函数名与实测 `P0_RPCS`=11 / `P1_RPCS`=8 常量漂移（历史遗留），本批不改动该文件——新契约测试以独立方式覆盖 28 RPC 全量（`test_proto_kb_service_declares_all_rpcs`），避免过度改动无关文件。
- **`pkg/adapters/runtime` 2 个预存在环境失败**：`TestSandboxFileScriptsRejectSymlinks` / `TestSandboxFileScriptsAllowWorkspaceOperations`——Windows 无符号链接权限 + `python3` 不在 PATH，status.md 基线测试记录已登记同款（origin/main 同 FAIL），本批零改动该包，与 issue 无因果。

## 权衡

- **契约测试用生成物 DESCRIPTOR 反射而非解析 .proto**：`kb_pb.DESCRIPTOR` / `rag_pb2.DESCRIPTOR` / `common_pb2.DESCRIPTOR` 是 proto 的运行时权威形态，直接反映实际加载的契约；无需引入 proto 解析依赖（零新增依赖，文件哈希用 hashlib）。
- **双视角语义在 object_store 层断言（而非 servicer 端到端）**：双视角是 `MinioObjectStore` 的构造期行为（注入 internal/public 两客户端），在 store 层断言更精确且无需拉起完整 servicer；servicer 侧单独覆盖上传三步流的 key 规则与「不写 kb_documents」。
- **RPC 面覆盖放在本契约测试文件独立实现**：不修改既有 `test_grpc_server.py` 的 stale 常量/命名，以新文件 `_proto_kb_rpcs()` 直读 DESCRIPTOR 断言 28 全量 + servicer handler 覆盖，职责单一。

## 开放问题

- **阶段 5（#068/#069）拆仓时 pin 形态升级**：当前 pin = 真源 + 镜像 + 契约测试；整目录迁出后是否升级为独立 proto 分发包/子模块，属阶段 5 决策。
- **#061（P3A）**：`MINIO_ACCESS_KEY`/`MINIO_SECRET_KEY`/`MILVUS_TOKEN` Secret 化（proto 无关）。
- **#065（P4B）**：`core_api` 模块与 NotifyDocumentUploaded 物理删除（D3 收口后）。
- **D2 身份机制演进**：租户身份传递的最终形态（header/metadata 规范）暂缓，未来变更须同步修订 `proto-contract.md` §4 与契约测试边界。

## 验证命令（goal 收口实测）

```
python -m pytest tests/test_proto_contract.py -q（kb-service）   → 22 passed
python -m pytest -q（kb-service 全量）                            → 639 passed, 28 skipped（P1J 基线 615 + 22 新增，零回归）
make validate-architecture（沙箱下等价直跑 4 guardrail）          → guard 全部输出通过（component import guard passed / inference legacy control plane retired / kb SQL centralization gate passed / kb pg init+migration role baseline consistent）
git diff --check                                                  → exit 0
```

> 注：`make` 顶层目标在本机 Windows 沙箱的已知环境限制下（P1A-P1J 记录一致）按 Makefile 逐目标等价直跑。
