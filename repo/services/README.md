# ANI Services 工作区

Console 与 BOSS 模块功能定义、OpenAPI 规划与实现任务的文档工作区，位于 ANI monorepo 的 `repo/services/` 下。

**定位**：文档与规划层，不包含前端代码。`repo/api/openapi/services/v1.yaml` 是 Services 接口的唯一权威源；`repo/api/openapi/v1.yaml` 是 Core 接口的唯一权威源。

## 从哪里开始看

| 你想… | 打开 |
|---|---|
| **Console 总控：哪些模块可用、缺什么 YAML** | [`docs/console-modules/governance/console-document-status-board.md`](docs/console-modules/governance/console-document-status-board.md) |
| **BOSS 总控：模块文档状态** | [`docs/boss-modules/governance/boss-document-status-board.md`](docs/boss-modules/governance/boss-document-status-board.md) |
| **某个 Console 页面的正式定义** | [`docs/console-modules/README.md`](docs/console-modules/README.md) → 按域进子目录 |
| **某个 BOSS 页面的正式定义** | [`docs/boss-modules/README.md`](docs/boss-modules/README.md) → 按域进子目录 |
| **Phase 3 联评怎么开** | [`tasks/phase3/PHASE3-JOINT-REVIEW-AGENDA.md`](tasks/phase3/PHASE3-JOINT-REVIEW-AGENDA.md) |
| **Core/Services 实现任务** | [`tasks/execution/`](tasks/execution/) |
| **原型 HTML（摘要导航）** | [`prototypes/ani-services-prototype-console.html`](prototypes/ani-services-prototype-console.html) · [`prototypes/ani-services-prototype-boss.html`](prototypes/ani-services-prototype-boss.html) |
| **Core API 契约（只读参考）** | [`../api/openapi/v1.yaml`](../api/openapi/v1.yaml) |
| **Services API 契约（只读参考）** | [`../api/openapi/services/v1.yaml`](../api/openapi/services/v1.yaml) |

## 目录结构

```
repo/services/
├── README.md                 ← 你在这里
├── docs/
│   ├── console-modules/     Console 模块详文（按业务域分子目录）
│   │   ├── governance/      状态板、matrix、workflow、backlog
│   │   ├── openapi-drafts/  P0 / Phase3 OpenAPI 草案
│   │   └── home/ compute/ inference/ knowledge/ ai-native/ …
│   └── boss-modules/        BOSS 模块详文（按业务域分子目录）
│       ├── governance/      状态板、matrix、workflow
│       └── overview/ tenant/ ops/ health/ metering/ audit/ …
├── tasks/
│   ├── execution/            CORE/SERVICES-TEAM-TASKS、依赖图、handler 指南
│   ├── phase2/ phase3/       验收与联评记录
│   ├── modules/prd|spec/     PRD/SPEC（按模块）
│   └── support/              辅助维护长文
└── prototypes/               Console/BOSS 原型 HTML（过程参考）
```

## 新增内容放哪里

| 类型 | 放哪 |
|---|---|
| 新 Console 模块详文 | `docs/console-modules/{域}/{module}.md` |
| 新 BOSS 模块详文 | `docs/boss-modules/{域}/{module}.md` |
| PRD / SPEC | `tasks/modules/prd/`、`tasks/modules/spec/` |
| OpenAPI Phase3 草案 | `docs/console-modules/openapi-drafts/phase3/` |
| 治理/状态更新 | `docs/console-modules/governance/` 或 `docs/boss-modules/governance/` |
| 联评/验收记录 | `tasks/phase3/` |
| 实现任务 | `tasks/execution/` |

**规则**：详文是唯一主维护源；PRD/SPEC 是辅助；草案在评审通过前不得写进详文「已冻结」。

## 相关

- Console 交付流程：[`docs/console-modules/governance/module-delivery-workflow.md`](docs/console-modules/governance/module-delivery-workflow.md)
- BOSS 交付流程：[`docs/boss-modules/governance/module-delivery-workflow.md`](docs/boss-modules/governance/module-delivery-workflow.md)
- Phase 3 整域索引：[`docs/console-modules/openapi-drafts/phase3/openapi-phase3-domain-draft.md`](docs/console-modules/openapi-drafts/phase3/openapi-phase3-domain-draft.md)
