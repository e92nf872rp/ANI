# DP2-02 — Public IAM OpenAPI and operation registry freeze

完成日期：2026-09-04

状态：`pass`（契约与生成门禁）；Gateway 运行时切换：`not_verified`

固定来源：ANI commit `0cedae825a489d936cf41815dc27f278f6d3213c`，tree `552e50bd5bdd49b6abb168b1ef99bfb74dbd1df8`

## 交付内容

- 将 `repo/api/openapi/v1.yaml` 冻结为 ANI 唯一公网 IAM 目标契约。
- 为 295 个 operation 固定唯一 `operationId`、Gateway Handler、后端 Owner，以及 Public、Authenticated-only、Authorized 分类。
- 从 OpenAPI 生成不可变 operation registry、Permission `{resource, actions, scope}`、typed obligations 和稳定错误映射；未知 operation、缺失标注和 policy revision mismatch 均 fail closed。
- 固定 Public operation 的 IAM decision 数为 0，受保护 operation 为一次 `ValidatePrincipal` 或一次 `CheckPermission`。
- Gateway 在决策前删除所有客户端 `x-ani-*`，仅允许注入目标可信上下文字段；不注入 Role 或 Permission 列表。
- 重新生成 Console、BOSS TypeScript schema 和 Core Go、Java、Python、TypeScript SDK；生成源与生成物同批提交。

## Breaking 结论

| 项目 | 固定结果 |
|---|---:|
| source operations | 236 |
| target operations | 295 |
| added operations | 59 |
| removed operations | 0 |
| changed operationIds | 6 |
| added schemas | 45 |
| removed schemas | 0 |
| changed existing schemas | 0 |
| machine-readable breaking rows | 263 |

六个 operationId 变化为 `revokeAPIKey → revokeIAMAPIKey`、`listAPIKeys → listIAMAPIKeys`、`missing → getBranding`、`createAPIKey → createIAMAPIKey`、`logout → logoutSession`、`missing → refreshSession`。Password login、refresh、logout、OIDC begin 和 API Key 请求/响应已切换到目标 Cookie、CSRF、Idempotency-Key、Service Principal 与稳定错误语义。

## 固定 Artifact

| Artifact | SHA-256 |
|---|---|
| target OpenAPI | `2466982a7e8f904c6bb6f7790588359c6faf9b230a39a0e28939fcbedc72d0e5` |
| operation policy | `851d236c93b80af097d3b319ee064615d553de64b53128892f39c3c8fda282a1` |
| IAM replacement manifest | `dffb3c5b2849cfa60a8d121976945420b5da794c14543ceb8c28f5ba9e19e780` |
| operation registry JSON | `319bd3746098b79d29da18141872b97263c1d899da0306654eda9fad736c2ad2` |
| OpenAPI breaking JSON | `cc45d4116b99a525ed58c29a1e33daae194c3cd8f54a6e36c9fdf9859e01be9a` |
| generated Go registry | `b28b90fef06dc013c61c4d339574262e27505de5d5a207a2d9fd59b6ed7f0bdc` |

Policy revision：`sha256:f222e2c6d3cd6442449cd722389d3d4fbfcdc7a0fee950c9d28385d3c264affa`。

## 验证

| 结果 | 门禁 |
|---|---|
| `pass` | target registry tests 20/20；registry 与 breaking generator `--check` |
| `pass` | OpenAPI validator、`make validate-openapi-spec`、固定 breaking comparison |
| `pass` | pinned `openapi-typescript 7.13.0` 重新生成，Console/BOSS byte-identical |
| `pass` | Core SDK generator；Go、Python、TypeScript smoke；Java source smoke |
| `not_verified` | Java compile/run：当前环境无 JDK |
| `pass` | target Gateway authz/trusted-header tests、`make validate-architecture`、`make test-python` |
| `pass` | DP2-02 完整相关 Go package 回归 |
| `fail` | legacy `validate-gateway-authz`、`validate-core-api-compatibility`、`validate-auth-contract`；它们固定旧字段、TransferOwnership 和旧 operationId，由本批次 replacement gates 取代 |

## 运行时边界

- Gateway 使用目标 registry 的实际 composition-root 接线和 59 个新增 operation 的生产 Handler 为 `not_verified`，由 DP2-05 及后续切换事项实现和验证。
- Console/BOSS UI 行为和四语言调用方升级为 `not_verified`；本批只冻结契约与生成客户端。
- 本批不修改部署、不切流、不发布远端 Artifact、不移除旧 Auth，也不启用 legacy fallback。

## 人工检查点与恢复

2026-09-04 已人工审核并接受精确 DP2-02 OpenAPI breaking diff，同时批准六个 `repo/sdks/core/**` 生成文件和本 Feature-batch 四个文档闭环路径。

提交前恢复方式是仅对本批明确文件应用经审查的 reverse patch；提交后使用针对本地 commit SHA 的新 revert commit。禁止 reset、stash、amend、force、push、部署或切流。
