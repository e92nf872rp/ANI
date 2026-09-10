# Core 团队任务分解

**生成日期**: 2026-06-09  
**基于**: `repo/api/openapi/v1.yaml` stubs（Branding + Tasks）  
**执行入口**: `repo/services/ani-gateway/internal/router/`  
**注**: Core handler 可以 import `pkg/adapters`（通过依赖注入），但 handler 文件本身不直接 new adapter

---

## TASK-CORE-001: 实现 getBranding / updateBranding / uploadBrandingLogo handlers

**Team**: Core  
**YAML 来源**: `v1.yaml` operationId=`getBranding`, `updateBranding`, `uploadBrandingLogo`  
**Files**:
- 修改: `repo/services/ani-gateway/internal/router/stubs.go` → 移除 `registerBranding` 调用中的 `notImplemented`
- 新建: `repo/services/ani-gateway/internal/router/branding_resources.go`

**Handler 逻辑**:

`getBranding`（GET /branding）：
1. 此路径已在 `isPublicPath()` 中豁免 auth（`middleware/auth.go:90`）
2. 从持久化存储（ConfigMap 或 K8s Secret）读取 branding 配置
3. 返回 Branding schema JSON（company_name, logo_url, theme_color 等）

`updateBranding`（PUT /branding）：
1. 验证 `tenant_id`（admin 操作）
2. 解析 requestBody，更新存储
3. 返回更新后的 Branding JSON

`uploadBrandingLogo`（POST /branding/logo）：
1. 接收 multipart/form-data 图片
2. 存储到对象存储，返回 logo_url
3. 更新 Branding 记录

**验收命令**:
```bash
# getBranding 无需 auth
curl http://localhost:8080/api/v1/branding
# 期望: 200 + branding 对象（不返回 501）

# updateBranding
curl -X PUT -H "X-Dev-Tenant-ID: admin" -H "Content-Type: application/json" \
  -d '{"company_name":"ANI"}' http://localhost:8080/api/v1/branding
# 期望: 200 + branding 对象
```
**前置依赖**: 无

---

## TASK-CORE-002: 实现 getTask / deleteTask handlers

**Team**: Core  
**YAML 来源**: `v1.yaml` operationId=`getTask`, `deleteTask`  
**Files**:
- 修改: `repo/services/ani-gateway/internal/router/stubs.go` → 移除 `registerTasks` 调用中的 `notImplemented`
- 新建: `repo/services/ani-gateway/internal/router/task_resources.go`

**Handler 逻辑**:

`getTask`（GET /tasks/{task_id}）：
1. 从 `c.MustGet("tenant_id").(string)` 提取租户 ID（隔离不同租户的任务）
2. 从 `pkg/ports` AsyncTaskPort 或任务数据库查询任务状态
3. 返回 AsyncTask schema JSON

`deleteTask`（DELETE /tasks/{task_id}）：
1. 验证任务属于当前租户
2. 取消或软删除任务
3. 返回 204

**验收命令**:
```bash
curl -H "X-Dev-Tenant-ID: test" http://localhost:8080/api/v1/tasks/00000000-0000-0000-0000-000000000001
# 期望: 200（若存在）或 404（若不存在），不返回 501
```
**前置依赖**: 无

---

## TASK-CORE-003: ~~实现 inferenceProxy（OpenAI兼容代理）~~ 已取消

**取消原因**：OpenAI 推理请求的数据面已明确归属独立 Envoy AI Gateway。`ani-gateway`
只承担控制面，不注册或实现第二套 `/v1/chat/completions` 反向代理；旧占位路由已移除。

相关入口、鉴权和限流以 Envoy AI Gateway 的发布流程为准，见
`docs/superpowers/specs/2026-08-31-envoy-ai-gateway-tenant-aware-dynamic-publication-design.md`。
