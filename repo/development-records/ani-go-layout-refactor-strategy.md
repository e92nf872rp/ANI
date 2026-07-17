# ANI Go Layout 与目录结构优化策略记录

> 日期：2026-07-08
> 状态：决策记录，不代表当前立即执行目录迁移
> 范围：解释 ANI 当前仓库结构偏离典型 Go 项目习惯的原因，并记录后续目录优化时机与原则

## 背景

ANI 最终交付形态是一个完整产品和代码仓库，不是天然拆分为两个独立产品仓库。当前 ANI Core 和 ANI Services 由同一整体研发组织推进，只是为了并行加速开发，在执行层面拆成 Core 与 Services 两个开发团队。

早期仓库还同时承载了 AI Coding 设计文档、产品规划、Services 原型、前端、SDK、部署、脚本和 Go 服务代码。因此 `repo/` 逐步形成了一个 product monorepo / workspace，而不是典型的单一 Go module 项目。

这解释了为什么当前仓库在 Go 工程师视角下会显得“不够标准”：它不是因为 Go 代码不能运行，而是因为仓库同时承载了产品、文档、多语言代码、Core/Services 协作和历史演进痕迹。

## 典型 Go 项目的三层

典型 Go 项目通常可以理解为三层：

```text
module  = 一个 Go 依赖与版本管理单元，由 go.mod 定义
package = Go 代码的编译与导入单元，通常一个目录就是一个 package
cmd     = Go 社区约定，用来放可执行程序入口，也就是 package main
```

常见结构示例：

```text
repo/
├── go.mod
├── go.sum
├── cmd/
│   ├── ani-gateway/
│   │   └── main.go
│   ├── auth-service/
│   │   └── main.go
│   └── ani/
│       └── main.go
├── internal/
│   ├── bootstrap/
│   ├── gateway/
│   └── adapters/
├── pkg/
│   └── ports/
├── api/
├── deploy/
└── scripts/
```

在这种结构中：

- `go.mod` 位于仓库根目录，定义主 module，例如 `github.com/kubercloud/ani`。
- `cmd/*` 下的每个目录通常都是一个可执行程序入口，文件里使用 `package main`。
- `internal/*` 放仓库内部实现，Go 工具会限制外部仓库 import。
- `pkg/*` 放确实需要跨模块或对外复用的库代码。
- 工程师通常可以在仓库根目录执行 `go test ./...`、`go build ./cmd/ani-gateway`。

## ANI 当前映射

ANI 当前不是单 module Go 项目，而是多个 Go module 通过 `repo/go.work` 聚合：

```text
repo/
├── go.work
├── pkg/go.mod
├── services/ani-gateway/go.mod
├── services/auth-service/go.mod
├── services/task-service/go.mod
├── services/reconcile-worker/go.mod
├── services/model-service/go.mod
└── cli/ani/go.mod
```

当前大致映射关系如下：

| Go 概念 | 典型使用 | ANI 当前对应 |
|---|---|---|
| module | 仓库根 `go.mod` 定义 `github.com/kubercloud/ani` | 多个子 module，由 `repo/go.work` 聚合 |
| package | 一个目录一组 `.go` 文件，可被 import | `pkg/ports`、`pkg/adapters/runtime`、`services/ani-gateway/internal/router` |
| cmd | 放 `package main`，编译成二进制 | 当前分散在 `services/*/main.go` 和 `cli/ani/main.go` |
| internal | 限制外部 import 的内部包 | `services/ani-gateway/internal/router`、`services/ani-gateway/internal/middleware` |
| pkg | 通常放对外可复用库 | ANI 当前把 `pkg` 自身做成独立 module |

## 当前结构偏离典型 Go 习惯的主要点

### 1. `repo/` 不是 Go module 根目录

当前 `repo/` 是 Go workspace 根目录，但不是 Go module 根目录。真正的 Go module 分散在 `repo/pkg`、`repo/services/*`、`repo/cli/ani` 等子目录。

这使得 Go 工程师常用的根目录命令体验不直接，例如：

```bash
go test ./...
go list ./...
```

在典型 Go 仓库中，这类命令通常可从仓库根目录直接运行；但在 ANI 中需要依赖 `Makefile` 中维护的包列表和缓存配置。

这不是编译错误，但会降低 Go 工程直觉。

### 2. `pkg` 被做成独立 module

当前 `repo/pkg/go.mod` 的 module 是：

```go
module github.com/kubercloud/ani/pkg
```

因此共享包 import path 形如：

```go
github.com/kubercloud/ani/pkg/ports
github.com/kubercloud/ani/pkg/adapters/runtime
github.com/kubercloud/ani/pkg/bootstrap
```

这可以工作，但在 Go 社区习惯里，`pkg` 通常是主 module 下的一个目录，而不是 module 名本身。

更常见的形态是：

```go
module github.com/kubercloud/ani
```

然后目录仍然可以是：

```text
pkg/ports
pkg/adapters/runtime
internal/bootstrap
```

当前做法的问题在于，`pkg` 既像共享库目录，又像独立发布单元。随着代码增长，它容易承载过多职责，例如 ports、adapters、bootstrap、types、generated pb 都被放到同一个共享 module 视野中。

### 5. 可执行入口没有使用典型 `cmd/` 约定

Go 中 `cmd/` 不是强制规则，但它是常见约定。典型结构中：

```text
cmd/ani-gateway/main.go
cmd/auth-service/main.go
cmd/task-service/main.go
cmd/ani/main.go
```

每个 `cmd/*` 目录都是一个 `package main`，可以被编译成一个二进制。

ANI 当前等价入口是：

```text
services/ani-gateway/main.go
services/auth-service/main.go
services/task-service/main.go
services/reconcile-worker/main.go
cli/ani/main.go
```

这能正常运行，但语义不够清晰。特别是 `services/` 在 ANI 中同时有两层含义：

1. Go 服务进程目录，例如 `ani-gateway`、`auth-service`、`task-service`。
2. ANI Services 业务层与 Services 团队相关文档/任务/历史代码。

因此新工程师容易困惑：`services/ani-gateway` 是 Core 服务进程，还是 ANI Services 业务代码？

### 6. Services 业务 proto 生成到 Core 共享 `pkg/generated/pb`

当前 proto 生成配置统一将 Protobuf 生成物输出到：

```text
pkg/generated/pb
```

其中既包括 Core 内部服务相关 proto，例如 `auth`、`task`，也包括 Services 业务域 proto，例如 `model`、`kb`、`inference`。

这会削弱 Core/Services 边界表达。因为 `pkg/generated/pb` 看起来像 Core 共享包，但里面包含 Services 业务协议。

结合 ANI 的架构规则：Services 应通过 Core OpenAPI REST API / Core SDK 调用 Core，而不应直接 import Core 内部包。若 Services 业务协议长期放在 Core 共享 `pkg` 下，会让边界认知变得暧昧。

## 为什么不是现在立刻做大规模重构

当前仍处于代码开发过程中。Core 和 Services 之所以分为两个团队，是为了并行加速，不代表它们最终必须拆成两个独立仓库。ANI 仍然是一个完整 product monorepo。

如果现在立刻追求最典型 Go 项目结构，风险包括：

- Services 真实开发需求尚未完全稳定，提前拆目录可能拆错边界。
- Core 后续还要接 Services 的真实任务，真实依赖关系需要通过协作暴露。
- 目录迁移会影响 import、Makefile、CI、Docker、Helm、SDK、proto 生成和文档链接。
- 大规模移动会打断当前功能闭环节奏。
- 过早追求 Go 标准结构，可能压过 ANI 作为完整产品仓库的真实协作需求。

因此当前不建议进行“大搬家式最优结构重构”。

## 为什么也不建议等所有代码完全开发完再重构

如果等到所有代码完全就绪后再通过 AI Coding 统一调整目录，风险也很高：

- import 路径会遍布全仓。
- CI、构建脚本、部署脚本、SDK 生成、proto 生成和文档链接都会固化旧路径。
- 临近发布时做大规模移动，会把功能风险和目录风险叠加。
- review 成本会很高，真正的功能 bug 容易被路径变更噪声淹没。
- 发布冻结前的大重构容易影响稳定性判断。

因此“最终全部写完再整理”也不是最佳时机。

## 当前决策

当前采取分阶段策略：

```text
现在：边界标清 + 防止继续混乱，不做大规模目录迁移
中期：第一轮 Core-Service 集成闭环完成后，开专门结构重构批次
后期：进入发布冻结或 RC 后，不再做大规模目录调整
```

### 阶段一：现在只做轻量治理

现在可以做的是认知和边界沉淀，而不是搬目录：

- 明确 ANI 是 product monorepo，不是纯 Go repo。
- 明确 Core/Services 分队是并行研发组织方式，不代表必须拆仓。
- 标注 active Core、active Services、legacy/frozen 目录。
- 新代码尽量避免继续加深模糊边界。
- 后续可考虑增加 `make test-core`、`make test-services`、`make test-all` 等清晰入口。
- 后续可考虑增加 import 边界检查，避免 Services 直接 import Core 内部包。

当前不修改目录、不移动代码、不调整 module。

### 阶段二：第一轮 Core-Service 集成闭环后，开专门重构批次

更合适的重构时间点是：

```text
Core API / Services API 基本稳定
Core 已经接过一轮真实 Services 开发任务
Gateway / SDK / proto / live gate 主链路跑通
没有大量未合并长分支
距离发布冻结仍有足够缓冲
```

此时可以开一个专门批次，例如：

```text
REPO-LAYOUT-HARDENING-A
GO-LAYOUT-CONSOLIDATION-A
```

该批次目标不是新增业务功能，而是收敛仓库结构、Go module/package/cmd 表达、构建入口和文档索引。

### 阶段三：发布冻结后只做小修

进入 RC 或 release hardening 后，不再做大规模目录调整。该阶段只修 bug、安全、部署和文档一致性问题。

## 后续重构批次的建议执行原则

当进入结构优化批次时，应遵守以下原则：

1. 先由 AI Coding 全仓扫描 import、go.mod、go.work、Makefile、CI、Docker、Helm、SDK、proto、文档链接。
2. 先输出目标目录设计和迁移矩阵，由人确认后再动手。
3. 使用 `git mv` 做路径迁移，尽量保留历史。
4. 功能变更和目录迁移分开，不混在一个 PR。
5. 每个迁移步骤都运行固定门禁，例如 `make test`、`make validate-architecture`、`git diff --check`。
6. 保持 Core/Services 跨层契约仍以 OpenAPI / SDK 为主，不因为目录迁移绕过架构边界。
7. 不为了形式上的 Go 标准，破坏 ANI 已有 ports/adapters、live gate、真实 provider 验证体系。

## 推荐目标方向

长期更 Go 化的方向可以是：

```text
repo/
├── go.mod
├── go.sum
├── cmd/
│   ├── ani/
│   ├── ani-gateway/
│   ├── auth-service/
│   ├── task-service/
│   └── reconcile-worker/
├── internal/
│   ├── bootstrap/
│   ├── gateway/
│   ├── auth/
│   ├── task/
│   └── adapters/
├── pkg/
│   └── ports/
├── api/
├── sdks/
├── deploy/
├── scripts/
└── docs/
```

但这只是目标方向，不是当前立即执行方案。

ANI 的真实目标不是“看起来像模板 Go 项目”，而是让 Go 代码、产品 monorepo、Core/Services 协作和真实验证门禁同时保持清晰。

## 最终结论

当前不应立刻把代码结构调整成所谓最优 Go 形态，也不应等所有代码完全开发完后再一次性大重构。

最佳策略是：

```text
现在记录原因和边界，轻量治理；
第一轮 Core-Service 集成闭环后，发布冻结前，开专门结构优化批次；
进入发布冻结后，不再做大规模目录迁移。
```

这既回应了 Core 工程师对 Go 工程习惯的合理诉求，也尊重 ANI 作为完整产品 monorepo 的真实背景。
