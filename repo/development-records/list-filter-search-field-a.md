# list-filter-search-field-a：列表过滤参数统一（search_field 约定）

> 分支：fix/instance-searchfield-and-ops-pagination
> 状态：已实施并实测通过（2026-09-16，ani-system）
> 依据：`kjs-study/修复bug/已修复的bug.md` 第 4 节「统一改造建议」用户已确认，前端所有列表统一传 `search_field=name|id&keyword=…`。

## 背景与目标

前端列表统一约定传 **`search_field=name|id&keyword=…`**（如 `search_field=name&keyword=test`）。此前后端各列表参数不统一：

- 网络类（VPC/子网/安全组）后端收 **`name`**（前缀匹配）；
- 存储/向量后端收 **`keyword`**（缺省仅按 name/bucket/key 模糊）；
- 两者均**不解析 `search_field`**，且**多数不支持 `search_field=id` 的按 ID 搜索**。

本次将 8 个列表全部收敛到 `search_field + keyword`，`search_field ∈ {id, name}`，缺省保持旧行为，并向后兼容旧参数。

## 改动清单

| 列表接口 | handler | 过滤语义 |
|---|---|---|
| 块存储 `/volumes` | `storage_resources.go:listVolumes` | id 或 name 模糊 |
| 文件存储 `/filesystems` | `listFilesystems` | id 或 name 模糊 |
| 对象存储 `/objects` | `listObjects` | id 匹配；缺省按 bucket/key 模糊 |
| 对象存储桶 `/buckets` | `listStorageBuckets` | id 或 name 模糊 |
| 向量存储 `/vector-stores` | `vector_store_resources.go:listVectorStores` | id 或 name 模糊 |
| VPC `/networks/vpcs` | `network_resources.go:listVPCs` | id 前缀；name 前缀 |
| 子网 `/networks/subnets` | `listSubnets` | id 前缀；name 前缀 |
| 安全组 `/networks/security-groups` | `listSecurityGroups` | id 前缀；name 前缀 |

### 核心实现

- **存储/向量**：`storageListFilters(c)` 解析 `search_field∈{id,name}`，`storageMatchesFilters(recordState, spec, idPart, nameParts...)` 在 `search_field=id` 时按资源 ID（`VolumeID/FilesystemID/ObjectID/BucketID/StoreID`）模糊匹配，否则按 name/bucket/key 模糊；`vectorStoreMatchesFilters` 同理（`StoreID`/`Name`）。
- **网络类**：新增 `networkNameFilter`/`networkIDKeyword` 两个 helper 将 `search_field`+`keyword` 归一为 `Name`/`Keyword` 两个请求字段（`NetworkResourceListRequest` 新增 `Keyword string`），`LocalNetworkService.ListVPCs/ListSubnets/ListSecurityGroups` 支持按 `VPCID/SubnetID/SecurityGroupID` 前缀过滤；`name` 保持前缀匹配。
- **OpenAPI**：v1.yaml 为 8 个 list 补 `search_field`（enum id/name）参数与 `keyword` 参数声明；旧 `name`/`status`/`state`/`keyword` 保留为向后别名。
- **兼容**：前端未传 `search_field` 时行为与旧版一致（网络类 name 前缀、存储/向量 keyword 模糊）。

## 验证

- 新增单测：`TestStorageHTTPVolumeListFiltersByKeywordAndStatus`（`search_field=name/id` 断言）、`TestStorageHTTPBucketListFiltersBySearchField`、`TestVectorStoreListFiltersById`、`TestVectorStoreListFiltersByNameViaSearchField`，全部通过。
- `go build ./services/ani-gateway/... ./pkg/...`、`go vet`、`git diff --check` 通过。
- 实机实测（2026-09-16，ani-system，镜像 `dev-20260916-searchfield` → `dev-20260916-searchfield2`）：

| 列表 | search_field=id | search_field=name |
|---|---|---|
| `/volumes` | ✅ 命中 | ✅ 命中 3(test-) |
| `/filesystems` | ✅ | ✅ 命中 5(test-) |
| `/objects` | ✅ | （对象无 name，缺省按 bucket/key） |
| `/buckets` | ✅ 命中 | ✅ `keyword=test` 命中 3 个 `test-ly-bucket*`（与前端截图一致） |
| `/vector-stores` | ✅ | ✅ |
| `/networks/vpcs` | ✅ | ✅ 命中 2(test-vp) |
| `/networks/subnets` | ✅ | ✅ |
| `/networks/security-groups` | ✅ | ✅（实测新建 `sf-sg-*` 后验证） |

- 向后兼容实机确认：网络类 `name=`、存储/向量 `keyword=` 行为保持旧版。

## 备注

- 推理服务/知识库（Service 层）过滤不在本次范围，沿用 `已修复的bug.md` 既有搁置决策。
- 无数据库 schema / Proto / SDK 破坏性变更；仅 Gateway router + pkg 层 OpenAPI 契约与外发参数（additive）。