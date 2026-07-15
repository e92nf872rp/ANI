# CORE-INSTANCE-CREATE-CONFIG-A — CreateInstanceRequest 按 kind 嵌套 `*_config`

完成日期：2026-07-15
对应 Sprint：Sprint 13（并行契约收敛切片）
验证结果：`make validate-core-alpha`、`make validate-core-api-compatibility`、gateway `demo_instances` targeted tests、`make gen-core-sdk` / `make validate-sdk-alpha`、`make validate-doc-entrypoints`、`git diff --check` 通过

## 实现了什么

在不拆 URL、不引入 `oneOf`/`discriminator`、不删除扁平字段的前提下，为 `POST /api/v1/instances` 增加按 kind 的嵌套配置：

- `vm_config` → `CreateVMInstanceConfig`
- `container_config` → `CreateContainerInstanceConfig`
- `gpu_container_config` → `CreateGPUContainerInstanceConfig`
- `sandbox_config` → 既有 `SandboxConfig`（语义不变）

扁平 `boot_image` / `ssh_*` / `replicas` / `gpu.*` 保留为 v1 兼容别名（deprecated）。Gateway 解析优先级：同名字段 `*_config` 优先；与扁平别名冲突或跨类型 config → `400`；仅扁平请求行为不变。共享 `image`/`cpu`/`memory` 仍留在顶层。`listInstances` query `kind` enum 补齐 `sandbox`。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/v1.yaml` | 修改 | 新增三个 `*_config` schema，扩展 `CreateInstanceRequest`，list `kind` 补 `sandbox` |
| `api/core-v1-compatibility-baseline.yaml` | 修改 | additive 再生基线 |
| `scripts/validate_core_alpha_contract.py` | 修改 | 断言新 config 存在且旧扁平 Alpha 字段仍在 |
| `services/ani-gateway/internal/router/demo_instances.go` | 修改 | 解析 `*_config`、冲突/跨类型校验、扁平兼容 |
| `services/ani-gateway/internal/router/demo_instances_test.go` | 修改 | 四种 kind config 路径 + 冲突 + 扁平兼容测试 |
| `sdks/core/*` | 修改 | `make gen-core-sdk` 再生 schema 常量 |
| `scripts/validate_sdk_alpha.py` | 修改 | `command_available` 对缺失 JDK 命令返回 False，走既有 source smoke 回退 |
| `services/docs/console-modules/compute/*.md` | 修改 | create 示例改为推荐 `*_config` |

## 完工标准达成

- [x] 仍只有统一 `/instances*` URL；`operationId: createInstance` 不变
- [x] 新客户端可按 kind 只填对应 `*_config`；旧扁平请求行为不变
- [x] 冲突/错类型 config → `400`
- [x] Alpha freeze 扁平字段仍在；兼容性基线 additive
- [x] 未拆 `InstanceRecord` / lifecycle / console / exec；未改 port 形状

## 备注

`InstanceLifecycleRequest` 的 `*_params` 拆分不在本批次范围（计划 Batch 4，可选后续）。本批次不宣称 real-provider 或 production ready。
