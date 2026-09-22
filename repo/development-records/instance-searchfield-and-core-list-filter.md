# IN-INSTANCE-SEARCHFIELD-AND-CORE-LIST-FILTER

- 批次：`IN-INSTANCE-SEARCHFIELD-AND-CORE-LIST-FILTER`（合并记录两次提交）
- 分支：`fix/instance-searchfield-and-ops-pagination`
- 日期：2026-09-15 ~ 2026-09-16
- 状态：**已修复并实测通过（ani-system）**；部署镜像：`dev-20260915-filter2` / `dev-20260915-filter3`

> 本批次修复 `kjs-study/修复bug/测试问题分析与修复记录.md` 中定位的**两类 Core 层后端过滤缺陷**：
> ① **实例列表搜索/分页/隐藏已销毁**（提交 `85a090e fix(instances)`，Bug-2/3/6）；
> ② **Core 层四类列表关键字/状态/名称过滤失效**（提交 `ce814cb fix(core-api)`，镜像仓库-1、存储列表、网络类-子网）。

---

## 一、实例列表搜索/分页/隐藏已销毁（提交 85a090e，Bug-2/3/6）

### Bug-2：`search_field=id/name` 后端未解析 → 按名称/ID 过滤失效

**根因**：列表 `keyword` 过滤没有按 `search_field` 限定匹配字段，前端传 `search_field=id|name` 但不生效，返回的是"全字段模糊"结果而非按指定字段。

**修复**（[instance_service.go](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/instance_service.go)、[instances.go](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/router/instances.go)）：
- 解析 `search_field` 限定 `keyword` 匹配字段（id / name），未传时多字段匹配；
- **孤儿实例也遵守 `keyword`/`search_field` 过滤**（原实现会无条件合并，绕过过滤泄漏不相关孤儿）。

**单测**：`TestMatchesInstanceListSearchField`（8 用例，覆盖 id/name/全字段匹配与隔离）全 PASS。

### Bug-3：操作历史分页 total 错误、next_cursor 恒空 → 无法翻页

**根因**（[operation_store.go](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/operation_store.go)）：`ListOperations` 不产出全量 total 与游标。

**修复**：操作历史 `total` 返回全量、`next_cursor` 正确翻页（首页 total 为全量、游标非空可翻第二页）。

**单测**：`TestLocalOperationStoreListOperationsPagination` 全 PASS（total 全量、游标翻页、无重复）。

### Bug-6：已销毁沙箱实例仍展示

**根因**：列表默认包含 `deleted` 终态。

**修复**：默认列表**排除 `deleted` 终态**（显式 `state=deleted` 仍可查）；孤儿实例遵守 `state` 过滤。

**单测**：`TestMatchesInstanceListExcludesDeletedByDefault`（9 用例）、`TestLocalInstanceServiceListExcludesDeletedByDefault`、`TestMatchesInstanceStateConsistentForOrphans`（8 用例）全 PASS。

### 实测证据（2026-09-15，ani-test2:30083，镜像 `test2-20260915-b2b3-fix2`）

- search_field=id / name 过滤生效；操作历史 total 全量 + next_cursor 翻页；deleted 默认隐藏、显式 state=deleted 可查。含 live K8s 孤儿 GPU 容器也遵守过滤。

---

## 二、Core 层四类列表过滤失效（提交 ce814cb，Bug-镜像仓库-1、存储、网络类-子网）

### 镜像仓库-1：镜像列表按关键词过滤无效

**根因**（[v1.yaml:7784-7791](file:///e:/go/project/ANI/repo/api/openapi/v1.yaml#L7784-L7791)）：`/registry/images` 无 `keyword` 参数，`RegistryImageListRequest` 无 `Keyword` 字段，handler 不接收关键词。

**修复**：
1. `/registry/images` OpenAPI 新增 `keyword` 查询参数（[v1.yaml:7794](file:///e:/go/project/ANI/repo/api/openapi/v1.yaml#L7794)）；
2. `RegistryImageListRequest` 新增 `Keyword` 字段（[image_registry.go](file:///e:/go/project/ANI/repo/pkg/ports/image_registry.go)），handler `listImages` 解析传入；
3. Harbor/本地适配器按「仓库名 + tag」模糊匹配（`registryMatchesImageKeyword`）；
4. **修复 Harbor 真实缺陷**（[harbor_image_registry.go:392-398](file:///e:/go/project/ANI/repo/pkg/adapters/registry/harbor_image_registry.go#L392-L398)）：删除 repository 循环里的关键词预过滤，避免"关键词仅命中 tag、不命中仓库名"的镜像行被提前 `continue` 跳过、tag 层过滤失效。

**单测**：`TestHarborImageRegistryListImagesKeywordHitsTagOnly`（关键词仅命中 tag 场景）等全绿。

### 存储列表过滤未实现（块/对象/文件/向量存储）

**根因**（[storage_resources.go](file:///e:/go/project/ANI/repo/pkg/ports/storage_resources.go#L473-L477)）：`StorageResourceListRequest` 仅 `TenantID/Limit/Cursor`，无 `Status/Keyword`；四类 handler 只传 TenantID，不解析 status/keyword。

**修复**：
1. `StorageResourceListRequest` 新增 `Status`/`Keyword` 字段；
2. `/volumes`、`/filesystems`、`/objects` OpenAPI 新增 `status`/`keyword` 查询参数（[v1.yaml](file:///e:/go/project/ANI/repo/api/openapi/v1.yaml)）；
3. handler 新增通用过滤 helper `storageMatchesFilters`（状态精确 + 名称关键词大小写不敏感模糊），`listVolumes`/`listFilesystems`/`listObjects` 应用（[storage_resources.go](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/router/storage_resources.go)）；
4. **向量存储（Core 层）**：`listVectorStores` 解析 `status`/`keyword`，新增 `vectorStoreMatchesFilters`，OpenAPI `/vector-stores` get 补参数（[vector_store_resources.go](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/router/vector_store_resources.go)）。

**单测**：存储过滤用例 + 向量存储 `TestVectorStoreListFiltersByStatus/Keyword/StatusAndKeyword` 全绿。

### 网络类-子网名称过滤未实现

**根因**（[network_service.go](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/network_service.go)）：`ListSubnets` 不解析 `name`。

**修复**：`listSubnets` 透传 `name` 参数（[network_resources.go](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/router/network_resources.go)），`LocalNetworkService.ListSubnets` 按名称前缀过滤。

### 实测证据（2026-09-15 ~ 2026-09-16，ani-system:30080，镜像 `dev-20260915-filter2`/`dev-20260915-filter3`）

| 接口 | 过滤 | 结果 |
|---|---|---|
| 子网 `/networks/subnets` | `?name=test-l` 前缀 | 10→1 ✅ |
| 块存储 `/volumes` | `?keyword=test-ly-vol`、`?status=available` | 5→1、5→2 ✅ |
| 文件存储 `/filesystems` | `?keyword=test-ly` | 13→2 ✅ |
| 对象存储 `/objects` | `?keyword=kb-docs` | 41→38 ✅ |
| 镜像 `/registry/images` | `?keyword=42` | 4→1（命中 tag=42）✅ |
| 向量存储 `/vector-stores` | `?status=ready`、`?keyword=test`、组合 | 23→19、23→2、2 ✅ |

过滤后返回项均命中对应状态/关键词，无"过滤失效返回全量"现象。

---

## 三、变更文件清单

| 文件 | 说明 |
|---|---|
| pkg/adapters/runtime/instance_service.go | Bug-2 search_field 解析 + 孤儿过滤 |
| pkg/adapters/runtime/operation_store.go | Bug-3 分页 total/游标 |
| pkg/adapters/runtime/instance_service_test.go | 搜索/隐藏已销毁测试 |
| pkg/adapters/runtime/operation_store_test.go | 分页测试 |
| pkg/ports/workload_runtime.go | 列表请求字段扩展 |
| services/ani-gateway/internal/router/instances.go | 透传 search_field/过滤，默认剔除 deleted |
| pkg/ports/image_registry.go | `RegistryImageListRequest.Keyword` |
| pkg/adapters/registry/harbor_image_registry.go | 仓库名+tag 关键词过滤 + 仓库层预过滤移除 |
| pkg/adapters/registry/local_image_registry.go | 本地关键词过滤 |
| pkg/adapters/registry/*_test.go | 关键词过滤测试 |
| pkg/ports/storage_resources.go | `StorageResourceListRequest.Status/Keyword` |
| services/ani-gateway/internal/router/storage_resources.go | `storageMatchesFilters` helper + 三列表应用 |
| pkg/adapters/runtime/network_service.go | 子网 name 前缀过滤 |
| services/ani-gateway/internal/router/vector_store_resources.go | 向量存储 status/keyword 过滤 |
| api/openapi/v1.yaml | 各类 list 补 status/keyword/name 查询参数 |

## 四、后续项

- 推理服务 / 知识库列表过滤（Service 层）：**按用户要求暂时搁置**——超出 Core 层范围，需扩展 gRPC proto + service 层查询，仅改 gateway 不够。