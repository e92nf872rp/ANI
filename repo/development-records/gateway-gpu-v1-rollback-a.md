# GATEWAY-GPU-V1-ROLLBACK-A：GPU 接口鉴权暂时回退 V1 链路（契约删 x-ani-authz）

> 状态：已完成（live 验证通过，ani-test2 隔离环境 `10.10.1.66:30083`，镜像 `test2-20260908-b`）
> 批次类型：hotfix（GPU 接口鉴权 V2→V1 暂时回退 + hotfix 分支 rebase main）
> 分支：ani-hotfix / `hotfix/network-store-read`（已 rebase 到 origin/main）

## 1. 背景与目标

两件事叠加触发本批次：

1. **生产 gateway 镜像回退**：线上集群 gateway 镜像被回退到早于 PR #143 的版本
   （digest `4dae93c3`），BOSS 平台 token 访问 GPU 接口再次全部 403
   `token scope not allowed for this path`。
2. **main 已全量 V2 化（PR #145）**：main 分支 v1.yaml 已为全部 authorized 操作补齐
   `x-ani-authz`，GPU 路径（/gpu-specs、/gpu-inventory、/gpu-scheduling）被标为
   `scope: platform`（单域）。V2 boundary 是 credential domain 互斥判断，一旦部署
   main，Console（tenant scope）访问 GPU 接口将全部 403。

GPU 接口是**双域共享资源**（BOSS platform + Console tenant），当前 V2 boundary
模型（own/tenant/platform 单选）无法表达。与产品确认：**暂时回退 V1 链路**，
等 boundary 模型扩展（cluster 双域）后再迁回 V2。

## 2. 决策：回退 V1，而不是在新镜像上重打 scope 补丁

- PR #143/#145 之后的 main 已把 authorized 操作全部迁到 V2；在 main 上重新给 GPU
  接口加 V2 补丁等于再次制造契约级冲突（单域 scope 必然挡掉另一域）。
- V1 链路（`scopeAllowedForPath` + rbac.go 角色准入）已在本分支验证过双域放行且
  `/quotas` 跨租户泄露已封堵（GATEWAY-QUOTA-PLATFORM-SCOPE-A），行为正确。
- 因此方向是**把契约改回与 V1 行为一致**，而不是改运行时。

## 3. 实现（契约 → 生成物全链同步）

只删 `x-ani-authz` 过不了交叉校验，实际同步了整条生成链：

| 文件 | 变更 |
|---|---|
| `repo/api/openapi/v1.yaml` | 13 个 GPU 操作删除 `x-ani-authz`；`x-ani-auth-classification: authorized → authenticated`（生成器用 classification 与目标注册表交叉校验，两者必须一致） |
| `repo/api/openapi/operation-registry.v1.json` | 13 条目同步为 authenticated/validate_principal，删除 permission |
| `services/ani-gateway/internal/authz/zz_generated_target_operation_registry.go` | 同步重新生成 |
| `services/ani-gateway/internal/authz/zz_generated_core_policies.go` | 重新生成，GPU 接口全部 `PolicySourceLegacy` → 运行时走 V1（scopeAllowedForPath platform‖tenant + rbac.go 角色级 RBAC） |
| `services/ani-gateway/internal/authz/target_operation_registry_test.go` | 冻结计数同步（authenticated 9→22、authorized 278→265） |

- `/quotas`（platform-only）与 `/quotas/me`（own）的 `x-ani-authz` **保留**：其 V2
  定义与 V1 行为一致（platform 精确匹配 / tenant 受 RLS 自查），不属于问题路径。
- 运行时代码（middleware/auth.go 的 `scopeAllowedForPath`）**零改动**——rebase 后
  本分支已包含 GPU 双放行 + /quotas platform-only 修复。
- auth-service 无改动。

## 4. 分支 rebase

`hotfix/network-store-read` rebase 到 origin/main，解决冲突：

- `repo/frontends/.../core-schema.d.ts`：main 已删除而 hotfix 有修改 → `git rm` 接受删除
- `middleware/auth.go` / `auth_test.go`：hotfix 的 GPU scope 修复与 main 的 V2 authz
  改造冲突 → 保留 main V2 结构 + 完整双放行逻辑（含 /gpu-scheduling、/quotas）
- 文档类（CURRENT-SPRINT.md 等）→ 按语义取合
- rebase 完成后遗留一处冲突标记（auth.go L274）导致 build 失败，已修复并验证

## 5. 验证

**门禁**：gateway `go build` 通过；authz + middleware 测试全过；
`generate_gateway_authz_test.py` 21 tests OK；路由覆盖 307/298 无错误；
OpenAPI 结构校验（v1.yaml + services v1.yaml）OK；`git diff --check` 通过。

**live（ani-test2 隔离环境，namespace ani-test2，NodePort 30083，镜像
`test2-20260908-b`，构建自本分支）**：

| 接口 | platform token | tenant token |
|---|---|---|
| GET /gpu-specs | 200 | 200 |
| GET /gpu-inventory | 200 | 200 |
| GET /gpu-scheduling/queues | 200 | 200 |
| GET /quotas（跨租户总览） | 200 | 403（泄露封堵保持） |
| GET /quotas/me | 403（预期） | 200 |

Console 联调账号实测：`tenant-a`/`admin`（tenant_name `tenant-a`）登录 200，
token 下 gpu-specs / gpu-inventory / gpu-scheduling/queues / quotas/me /
instances 全 200。

## 6. 已知问题（main 既有，不在本批范围）

1. PR #145 给 9 个 `/admin/tenants*` 操作加入 v1.yaml 但未写 dp2 注解
   （x-ani-handler/owner/classification），`validate-gateway-authz` drift 门禁在
   main 本身为红 → 需 main 独立批次补注解。
2. `GET /auth/api-keys` operationId 变更与冻结兼容性基线冲突，
   `validate_core_api_compatibility.py` 在 main 为红 → 需 main 独立决策
   （恢复 operationId 或更新基线）。

## 7. 后续项

- GPU 接口迁回 V2 authz：前置条件是 V2 boundary 模型扩展支持双域（cluster boundary），
  届时恢复 v1.yaml 的 x-ani-authz + 注册表/生成物同步 + permission 数据播种。
- 本分支与 fork 远端已分叉（rebase），推送需 force push（已经用户确认）。
