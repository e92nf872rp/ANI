# OBJECT-STORAGE-BUCKET-FIX-A — 对象存储后端桶一致性五项缺陷修复

完成日期：2026-09-17
对应 Sprint：Sprint 13（Core real provider 与 live gate 收敛）
分支：`fix/object-storage-bugs`（基于 main `10a5e5b`），提交 `1e4588c`
验证结果：本批次相关 `go test` 全通过，`gofmt -l` 改动文件无输出，`git diff --check` 干净，`make validate-architecture` 通过；K8s 测试环境（ani-system 10.10.1.66:30080）实测 28 项断言全 PASS / 0 FAIL

## 实现了什么

修复对象存储（桶）控制面记录与底座 MinIO 之间的五项不一致：创建桶支持 `storage_class` 并按 `access_mode` 推导 `acl`/`acl_label` 回显；下载预签名 URL 改用浏览器可达地址并被真实校验；对象不存在时返回可辨识错误；ACL 变更同时落到控制面存储与 MinIO 桶策略；存储类型变更持久化。

来源：测试异常分析文档 `kjs-study/修复bug/对象存储后端Bug详细分析.md`（未入库）。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/v1.yaml` | 修改 | `CreateStorageBucketRequest` 新增 `storage_class`（enum `standard`/`infrequent_access`，default `standard`） |
| `pkg/ports/object_store.go` | 修改 | 新增 `BucketACLPolicy` 类型与常量；新增可选能力接口 `ObjectStorePolicyApplier`（`ApplyBucketPolicy`） |
| `pkg/ports/storage_resources.go` | 修改 | `StorageBucketCreateRequest` 新增 `StorageClass` 字段 |
| `pkg/adapters/objectstore/minio_store.go` | 修改 | `SignedDownloadURL` 改用 `publicEndpoint`；实现 `ApplyBucketPolicy`（`tenant_read` 走 `PUT ?policy=`，`private` 走 `DELETE ?policy=` 且 404 视为成功） |
| `pkg/adapters/runtime/storage_service.go` | 修改 | 创建桶校验并持久化 `storage_class`、按 access_mode 同步 ACL；`GenerateBucketObjectPresignedURL` 先 hydrate 对象缓存；`SetStorageBucketACL`/`SetStorageBucketClass` 双写控制面存储；新增 `applyBucketACLPolicy` 等辅助函数 |
| `services/ani-gateway/internal/router/storage_resources.go` | 修改 | 创建桶请求透传 `storage_class` |
| `pkg/adapters/objectstore/minio_store_test.go` | 修改 | 新增桶策略租户前缀断言、下载 URL 使用 public endpoint 断言 |
| `pkg/adapters/runtime/storage_service_test.go` | 修改 | `fakeObjectStore` 增加 `ApplyBucketPolicy` 记录；新增 3 用例 |
| `pkg/adapters/runtime/storage_service_store_authority_test.go` | 修改 | store 权威性路径适配 |
| `pkg/adapters/runtime/storage_store_test.go` | 修改 | 注入 `fakeObjectStore` |
| `services/ani-gateway/internal/router/storage_resources_test.go` | 修改 | 新增 `testObjectStore` stub 与断言 |

无 DB 迁移、无生成物变更（`storage_class` 为 additive 可选字段）。

## 缺陷与修法

| # | 现象 | 根因 | 修法 |
|---|---|---|---|
| Bug1 | 创建桶传 `storage_class` 被忽略，`acl`/`acl_label` 不回显 | 请求结构体与 ports 请求均无该字段；创建路径不推导 ACL | 契约 + 结构体 + ports 新增 `storage_class`（枚举校验，非法 `400 UNSUPPORTED`），并按 `access_mode` 推导 `acl`/`acl_label` |
| Bug2 | 下载预签名 URL 用内部集群地址，浏览器不可达 | `SignedDownloadURL` 用内部 endpoint 签名，上传用 public endpoint | 下载改用 `publicEndpoint`；未配置底座时返回明确错误而非 mock 链接 |
| Bug3 | 预签名不存在对象时错误语义混淆 | 预签名前未 hydrate 对象缓存 | 先 `hydrateObjectsFromStore`，不存在时返回 `object %q not found in bucket %s` 的 NOT_FOUND |
| Bug4 | ACL 变更只改内存，重启后丢失；底座桶策略未变 | 无桶策略能力抽象，ACL 未落控制面存储 | 新增 `ObjectStorePolicyApplier` 可选能力 + MinIO 租户前缀桶策略（Resource 限定 `arn:aws:s3:::<bucket>/<tenantID>/*`）；ACL 归一（`public_read`→`tenant_read`）后双写存储与底座 |
| Bug5 | 存储类型变更不持久化 | 变更未写控制面存储 | `SetStorageBucketClass` 增加 `upsertBucket` 持久化 |

## 完工标准达成

- [x] 本批次相关 `go test`（objectstore / runtime storage 相关 / ani-gateway）全通过
- [x] `make validate-architecture` 通过，`gofmt -l` 改动文件无输出，`git diff --check` 干净
- [x] 镜像 `docker.changqingyun.cn/ani/ani-gateway:dev-20260917-objstore` 构建推送成功（digest `sha256:ee3c4bb649e65af9c48c0d3602a7e513d101092fa5040c56a033bd5199360942`）
- [x] 部署至 ani-system（etcd 预检 42% 安全 → `kubectl set image` 未改 env → rollout 成功 → healthz 200）
- [x] 实测 28 项断言全 PASS / 0 FAIL

## live 实测证据（ani-system 10.10.1.66:30080）

| 断言组 | 关键证据 |
|---|---|
| Bug1 创建与持久 | 创建桶 `access_mode=public_read` + `storage_class=infrequent_access` 回显 `acl=tenant_read`/`acl_label=租户内读`/`storage_class=infrequent_access`，重列一致；非法枚举 `glacier` → `400 UNSUPPORTED` |
| Bug2 预签名 | `upload_url`/`download_url` host 均为 `10.10.1.66:30900`；真实 PUT 200、GET 200（body `hello`） |
| Bug3 对象不存在 | `404 NOT_FOUND`，消息含 `object "probe/does-not-exist-….txt" not found in bucket probe-obj-…`；非法 method `PATCH` → 400 |
| Bug4 桶策略双向 | `acl=tenant_read` 时裸 URL 匿名 GET **200**；切回 `private` 后匿名 GET **403**（MinIO `AccessDenied`）——证明策略真实落到 MinIO；ACL 切换后重列持久 |
| Bug5 存储类型 | `standard`/`infrequent_access` 双向切换 200，重列持久 |
| 回归 | 桶列表、实例列表仍 200 |

## merge origin/main 后复测（2026-09-17，镜像 `dev-20260917-objstore2`）

`git merge origin/main 143c4fe` 后代码树统一，重新构建部署复测（digest `sha256:b0886c7e6167f222ed4c9d7e6dd4b5b8c8309aaa5ec0299654f18d220a3869d7`）：

- 部署：etcd 预检 912990208/2147483648（43%）→ `kubectl set image -n ani-system`（未改 env）→ rollout 成功 → healthz 200（Pod `ani-gateway-6778c5789-qmx62` 1/1 Running）。
- 本批次 28 项断言复跑 **28 PASS / 0 FAIL**（预签名 host 仍为 `10.10.1.66:30900`；`tenant_read` 裸 URL 匿名 GET 200、切回 `private` 后 403；ACL/存储类型切换重列持久）。
- 网络存储系列回归 **19 PASS / 0 FAIL**：历史安全组规则列表 200（3 条）、创建带预设规则安全组后 `GET /rules` 立即 3 条且落库、删除安全组 200 且明细级联清零、有存活子网时 `DELETE /vpcs/{id}` 409、清理子网后 200、实例列表 `vpc_id`/`subnet_id` 过滤与全量结果精确一致（10/10）且未知值返回 0。
- `network_security_group_rules` 幂等回填在 ani-system `INSERT 0 0`（无断层）；ani-test2 补齐 3 行后摘要/明细全对齐。
- 探针脚本自身两处缺陷（非产品缺陷）：`/instances?limit=200` 超上限返回 400（上限 100）；`vpc_id`/`subnet_id` 嵌套在 `network` 对象下而非顶层字段。

## 备注

- **并集构建（已不再需要）**：ani-system 原运行 `dev-20260917-multival`（网络存储分支构建），本批次分支基于 merge-base，首次构建曾保留原分支代码只覆盖本批次文件（两个重叠文件用并集版本）。2026-09-17 merge `origin/main 143c4fe` 后代码树统一，已改为整体覆盖构建机源码树后构建，并集方式废弃。
- **构建机代码树漂移**：部署构建机（192.168.18.35）`/root/ani-build` 与分支树存在漂移（490 个 `.go` 中 98 个 md5 不一致）。本轮改用 `git archive HEAD` 打包 `pkg`/`runtimeadmin`/`services/ani-gateway` 整体覆盖（旧树备份为 `_backup_premerge.tar.gz`），漂移问题在本次构建路径上已消除。
- **Bug5 有意不做底座 apply**：MinIO 的 storage class 是对象级属性，桶级无法等价表达，故只做控制面持久化，不做"假成功"的底座调用。
- **`applyBucketACLPolicy` 静默跳过语义**：`objectStore == nil` 或底座未实现 `ObjectStorePolicyApplier` 时返回 nil（保持 local profile 兼容），与原分析文档"无底座应报错"的建议不同。
- 遗留：桶详情无独立 `GET /buckets/{id}` 端点（实测只能经列表接口回查，本次未新增端点）；`public_read` 在响应中归一为 `tenant_read`，语义上并非真正的公网匿名读。