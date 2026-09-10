# MODEL-REPOSITORY-P0-BACKEND — 模型仓库后端第一闭环

完成日期：2026-09-01
对应 Sprint：Sprint 13（Services 受控并行实现）
验证结果：模型服务、ANI Gateway、inference-service 与共享 pkg 的 focused/full/race/vet/build 通过；仓库 `make test`、`make validate-services`、`make validate-architecture` 和文档入口检查通过；PG live integration 因未设置 `INFERENCE_TEST_DATABASE_URL` 按规则跳过；未执行真实集群、对象存储或数据库写入。

## 实现了什么

模型仓库现支持租户隔离的模型注册、版本注册与查询、版本列表、预签名上传地址，以及对象存储版本的下载描述。模型和版本的写操作带租户范围的幂等重放/哈希冲突保护；删除模型前会检查未删除的推理服务引用。保留现有 `pvc://` 实验链路，远程 ModelScope/Hugging Face 导入仍返回契约规定的未实现响应。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/proto/model/v1/model_service.proto` | 修改 | 增加幂等键、列表过滤、版本列表 RPC、上传过期时间；仅 additive 演进 |
| `pkg/generated/pb/model/v1/model_service*.pb.go` | 生成 | 与 proto 对应的内部 gRPC 生成物 |
| `deploy/migrations/20260901000300_model_repository_idempotency.sql` | 新增 | 租户 RLS 下的模型变更幂等记录表 |
| `services/model-service/internal/repo/model_repo.go` | 修改 | 幂等 claim/replay、source/capability/keyword 过滤、引用删除保护 |
| `services/model-service/internal/service/model_service.go` | 修改 | 版本校验、上传 URL、对象下载 URL、租户/对象边界 |
| `services/model-service/internal/service/object_store.go` | 新增 | 严格 `object://models/<tenant>/<model>/<version>/<document>/<file>` 解析与路径校验 |
| `pkg/types/model_object_store.go` / `pkg/bootstrap/model_object_store.go` | 新增 | 跨 Services/Core 的对象存储端口与结构化装配适配 |
| `services/ani-gateway/internal/router/model_*` | 修改 | 过滤、版本、上传 URL 路由和稳定错误映射；Gateway 不代理模型字节 |
| `services/inference-service/internal/catalog/modelsvc/adapter.go` | 修改 | 解析租户自有、ready 的对象版本，同时保留 PVC 版本 |

## 完工标准达成

- [x] 模型/版本创建转发并保留 `Idempotency-Key`，同租户同请求可 replay，哈希不同返回冲突
- [x] 列表支持状态、来源、能力和关键字过滤；版本列表通过内部 gRPC 暴露
- [x] 上传地址使用租户/模型/版本/文档确定性对象键，版本注册以对象 `size`/`sha256` 校验通过为准
- [x] 模型删除在仍被未删除推理服务引用时返回 `MODEL_IN_USE`
- [x] 推理 catalog 拒绝跨租户、非法对象路径和不兼容任务；PVC 路径保持兼容
- [x] `make validate-architecture`、文档入口检查和 `git diff --check` 通过
- [x] 仓库级 `make test` 与 `make validate-services` 在沙箱外回环环境通过

## 备注

- 本批次不实现远程 ModelScope/Hugging Face 下载、异步导入 worker、对象存储真实 live 验证、加密/解密流程或 Console 页面。
- PG 迁移与 RLS 集成测试代码已具备；当前环境未提供 `INFERENCE_TEST_DATABASE_URL`，因此只完成编译/静态验证。
- 本批次保留工作区中原有的 `api/openapi/services/v1.yaml` description-only 改动，未扩大其契约范围。
