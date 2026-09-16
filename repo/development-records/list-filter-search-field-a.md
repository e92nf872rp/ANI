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

## 补充：状态过滤参数统一（status → state，2026-09-16）

依据 `kjs-study/修复bug/已修复的bug.md`「数据库状态列名与接口参数命名核查（state vs status）」的结论：DB 统一 `state`，但接口层面实例/网络类用 `state`、四类存储接口用 `status`。为统一，将四类存储 GET 列表的状态过滤参数从 `status` 更名为 `state`：

| 列表接口 | 原参数 | 现参数 |
|---|---|---|
| 块存储 `/volumes` | `status` | `state` |
| 文件存储 `/filesystems` | `status` | `state` |
| 对象存储 `/objects` | `status` | `state` |
| 向量存储 `/vector-stores` | `status` | `state` |
| 对象存储桶 `/buckets` | —（原本无状态过滤参数） | 不变 |

- **OpenAPI**：`api/openapi/v1.yaml` 对应 4 个 list 的 `name: status` → `name: state`；`/buckets` 无状态参数，不改。
- **handler**：`storage_resources.go:storageListFilters(c)` 统一读取 `state`（`c.Query("state")`）；该函数同时被 volumes/filesystems/objects/buckets/vector-stores 复用，一处改动即覆盖。
- **单测**：`TestStorageHTTPVolumeListFiltersByKeywordAndStatus` 与 `TestVectorStoreListFiltersByState(AndKeyword)` 改用 `?state=` 断言，全部通过。
- 该更名属查询参数外发名变更，未触碰 DB 列、DB schema 或 Proto。