# KuberCloud ANI — Monorepo

广州常青云科技有限公司 | AI 专有云平台

## 当前状态

```text
当前阶段：以 ../ANI-DOCS-INDEX.md、../ANI-06-开发计划.md Section 零和 CURRENT-SPRINT.md 为准
当前执行：Phase 1 / Sprint 13 / Core real provider 与 live gate 收敛启动；Sprint 12 Core「Services 支撑 Handler」已完成 Tier1 local profile 收口
当前执行入口：CURRENT-SPRINT.md
全局计划入口：../ANI-06-开发计划.md
文档导航：../ANI-DOCS-INDEX.md
当前边界：Sprint 13 必须沿用 Sprint 12 已闭合的 Core handler/ports/adapters/router 边界接入真实 provider 与 live gate；未跑通对应 live gate 前不得标 runtime/production ready
历史真实环境：真实服务器只读验证已完成；Rook-Ceph 正式部署已完成；`ani-rbd-ssd` StorageClass 已上线；KubeVirt VM RBD storage smoke 和逐节点 reboot resilience 已通过；未执行手工挂载、fstab 修改、系统盘变更、默认 StorageClass 切换或已有 PVC 迁移
```

## 代码仓库结构

```
repo/
├── services/                   # Go 服务（Core + Services 暂存于同一 monorepo）
│   ├── ani-gateway/            # 统一 HTTP 入口（Core API / Services API 路由）
│   ├── auth-service/           # Core 认证授权服务（JWT/RBAC/OIDC/API Key）
│   ├── task-service/           # Core 异步任务/outbox/worker mutation
│   ├── model-service/          # ANI Services 早期逻辑，不属于 Core；6.15-6.20 后按新定义删除或覆盖
│   ├── kb-service/             # ANI Services 空骨架，不属于 Core；6.15-6.20 后按新定义建设
│   └── metering-service/       # 平台计量服务骨架
│
├── ai/                         # Services 早期原型/未来方向；不是最终边界定义
│   ├── rag-engine/             # RAG 引擎（LangChain + Milvus）
│   ├── doc-parser/             # 文档解析服务（Docling + PaddleOCR）
│   └── whisper-service/        # 语音转写服务（Faster-Whisper）
│
├── operators/                  # Go K8s Operator
│   ├── inference-operator/     # InferenceService CRD Controller
│   └── upgrade-operator/       # ANIPatch CRD Controller（在线升级）
│
├── frontends/                  # TypeScript 前端（需拆 Core client 与 Services client）
│   ├── console/                # 用户控制台（React 18 + TDesign）
│   └── boss/                   # 运营运维后台（React 18 + TDesign）
│
├── cli/ani/                    # Go CLI 工具（cobra + viper）
├── installer/ani-installer/    # Go 安装程序（bubbletea TUI）
│
├── api/
│   ├── openapi/                # API 契约（v1.yaml 为 Core；services/v1.yaml 为 Services）
│   └── proto/                  # Protobuf 定义（内部 gRPC）
│
├── pkg/
│   ├── ports/                  # ANI 产品能力抽象，不是 TCP/IP 端口
│   ├── adapters/               # 默认组件适配器与 local profile
│   └── bootstrap/              # 能力装配与服务启动配置
│
├── sdks/
│   ├── core/                   # Core 四语言 SDK
│   └── services/               # Services 四语言 SDK
│
├── deploy/
│   ├── helm/                   # ANI 平台 Helm Charts
│   └── docker/                 # docker-compose 本地开发环境
│
├── scripts/                    # 构建、发布、维护脚本
├── Makefile                    # 统一构建入口
├── .github/workflows/          # CI/CD 流水线
└── .env.example                # 环境变量模板
```

## 快速开始

```bash
# 进入仓库后先做基础验证
make test
make validate-architecture
make validate-doc-entrypoints

# 当前冲刺任务和更细的启动方式见 CURRENT-SPRINT.md
```

详见：[本地开发环境搭建指南](deploy/docker/README.md)

## 文档

产品规划文档位于 `../`（父目录）。新开发者或 AI 工具不要从头顺读所有文档，推荐顺序：

1. `../ANI-DOCS-INDEX.md`
2. `../CLAUDE.md`
3. `CURRENT-SPRINT.md`
4. `../ANI-06-开发计划.md` Section 零和当前 Sprint
5. `../ANI-05-系统架构设计.md`
6. `api/openapi/v1.yaml` 和 `api/openapi/services/v1.yaml`

版本管理：
- 策略文档：`../ANI-12-版本管理策略.md`
- 当前阶段：`v0.x` 开发期
- 首个正式版本目标：`v1.0.0`，2026-09-30
- 提交前至少执行：`make test && make validate-architecture && git diff --check`

<!-- 历史回归门禁校验器兼容标记（请勿删除；对应 dev-records 历史批次与 make validate-* 门禁） -->
**历史回归门禁 token（校验器兼容，勿删）：** Sprint 11 / Core Real Deployment Validation 正式部署完成；真实服务器只读验证已完成；Rook-Ceph 正式部署已完成；Sprint 11 执行环境：正式部署执行环境。
