# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目背景

广州常青云科技有限公司旗下战略级新产品 **KuberCloud ANI**（KuberCloud AI-Native Infrastructure，中文名：AI专有云）的规划文档集，当前产品定义版本 **V7**。

**必读顺序（任何参与本项目的 Claude 实例，在开始任何任务前必须先读）：**

```
ANI-00  产品战略与开发哲学   ← 最高优先级，所有决策的出发点
ANI-01  客户画像与场景分析
ANI-02  产品功能设计
ANI-03  产品路线图
ANI-04  技术栈设计
ANI-05  系统架构设计
ANI-06  开发计划            ← 详细开发点拆解与月度里程碑
ANI-07  部署工程设计        ← Installer、多部署目标、Region/AZ、计量、白牌化
ANI-08  安全架构设计        ← 平台自身安全（第一层）+ 安全服务能力（第二层）
ANI-09  数据模型设计        ← PostgreSQL 全量表结构 / Milvus Collection / Redis Key / MinIO Bucket
ANI-10  GPT审查提示词集     ← 8组结构化审查提示词，用于 GPT 5.5 跨模型设计审查
ANI-11  代码实现规范        ← Repository接口、三条核心流程时序、Bootstrap模式、Go规范、前端路由
ANI-12  版本管理策略        ← SemVer、发布门禁、Git tag、升级包、兼容性边界
```

**代码仓库位置：** `repo/`（相对于本文档目录）

```
repo/
├── services/ani-gateway/      # Go，统一 Web Server（Hertz）
├── ai/rag-engine/             # Python，RAG 引擎
├── operators/inference-operator/ # Go，K8s Operator
├── frontends/{console,boss}/  # TypeScript，React 18 + TDesign
├── cli/ani/                   # Go，ani CLI
├── installer/ani-installer/   # Go，bubbletea TUI 安装程序
├── api/openapi/v1.yaml        # OpenAPI 3.1 Spec（先于代码）
├── deploy/docker/             # docker-compose 本地开发环境
├── Makefile                   # make deps / make dev-gateway / make test
└── .env.example               # 环境变量模板，cp 为 .env
```

## 开发阶段命名强制约定

为避免产品计划阶段与 AI 代码生成批次混淆，所有 Claude/Codex/GPT 实例必须遵守：

1. `ANI-06` 中的 `模块 1/2/3...` 是产品开发计划的唯一模块编号来源。
2. 代码生成批次不得再使用 `Stage 3A/3B/3C` 这类容易误解为模块 3 的名称。
3. 代码生成批次必须使用可回溯命名：`M{模块号}.{小节号}-{主题}-{批次}`，例如 `M2.1-TASK-A`。
4. 当前项目进度是 `ANI-06 / 模块 2 / 2.1 Gateway 骨架 / NATS JetStream 异步任务框架`：
   - `M2.1-TASK-A` 已完成：最小 `task-service` 查询接口。
   - `M2.1-TASK-B` 已完成：transactional outbox + NATS publisher。
   - `M2.1-TASK-C` 下一步：worker mutation RPCs with tenant-safe writes。
5. `Stage 3A/3B/3C` 只允许作为历史旧名出现，并必须注明“不代表模块 3”。
6. 任何进度更新必须同步写入 `repo/development-records/README.md`，并在对应设计文件中标注产品计划映射。

## 版本管理强制约定

所有发布、tag、镜像、Helm Chart、升级包和 Codex Cloud 交付分支必须遵守 `ANI-12-版本管理策略.md`：

1. ANI 采用 SemVer 2.0：`vMAJOR.MINOR.PATCH[-pre.N]`。
2. 首个正式版本为 `v1.0.0`，目标日期 `2026-09-30`，对应 Phase 1 POC 交付就绪。
3. 当前仍处于 `v0.x` 开发期，不得标记为 `v1.0.0` 或 `rc`。
4. 产品阶段、开发模块、代码生成批次与版本号互相独立：`模块 3`、`M2.1-TASK-C` 都不是 SemVer。
5. 任何 API/Proto/DB/CRD/Helm/安全模型的破坏性变更必须按 `ANI-12` 判断 MAJOR/MINOR/PATCH。

**三条核心原则（来自 ANI-00）：**
1. 产品完全从零构建，底层最大化复用成熟开源组件，ANI 的价值在于"编排"与"封装"
2. 全程利用 AI 大模型辅助编码加速开发，架构设计必须对 AI 辅助友好（Spec-First、强类型、单一职责）
3. 这是生产级平台，不是原型或玩具——稳定性、可扩展性、可观测性、安全性有明确的量化标准

---

## Karpathy 四条开发原则

> 来源：Andrej Karpathy 关于 LLM 辅助编程的核心建议，整理自 [forrestchang/andrej-karpathy-skills](https://github.com/forrestchang/andrej-karpathy-skills)

### 原则一：先思考，再编码
**不要假设。不要掩饰困惑。要揭示取舍。**

- 如果需求有歧义，明确说出来并询问，而不是悄悄选一种猜测实现
- 存在多种合理方案时，列出并说明各方案的取舍，由人决策
- 面对复杂问题，先陈述理解再动手
- 遇到不确定的地方，停下来问，而不是带着疑惑继续

### 原则二：用能解决问题的最小代码
**拒绝一切带有猜想的实现。**

- 不实现没被要求的功能，哪怕"感觉以后用得到"
- 不为一次性代码创建抽象层
- 不加"灵活性""可配置性"等未被要求的扩展点
- 200 行能写成 50 行的，重写

### 原则三：只触碰你必须改动的部分
**只清理你自己制造的脏。**

- 不顺手"优化"任务范围之外的代码、注释或格式
- 不重构没坏的东西
- 保持现有代码风格，即使你有不同偏好
- 发现死代码，提出来，不要自作主张删除

### 原则四：定义成功标准，循环迭代直到验证通过
**把任务转化为可验证的目标。**

- 每个任务开始前明确"什么状态算完成"
- 多步骤任务先列出简短计划和验证步骤
- 优先给 Claude 成功标准而非操作指令：不是"修复这个 bug"，而是"写一个能复现 bug 的测试，再修复它"

---
