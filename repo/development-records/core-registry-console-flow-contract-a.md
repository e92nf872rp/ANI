# CORE-REGISTRY-CONSOLE-FLOW-CONTRACT-A：Console 镜像仓库流程契约补齐

> **批次类型：** Feature batch（Core v1 契约）
> **完成日期：** 2026-07-22
> **Scope：** `repo/api/openapi/v1.yaml`、`repo/frontends/console/src/api/core-schema.d.ts`、`repo/scripts/validate_openapi_spec_test.py`
> **Product line：** Core / Console 支撑

## 交付内容

基于 2026-07-22 产品原型更新，按“暂不考虑 BOSS 和权限”的边界补齐 Console 镜像仓库主流程所需的最小 Core v1 契约。

### 契约变更

- `RegistryImage` 新增可选 `purpose` 字段，枚举为 `container`、`gpu`、`sandbox`、`system`，用于 Console 创建向导筛选镜像用途。
- `GET /registry/images` 新增可选 `purpose` query，同步上述四类用途枚举。
- `RegistryImageReference.kind` 扩展为 `vm_instance`、`container_instance`、`gpu_container_instance`、`sandbox_instance`，覆盖原型中的四类算力引用。
- `POST /instances` 的 `422 PreconditionFailed` 描述补充镜像门禁错误语义：`ImageNotFound`、`ImageScanning`、`ImageVulnerabilityBlocked`、`ImagePurposeMismatch`。
- 重新生成 Console Core OpenAPI TypeScript schema。

## 验证命令

```bash
cd repo
python3 -m unittest validate_openapi_spec_test
PATH=/tmp/ani-pybin:$PATH make validate-openapi-spec validate-core-api-compatibility
PATH=/tmp/ani-pybin:$PATH make validate-sdk-beta
PATH=/tmp/ani-pybin:$PATH make validate-doc-api
git diff --check
```

## 边界声明

- 本批次只做 Console 镜像仓库流程的 Core v1 契约和生成物，不实现 Gateway handler、adapter 或 Console 页面。
- 本批次不新增 BOSS 配额、BOSS 扫描大盘、GC、项目权限、机器人凭据或 pull-secret UI 契约。
- 本批次不声明 registry runtime ready、Harbor production ready 或 full platform production ready。
