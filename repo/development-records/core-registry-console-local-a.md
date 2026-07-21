# CORE-REGISTRY-CONSOLE-LOCAL-A — 镜像仓库 Console v1 契约后端实现

完成日期：2026-07-21
对应 Sprint：Sprint 13（并行 Core API 实现切片）
验证结果：受影响 Go 包测试、Core OpenAPI/compatibility/SDK beta、Core beta、`make test`、`make validate-architecture`、`make validate-doc-entrypoints`、`git diff --check` 通过

## 实现了什么

在镜像仓库 v1 契约已评审合入后，补齐 Console 首屏和镜像列表需要的 Core 后端实现：

- `GET /registry/overview`：返回项目、仓库、artifact、tag 资源计数与状态分布、能力清单、创建顺序、资源关系和删除风险提示。
- `GET /registry/images`：返回平铺镜像 tag 视图，包含完整镜像引用、digest、大小、扫描摘要和 pull 命令。
- `GET /registry/projects/{project}/push-instructions`：返回 login/tag/push 命令模板，不返回凭据明文。
- `GET /registry/projects/{project}/repositories/{repository}/tags/{tag}/references`：返回镜像 tag 的本地引用方和 `delete_blocked`。
- `DELETE /registry/projects/{project}/repositories/{repository}/tags/{tag}`：被引用 tag 返回 `409 CONFLICT`，未引用 tag 返回删除记录。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/image_registry.go` | 修改 | 扩展 `ImageRegistry` port 与 DTO，覆盖 overview、image list、push instructions、tag delete/reference |
| `pkg/adapters/registry/local_image_registry.go` | 修改 | 增加确定性的 local profile 实现和删除风险/引用模拟 |
| `pkg/adapters/registry/not_configured.go` | 修改 | 补齐扩展后的 not-configured adapter |
| `services/ani-gateway/internal/router/registry_resources.go` | 修改 | 注册新增 `/registry/*` 路由、响应映射和 404/409 错误语义 |
| `pkg/adapters/registry/local_image_registry_test.go` | 修改 | 覆盖 local profile 的 overview/images/push/tag reference/delete 行为 |
| `services/ani-gateway/internal/router/registry_resources_test.go` | 修改 | 覆盖响应映射和 HTTP 路由级契约命中 |

## 完工标准达成

- [x] 实现阶段只基于已合入 Core v1 契约推进，未继续修改契约。
- [x] 首屏总览覆盖资源计数/状态、能力清单、创建顺序、资源关系和删除风险提示。
- [x] 删除前引用查询与删除冲突语义可验证。
- [x] Gateway handler 不直接依赖 Harbor/Kubernetes SDK，仍通过 `ports.ImageRegistry`。
- [x] local profile 明确 `dev_profile.real_provider=false`，不声明 Harbor/Trivy real-provider 或 production ready。

## 备注

本批次不新增真实 Harbor/Trivy provider、不新增 live gate、不触碰前端页面。真实镜像仓库对接、推拉凭证回读、扫描报告回读和生产形态门禁仍需后续单独批次证明。
