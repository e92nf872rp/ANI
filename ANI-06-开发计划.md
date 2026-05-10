# KuberCloud ANI · 开发计划

> 版本 V1 | 广州常青云科技有限公司 | 内部产品规划文件

---

## 一、核心约束

| 约束 | 说明 |
|---|---|
| **交付截止** | **2026 年 9 月 30 日**，第一个生产可用版本 |
| **首个正式版本** | **v1.0.0**，版本规则见 `ANI-12-版本管理策略.md` |
| **开发模式** | AI 开发为主，人工为辅——接口设计与架构决策由人主导，代码实现最大化借助 Claude Code / Cursor 等工具生成 |
| **技术路线** | 完全从零构建，最大化复用成熟开源组件，ANI 价值在于"编排"与"封装" |
| **开发语言** | Go（平台层）、Python（AI 应用层）、TypeScript（前端） |

**每个模块的 AI 辅助标准流程：**
1. 人工编写 OpenAPI Spec / Protobuf 定义（明确接口契约）
2. AI 生成 Server Stub、Client、单元测试骨架
3. 人工审查逻辑正确性和安全边界
4. AI 补充错误处理、日志、Metrics 等横切代码
5. 人工做集成测试和边界 case 验证

---

## 二、月度里程碑

```
2026-05  M1  基础设施底座 + ANI Gateway 骨架 + 存储底座 + Harbor 部署
2026-06  M2  认证授权 + 模型仓库（含国密加密 / HuggingFace / ModelScope 导入）
2026-07  M3  推理部署 Operator + 知识库 RAG 引擎
2026-08  M4  Console + BOSS 前端全量 + 镜像仓库管理 + CLI 工具
2026-09  M5  可观测性完整闭环 + 信创适配 + 集成测试 + 离线安装包
─────────────────────────────────────────────────────
2026-09-30   ✅ 第一版本 POC 交付就绪
```

版本里程碑：
- 2026-05 到 2026-08：使用 `v0.x.y` 或 `v1.0.0-alpha/beta.N` 标记内部构建和集成测试版本。
- 2026-09：进入 `v1.0.0-rc.N` 发布候选，只允许修复阻断交付的问题。
- 2026-09-30：目标发布 `v1.0.0`。
- 版本号只表达发布兼容性，不表达 `ANI-06` 的模块编号；模块进度仍以 `模块 1/2/3` 和 `M2.1-TASK-C` 这类代码生成批次记录为准。

---

## 三、Phase 1 详细开发点（2026-05 ~ 09-30）

### 模块 1：基础设施底座（M1）

**目标：** 在 K8s 1.36 上搭建完整 AI 平台底座，让 GPU 资源可被统一调度。

#### 1.1 Kubernetes 集群

- [ ] **K8s 1.36 集群部署规范**
  - 节点规划：Master ×3（HA）、GPU 工作节点、存储节点
  - 安装方式：RKE2 1.36（比 kubeadm 更适合离线环境和安全加固）
  - 容器运行时：containerd 2.1+
  - 离线安装包制作：镜像预拉取 + 离线 Helm Chart 打包
  - **开源组件：** RKE2 1.36、containerd 2.1

- [ ] **KubeOVN 1.13+ 网络部署**
  - 多租户 VPC 规划（每客户独立 VPC，物理隔离）
  - NetworkPolicy 模板（租户隔离 + AI Agent 沙箱出口限制）
  - BGP 配置（与客户现有网络对接）
  - **开源组件：** KubeOVN 1.13+、OVN/OVS

#### 1.2 GPU 算力纳管

- [ ] **NVIDIA GPU Operator**
  - DaemonSet 自动化下发 GPU 驱动和容器工具包
  - 支持：A10、A30、A100、H100 系列
  - **开源组件：** nvidia-gpu-operator latest

- [ ] **HAMi GPU 虚拟化**（核心差异化能力）
  - GPU 切片：MIG 模式（A100）+ vGPU 模式（其他卡型）
  - 多租户 GPU 配额隔离
  - 异构算力：昇腾 910B/C（信创关键，HAMi 唯一同时支持 NVIDIA+昇腾+海光的开源方案）
  - GPU 利用率实时采集（核心卖点，解决客户 GPU 买了不会用的问题）
  - **开源组件：** HAMi 2.4+
  - **自研：** HAMi K8s Operator 配置层（Go）

- [ ] **Volcano AI 批调度**
  - Gang Scheduling（多 Pod 协同任务，训练时必需）
  - 队列管理：推理队列（低延迟优先）/ 训练队列（资源复用）
  - 资源抢占策略
  - **开源组件：** Volcano 1.10+

- [ ] **GPU 资源看板**（第一个对外可见成果）
  - GPU 利用率、显存使用率、任务队列状态
  - 按节点 / 按租户 / 按任务维度聚合
  - **实现：** DCGM Exporter → Prometheus → Grafana Dashboard

#### 1.3 存储底座

- [ ] **MinIO**（模型仓库和数据集的对象存储）
  - 多节点纠删码部署（≥4 节点）
  - 完全离线，不依赖外网
  - Bucket 规划：`ani-models`、`ani-datasets`、`ani-kb-docs`
  - **开源组件：** MinIO RELEASE.2025+

- [ ] **Milvus 向量数据库**
  - Milvus Operator 方式部署（K8s 原生）
  - 生产用 Cluster 模式，测试用 Standalone
  - **开源组件：** Milvus 2.5+

- [ ] **PostgreSQL 17**
  - CloudNativePG Operator 管理（主从 + PgBouncer 连接池）
  - 初始 Schema：租户表、模型元数据表、权限表、审计日志表
  - Row-Level Security（RLS）实现多租户数据隔离
  - **开源组件：** CloudNativePG 1.x、PostgreSQL 17

- [ ] **Harbor 容器镜像仓库**（独立部署，与 ANI 松耦合）
  - Helm Chart 独立部署，不依赖 ANI 其他组件
  - 集成 Trivy 漏洞扫描
  - ANI Gateway 新增 `harbor-proxy` 模块（Go）：转发 Console/BOSS 请求到 Harbor API，附加认证头，屏蔽 Harbor 内部地址
  - **开源组件：** Harbor 2.x

---

### 模块 2：ANI Gateway（统一 Web Server 层）（M1-M2）

**目标：** 所有消费者的唯一入口，从这里衍生出 REST API、SDK、CLI、运维 Skills。

> 这是整个架构最关键的模块，M1 第一周就要搭好骨架，后续所有模块都依赖它。

#### 2.1 Gateway 骨架（Go + Hertz）

- [ ] **项目初始化**
  ```
  ani-gateway/
  ├── cmd/gateway/          # 启动入口
  ├── internal/
  │   ├── handler/          # HTTP Handler
  │   ├── middleware/       # 中间件链
  │   ├── router/           # 路由注册
  │   └── service/          # 业务编排
  ├── pkg/
  │   ├── auth/             # JWT/OAuth
  │   ├── ratelimit/        # 限流
  │   ├── errors/           # 统一错误类型
  │   └── harbor/           # harbor-proxy 模块
  ├── api/openapi/          # OpenAPI 3.1 Spec（代码先于实现）
  └── api/proto/            # Protobuf 定义
  ```
  - **框架：** Hertz 0.9+（CloudWeGo，字节开源，日万亿级请求生产验证）

- [ ] **Middleware 链**（按顺序执行）
  1. TLS 终止 + RequestID 注入（全链路唯一 ID）
  2. JWT 认证（验证 + 解析租户/用户信息）
  3. RBAC 授权（OPA 策略检查）
  4. 令牌桶限流（按租户维度，防止单一客户耗尽 GPU 资源）
  5. 审计日志打点（异步写入，不阻塞主流程）
  6. 路由分发 → 对应 gRPC Service
  7. 统一错误响应：`{ code, message, request_id, details }`

- [ ] **OpenAPI 3.1 Spec-First 工作流**
  - 所有 API 的 Spec 定义先于代码，禁止反向
  - `make gen-api`：oapi-codegen 生成 Go Server/Client → buf 生成 Protobuf 代码 → grpc-gateway 生成 REST 转发层
  - 同一 Spec 同时生成：Go SDK、Python SDK、TypeScript SDK、API 文档站

- [ ] **SSE 流式输出**
  - `/v1/chat/completions` 流式接口（OpenAI 兼容格式）
  - Hertz SSE Handler 封装，客户端断线检测与资源释放

- [ ] **NATS JetStream 异步任务框架**
  - Subject 规划：`ani.tasks.model.*`、`ani.tasks.kb.*`、`ani.tasks.import.*`
  - 提交：`POST /api/v1/tasks` → `202 Accepted + { task_id }`
  - 查询：`GET /api/v1/tasks/{id}` → `{ status, progress, result }`
  - Webhook 回调：任务完成后主动推送到客户配置的 URL
  - **开源组件：** NATS JetStream 2.10+
  - **当前实现对齐（2026-05-11）：**
    - 当前仍属于 `模块 2 -> 2.1 Gateway 骨架 -> NATS JetStream 异步任务框架`，尚未进入 `模块 3：模型管理平台`。
    - 代码生成批次命名统一为 `M2.1-TASK-A/B/C`；历史记录里的 `Stage 3A/3B/3C` 只是旧的内部切片名，不代表 `ANI-06` 的模块 3。
    - `M2.1-TASK-A`：已实现最小 `task-service` 查询接口，支持 `GetTask`，读取前设置租户上下文以配合 PostgreSQL RLS。
    - `M2.1-TASK-B`：已实现 `transactional outbox` 仓储与 NATS outbox publisher，使用 `FOR UPDATE SKIP LOCKED` 拉取未发布事件。
    - `M2.1-TASK-C`：待实现 worker mutation RPC，包括任务领取、租约心跳、进度更新、失败、完成；实施前必须先修正 `task_service.proto` 的租户/安全上下文。
    - 进度记录文件：
      - `repo/development-records/m2-1-task-a-b-task-service-outbox.md`
      - `repo/development-records/2026-05-11-handoff-codex-cloud.md`
    - 当前验证状态：`make gen-proto`、`make test`、`make build` 已通过。

#### 2.2 认证授权（Go）（M2）

- [ ] **Dex（OIDC IdP）**
  - 对接企业 AD/LDAP（客户现有用户体系，无需重建账号）
  - SAML 2.0 支持（金融/国央企常用）
  - **开源组件：** Dex latest

- [ ] **JWT 服务**
  - AccessToken（1 小时过期）+ RefreshToken（7 天）
  - Token 吊销：黑名单机制，Redis 存储
  - API Key 管理：长期 Token，供 CLI / SDK / 自动化脚本使用

- [ ] **RBAC 服务**
  - 角色：`platform-admin` / `tenant-admin` / `user` / `auditor`
  - 权限粒度：API 路径 + HTTP Method
  - 与 Dex 集成：从 OIDC Token 的 `groups` 字段提取角色

---

### 模块 3：模型管理平台（Go + Python）（M2）

**目标：** IT 管理员无需懂 AI，把模型文件变成一个可调用的内网 API。

#### 3.1 私有模型仓库（Go）

- [ ] **模型元数据服务**
  - 数据表：`models (id, name, version, format, size_bytes, status, is_encrypted, encrypt_algo, encrypt_hint, meta_json)`
  - API：`GET/POST /api/v1/models`、`GET /api/v1/models/{id}`、`DELETE /api/v1/models/{id}/versions/{ver}`
  - 版本管理：同一模型多版本并存，支持 tag（latest / stable）
  - 能力标签：文本生成、嵌入、语音识别、视觉理解等

- [ ] **模型文件上传**
  - 分片上传 + 断点续传（支持 >100GB 大文件）
  - 上传写入 MinIO `ani-models` Bucket
  - 格式支持：HuggingFace safetensors、GGUF
  - 完整性校验：SHA256 checksum 验证后才更新状态为 `ready`

- [ ] **内置模型预配置模板**
  - Qwen2.5-7B / 14B / 72B（通义千问）
  - DeepSeek-V3 / R1-7B / 32B（幻方）
  - GLM-4-9B（智谱 AI）
  - BGE-M3（BAAI 开源，知识库向量化必需）
  - Faster-Whisper（语音转写）
  - 每个模型预置推荐 GPU 型号、显存要求、并发建议值

#### 3.2 模型加解密（Go，国密优先）

> 企业自训练/微调的模型是核心资产。平台提供存储加密保护，密钥由用户完全持有，平台不保存。

- [ ] **加密算法支持层**
  - **默认算法：SM4-GCM**（国密分组密码，128-bit 密钥，认证加密防篡改）
  - **扩展支持：** ZUC（祖冲之序列密码，3GPP 国密标准）、SM1（硬件实现为主）
  - **国际兼容：** AES-256-GCM（备选，非国密场景）
  - 密钥派生：PBKDF2 + SM3（用户输入密码 → 派生加密密钥，杜绝明文密码直接使用）
  - **开源组件：** `github.com/tjfoc/gmsm`（Go 国密库，SM1/SM2/SM3/SM4 完整实现）

- [ ] **加密文件格式（`.anip` — ANI Protected）**
  ```
  [文件头 64 bytes]
    magic:      "ANIP" (4 bytes)
    version:    uint8
    algo:       uint8  (0x01=SM4, 0x02=ZUC, 0x03=AES256)
    salt:       32 bytes (PBKDF2 盐值)
    digest:     SM3 摘要 (32 bytes，用于完整性校验)
  [加密数据流，分块处理]
  ```

- [ ] **模型加密 CLI 工具**
  ```bash
  ani model encrypt ./qwen2.5-72b/ --algo sm4 --out qwen2.5-72b.anip
  ani model decrypt qwen2.5-72b.anip --out ./qwen2.5-72b-decrypted/
  ```
  - 流式分块加解密（512MB/chunk），不全量读入内存，支持超大模型文件
  - 加密过程显示进度条和预计剩余时间

- [ ] **推理时运行时解密**
  - `InferenceService` CRD 新增 `encryptionKeyRef`（引用 K8s Secret 存储的密钥）
  - 推理 Pod 启动流程：
    ```
    Init Container（Go 实现）:
      1. 从 K8s Secret 读取密钥
      2. 从 MinIO 下载 .anip 文件
      3. 流式解密到 emptyDir（tmpfs 内存盘）
    主容器（vLLM）:
      4. 从 emptyDir 加载明文模型
    Pod 销毁时:
      5. emptyDir 随 Pod 消失，明文和密钥均不落盘
    ```
  - 密钥传递：用户通过 Console/API 提交密钥 → 转存为 K8s Secret → Init Container 通过环境变量读取

- [ ] **微调模型加密发布**
  - 微调完成后可选"加密后发布到仓库"
  - 工作流：微调完成 → 加密 API → 加密文件写入 MinIO → 元数据标记 `is_encrypted=true`

#### 3.3 远程模型导入（Go + Python）

> 模型不预先打包进镜像，Pod 启动时从模型仓库动态拉取，实现镜像与模型彻底解耦。

- [ ] **HuggingFace 导入**
  - `POST /api/v1/models/import` `{ source: "huggingface", repo_id: "Qwen/Qwen2.5-72B-Instruct" }`
  - 异步执行，返回 `task_id`，客户端轮询或 Webhook 通知进度
  - Python 下载服务：`huggingface_hub` 库，支持 `HF_ENDPOINT` 配置（指向国内镜像站）
  - 断点续传：记录已下载 shard，中断后从断点继续，不重下
  - 下载专属 Pod 开放外网出口（KubeOVN NetworkPolicy），其他 Pod 保持内网隔离
  - **开源组件：** huggingface_hub latest

- [ ] **ModelScope 导入**
  - `POST /api/v1/models/import` `{ source: "modelscope", model_id: "qwen/Qwen2.5-72B-Instruct" }`
  - 使用 `modelscope` Python SDK
  - 共用 HuggingFace 的任务调度框架，逻辑一致
  - **开源组件：** modelscope latest

- [ ] **推理 Pod 模型动态加载**（Init Container 模式）
  ```
  vLLM 推理 Pod 启动时:
    Init Container（Go 单一二进制）:
      1. 检查节点 PVC 缓存是否已有该模型版本
      2. 如无缓存：调用模型仓库 API → 获取 MinIO presigned URL → 下载
      3. 如模型加密：执行解密（SM4/ZUC）
      4. 将模型文件 ready 信号写入共享 emptyDir
    主容器（vLLM）:
      5. 从 emptyDir / PVC 缓存路径加载模型启动
  ```
  - 节点 PVC 缓存：避免同一节点多次下载同一模型版本
  - 好处：vLLM 镜像仅含推理运行时，无模型文件，镜像体积小，版本切换无需重新构建镜像

#### 3.4 一键推理部署（Go Operator + Python）（M3）

- [ ] **InferenceService K8s Operator（Go）**
  ```yaml
  apiVersion: ani.kubercloud.io/v1
  kind: InferenceService
  metadata:
    name: qwen2.5-72b-prod
  spec:
    model: qwen2.5-72b:v2          # 模型仓库 ID
    replicas: 2                     # 副本数
    gpuType: A100                   # GPU 型号
    gpuCount: 4                     # 每副本 GPU 数量
    maxConcurrency: 8               # 最大并发请求数
    encryptionKeyRef:               # 仅加密模型需要
      secretName: model-key-qwen
      key: password
  ```
  - Controller 监听 CR，自动创建 vLLM Deployment + K8s Service + 自动注入 Init Container
  - 状态机：`Pending` → `Downloading` → `Decrypting` → `Deploying` → `Running` / `Failed`

- [ ] **vLLM 推理服务封装（Python）**
  - 启动参数模板（按 GPU 型号和模型大小自动推荐 `--tensor-parallel-size`、`--gpu-memory-utilization`）
  - 暴露标准 OpenAI 兼容接口：`/v1/chat/completions`、`/v1/embeddings`
  - **开源组件：** vLLM 0.6+

- [ ] **推理服务路由（Go，ANI Gateway 层）**
  - 路由规则：`/v1/chat/completions` + `X-Model-Name: qwen2.5-72b` → 转发至对应 vLLM Service
  - 超并发排队：超出 `maxConcurrency` 时排队等候（而非直接返回 429）
  - 负载均衡：多副本轮询
  - 调用审计：记录 request_id / 用户 / 模型 / prompt_tokens / completion_tokens / 延迟

---

### 模块 4：企业知识库问答（Go + Python）（M3）

**目标：** Phase 1 核心交付物，业务用户最直接感知的 AI 能力，决定客户续费。

#### 4.1 文档管理（Go）

- [ ] **文档上传 API**
  - 格式：PDF、Word(.docx)、Excel(.xlsx)、PPT(.pptx)、TXT、Markdown
  - 文件存入 MinIO `ani-kb-docs`，上传完成后发 NATS 消息触发解析任务

- [ ] **文档解析服务（Python）**
  - **开源组件：** Docling（IBM 开源，PDF 版面分析 + 表格识别 + OCR 最完整）
  - OCR：PaddleOCR（中文准确率高于 Tesseract）
  - 输出：结构化 Markdown，保留标题层级和表格
  - 扫描件 PDF 走 OCR 路径，数字 PDF 直接提取不走 OCR

#### 4.2 RAG 引擎（Python）

- [ ] **向量化服务**
  - 嵌入模型：BGE-M3（BAAI，中英文双语效果最佳，免费开源）
  - 切片策略：语义边界切分（chunk ≈ 512 token，不硬截断段落）
  - 写入 Milvus（Collection 按知识库 ID 隔离，租户间不互通）
  - **开源组件：** sentence-transformers、Milvus 2.5+

- [ ] **混合检索**
  - 语义检索：Milvus ANN 向量搜索（召回语义相关段落）
  - 关键词检索：PostgreSQL pg_trgm 全文搜索（召回精确关键词）
  - 融合重排：RRF（Reciprocal Rank Fusion）算法，两路召回合并去重排序
  - Top-K：默认召回 5 段，可按知识库配置覆盖

- [ ] **问答生成**
  - Prompt 模板：系统提示词 + 检索上下文 + 用户问题
  - 来源引用：每段答案附来源文档名 + 页码（从 Milvus metadata 提取）
  - 置信度过滤：相似度低于阈值时返回"未找到相关内容"，不编造答案
  - 多轮对话：保留最近 10 轮历史，支持追问

- [ ] **知识库管理 API（Go）**
  - `POST /api/v1/knowledge-bases` — 创建知识库
  - `POST /api/v1/knowledge-bases/{id}/documents` — 上传文档
  - `GET /api/v1/knowledge-bases/{id}/documents` — 文档列表及解析状态
  - `DELETE /api/v1/knowledge-bases/{id}/documents/{doc_id}` — 删除文档
  - `POST /api/v1/knowledge-bases/{id}/query` — 执行问答
  - 权限隔离：知识库归属租户，跨租户无法访问

---

### 模块 5：前端 Console（TypeScript + React）（M4）

**目标：** IT 管理员和业务部门用户的操作界面，30 分钟能学会用。

#### 5.1 工程搭建

- [ ] **Monorepo 初始化**（Console + BOSS 共一个仓库）
  - pnpm workspace + Turborepo 构建缓存
  - Vite 5 + React 18 + TypeScript 5
  - TDesign React 1.x（腾讯开源企业组件库，中文友好，有 Mobile 版）
  - TanStack Router（类型安全路由 + 代码分割）
  - TanStack Query（服务端数据缓存与同步）
  - Zustand（轻量客户端 UI 状态）
  - 从 OpenAPI Spec 自动生成 TypeScript SDK（openapi-typescript-codegen）

- [ ] **OIDC 鉴权流程**
  - 跳转 Dex → 回调处理 Token → AccessToken 无感刷新
  - 多租户切换（一个账号可属于多个租户）

#### 5.2 Console 主要页面

- [ ] **仪表盘（首页）**
  - GPU 资源卡片：总量 / 已用 / 空闲
  - 推理服务列表：运行中 / 部署中 / 异常（含快捷操作）
  - 知识库调用量 7 日趋势图

- [ ] **模型管理页**
  - 模型列表（名称、版本、状态、是否加密、GPU 占用）
  - 模型来源：本地上传（分片进度条）/ HuggingFace 导入 / ModelScope 导入
  - 一键部署弹窗（选 GPU 数量、并发数、是否需要输入解密密码）
  - 推理服务日志实时查看（SSE 流式）

- [ ] **知识库管理页**
  - 知识库列表 + 新建
  - 文档管理（上传、解析进度、删除）
  - 知识库问答测试界面（对话框，带来源引用高亮）

- [ ] **容器镜像仓库页**（封装 Harbor API，via harbor-proxy）
  - 项目（Project）列表与创建
  - 镜像仓库（Repository）列表、搜索
  - 镜像 Tag 列表、漏洞扫描结果查看（Trivy）
  - 拉取命令一键复制
  - 镜像删除（二次确认）
  - **不做：** Harbor 用户管理、LDAP 配置等运维操作（保留在 Harbor 原生 UI）

- [ ] **用量报表页**
  - 按时间段查询调用量
  - 按模型 / 知识库 / 用户维度统计
  - Token 消耗量 + GPU 计算时长

---

### 模块 6：前端 BOSS（TypeScript + React）（M4）

**目标：** 常青云内部运营和运维团队的后台，与 Console 同步全量开发。

与 Console 共享 Monorepo 脚手架、TDesign 组件库、API SDK。

- [ ] **多租户管理**
  - 租户列表（创建、查看、禁用、配额修改）
  - 租户管理员账号初始化 + 重置密码
  - 租户资源使用概览

- [ ] **资源配额管理**
  - 按租户分配 GPU 配额（最大并发数、最大 GPU 数量）
  - 配额使用率趋势图

- [ ] **计费与账单**
  - GPU 计算时长统计（按租户 / 按模型）
  - Token 消耗量统计
  - 账单报表 CSV 导出

- [ ] **平台健康大盘**
  - 嵌入 Grafana Dashboard（Grafana Embedding API）
  - 系统告警列表（来自 AlertManager，P0/P1 分级显示）
  - 节点状态列表（GPU 节点在线 / 离线 / 异常）

- [ ] **运维操作面板**（运维 Skills 触发界面）
  - 手动触发运维 Skills（模型回滚、知识库重新索引、推理扩容等）
  - Skills 执行历史 + 日志查看

- [ ] **镜像仓库运维管理**（BOSS 专属，封装 Harbor API）
  - Harbor 项目配额管理（按租户分配存储配额）
  - 全局漏洞扫描报告汇总
  - 垃圾回收任务触发 + 状态查看
  - Harbor 系统配置查看（只读）

- [ ] **工单与客户列表**
  - 客户基本信息管理
  - 简单工单记录（问题描述 + 处理状态）

---

### 模块 7：CLI 工具 `ani`（Go）（M4）

- [ ] **cobra + viper 骨架**
  - 全局配置：`~/.ani/config.yaml`
  - `ani config set endpoint https://ani.company.internal`
  - `ani config set api-key <key>`

- [ ] **第一版子命令集**
  ```bash
  # 模型管理
  ani model list
  ani model upload ./qwen2.5-72b/ --name qwen2.5-72b --version v1
  ani model encrypt ./qwen2.5-72b/ --algo sm4 --out qwen2.5-72b.anip
  ani model import --source huggingface --repo Qwen/Qwen2.5-72B-Instruct
  ani model import --source modelscope --model qwen/Qwen2.5-72B-Instruct
  ani model deploy qwen2.5-72b --gpu-count 4 --replicas 2
  ani model status qwen2.5-72b
  ani model logs qwen2.5-72b --follow

  # 知识库
  ani kb create --name "人事制度"
  ani kb upload <kb-id> ./docs/
  ani kb query <kb-id> "差旅报销标准是什么？"

  # 集群
  ani cluster gpu-status
  ani cluster nodes
  ```
  - CLI 完全复用 Go SDK，不重复实现 HTTP 调用逻辑

---

### 模块 8：可观测性（贯穿 M1-M5）

- [ ] **指标采集（Prometheus）**
  - DCGM Exporter：GPU 利用率、显存、温度、功耗
  - vLLM 内置 Prometheus 端点：QPS、TTFT、Token 速率
  - ANI Gateway 自定义 Metrics：请求量、P50/P99 延迟、错误率、每个租户调用量

- [ ] **Grafana 仪表板**（预置 3 套模板）
  - GPU 集群大盘
  - 推理服务大盘
  - 知识库服务大盘

- [ ] **分布式追踪（OpenTelemetry + Jaeger）**
  - ANI Gateway 自动注入 TraceID（与 RequestID 关联）
  - 所有 Go 微服务传递 Trace Context
  - 一个 request_id 可查到完整调用链（Gateway → Service → vLLM）

- [ ] **日志（Loki + Promtail）**
  - 结构化 JSON 日志
  - 按 tenant_id 过滤
  - 审计日志单独 Collection，追加写入，不可篡改

- [ ] **告警规则（AlertManager）**
  - GPU 温度 > 85°C → P1
  - 推理服务错误率 > 5% → P0（立即响应）
  - 磁盘剩余 < 20% → P1
  - API P99 延迟 > 2s → P1
  - 推理 TTFT > 10s → P1

---

## 四、Phase 2 开发点预览（2026-10 起）

### 文档智能处理
- [ ] 合同要素结构化提取（LLM + JSON Schema 输出）
- [ ] 批量文档处理（100 份并行，NATS 任务队列）
- [ ] 公文智能起草（公文格式模板 + LLM 生成）
- [ ] 文档摘要（可配置摘要长度）

### 会议智能
- [ ] Faster-Whisper 语音转写（Python）
- [ ] 发言人区分（Speaker Diarization，pyannote.audio）
- [ ] 会议纪要结构化生成（LLM）
- [ ] 企微 / 钉钉 Bot 集成（Webhook）

### 模型微调平台（轻量版）
- [ ] 数据标注界面（Q&A 对人工标注，前端）
- [ ] LLaMA-Factory 封装（LoRA 微调，Python）
- [ ] 微调任务管理（进度、日志、Eval 对比）
- [ ] 微调模型一键加密后发布为推理服务

### 等保合规强化
- [ ] 等保 2.0 三级合规架构完整文档（必需交付物）
- [ ] 数据脱敏中间件（NER 识别证件号、手机号，推理前自动屏蔽）
- [ ] Vault 集成（敏感配置统一管理）

---

## 五、开发依赖关键路径

```
M1（5月）
├── K8s 集群 + KubeOVN ──────────────────────────────────→ 所有 Pod 依赖此
├── MinIO + PostgreSQL + Milvus ─────────────────────────→ 模型仓库 / RAG 依赖此
├── Harbor 独立部署 ──────────────────────────────────────→ 镜像仓库页面依赖此
└── ANI Gateway 骨架 + Middleware 链 ────────────────────→ 所有 API 依赖此 ⭐

M2（6月）
├── Dex + JWT + RBAC ───────────────────────────────────→ 所有接口鉴权依赖此
├── 模型仓库 API（上传 + 元数据）──────────────────────→ 推理部署依赖此
├── 模型加解密（gmsm + .anip 格式 + CLI）────────────→ 加密推理依赖此
└── HuggingFace / ModelScope 导入 + Init Container ──→ 动态加载依赖此

M3（7月）
├── InferenceService Operator ──────────────────────────→ 模型部署起点 ⭐
├── vLLM 推理服务封装 ──────────────────────────────────→ 推理 API 依赖此
├── RAG 引擎（文档解析 + 向量化 + 混合检索 + 问答）→ 知识库问答 ⭐
└── 知识库管理 API ─────────────────────────────────────→ 前端依赖此

M4（8月）
├── Console 前端（Monorepo）────────────────────────────→ 依赖 M1-M3 全部 API
├── BOSS 前端（同上）──────────────────────────────────→ 依赖 M1-M3 全部 API
└── ani CLI（复用 Go SDK）──────────────────────────────→ SDK 依赖 Gateway Spec

M5（9月）
├── 可观测性完整闭环 ───────────────────────────────────→ 依赖各服务暴露 Metrics
├── 信创适配（UOS + ARM64 构建）────────────────────────→ 依赖 M1-M4 全部完成
└── 集成测试 + 性能基线 + 离线安装包 ──────────────────→ 最终交付验证
```

---

## 六、AI 辅助的关键加速点

| 模块 | 人工负责 | AI 生成 |
|---|---|---|
| ANI Gateway | OpenAPI Spec 定义、安全边界审查 | Handler 骨架、Middleware 实现、错误处理 |
| 模型加密 | 算法选型、密钥安全设计 | SM4-GCM 流式加解密完整实现（基于 gmsm） |
| RAG 引擎 | Prompt 模板调优、检索策略 | LangChain Pipeline 代码、向量化服务 |
| K8s Operator | CRD 设计、状态机 | controller-runtime Controller 实现 |
| 所有 CRUD API | Spec 定义、权限设计 | Server Stub、Client SDK、单元测试 |
| 前端页面 | 交互逻辑、信息架构 | TDesign 组件拼装、TanStack Query hooks |
| CLI 工具 | 命令设计、用户体验 | cobra 子命令实现、帮助文档 |

---

## 七、开源组件选型清单

所有组件均满足：① 生产级成熟度 ② 符合 Go/Python/TS 技术栈 ③ 支持完全离线部署 ④ 有信创替代路径

| 层级 | 组件 | 版本 | 选型理由 |
|---|---|---|---|
| 编排 | Kubernetes（RKE2） | 1.36 | 行业标准，RKE2 适合离线和安全加固 |
| 网络 | KubeOVN | 1.13+ | Go 实现，国内主导，原生 VPC 多租户 |
| 容器运行时 | containerd | 2.1+ | K8s 推荐标准运行时 |
| GPU | HAMi | 2.4+ | 唯一同时支持 NVIDIA+昇腾+海光 的开源方案 |
| GPU 调度 | Volcano | 1.10+ | K8s 原生 AI 批调度事实标准 |
| LLM 推理 | vLLM | 0.6+ | 最高吞吐量，OpenAI 兼容，社区最活跃 |
| 语音 | Faster-Whisper | latest | Whisper 最快推理实现 |
| 向量库 | Milvus | 2.5+ | 国内团队，K8s 原生，亿级向量 |
| 对象存储 | MinIO | 2025+ | S3 兼容，离线可用，信创可替换 |
| 关系数据库 | PostgreSQL 17 | 17 | 信创兼容（金仓 KingbaseES 兼容 PG 协议） |
| Web 框架 | Hertz | 0.9+ | 字节开源，高性能，gRPC 原生，生产验证 |
| 消息队列 | NATS JetStream | 2.10+ | 轻量，Go 原生，比 Kafka 运维简单 10 倍 |
| 认证 | Dex | latest | OIDC 标准，LDAP/SAML 双协议 |
| 监控 | Prometheus + Grafana | latest | K8s 原生监控行业标准 |
| 追踪 | OpenTelemetry + Jaeger | latest | 标准化 Trace，Go SDK 完善 |
| 日志 | Loki + Promtail | latest | 轻量，K8s 原生，比 ELK 省 60% 资源 |
| 安全 | OPA + Falco | latest | K8s 准入控制 + 运行时安全双保险 |
| TLS | cert-manager | latest | K8s 证书自动化标准 |
| 文档解析 | Docling | latest | IBM 开源，PDF/表格/OCR 最完整 |
| OCR | PaddleOCR | latest | 中文识别准确率最高 |
| RAG 框架 | LangChain | 0.3+ | Python RAG 生态最成熟 |
| 微调 | LLaMA-Factory | latest | 国产模型全覆盖，LoRA 标准实现 |
| 国密加密 | gmsm | latest | Go 国密唯一成熟实现（SM1/SM2/SM3/SM4） |
| 镜像仓库 | Harbor | 2.x | 企业级标准，独立部署，ANI 只做 API 封装 |
| HF 下载 | huggingface_hub | latest | 官方 Python SDK，支持断点续传 |
| MS 下载 | modelscope | latest | 魔搭官方 SDK，国内模型首选 |
| CLI | cobra + viper | latest | Go CLI 事实标准（kubectl 同款） |
| 前端框架 | React 18 + TDesign | 18 / 1.x | 企业组件库，中文友好，有 Mobile 版 |
| 构建工具 | Vite 5 | 5+ | 最快前端构建，HMR 秒级 |

---

## 八、Phase 1 非功能验收标准

| 指标 | 要求 | 验证方式 |
|---|---|---|
| API P99 延迟 | < 200ms（不含推理） | k6 压测 |
| 知识库问答端到端 | < 3s | 自动化测试 |
| 推理首 Token（TTFT） | < 2s（7B 模型，A100） | vLLM Benchmark |
| 故障自愈 | Pod 崩溃后 < 60s 恢复 | 手动 kill Pod 验证 |
| 通信安全 | 所有外部 API 强制 TLS 1.3 | SSL Labs 扫描 |
| 审计覆盖 | 100%（每次推理调用可追溯） | 随机抽样查审计日志 |
| 断网运行 | 完全断外网后所有功能正常 | 断网测试用例 |
| 首次部署 | 离线安装包 < 2 小时完成 | 全新环境演练 |
| 信创适配 | 统信 UOS 20 + ARM64 构建通过 | CI 多架构构建 |
| 多租户隔离 | 租户 A 无法访问租户 B 数据 | 渗透测试用例 |
