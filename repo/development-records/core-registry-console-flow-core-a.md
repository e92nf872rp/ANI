# CORE-REGISTRY-CONSOLE-FLOW-CORE-A — Registry image purpose implementation

完成日期：2026-07-22
对应 Sprint：Sprint 13（并行 Registry Console Flow 实现切片）
验证结果：`go test ./pkg/adapters/registry ./services/ani-gateway/internal/router -count=1`、`make test`、`make validate-architecture`、`git diff --check` 通过

## 实现了什么

在 `CORE-REGISTRY-CONSOLE-FLOW-CONTRACT-A` 契约已批准后，补齐 Core 后端实现闭环：

- `ports.RegistryImageListRequest` 增加 `Purpose`，`ports.RegistryImage` 增加 `Purpose`。
- Gateway `GET /registry/images?purpose=` 透传 purpose query，并在响应 item 中返回 `purpose`。
- local registry profile 提供四类确定性镜像：`container`、`gpu`、`sandbox`、`system`，并按 purpose / repository / tag / scan_status 过滤。
- Harbor adapter 在没有独立 purpose 元数据的前提下，按 repository/tag 命名保守派生 purpose：`gpu*`、`sandbox*`、`system*`，默认 `container`，并支持 purpose 过滤。

## 边界

- 不实现 `POST /instances` create image gate；契约中的 createInstance 422 语义属于后续实例模块切片。
- 不实现 Console 前端页面或 GPU 创建 Dialog 改动。
- 不实现 BOSS、权限、配额、GC、robot credentials 页面。
- 不改变默认 demo image 行为；只有请求显式传入 `image` 时触发 registry gate。
- Harbor purpose 为当前最小兼容派生逻辑，不声明 Harbor 已具备持久化 image purpose metadata。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/image_registry.go` | 修改 | Registry image list request/response 增加 purpose |
| `pkg/adapters/registry/local_image_registry.go` | 修改 | local profile 四类镜像 catalog 与 purpose 过滤 |
| `pkg/adapters/registry/harbor_image_registry.go` | 修改 | Harbor purpose 派生与过滤 |
| `services/ani-gateway/internal/router/registry_resources.go` | 修改 | `/registry/images?purpose=` query 透传和响应序列化 |
| `*_test.go` | 修改 | local adapter、router response/query 回归测试 |

## 完工标准达成

- [x] Core registry image response exposes `purpose`
- [x] `GET /registry/images?purpose=gpu` 只返回 GPU purpose 镜像
- [x] Gateway route 把 purpose query 转发到 port
- [x] 不包含 Console/BOSS/权限实现
- [x] 不包含 instances 模块实现
