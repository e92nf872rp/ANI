# KuberCloud ANI · 版本管理策略

> 版本 V1 | 广州常青云科技有限公司 | 内部工程治理文件

---

## 一、适用范围

本文定义 KuberCloud ANI 的产品版本、代码标签、发布包、数据库迁移、API/Proto/CRD 兼容性规则。

本策略适用于：
- Git tag
- 容器镜像 tag
- Helm Chart version / appVersion
- `.anipatch` 升级包
- API / Proto / SDK 发布
- Installer 离线包
- 数据库迁移包

不适用于：
- `ANI-06` 的产品计划模块编号，例如 `模块 2`、`模块 3`
- 代码生成批次编号，例如 `M2.1-TASK-C`
- 模型文件自身版本，例如 Qwen、DeepSeek、BGE 的上游模型版本

---

## 二、版本号规范

ANI 采用 SemVer 2.0：

```text
vMAJOR.MINOR.PATCH[-pre.N]
```

示例：

```text
v0.1.0-alpha.1
v0.3.0-beta.1
v1.0.0-rc.1
v1.0.0
v1.1.0
v1.1.1
v2.0.0
```

## 三、首个正式版本

首个正式版本定义为：

```text
v1.0.0
```

目标日期：

```text
2026-09-30
```

含义：
- Phase 1 POC 交付就绪。
- 可在客户现场完成安装、部署、基础运维和回滚。
- 核心 P0 功能达到验收标准。
- 不是功能全集，不包含 Phase 2/3 延后项。

在 `v1.0.0` 之前，所有版本均使用 `v0.x.y` 或 pre-release 标识。

---

## 四、版本变更触发条件

| 类型 | 触发条件 |
|---|---|
| **MAJOR** | OpenAPI `/api/v1` 破坏性变更；Protobuf 字段删除/重编号/语义不兼容；数据库迁移需要人工处理或不可自动回滚；CRD `apiVersion` 不兼容升级；Helm values 结构不兼容；移除已有认证方式；多租户/RLS 安全模型破坏性调整；NATS subject 或 payload 不兼容；MinIO bucket/object layout 不兼容 |
| **MINOR** | 新功能模块；新增 API/Proto 方法且向后兼容；新增认证方式；新增模型导入源；新增 CRD 字段且默认兼容；新增 Installer 部署目标；新增 SDK 能力；数据库只做向后兼容加法迁移 |
| **PATCH** | Bug 修复；安全补丁；性能优化；非破坏性 UI 改进；文档修正；CI/构建修复；向后兼容的小型迁移 |
| **Pre-release** | `-alpha.N` 内部开发验证；`-beta.N` 功能基本完成、待测试；`-rc.N` 发布候选，只允许修复阻断问题 |

---

## 五、兼容性边界

### 5.1 API / SDK

- REST API 以 OpenAPI 为唯一契约来源。
- gRPC API 以 Protobuf 为唯一契约来源。
- `MINOR` 和 `PATCH` 不允许破坏已发布字段、枚举值、HTTP 状态语义和错误码语义。
- 删除字段必须至少经历一个 `MINOR` 版本的 deprecated 周期。
- 破坏性 API 变更必须升级 MAJOR，或新增 `/api/v2` 并保留 `/api/v1`。

### 5.2 数据库

- `PATCH` 和 `MINOR` 默认只允许兼容迁移。
- 删除列、改列类型、重建大表、强制停机迁移属于 MAJOR 风险。
- 所有迁移必须可审计、可重复执行，并记录回滚策略。
- 大表变更必须给出在线迁移方案。

### 5.3 CRD / Operator

- CRD 字段新增必须提供默认值或向后兼容行为。
- CRD 字段删除、语义变更、`apiVersion` 不兼容升级属于 MAJOR。
- Operator 必须支持至少从上一个 MINOR 版本滚动升级。

### 5.4 部署与升级包

- `.anipatch` 包名必须包含目标版本：`patch-vX.Y.Z.anipatch`。
- `manifest.yaml` 必须声明：
  - `version`
  - `from_versions`
  - `min_supported_version`
  - `components`
  - `migrations`
  - `rollback_supported`
- Patch 包必须签名，安装前强制验签。

---

## 六、Git 与制品命名

### 6.1 Git tag

正式 tag：

```text
v1.0.0
v1.1.0
v1.1.1
```

预发布 tag：

```text
v1.0.0-alpha.1
v1.0.0-beta.1
v1.0.0-rc.1
```

### 6.2 分支

| 分支 | 用途 |
|---|---|
| `main` | 主干，必须保持可构建、可测试 |
| `release/vX.Y` | 维护某个 MINOR 发布线 |
| `codex/mx-y-topic-z` | Codex Cloud 代码生成任务分支，例如 `codex/m2-1-task-c-worker-mutations` |
| `hotfix/vX.Y.Z-topic` | 生产补丁修复 |

### 6.3 镜像与 Chart

容器镜像：

```text
harbor.ani.internal/ani/ani-gateway:v1.0.0
harbor.ani.internal/ani/task-service:v1.0.0
```

Helm Chart：

```yaml
version: 1.0.0
appVersion: v1.0.0
```

---

## 七、发布门禁

每个可发布版本至少满足：

```bash
cd repo
make gen-proto
make test
make build
```

`v1.0.0-rc.N` 和正式版本还必须满足：
- 数据库迁移 dry-run 通过。
- Helm lint 通过。
- 离线安装包验签流程通过。
- 回滚演练通过。
- 多租户隔离测试通过。
- 安全扫描无 Critical 漏洞；High 漏洞必须有处置说明。
- `repo/development-records/README.md` 已更新到对应版本。

---

## 八、与开发计划的关系

- `Phase 1/2/3` 是产品路线图阶段，不等于 SemVer 的 MAJOR。
- `模块 1/2/3` 是 `ANI-06` 的开发模块编号，不等于 SemVer 的 MAJOR。
- `M2.1-TASK-C` 是代码生成批次，不等于 SemVer。
- 版本号只表示发布兼容性和制品生命周期。

当前状态：
- 当前代码仍处于 `v0.x` 开发期。
- 当前实现位置是 `M2.1-TASK-A/B` completed，下一步 `M2.1-TASK-C`。
- 尚未进入 `v1.0.0-rc` 阶段。
