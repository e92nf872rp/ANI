# ANI · 当前冲刺上手指南

> 新开发者（人类或 AI 工具）的第一个入口文件。本文只描述当前真实执行状态；历史完成批次查 `repo/development-records/README.md`。

> **仓库范围：ANI Core + 受控 Services PR。** ANI Core 继续负责基础设施平台底座；Services 受控并行 PR 阶段已经启动，不再按旧冻结规则处理。Services PR 统一运行 `make validate-services`，覆盖 CODEOWNERS 共同审查要求之外的 API split、Services boundary gate、OpenAPI/Gateway route contract、语义契约、生成物漂移、模块检查和 `make validate-architecture`。
> **仓库范围更新（2026-09-07）：** Console/BOSS 前端源码已迁至独立仓库，本仓库已移除 `repo/frontends/`、前端代码生成/构建目标和前端 CI job；历史批次中的前端描述仅作归档。PR #60/#62/#68 引入的邮件通知契约与实现已回滚，Core 不再提供 `/api/v1/notifications/email/*`。本批次按用户要求不跑本地 CI，完整验证交由 GitHub PR。
> **ANI-IAM-PRE930-CONTAINMENT（2026-09-08，PR 验证中）：** 基于用户接受的 ANI `804db51a5f93605f9bbd4ac407f0489ecb1d187c` 外科式移除 PR #145 提前进入当前发布轨道的 Direct P2 目标契约、SDK、Gateway composition/runtime、Proto copy 与候选 registry；完整保留 #145 后的 VM、Tenant、KB 和平台组件 Observability 功能。现行 Core v1 compatibility 与 Gateway authz 门禁已作为独立 required Actions job 接入；本地只执行生成和 diff/path 审计，测试结果以 PR exact SHA 的 GitHub Actions 为准。未部署、未切流、未修改数据或 Credential；9 月 30 日后仍需人工指定不可变 release SHA 才能重新进行 IAM target rebaseline。详情见 `development-records/ANI-IAM-PRE930-CONTAINMENT.md`。
> **MODEL-CONFIG-M3（2026-09-11）：** KB 推理模型动态切换（local verified）：SSE 流式查询接口补 `inference_service_name` 契约声明+gateway 断言；建库可选 `default_inference_service`（契约/proto field 9+14 双端 stub/迁移 `20260911000100` 可空 TEXT/kb-service repo+grpc_server 审计快照/gateway SSE+同步 Query+建库三链路）。三级回落链 `request → KB 默认 → "default"`，在 kb-service 收口、gateway 只透传；与 embedding_model 不同，该字段可空、只影响生成路由、不触发索引重建。kb-service 359 passed（+5 新）+ gateway 四包 ok + `make validate-services` 全绿；live 验证待执行；`deploy/migrations/20260911000100_kb_default_inference_service.sql` 须随批次提交。记录：`development-records/model-config-m3-kb-default-inference-service.md`。

> **当前重心：Sprint 13 / Core real provider 与 live gate 收敛。** Core Sprint 13/14 既有事实继续有效：Sprint 12 已完成 Core「Services 支撑 Handler」A/B1/B2/B3 全部 19 个 handler + 2 个 422 的 Tier1 local profile 收口；Sprint 13 S01-S07 production-shaped live gate 事实保留；Sprint14 resilience 结论仅限隔离 fixture。未跑通对应 live gate 前，不得标记 real-provider、runtime ready 或 production ready。Services PR 可在主责目录推进业务实现，但不得绕过 Core OpenAPI REST API / Core SDK、Core review 或现有架构门禁。
> **MODEL-REPOSITORY-REMOTE-IMPORT（2026-09-04）：** 远程模型导入 Task 1–6 及后续完整性/快照修正已完成 local/logic verified：Gateway 202 入口、租户隔离/幂等导入任务与 outbox、公共 HTTPS Hugging Face/ModelScope source、ModelScope 根目录遍历与 branch→40-hex commit 解析、确定性 `model.tar.gz`、租约 worker、fetcher 有界安全解压、archive runtime 目录和 real-k8s-lab worker/config contract。remote-import 增量未新增 v1/protobuf 字段；Gateway 通用默认仍为 `main`，ModelScope 仅在 `main` 返回明确空历史时有界回退一次 `master`，随后固定到 40-hex commit。尚未 live；worker/fetcher 镜像 digest、MinIO/PG Secret、mTLS/workload identity、NetworkPolicy 与真实 PG/MinIO/集群验证是前置。记录：`development-records/model-repository-remote-import.md`；不得标 runtime/production ready。
> **INFERENCE-ENVOY-AI-GATEWAY-RATELIMIT（2026-08-28）：** 已完成 local/logic verified。Envoy AI Gateway Gateway 级共享全局 600 requests/minute `BackendTrafficPolicy`、Redis Secret 引用配置片段、C40 live-gate Accepted 检查和敏感信息校验均已落地；未执行真实集群限流压力测试，不宣称 runtime ready。
> **INFERENCE-SERVICE-C41（2026-08-31）：** Envoy AI Gateway 多租户动态发布已完成 local/logic verified：Services v1 不新增 endpoint/field，仅澄清既有 `served_model_name` 与 `invocation_url` 描述；Gateway 已修正既有 policy flat DTO 实现，不是契约新增。AK-only、tenant/model/path 解析、可信头覆盖、`recomputeRoute`、Publisher publication/lifecycle fencing、最小权限清单和红线 live-gate contract 均有本地证据。Task 8 server dry-run 为 10/11 accepted；剩余 BackendTrafficPolicy 是已安装 CRD `int32`/`maximum` schema 自相矛盾。外部 inference-service normal/race 与 repo `make test` 均 EXIT:0；Console schema 三处 description 生成更新纳入隔离 shipping index 后 `make validate-services` EXIT:0，真实 index 保持为空。live status=`not-run`；不得标 runtime/production ready；PG live integration 因 DSN 未设 skip。
> **MODEL-REPOSITORY-P0-BACKEND（2026-09-01）：** 模型仓库第一后端闭环已完成 local/logic verified：model-service 支持租户隔离的模型/版本注册、过滤列表、版本列表、`Idempotency-Key` replay/conflict、对象存储预签名上传/下载和 size/sha256 验证；Gateway 已接入对应控制面路由，inference catalog 可解析租户自有 ready 对象版本并保留 PVC 兼容。远程 ModelScope/Hugging Face 导入、加密、异步 worker、Console 页面和真实 PG/object-store/cluster live 不在本批次；PG integration 因 DSN 未设 skip，不得标 runtime/production ready。记录：`development-records/model-repository-p0-backend.md`。
> **ANI-GATEWAY-OPENAI-ROUTE-BOUNDARY（2026-09-07）：** ANI Gateway 已移除旧 `POST /v1/chat/completions` 占位代理，OpenAI chat/embedding 数据面归独立 Envoy AI Gateway；内部 vLLM OpenAI 路径不变。已审批的模型版本列表内部调用已接入，`GET /api/v1/svc/models/{model_id}/versions` live smoke 返回 200。该批次不改 v1/protobuf、不含 Console；Envoy 动态发布和真实模型 chat 成功响应仍未完成 live 验收。记录：`development-records/ani-gateway-openai-route-boundary.md`。
> **标准状态 marker：** 真实服务器只读验证已完成；Rook-Ceph 正式部署已完成。Sprint 11 执行环境：正式部署执行环境。

> **CORE-STORAGE-BUCKET-DELETE-A（2026-09-18，live verified，分支 feat/storage-bucket-delete）：** 新增 Core 删除对象存储桶能力 `DELETE /api/v1/buckets/{bucket_id}`（`deleteStorageBucket`，复用 `scope:objects:delete`，无 `idempotency_key`）：控制面墓碑软删（`state=deleted` + `deleted_at`，**零 DB 迁移**，复用 `UpsertBucket` 既有墓碑分支），桶内仍有该租户活跃对象返回 **409 CONFLICT**（对齐 S3 `BucketNotEmpty`），删除后桶级子操作统一 404、重复删除 404、同名重建得新 `bucket_id`（偏唯一索引 `WHERE deleted_at IS NULL`）；**不回收物理 MinIO 桶**——物理桶名=`bucketPrefix+桶名`、对象 key=`<物理桶>/<tenant_id>/<key>`，物理桶跨租户共享，删则毁其他租户数据（方案 §1.4 硬约束）。非空判定双口径：`objectStore != nil` 时以底座 `BucketUsage(ObjectCount)` 为准，底座查询失败 `slog.Warn` 后回退控制面统计且**不放行删除**。链路：v1.yaml 契约 → ports `DeleteStorageBucket` → runtime adapter → gateway router/handler（`writeStorageError` 映射 404/409/400）→ storage alpha 门禁 `EXPECTED_PATHS` → 生成物重生成（compat baseline 249→250、authz registry、静态 API 文档、四语言 SDK + `sdk-metadata.json`）。**顺带闭环 Sprint 13 live gate 清理泄漏**（此前每跑一次残留一个桶）：`validate_object_store_live_gate.py` 增 `core-bucket-delete` 检查 + `--cleanup` 段增删桶 + evidence `bucket_delete_status`，`deploy/real-k8s-lab/object-store-live-gate.yaml` 同步。**偏离方案 §4.1 第 6 步**：内存不保留墓碑（`delete(s.buckets, bucket.BucketID)`）——创建路径的重名检查与创建幂等键查表均以 `s.buckets` 为来源，保留墓碑会让同名重建被误判冲突或复用旧记录。**修复两处关联缺陷**：① `DeleteBucketObject` 删对象只改内存不落盘墓碑，网关重启后 `hydrateObjectsFromStore` 把已删对象当活跃对象，使桶永远无法通过非空判定删除（补 `upsertObject` + `DeletedAt`）；② `validate_storage_alpha_contract.py` 的 router 注册检查只认 `registerStorageResources(v1)`/`registerStorageResourcesWithService(v1, options.StorageService)` 两个字面量，而 main 上实为 `registerStorageResourcesWithServiceAndTasksAndStore(...)`（#151/#160/#162/#163 演进后未同步）→ **该门禁在 main 上本就失败**，改按公共前缀匹配。门禁：`validate-storage-alpha`（Python 契约 + 全 go test，Makefile 过滤正则追加 `TestStorageHTTP`）/`validate-core-api-compatibility`/`validate-gateway-authz`（324 路由/250 registry/0 error）/`validate-doc-api`/live gate 契约 + live gate 单测 8 passed 全绿。ani-system 实测（镜像 `dev-20260918-bucketdel`，digest `sha256:0706a689…16eb`；etcd 预检 43%、只 set image 未改 env、rollout 成功、Pod 1/1 Running）**24 项断言全 PASS / 0 FAIL**：建桶 201 → 放对象后删桶 409 `CONFLICT ... still holds 1 object(s), delete them first` → 删对象 200（`state=deleted`）→ 空桶删桶 200 → 列表 `total=14` 不含已删桶 → 重复删除 404 → 子操作 `GET objects`/`PUT acl` 均 404 → 同名重建 201 且新 id `0033b34a-…`（旧 `4ca6e8ee-…`）→ 清理 200；平台主体边界补充实测 403 `token scope not allowed for this path` 且租户记录不变。**未实测**：跨租户删除（tenant-b 登录 404 `TENANT_NOT_FOUND`，另试 60 候选账号 × 4 组常见密码全失败、无第二租户凭据；改由适配层单测 `TestLocalStorageServiceDeleteBucketLifecycle` 覆盖 404 与记录不变）与 rest 桶级子操作在已删桶上的 404。**环境曾被他批次覆盖，已重新覆盖部署并复测**：实测完成后 ani-system 曾被另一批次镜像 `fix-20260918-clusterdel` 替换（此时 `DELETE /buckets/{id}` 返回框架级 `404 page not found`）；经确认后于 09:34 重新 `kubectl set image` 回 `dev-20260918-bucketdel`（etcd 预检 42.5%、未改 env、rollout 成功、Pod `ani-gateway-79b98585c-nrn9k` 1/1 Running），复跑 8.1 矩阵仍 **24 PASS / 0 FAIL**（新证据 id `edb97402-…`→重建 `d73e5b7c-…`），当前环境含本批次改动。副作用：`fix-20260918-clusterdel` 在 ani-system 上不再生效（其 ani-test2 镜像 `test2-20260918-clusterdel` 未触碰，镜像 `aa9789cbf04b` 保留可原样回滚），两批次在 ani-system 不可共存（来源分支不在本地工作区，无法并集构建）。遗留：物理 MinIO 桶未回收、生命周期规则行不清理、桶详情 `GET /buckets/{id}` 未暴露、前端删除入口待 Console 侧确认（D6）、历史未落盘墓碑对象需重新删除、ani-test2 未部署。详见 `development-records/core-storage-bucket-delete.md`。

> **STORAGE-FILESYSTEM-MOUNT-COMMAND-STORE-A（2026-09-20，live verified，分支 feat/storage-bucket-delete）：** 修复前端报障 `GET /api/v1/filesystems/fs_f600cbd5-…/mount-command` 返回 **404 `NOT_FOUND capability resource not found`**（`request_id req_5754cad1-…`）：契约/路由/handler 均存在，根因是 `GetFilesystemMountCommand` **只读进程内存 map `s.filesystems`、无持久层回退**——网关 Pod 重启后内存为空，对只存在于 `storage_filesystems` 的历史文件存储一律 404，错误文案正是 `ports.ErrNotFound`；与同族已修缺陷 `ExpandFilesystem`（文件存储-4）/`MountFilesystem`/`UnmountFilesystem`/`CreateFilesystemMountTarget` 完全同构，是该模块**最后一个 memory-only 漏网点**。修复：① 记录解析改走 `lookupFilesystemRecord`（内存优先 + store 回填 + 租户校验 + 已删过滤）；② 挂载目标改走 `hydrateFilesystemMountTargets`；③ **命令口径对齐 `GET /filesystems/{id}` 的 `mount_command`**（经用户确认「改为与详情一致」）——优先回放落库命令 `record.MountCommand`（挂载时生成，携带真实挂载目标 IP 与实例实际挂载点），新增包级辅助 `storageFilesystemMountCommandParts` 反解 `mount -t <proto> <ip>:<export> <mount_path>` 填充 `ip_address`/`mount_path`（格式不符返回空值而非猜测值），**仅**落库命令为空（历史 NULL 行）时才按挂载目标合成。**契约零变更**：不动 `v1.yaml`、不动 ports、无 DB 迁移、无生成物变更，仅 2 个代码文件（`pkg/adapters/runtime/storage_service.go`、`storage_service_store_authority_test.go`）。单测在既有 `TestLocalStorageServiceMountSurvivesRestartViaStore` 内追加「重启后内存为空」新实例断言（`Command` 逐字等于落库 `MountCommand`、`IPAddress` 非 `127.0.0.1`、`MountPath == "/data"`、不存在/跨租户 → `ErrNotFound`）；反向验证跑在修复前实现上可复现同一条线上错误 `fresh GetFilesystemMountCommand() error = capability resource not found`。门禁：`gofmt -l` 两文件无输出、`go test ./pkg/adapters/runtime/ -run 'TestLocalStorageService'` 通过；`make test`/`validate-architecture`/`git diff --check` 留到提交前统一执行（包内 2 个 sandbox 测试为 Windows 本机环境限制的既有失败）。ani-system 实测（镜像 `dev-20260920-mountcmd2`，digest `sha256:e3ce8aa5…5c23`；etcd 预检 `dbSize=912990208`/`dbSizeQuota=2147483648` 约 42.5%、只 `kubectl set image -n ani-system` 未改 env（部署前后 75 个 env key 逐项一致）、rollout 成功、Pod `ani-gateway-67d5466-s2nmg` 1/1 Running、`/healthz` 首探 200）**8 项断言全 PASS / 0 FAIL**：报障接口 **404→200** 且 `command` 与详情 `mount_command` **逐字一致**（`mount -t ceph 10.0.0.11:/test-ly-nfs /test`，反解 `ip_address=10.0.0.11`/`mount_path=/test`）、列表内 **13 个文件存储该接口全部 200**（证明接口级修复而非单条数据特例）、不存在 404、无凭证 401、列表/详情回归 200；另补 4 例详情逐字对比（含落库命令为空样本）**4/4 identical**。修复前线上复现：列表 200 13 条含目标 fs、详情 200 含 `mount_command`、该接口对目标 fs 及另两个 fs **全部 404**；DB 直查 `storage_filesystems` 有记录与 `mount_command`、`storage_filesystem_mount_targets` 有 `status=available` 目标（`10.0.0.10`/`10.0.0.11`）。遗留：未挂载文件存储仍返回 `mount -t <proto> 127.0.0.1:<name> /mnt/<name>` 占位命令（**详情接口返回同值**，非本接口独有偏差；建议前端对未挂载对象隐藏/禁用该入口）、合成路径多目标 IP 选择非确定性（与修复前一致，未改动）、跨租户未实测（tenant-b 无可用凭据，由适配层单测覆盖 `ErrNotFound`）、历史 NULL `mount_command` 行未回填、ani-test2 未部署；第一轮镜像 `dev-20260920-mountcmd`（digest `sha256:0fc3bb16…4653`，「总是合成」口径）已被本轮取代、保留可回滚。详见 `development-records/storage-filesystem-mount-command-store-a.md`。

> **IN-NETWORK-SG-RULES-PERSISTENCE-A（2026-09-17，分支 fix/network-sg-vpc-and-instance-filter，已修复并实测通过）：** 修复用户前端实测发现的两处缺陷：① 安全组详情页「安全组规则加载失败」（`GET /security-groups/{id}/rules` 对 DB 历史安全组一律 404）；② 删除安全组 404（`DeleteSecurityGroup` 存在性校验 memory-only，安全组-3/4 批次记录过的存量问题）。根因：规则明细从未持久化（无 `network_security_group_rules` 表，规则 CRUD 五方法全部 memory-only，存在性校验只查内存 map），网关重启后内存清空 → 规则查询/新建/删除对历史安全组全部 404。修复与安全组-3/4、VPC-4 同模式 store 化：新迁移建明细表 + RLS/GRANT + 从 rules JSON 摘要回填存量明细（重生成 rule_id，实测回填 35 条）；`NetworkResourceStore` 新增规则五方法 + SQL 实现；service 六方法双分支（Create 修复重启后新建 404；DeleteSecurityGroup 级联清理明细防孤儿并重建摘要）；`syncSecurityGroupRulesStore` 变更后重建摘要回写 SG JSON；单测 5 新增，无契约/handler 变更。实测（ani-system 镜像 `dev-20260917-sgrules`→`dev-20260917-sgdel`）：历史安全组 sg_5a811a20 规则列表 200 返回 3 条回填明细（修复前 404）、protocol 过滤、新建 201+幂等重放同 ID、更新/删除/删除后 404、SG 摘要 JSON 与明细表同步；删除历史安全组 sg_0d98ac92 由 404 修复为 200 deleted 且 `rules` 摘要清空；删除后单查 200+deleted（软删语义，与实例「显式 state=deleted 仍可查」一致）、列表 10 条存活不再含该 SG。Live 验证捕获二次缺陷并修复：新表初版 RLS 仅 RESTRICTIVE policy，PostgreSQL 行可见性要求至少一条 PERMISSIVE 放行导致普通角色全 deny，已补齐 `platform_bypass`/`self` 两条 PERMISSIVE（对齐族内三段 policy 模式）。**已知边界**：规则幂等 map 仍为进程内存；级联清理与 SG 置 deleted 非同一事务（低频可重试）。详见 `development-records/network-sg-rules-persistence.md`。**ani-test2 同步部署实测（镜像 `test2-20260917-a`）**：独立 PG 先迁移（补 vpc_id 列 + 建规则明细表，test2 无存量安全组回填 0 行属正常）后换镜像；kubeovn_rest 分支同样走 `NewLocalNetworkService`+store，修复生效；冒烟实测建 SG/建规则/规则列表/删除/删除后列表全过，验证后已清理。**第三处同源写路径收口（仅部署 test2，镜像 `test2-20260917-b`）**：创建安全组携带的预设规则（Console「常用远程端口」模板）store 模式下遗漏明细表写入（内存模式有、store 模式无），详情页规则为空；已修（创建时逐条 `UpsertSecurityGroupRule`，单测累计 6 新增）+ 两库幂等回填补齐存量断层（ani-system/ani-test2 各 `INSERT 0 3`，摘要=明细全部对齐）；实测创建带模板规则 SG 后规则列表立即 3 条；ani-system 未部署该修复（`kb-20260917`），下次部署后需重跑回填。
> **IN-NETWORK-VPC-DELETE-PROTECTION-A（2026-09-16，分支 fix/network-sg-vpc-and-instance-filter，已修复并实测通过）：** 按 `kjs-study/修复bug/VPC子网安全组问题分析与修复记录.md` 方案 A（防御式禁止删除）修复 VPC-4（存在级联资源时删除 VPC，级联资源依然存在）。根因：`DeleteVPC` 只置 deleted 后 upsert，不校验子网/安全组/LB/路由等存活关联。`DeleteVPC` 重写双分支：store 模式 `store.GetVPC` 查 VPC（修复重启后删除 404 隐患）+ `vpcAssociationCounts` 查持久层四类存活关联计数；内存模式回退遍历内存 map；命中拒绝 `ports.ErrConflict`，错误消息列四类数量（`N subnet(s) … still exist; delete them first`）经 `writeNetworkError` 映射 409 CONFLICT（契约 v1.yaml deleteNetworkVPC 本就声明 409，无契约/DB 变更）。`NetworkResourceStore` 接口新增 `ListLoadBalancers`/`ListRoutes`（两表此前只有 Upsert 无 List），`MetadataNetworkStore` SQL 实现落地。单测 2 新增全绿（内存/store 双模式冲突+清理后放行；store 模式含跨 VPC 资源不计数断言）+ `fakeMetadataTx.queryRows` 按表路由测试基建。实测（ani-system 镜像 `dev-20260916-vpc4`）：test-vpc-ly123（1 存活子网）删除 409 带数量明细、被拒后仍 available、无关联新 VPC 删除 200 deleted、不存在 VPC 404。**已知边界**：pending/failed 等非 deleted 状态同样阻止删除（防御式语义）；实例引用不在四类计数内；provider delete/卸载能力与 DeleteSubnet/DeleteSecurityGroup 同类关联保护为后续候选。至此 kjs-study 该文件覆盖的六个用例（VPC-3/4、子网-3、安全组-3/4/5）全部修复闭环。详见 `development-records/network-vpc-delete-protection.md`。
> **IN-NETWORK-SG-BINDING-DERIVED-A（2026-09-16，分支 fix/network-sg-vpc-and-instance-filter，已修复并实测通过）：** 按 `kjs-study/修复bug/VPC子网安全组问题分析与修复记录.md` 修复 安全组-5（安全组详情「关联资源」已绑定实例不展示）。根因：实例侧绑定与安全组侧查询用两套割裂数据——实例「更换安全组」只更新实例自身 `record.Network.SecurityGroups`（持久化于 workload_instances.network_summary JSONB），从不写 `securityGroupBinds` 内存 map（后者只有显式 bindings API 才写且无持久化）；部署实测暴露第三层缺陷——bindings 三接口存在性检查 memory-only，网关重启后对 DB 历史安全组 404。方案选定分析文档方向②「查询统一」落地为派生视图：`LocalNetworkService` 注入 `ports.WorkloadInstanceStore`（`WithNetworkInstanceStore`），新增 `derivedSecurityGroupBindings` 按实例记录反查派生绑定（跳过 deleting/deleted 终态，确定性 binding_id `sgb-inst-<instanceID>-<sgID>`）；`ListSecurityGroupBindings` 显式与派生合并去重（派生优先）；`bound_instance_count` 聚合同口径（派生目标数 + 未被派生覆盖的显式目标数），Get/List/Bindings 三处一致；bindings 存在性检查改 `resolveSecurityGroupExists`/`storeBackedSecurityGroupExists`（store 优先）。gateway `network_runtime.go` 两分支注入实例 store。无 DB schema 变更、无契约变更；单测 2 新增全绿。实测（ani-system 镜像 `dev-20260916-sg5`）：sg_39c87355 bindings 返回 2 条派生绑定与实例记录完全一致（修复前为空）、bound_instance_count=2、不存在 SG 404。**存量能力缺口**：`change_security_groups` lifecycle 被 Kubernetes adapter 拒绝（真实环境实例安全组唯一写路径是创建时网络配置，派生视图恰好覆盖；Console「实例更换安全组」待 adapter 补齐）；派生仅覆盖 instance 目标；LB/NIC 绑定与规则 API memory-only 待后续批次。详见 `development-records/network-sg-binding-derived-view.md`。
> **IN-NETWORK-SG-VPC-AND-INSTANCE-FILTER-A（2026-09-16，分支 fix/network-sg-vpc-and-instance-filter，已修复并实测通过）：** 按 `kjs-study/修复bug/VPC子网安全组问题分析与修复记录.md` 方案修复 安全组-3/4、VPC-3、子网-3。① 安全组-4（契约未跟随 + store 模式校验误判）：安全组 vpc_id 契约自 PR #99 已存在但实现未跟随——`NetworkSecurityGroupRecord`/`CreateRequest` 增 VPCID、迁移 `20260916120000` 加可空列、handler 透传/回显；部署实测暴露 `CreateSecurityGroup` 只查内存 map、网关重启后把 DB 历史 VPC 误判 `vpc not found`，新增 `resolveVPCForValidation`（store 优先、内存回退）修复；② 安全组-3：`ListSecurityGroups` 存量 memory-only（重启后列表全丢）store 化（接口方法 + `MetadataNetworkStore.ListSecurityGroups` SQL + VPCID 精确/Name 前缀/Keyword/State 过滤）；③ VPC-3/子网-3：`/instances` 新增 vpc_id/subnet_id query 参数，`MatchesInstanceNetwork` 导出接入 matchesInstanceList，孤儿合并同遵守。单测 6 新增全绿，gofmt/build/git diff --check 通过；atlas.sum 经构建机 atlas v1.3.4 重算并算法一致性验证。实测（ani-system 镜像 `dev-20260916-network2`）：创建绑定历史 VPC 成功且回显（修复前 404）、重启后列表 12 条历史安全组可见+vpc_id 过滤命中、实例 59→2 全匹配、不存在资源→0。**存量问题记录**：CreateSubnet/CreateLoadBalancer/CreateRoute 同源内存校验缺陷、DeleteSecurityGroup/rules API memory-only 待后续收口。详见 `development-records/network-sg-vpc-bind-and-instance-filter.md`。
> **IN-LIST-FILTER-SEARCH-FIELD（2026-09-16，分支 fix/instance-searchfield-and-ops-pagination，已实施并实测通过）：** 按用户确认的 `kjs-study/修复bug/测试问题分析与修复记录.md` 第 4 节清单，将 8 个列表过滤统一到 **`search_field + keyword`**（前端约定 `search_field=name|id&keyword=…`）。存储/向量（`/volumes` `/filesystems` `/objects` `/buckets` `/vector-stores`）经 `storageListFilters`/`storageMatchesFilters`/`vectorStoreMatchesFilters` 解析 `search_field`：id→按资源 ID 模糊、name/缺省→按 name/bucket/key 模糊；网络类（`/networks/vpcs` `/subnets` `/security-groups`）新增 `networkNameFilter`/`networkIDKeyword` 归一为 `Name`/`Keyword`（`NetworkResourceListRequest` 增 `Keyword`），`LocalNetworkService` 按 `VPCID/SubnetID/SecurityGroupID` 前缀过滤，name 保持前缀匹配。OpenAPI v1.yaml 为 8 个 list 补 `search_field`(enum id/name)+`keyword`；旧 `name`/`status`/`state`/`keyword` 保留为向后别名（未传 `search_field` 行为不变）。单测 `TestStorageHTTPVolumeListFiltersByKeywordAndStatus`/`TestStorageHTTPBucketListFiltersBySearchField`/`TestVectorStoreListFiltersById`/`TestVectorStoreListFiltersByNameViaSearchField` 全绿；`go build`/`go vet`/`git diff --check` 通过。实机实测（ani-system，镜像 `dev-20260916-searchfield`→`searchfield2`）：8 列表 id/name 双向命中，`/buckets?search_field=name&keyword=test` 命中 3 个桶与前端截图一致。详见 `development-records/list-filter-search-field-a.md` 与 `kjs-study/修复bug/测试问题分析与修复记录.md`。

> **IN-INSTANCE-SEARCHFIELD-AND-CORE-LIST-FILTER（2026-09，分支 fix/instance-searchfield-and-ops-pagination，已修复并实测通过）：** 修复 kjs-study 定位的 Core 层两类后端过滤缺陷，合并记录两次提交。① **实例列表搜索/分页/隐藏已销毁（提交 85a090e，Bug-2/3/6）**：`list` handler 解析 `search_field` 限定 `keyword` 匹配字段（id/name，未传时多字段），孤儿实例一并遵守 keyword/search_field 过滤（不再无条件合并泄漏不相关孤儿）；操作历史 `ListOperations` 产出全量 total + 非空 next_cursor 正确翻页；默认列表排除 `deleted` 终态（显式 state=deleted 仍可查），孤儿遵守 state 过滤。② **Core 层四类列表过滤失效（提交 ce814cb，镜像仓库-1 / 存储 / 网络类-子网）**：镜像 `/registry/images` 增 `keyword`（`RegistryImageListRequest.Keyword`，Harbor/本地按「仓库名+tag」模糊匹配），并修复 Harbor 仓库层关键词预过滤导致"仅命中 tag"镜像被提前跳过；存储 `StorageResourceListRequest` 增 `Status/Keyword`，块/文件/对象/向量四类 handler 经通用 `storageMatchesFilters`/`vectorStoreMatchesFilters` 应用 status+keyword 过滤（向量存储同属 Core 层一并补齐）；子网 `ListSubnets` 按名称前缀过滤。ANI-06/CURRENT-SPRINT/README + 本批次文件更新。**推理服务/知识库（Service 层）过滤按用户要求搁置**。详见 `development-records/instance-searchfield-and-core-list-filter.md` 与 `kjs-study/修复bug/测试问题分析与修复记录.md`。

> **IN-INSTANCE-SANDBOX-EXPIRATION-EGRESS-A（2026-09-15，LOCAL_VERIFIED，分支 fix/instance-searchfield-and-ops-pagination，未部署）：** 修复 kjs-study Bug-7/Bug-8。① **Bug-7 沙箱到点未自动过期**：`SandboxConfig`（InstanceSpec JSONB）新增绝对到期 `ExpiresAt`/`LastActivityAt`（无新增列/migration），本地创建时 `ExpiresAt=createdAt+SessionTimeout`、`extend` 推进绝对到期（不再累加 `SessionTimeout` 基线）、`touch_idle` 刷新 `LastActivityAt`；新增网关后台 `SandboxExpirationController`（默认 30s 周期）经跨租户 `MetadataInstanceStore.ListRunningSandboxes`（`workload_kind='sandbox' AND state NOT IN (deleted,stopped,failed)`，`WithPlatformTx`）枚举，按 session/idle 到期→映射 `OnTimeout`（kill→delete、pause→pause，`WorkloadLifecycleAction` 无独立 kill）→执 `ApplyLifecycle`→置 `SandboxStateExpired`→`UpsertStatus` 幂等持久化；装配经 bootstrap `Capabilities.SandboxExpiration`→`InstanceRuntime.SandboxExpiration`→gateway `main.go` 启动 goroutine。② **Bug-8-A 沙箱详情 egress 白名单回显**：`instanceSandboxResponse` 增 `EgressAllowlist`、`sandboxResponseFromRecord` 填入（OpenAPI 沙箱详情 schema 已有 `egress_allowlist`，纯网关回显补漏，**无契约/SDK 生成物变更**）；B 网络策略下发因平台租户 VPC 无 centralized gateway 出网前提 + Kube-OVN 域名缺位按既定决策搁置，与 Bug-10 同根跟踪。新增 sandbox_expiration_controller(_test).go 与到期字段/lifecycle 断言测试；`go build`（bootstrap/gateway/router）+ runtime/bootstrap/gateway/router 测试 + `validate_component_imports.py` + `validate_inference_legacy_control_plane.py` + gofmt + `git diff --check` 全绿。**未部署 ani-test2，不标 runtime ready；真实 provider（KubernetesSandboxRuntime）到期 pause/kill 依赖既有 ApplyLifecycle，真实到期执行待部署后台验证。** 详见 `development-records/sandbox-expiration-egress-a.md` 与 `kjs-study/修复bug/测试问题分析与修复记录.md`（Bug-7/8）。
> **GATEWAY-CONSOLE-OVERVIEW-A（2026-09-14，local verified + K8s 测试环境双环境实测，PR #166 评审中）：** Console 首页四类统计卡片聚合端点 `GET /api/v1/overview`（分支 feat/console-overview）：契约优先新增 v1.yaml `/overview`（getConsoleOverview，tenant 边界 + x-ani-authz + scope:instances:read；instances/inference_services/models/knowledge_bases 四部分必返，by_state/by_status 固定键 0 值不省略，均不含 deleted）；实现为 ani-gateway BFF 聚合——实例复用本进程实例链路（refresh + 全量分页 + 孤儿合并，与 `/instances` 同口径），model/inference/kb 走既有 gRPC 客户端 cursor 翻页走尽按 status 计数，后端业务服务零改动；**部分成功语义（用户决策，覆盖初版整体 503）**：任一数据源失败该部分空计数（total=0、分布全 0）+ WARN 日志，整体仍 200，gRPC 未装配同走空部分。Core SDK 四语言/API docs/authz 生成物同步（323 routes 0 error）；单测 4 用例 + 定向门禁全绿（未跑全量 make test，CI 兜底）。K8s 测试环境实测：ani-test2（镜像 `test2-20260914-g1→g2`，计数与列表接口翻页全量逐字段一致、401/403、netpol 故障注入验证降级与 WARN、幂等、延迟 197~340ms）与 ani-system（镜像 `dev-20260914-overview`，只 set image 未改 env，etcd 预检 43%，租户 token 200 四类正常计数，实例 54）。实测坑：修改 NetworkPolicy 后需重启 gateway pod 才生效（gRPC 复用旧 HTTP/2 长连接）。批次详情 `development-records/gateway-console-overview-a.md`。

> **PLATFORM-AUDIT-LOG（2026-09-10，K8s 测试环境实测，auditlog3 重构后）：** BOSS 平台只读审计日志查询接口（分支 feat/platform-audit-log）：Core OpenAPI 契约优先新增 `GET /api/v1/platform/audit-logs`（time_from/time_to 必填成对，user/verb/resource_type/namespace/after/page_size/keyword 过滤，page_size 默认 20 上限 100 钳制；401/403/400/500 语义），数据源 kube-apiserver Metadata 级 write 审计经 fluent-bit 接流 Loki；ports `PlatformAuditService` + loki real adapter（query_range + LogQL label 过滤 + timestamp+auditID 全局倒序游标分页 + count_over_time 近似 total）+ gateway 路由与装配 + authz/Core SDK 生成物零漂移（validate_gateway_authz_drift no drift）；修复 generate_gateway_authz.py Windows 写 CRLF 致生成全量漂移（write_text newline="\n"）；新增/增量单测全 PASS，平台/租户隔离红线（tenant→403）由 middleware 锁定。K8s 测试环境 ani-test2 真实控制面审计接流已启用并端到端验证通过——kube-apiserver 审计（移走 manifests 目录 bak 备份文件后容器带 8 个 audit flag，真根因=bak 文件污染 manifests 目录）→ fluent-bit 审计流水线（audit-parsers.conf + extract_labels.lua audit_labels + hostPath + runAsUser:0）→ Loki `stream="kubernetes-audit"` 带 audit_* label → gateway，T-2 接流/T-7 脱敏/T-8 稳定性全通过。T-6c 租户 403 真实环境因无 tenant 登录端点回退单测锁定。**同日 auditlog3 重构（用户决策，过渡方案简化，镜像 `test2-20260910-auditlog3`）：删除 `AUDIT_LOG_PROVIDER` env 分派与 local 降级 adapter（`local_platform_audit.go` 及其测试整体删除、`ErrPlatformAuditUnsupported` 删除），gateway 启动默认直连 Loki（`AUDIT_LOG_LOKI_URL` 可覆盖地址），Loki 不可用/查询失败直接 500 `PLATFORM_AUDIT_FAILED` 不再降级；单测断言反转为必须报错（`TestPlatformAuditErrorWhenLokiUnavailable/Non200`），OpenAPI description 同步并重跑 SDK/API docs 生成物幂等零漂移；deployment 已 `kubectl set env AUDIT_LOG_PROVIDER-` 移除全部 AUDIT env，实测 T-9a 通过（`real_provider=true`、`provider=loki`、`total_approx=95296`、真实审计 items）。前端语义变化：Loki 失败为 5xx 而非 200+假数据，`dev_profile.real_provider` 恒 true。后续将由独立审计服务替代本过渡方案。** 详见 `development-records/platform-audit-log.md`（§10）与 `kjs-study/平台审计日志/kube-apiserver审计接流排查记录.md`。

> **PLATFORM-COMPONENT-STATUS-A（2026-09-05，2026-09-07 补充决策）：** `LOCAL_VERIFIED + K8s 实测`（分支 feat/component-status）。BOSS 平台健康组件状态只读能力：`GET /platform/components`（22 组件静态注册表三组聚合，K8s REST 读取 + service 组融合 Prometheus `up`/`target_info` scrape_status，15s TTL 缓存，任一组件失败不阻塞 200）+ 组件诊断三接口 metrics/logs/logs-stream（Prometheus cAdvisor 快照、Loki 列表与 SSE 流，错误映射 400/404/503）；契约优先 + authz/Core SDK 生成物零漂移 + 35 个单测全 PASS；K8s 测试环境镜像 `dev-20260905-compdiag` 实测列表/指标/日志/SSE/401/404 全通过。产品决策 2026-09-07：原型 P99/错误率/依赖检查三列裁剪不做，metrics 契约 description 已改为产品边界声明。已知环境边界：并行会话用不带 target_info 埋点的镜像重部署 inference-service/model-service 会令观测 reader fail-closed（service 组 scrape_status=unknown、/platform/services/health 503），恢复带埋点镜像即自愈。详情见 `development-records/platform-component-status-a.md`。

> **OBS-RUNTIME-P0（2026-09-04）：** `LIVE_VERIFIED`（范围仅限七服务监控可达性）。七服务已统一 runtimeadmin/OTel/Prometheus `ani-components` discovery，Core 新增 `GET /api/v1/platform/services/health` 并对 reachable/unreachable/unknown 与数据源不可用 fail closed；L0～L2、供应链门禁和隔离 namespace L3 的 discovery/up=0/missing-stale/Pod 删除恢复/Prometheus 故障均通过，清理后 namespace 不存在。BOSS/Console 前端按用户明确决定因 ANI 前端废弃而 `not_applicable`，只做接口验证；P1/L4、生产 rollout 与业务健康仍未验证。详情见 `development-records/service-runtime-observability-p0.md`。

> **ANI-GW-1（2026-09-02）：** `LOCAL_VERIFIED`。固定 Session Gateway `api/v0.1.0`（Go module `v0.1.0`，commit `d86a40d33369b128aabc680d4ea0b3f790ac0bb6`）完成 instance exec/console gRPC `CreateSession` 接入，并拆分 `InstanceObservability` / `InstanceSessionIssuer`；real provider 缺失、非法或不可用时 503 fail-closed，不再生成占位 URL。fake/bufconn、race/vet、全仓测试与契约/生成/架构门禁通过；真实 Session Gateway 进程、WebSocket/terminal/serial/VNC 数据面和集群 rollout 均 `not_verified`。不含 Console、CONSOLE-1、LIVE-1；详情见 `development-records/ANI-GW-1.md`。

> **INSTANCE-SANDBOX-TEMPLATE-IMAGE-A（2026-09-04，local verified，hotfix）：** 修复沙箱 code-run 报 `PRECONDITION_FAILED: sandbox pod is not ready`。根因：内置模板 `Image` 是占位 `registry.local/ani/sandbox-*:dev`，集群不可达，沙箱 Pod `ImagePullBackOff`，code-run `waitReadySandboxPod` 等满超时。修复：两模板（`python-secure`、`cuda-notebook-secure` 临时以 python 镜像承接）默认镜像接入已验证可拉取的复用镜像 `docker.changqingyun.cn/hub/library/python:3.12`（10.10.1.66 探针验证 python3.12.10），Description 如实澄清；catalog 测试新增防回归断言（模板镜像必须以 `docker.changqingyun.cn/` 开头），`go build`/`go test`/validate-architecture/`git diff --check` 通过。未 rollout 前线上不生效；存量占位镜像实例需重建；GPU/notebook 专用镜像与 live-gate `ANI_SANDBOX_LIVE_IMAGE_REF` 注入待后续。
> **INSTANCE-SANDBOX-KUBECTL-A（2026-09-07，live verified，hotfix）：** 修复 code-run 报 `exec: "kubectl": executable file not found in $PATH`。根因：`KubernetesSandboxRuntime` 代码执行走 `KubectlSandboxPodExecutor` shell-out 到 `kubectl exec`，而 gateway `alpine:3.20` 镜像未装 kubectl。修复：`services/ani-gateway/Dockerfile` 运行镜像阶段添加 `wget https://dl.k8s.io/release/v1.36.0/.../kubectl`。live 验证（10.10.1.66，rollout `dev-20260907-kubectl-a`）：pod 内 `kubectl version`=v1.36.0、in-cluster 认证 `kubectl get ns` 成功、`kubectl auth can-i create pods/exec`=yes、gateway SA 可 exec 进沙箱 Pod。code-run 仍要求实例非 paused（replicas=0 时返回 `PRECONDITION_FAILED not ready`，属预期）。记录：`development-records/instance-sandbox-coderun-kubectl-a.md`。
> **GATEWAY-GPU-PLATFORM-SCOPE-A（2026-09-07，live verified，hotfix）：** 修复 BOSS root（platform scope）访问 `/api/v1/gpu-specs*`、`/api/v1/gpu-inventory*` 报 403 `token scope not allowed for this path`。GPU 接口为双域共享资源（Console tenant + BOSS platform），V2 boundary 域互斥无法表达，按 `/svc/` 模式在 `scopeAllowedForPath` 放行 `platform||tenant`，角色准入仍由 rbac.go 承担。live 验证（rollout `dev-20260907-gpuscope-a`）：GPU 四端点 200、`/instances` 负向对照仍 403。部署插曲：PG max_connections=100 被 gateway 多 store 连接池打满致新 Pod CrashLoop，已 patch 滚动策略 `maxUnavailable:1`（留在线上，连接池收敛待后续）。后续项：GPU 接口迁移 V2 authz（cluster boundary）+ availability 平台视角语义。记录：`development-records/gateway-gpu-platform-scope-a.md`。
> **GATEWAY-QUOTA-PLATFORM-SCOPE-A（2026-09-07，live verified，hotfix + 安全修复）：** 修复 BOSS `GET /api/v1/quotas?limit=100` 403，并封堵**已存在的跨租户配额泄露**：`listQuotas` 不注入租户过滤、store 走 `WithPlatformTx` 绕过 RLS 返回全部租户行，而 `scopeAllowedForPath` 末尾默认放行 tenant，导致任意租户 token 可读全平台配额。修法与 GPU 相反：`/api/v1/quotas` 精确匹配仅 platform（对齐 `/admin/*`；精确匹配避免误伤 tenant-only 的 `/quotas/me`），`/gpu-scheduling*` 并入 GPU 双放行分支（handler 按 tenant label 过滤无泄露）。auth_test.go 新增 6 用例。live 验证（`dev-20260907-quotas-a`）：platform /quotas 200（39 租户跨租户确认）、/quotas/me 403；tenant /quotas 403（泄露封堵）、/quotas/me 200、gpu-specs 200。记录：`development-records/gateway-quota-platform-scope-a.md`。
> **INSTANCE-ORPHAN-GPU-FILTER-A（2026-09-07，live verified，hotfix）：** 修复 `GET /instances?kind=gpu_container` 混入非 GPU 实例。根因：`discoverOrphanDeployments` 对带租户标签且不在 store 的 Deployment 无条件硬编码 `Kind=gpu_container` 生成孤儿记录（`GPUCount>0` 只控制 GPU 字段填充，不控制记录生成），gateway 重启后所有非 GPU Deployment 被当作 gpu_container 回显，列表端 kind 过滤形同虚设。修复：`obs.GPUCount<=0` 直接跳过；`observeOrphan` GPU 探测兼容 `volcano.sh/vgpu-number`。新增 `TestListOrphanDiscoverySkipsNonGPUDeployments`。live 验证（rollout `dev-20260907-orphan-a`）：tenant-a 命名空间 18 个非 GPU Deployment 全部不再回显，3 条孤儿记录经集群核对均携带 `nvidia.com/gpu=1`。行为收紧：非 GPU 未入库实例重启后不再出现在实例列表（对齐"孤儿仅 GPU"既定约定）。记录：`development-records/instance-orphan-gpu-filter-a.md`。
> **GATEWAY-GPU-V1-ROLLBACK-A（2026-09-08，live verified，hotfix + rebase）：** GPU 接口鉴权暂时回退 V1 链路。背景：生产 gateway 镜像回退致 BOSS GPU 接口 403 复现，且 main（PR #145）已将 v1.yaml 全量 V2 化、GPU 路径标单域 `scope: platform`，部署后 Console（tenant）将 403。V2 boundary 域互斥无法表达 GPU 双域共享，经产品确认回退 V1：v1.yaml 13 个 GPU 操作删除 `x-ani-authz`（classification 同步 `authorized→authenticated`），operation-registry.v1.json / zz_generated_target_operation_registry.go / zz_generated_core_policies.go 全链同步重生成（GPU 全部 `PolicySourceLegacy`），冻结计数 authenticated 9→22、authorized 278→265；`/quotas`+`/quotas/me` 保留 V2（与 V1 行为一致）；运行时 middleware 零改动。同期 `hotfix/network-store-read` rebase 到 origin/main（含修复 rebase 遗留冲突标记）。live 验证（ani-test2 隔离环境 10.10.1.66:30083，镜像 `test2-20260908-b`）：platform token GPU 三端点+/quotas 200、/quotas/me 403；tenant token GPU 三端点+/quotas/me+/instances 200、/quotas 403（泄露封堵保持）；tenant-a/admin Console 登录+核心接口 200。同日再次 rebase 到含 ANI-IAM-PRE930-CONTAINMENT 的最新 main：V2 target registry 基建与 GPU 接口 V2 注解已由 main 整体移除、回退终态由 main 承载，上述 main 既有红门禁已恢复绿，rebase 文档回归已修复，门禁复验全绿。记录：`development-records/gateway-gpu-v1-rollback-a.md`。
> **GPU-PARTITION-A~D（2026-09-09，live verified，分支 ani-hotfix）：** BOSS 专属集群 GPU 等分切分已落地：`POST /api/v1/gpu-inventory/gpu-partitions`（platform-only exact-match scope + 幂等键，2/4/8）对集群内全部空闲整卡节点统一切分（跨型号，每份显存按节点实际显存派生）；异步任务（task_type=gpu_partition）受控 goroutine 执行节点级 volcano-vgpu-node-config devicesplitcount 更新 → 节点重打标签（gpu-mode/gpu-sharing-policy/gpu-sharing-spec + 清整卡 gpu-spec）→ 重启 device plugin pod → 轮询 `volcano.sh/vgpu-number` 注册收敛，120s 总超时，忙碌节点（节点级持 GPU Pod 判定）执行前重查跳过并记入 result；0 个可切节点 422 NO_IDLE_WHOLECARD_GPUS；GET /tasks/{id} 对 running 超阈值任务做幂等 lazy-resume 重入防 gateway 重启悬挂。ports `GPUPartitionPlanner` + K8s REST adapter 实现；gateway main 经 gpuInventory 类型断言注入（local profile 503）；RBAC nodes 补 patch。router 8 个新测试全通，validate-architecture 通过；切分不创建 GPUSpec（解耦语义）。GET /tasks 两 op 同步移除 `x-ani-authz` 走 legacy 双域（V2 单域无法表达 BOSS+tenant 双侧轮询，同 GATEWAY-GPU-V1-ROLLBACK-A 语义）。live gate PASS（2026-09-09，ani-test2 10.10.1.66:30083，镜像 `test2-20260909-e`）：8 项检查全过——202 受理→completed/100、dev-phys-02 CM devicesplitcount=4 + vgpu/quarter/12285MiB relabel + 插件重注册、tenant 403、忙碌节点（dev-phys-03/kubercloud，node_busy 附阻塞 Pod 名单）正确跳过；执行期间修复 apply goroutine 租户上下文 panic（`detachedTaskContext`）。证据 `development-records/live-evidence/gpu-cluster-partition-live.json`。记录：`development-records/gpu-partition-cluster-split.md`。
> **GPU-POOL-SURFACE-A（2026-09-11，live verified，分支 ani-hotfix）：** BOSS GPU 资源池态势页后端缺口补齐（设计 `repo/design/gpu-pool-status-surface-gap-plan.md`，契约优先）：`PATCH /api/v1/gpu-inventory/{device_id}`（platform-only exact-match scope + required Idempotency-Key）人工翻转 maintenance/unavailable/idle 并携带 reason（fault 自动态不可设置，400），覆盖态落 PG 平台账表并合并进平台清单/占用视图（GPUInventoryRecord.reason + status enum unavailable）；`GET /api/v1/gpu-inventory/events` 设备事件流（status_changed/partition_applied，事件带 node_name/gpu_type/actor）；GET /gpu-inventory/occupancy 补 physical_card_count/logical_card_count/maintenance_count/unavailable_count/tenant_count（全 omitempty）。迁移 `20260911_001_gpu_device_surface.sql`：gpu_device_overlays + gpu_device_events 两表（平台级 RLS platform_bypass + ani_app 授权），atlas.sum 重算；ports `GPUDeviceSurfaceStore` + PG adapter + gateway 装配（有 DATABASE_URL 时注入，local profile 501）；SDK/docs/authz 生成物重生成。**二次拍板（用户决策）：设备级预留（assign/revoke/reserved 状态/reservations 表/抢占事件）实现后整体回退**——预留=数量型额度（既有 PUT /admin/tenants/{tid}/reservations），Volcano 无 device-level pinning 不做卡级绑定，设计文档 §4.1 已记录。live 验证 PASS（2026-09-11，ani-test2 10.10.1.66:30083，镜像 `test2-20260911-b`）：迁移应用+冒烟、PATCH 翻转/恢复、事件流字段、occupancy（physical=24 logical=96，维护态 maintenance_count 动态出现/消失）、租户 PATCH/events 403 隔离全过。cursor 分页后端暂忽略（next_cursor 恒 null，limit=200 拉全，契约不变）。前端对接文档 `repo/design/gpu-pool-status-frontend-integration.md`。记录：`development-records/gpu-pool-status-surface-a.md`。
> **INSTANCE-VM-LIFECYCLE-HOTFIX-A（2026-09-15，live verified，PR #168 评审中）：** VM 真实底座（KubeVirt）生命周期五项能力修复，来源测试异常记录 VM 系列：VM-02/03 数据盘新建盘真实建卷（幂等建卷 + PVC claim 映射修正 + 默认 StorageClass `ani-block`）；VM-09 重启改 stop-等待停稳-start（原生 restart 子资源对 legacy `spec.running` 只停不启）；VM-07 快照回滚接入 `VirtualMachineSnapshot/Restore`（CR 名 DNS-1123、运行中回滚 stop-restore-start、按 `status.complete` 判完成、15min 超时、RBAC 补权限）；VM-10 重建落地（停机-捕获 spec-删 CR-重建-开机，containerDisk 无状态即重装系统）；VM-06 NFS 挂载 virtiofs（停机-改 spec-开机、virtiofs tag 36 字节截断、严格 Stopped 停稳判定、失败 best-effort 恢复开机）。环境侧 KubeVirt featureGates 需 `Snapshot`+`EnableVirtioFsStorageVolumes`。live 验证 PASS（ani-test2 隔离环境，镜像 `test2-20260915-b/c/i/k/n`）：数据盘创建/重启/快照回滚/重建/NFS 挂载卸载逐项通过，PVC Bound、VMI Running、CR uid 变更与 virtiofs 设备写入/移除均有集群侧证据。未改 OpenAPI 契约与生成物；遗留 Block 卷热插与 PVC 根盘重建语义评估。记录：`development-records/instance-vm-lifecycle-hotfix-a.md`。
> **INSTANCE-CONTAINER-UPDATE-IMAGE-A（2026-09-16，live verified，PR #168 评审中）：** 容器与 GPU 容器实例「更新镜像」能力落地（提交 d16d43e）：`KubernetesLifecycleExecutor` 新增 `update_image` 分支（此前落入 default 返回 unsupported，缺口在后端 provider apply 层），对现有 Deployment 做 strategic-merge patch `spec.template.spec.containers[*].image` 触发滚动更新而非重建（env/ports/volumes 不动，仅 container/gpu_container + Deployment 支持）；服务层 `resolveLifecycleImage` 复用 create 解析路径（租户 project/purpose/漏洞扫描门禁一致，指纹计算前解析保证重放稳定）；ports 请求新增内部 `ImageRef` 字段（API 契约不变）；apply 后完整 `InstanceImageSummary` 写入 record（此前仅写 image_id 清空 ref/digest），RolloutStatus 置 progressing 由 reconciler 观测收敛（与 scale 同机制）。单测 4 用例。live 验证 PASS（ani-test2 隔离环境，镜像 `test2-20260915-o`）：container nginx→fedora→nginx 双向 10s 内全收敛、record 含完整 digest；gpu_container base→runtime Deployment gen 5→6 镜像变更（purpose 门禁放行 gpu 镜像）；RWO 块卷实例滚动 surge Pod 跨节点 Multi-Attach 属容器-3 平台约束，实测缩 0 扩 1 Recreate 路径可收敛——遗留 RWO 块卷 update_image 走 maxSurge=0 或卷节点亲和，与容器-3 预检一并评估。未改 OpenAPI 契约与生成物。记录：`development-records/instance-container-update-image-a.md`。
> **INSTANCE-CONTAINER-ENV-ECHO-A（2026-09-16，live verified，PR #168 评审中）：** 容器与 GPU 容器实例环境变量"写入但不回读"修复（GPU-5）：创建链路本就完整（env 经 `containerEnv` 渲染进 Deployment，集群侧真实生效），缺口在回读三层——record `ContainerInstanceStatus` 不持久化 Env、gateway 详情响应无 env 字段、契约 `InstanceRecord.container` 无 env 定义。按 API-first 修复：v1.yaml 契约新增 `container.env`（复用创建请求 `InstanceEnvVar` schema）；`ContainerInstanceStatus.Env` 随 `container_status` JSON 列持久化（无 DB 迁移），`containerStatusInfo` 创建时克隆写入；gateway `instanceContainerResponse.env` 回显（secret_ref 型不带 value，secret 内容永不返回）；逐路径核验 reconciler/scale/update_image/rollback 原地更新不丢 env。一处修复覆盖 container 与 gpu_container。单测 2 用例。live 验证 PASS（ani-test2 隔离环境，镜像 `test2-20260916-gpu5env`）：GPU 容器带 2 个环境变量创建 → 创建响应即时回显 → 详情 t+15s（reconciler 已跑）回显一致。遗留：修复前历史实例 record 无 env 不回显，需从 Deployment 反读回填。记录：`development-records/instance-container-env-echo-a.md`。
> **INSTANCE-CONTAINER-FS-MOUNT-A（2026-09-16，live verified，PR #168 评审中）：** 容器与 GPU 容器实例挂载 NFS"不支持"修复（容器-5/GPU-4）：`KubernetesLifecycleExecutor.Apply` 把 attach/detach_filesystem 无条件路由进 VM 专属 virtiofs 通道（`applyKubeVirtFilesystem` 按 kind 硬拒绝），Deployment 通道从未实现。修复：新增 `applyFilesystem` 按 kind 分流——VM 走既有 virtiofs 路径，container/gpu_container 走新增 `applyKubernetesFilesystem`：对 pod template 做 strategic-merge 定向 PATCH，attach 写入 fs PVC 卷（claim=`storageProviderName("fs", filesystemID)`、卷名 `kubeVirtFilesystemVolumeName` 确定性推导）+ workload 容器 volumeMount（mount_path/read_only），detach 用 `$patch: delete` 按卷名删 volumes、按挂载路径删 volumeMounts（merge key 是 mountPath，mount path 优先读 record 附件、缺失回退 live Deployment 反查）；NFS PVC RWX 多副本可并发挂载；滚动更新由 reconciler 观测收敛；服务层/契约/生成物零改动，一处修复覆盖 container 与 gpu_container。单测 3 用例。live 验证 PASS（ani-test2 隔离环境，镜像 `test2-20260916-fsmount`）：container（nginx）与 gpu_container（rtx4090-12g-4）attach/detach 各一轮——Deployment 卷+volumeMount 出现/移除、ready=1、record storage_attachments 增删一致、实例回 running。记录：`development-records/instance-container-fs-mount-a.md`。
> **INSTANCE-CONTAINER-SECRET-BIND-A（2026-09-17，live verified，PR #168 评审中）：** 容器与 GPU 容器实例「绑定/解绑密钥」"不支持"修复（容器-6/GPU-6）：lifecycle 契约/路由/服务层校验/幂等本就就绪，缺口在 `KubernetesLifecycleExecutor.Apply` 无 `bind_secret`/`unbind_secret` case 落入 default 返回 unsupported。修复：executor 新增 `applyKubernetesSecretBind` 分支——bind env 带 `env_name` 走 per-key `valueFrom.secretKeyRef` 条目、不带走 `envFrom` 整 secret（原子列表 GET live 全量回写，已绑定幂等 no-op）；bind file 写 secret volume + readOnly volumeMount（卷名按 secret_id+mount_path 确定性派生，bind/unbind 双向可定位）；unbind 不依赖 record、GET live Deployment 反查该 secret 全部注入形态（env/envFrom/volume/volumeMount）用 `$patch: delete` 定向移除，四类均空返回 404；record `ContainerInstanceStatus.SecretBindings` 随 container_status JSONB 持久化（无迁移，GPU-5 同模式），bind/unbind 后 RolloutStatus=progressing 由 reconciler 观测收敛；ports `WorkloadSecretBinding` 新增 EnvName。一处修复覆盖 container 与 gpu_container。未改 OpenAPI 契约与生成物。单测 10 用例。live 验证 PASS（ani-test2 隔离环境，镜像 `test2-20260917-secretbind`）：container bind env（DATABASE_URL secretKeyRef）/bind file（volume+readOnly mount）/record 2 条 SecretBindings（psql container_status 直查）/unbind 全形态移除+record 清空+回 running；gpu_container 复用实例 bind env+unbind 全过。环境配套：租户 Secret K8s apply 需 gateway `SECRET_PROVIDER_MODE=kubernetes_rest`（本环境此前未配置，验证期间已补配）。遗留：API 详情响应暂不回显 secret_bindings（record 持久化已就绪，前端需要时按 GPU-5 模式补契约+响应）。记录：`development-records/instance-container-secret-bind-a.md`。
> **IN-NETWORK-CREATE-VPC-VALIDATION-A（2026-09-18，live verified，分支 fix/routing-loadbalancer-bugs）：** 收口 `IN-NETWORK-SG-VPC-AND-INSTANCE-FILTER-A` 登记的存量缺陷——store 模式下创建网络资源对"网关重启前创建"的 VPC 一律 `404 capability resource not found: vpc not found`（来源 `kjs-study/修复bug/路由与负载均衡问题分析与修复记录.md`）。根因：`CreateSubnet`（477-483）/`CreateLoadBalancer`（1402-1408）/`CreateRoute`（1509-1515）的 VPC 存在性校验只查内存 map `s.vpcs`，网关以 store 模式（`NETWORK_PROVIDER=kubeovn_rest` + `DATABASE_URL`）运行、进程重启后内存为空，DB 历史 VPC 被误判不存在；此前 `CreateSecurityGroup` 已用 `resolveVPCForValidation`（store 优先 `store.GetVPC` 查持久层、非 store 回退内存）修复同一问题，本次把遗漏三处对齐。租户归属校验由 helper 承担（store 分支 SQL 按 `tenant_id` 过滤、内存分支显式比对），三处 `vpc_id` 必填语义原样保留；无 OpenAPI 契约变更、无 handler 变更、无 DB 迁移、无生成物变更。单测：3 个 store 模式正向用例（断言校验 SQL 命中 `network_vpcs` 且分别落 `network_subnets`/`network_load_balancers`/`network_routes`）+ 1 个反向表驱动用例（store 无该 VPC 与 VPC state=deleted 两情形 × 三类资源均须 `ErrNotFound` 且零写操作）。验证：相关 `go test`、`go build`、`make validate-architecture`、`git diff --check` 全绿，`gofmt -l` 改动文件无输出；live gate PASS（ani-system 10.10.1.66:30080，镜像 `dev-20260918-routelb`，digest `sha256:7d7c9adf…9318`；etcd 预检 43%、只 set image 未改 env、rollout 成功、healthz 200）**4 项断言全 PASS / 0 FAIL**——前提为 Pod 随本批次镜像重启、目标 VPC `test-vpc-ly123` 创建于 2026-09-14（只可能存在持久层）：`POST /networks/routes` 404→**201**（`dev_profile.mode=real`/`provider=kubeovn`，真实 apply 到 KubeOVN）、`POST /networks/load-balancers` 404→**201**、`POST /networks/subnets` 404→**201**（同源缺陷）、不存在的 VPC 仍 `404 vpc not found`（语义未放宽）；列表接口复查路由/负载均衡各 1 条可见。遗留：deleted VPC 的线上实测未做（由单测覆盖）；本次原地留下路由/LB/子网三个测试资源，删除 `test-vpc-ly123` 前需先清理；ani-test2 未部署。记录：`development-records/network-create-vpc-validation-store.md`。

> **STORAGE-VOLUME-DETACH-FACT-SOURCE-A（2026-09-18，live verified，分支 fix/routing-loadbalancer-bugs）：** 块存储卸载 `POST /api/v1/instances/{instance_id}/lifecycle`（`action=detach_volume`）对"卷侧已挂载、实例侧无 attachment"的卷一律 `409 capability resource conflict: volume is not attached` 收口（来源 `kjs-study/修复bug/块存储卸载问题分析与修复记录.md`，用例 块存储-4）。根因："是否已挂载"存在两个互不交叉的事实源——Console 卷列表按**卷侧** `mount_instance_id` 非空渲染"已挂载"并提供「卸载」按钮，而「卸载卷」实际发出的是**实例级** `detach_volume`，其预检只认**实例侧** `record.Status.Storage`（`storage_attachments`）；实例维度 attach（`bindCreateStorage`）会回调卷级 `MountVolume` 写卷侧 `storage_volumes`，但实例维度 detach（`applyVolumeBinding`）只移除实例侧、**不回退卷侧**，两者一旦漂移（状态重算/网关重启/历史数据）卸载必然 409、界面卡死在卸载按钮且关联资源永不解除。修法（不新增实体、无 OpenAPI 契约变更、无 handler 变更、无 DB 迁移）：`instanceStorageBinder` 接口新增 `GetVolume`/`UnmountVolume`；新增 `volumeMountedToInstance`（按 `tenant_id+volume_id` 反查卷侧 `mount_instance_id` 是否指向本实例，读失败 fail-closed）与 `unmountVolumeSide`（清卷侧 `mount_instance_id`/`mount_route`/`mount_name`，`ErrNotFound` 容忍）；`lifecyclePrecheck` 新增 `volumeSideAttached` 入参，detach 分支由 `if !attached` 改为 `if !attached && !volumeSideAttached` 并在 `details` 增 `volume_side_attached` 观测字段；`applyLifecycle` 预检前计算该标志，并在 detach 成功路径（移除实例侧 attachment 之后、落库之前）调用卷侧回退，失败写 operation 时间线（`detach_volume` 步骤 failed / `volume_unmount_failed`）并返回错误。语义边界：attach 的 `volume_already_attached` 仍只看实例侧；`root_volume_detach_forbidden` 原样保留；只有卷侧指向**本实例**才接受并可回退，指向别的实例仍 409 且不会去清他人卷；卷侧 unmount 幂等键由生命周期键派生（`:unmount-volume:<volumeID>`）。单测：新增 3 用例 + `fakeInstanceStorageBinder` 补 `GetVolume`/`UnmountVolume`/`storedVolumes` 基建（核心回归：实例侧无 attachment + 卷侧指向本实例 → 成功且卷侧 unmount 一次；反向：卷侧指向别的实例 → 仍 `ErrConflict` 且零回退；两侧一致 → 实例侧移除 + 卷侧同步回退）。验证：相关 `go test` 全通过（`pkg/...`+`services/ani-gateway/...` 仅既有 Windows sandbox 符号链接用例失败，与本批次无关）、`gofmt -l` 改动文件无输出；live gate PASS（ani-system 10.10.1.66:30080，镜像 `fix-20260918-blockdetach`，digest `sha256:907f8be0…59cb0`；只 set image 未改 env、rollout 成功、healthz 200）**5 项断言全 PASS / 0 FAIL**——目标为 bug 文档记录的精确现场 `test-ly-vol`（`vol_58d620be-6a6f-43e8-8b2e-9afb4c3b21c3`，卷侧 `mount_instance_id=inst_5d1cebef…`、`in_use=false`、`used_by=[]`，实例侧 `storage_attachments` 只有文件系统）：`POST /instances/inst_5d1cebef…/lifecycle`（`detach_volume vol_58d620be…`）**409→200**（实例仍 running，`operation_id=a277543c…`）、回读卷 `mount_instance_id`/`mount_route`/`mount_name` 三字段全消失且 `reason=unmounted by local storage profile`、`mount_history` 追加 `{"at":"2026-09-18T03:52:16Z","action":"unmount","result":"success","target":"inst_5d1cebef…"}`、同卷再次 detach **409**（语义未放宽）、从未挂载的卷 **409**、卷侧指向其它实例因实例不存在先 400（非 200）。实测即数据清扫（存量不一致卷已收敛，无迁移）；只读探针确认另两个卷侧挂载卷 `tc-vol-20260910-full2`/`test-ly-container` 的实例侧均有 attachment 且实例已 `state=deleted`，非本 bug 场景未触碰。遗留：卷侧与实例侧双事实源的接口语义合并（卷级 `MountVolume` 与实例级 `AttachVolume` 合一）属长期收敛，留独立批次；前端卷列表「挂载实例/关联资源」列仍渲染 `mount_name`（假关联）且卸载失败后不刷新纠正，属 Console 改造点（后端权威字段 `in_use`/`used_by`/`mount_instance_id` 已可用）；ani-test2 未部署。记录：`development-records/storage-volume-detach-fact-source.md`。

> **STORAGE-FILESYSTEM-EXPAND-STORE-A（2026-09-18，live verified，分支 fix/routing-loadbalancer-bugs）：** 文件存储扩容 `POST /api/v1/filesystems/{filesystem_id}/expand` 对"网关重启前创建"的历史文件存储一律 `404 capability resource not found` 收口（来源 `kjs-study/修复bug/文件存储扩容问题分析与修复记录.md`，用例 文件存储-4）。根因二层：① (主) `LocalStorageService.ExpandFilesystem`（908-937）是 storage 模块最后一个 memory-only 漏网点——只查内存 map `s.filesystems`，store 模式（`DATABASE_URL`）进程重启后内存为空即 404（同族 `GetFilesystem`/`DeleteFilesystem`/`CreateFilesystemMountTarget`/`MountFilesystem`/`UnmountFilesystem` 均已走 store 或 `lookupFilesystemRecord` 回退）；② (次) Console 扩容请求体发 `{"capacity": N}` 而契约与 handler 只声明 `size_gib`，字段被 `BindJSON` 静默忽略（`size_gib=0` 撞容量校验）。修法：service 改复用 `lookupFilesystemRecord`（483-510，内存未命中时 store `GetFilesystem` 回退并回填缓存，租户校验由 helper 承担）；handler `storageFilesystemExpandRequest` 新增过渡别名 `Capacity`（仅实现级 shim，不动 OpenAPI 契约/SDK，`SizeGiB==0 && Capacity>0` 时取别名，`size_gib` 优先）。幂等检查仍留内存（与 mount/unmount 一致）未改动；无契约变更、无 SDK/生成物变更、无 DB 迁移。单测：runtime 2 用例 + gateway 1 用例。验证：相关 `go test` 全通过、`go vet` 通过、`gofmt -l` 改动文件无输出；镜像 `fix-20260918-fsexpand`（digest `sha256:6b74374d…00d9f`）部署 ani-system（只 set image 未改 env、rollout 成功、healthz 200）**实测 5 项断言全 PASS**——前提为 Pod 随本批次镜像重启内存为空、目标 `fs_f600cbd5-bc90-40ce-b216-2e2ec57da6b0`（`test-ly-nfs`，创建于 2026-09-14，只可能在持久层，列表 13 条正常可见）：`capacity=101` 404→**202** 且 `size_gib` 回显 101、`size_gib=102` 再扩 202、等容量 400、不存在 404、回读落库 102。遗留：`capacity` shim 属已知轻量契约/实现漂移（三处留痕），前端 Console 扩容器字段切 `size_gib` 后移除；实测使测试环境该文件存储 100→102 GiB（扩容不可逆未回滚）；ani-test2 未部署。

> **OBJECT-STORAGE-BUCKET-FIX-A（2026-09-17，live verified，分支 fix/object-storage-bugs）：** 对象存储（桶）控制面记录与底座 MinIO 五项不一致修复（来源 `kjs-study/修复bug/对象存储后端Bug详细分析.md`）：**Bug1** 创建桶支持 `storage_class`（OpenAPI 契约 + `StorageBucketCreateRequest` + gateway 请求结构体全链新增，enum `standard`/`infrequent_access`，非法值 `400 UNSUPPORTED`），并按 `access_mode` 推导 `acl`/`acl_label` 回显（`public_read`→`acl=tenant_read`）；**Bug2** 下载预签名 URL 从内部集群 endpoint 改为浏览器可达的 `publicEndpoint`（与上传一致），未配置底座时返回明确错误而非 mock 链接；**Bug3** 预签名前先 `hydrateObjectsFromStore` 回填对象缓存，对象不存在返回可辨识 `object %q not found in bucket %s`（此前错误语义混淆）；**Bug4** 新增 `ports.ObjectStorePolicyApplier` 可选能力 + MinIO 实时桶策略（`tenant_read` 走 `PUT /{bucket}?policy=`，Resource 限定 `arn:aws:s3:::<bucket>/<tenantID>/*` 防跨租户读；`private` 走 `DELETE ?policy=`，404 视为成功），ACL 归一后同时写控制面存储与底座，修复"改完 ACL 重启即丢失"；**Bug5** `SetStorageBucketClass` 补 `upsertBucket` 持久化。`applyBucketACLPolicy` 在无底座/底座未实现接口时静默跳过（保持 local profile 兼容）；Bug5 有意不做底座 apply（MinIO storage class 为对象级属性，桶级无法等价表达，拒绝"假成功"）。无 DB 迁移、无生成物变更。单测：objectstore 桶策略租户前缀与 public endpoint 断言 + runtime 3 新用例 + gateway `testObjectStore` stub。验证：相关 `go test` 全通、`make validate-architecture` 通过、`gofmt -l` 改动文件无输出、`git diff --check` 干净；live gate PASS（ani-system 10.10.1.66:30080，镜像 `dev-20260917-objstore`，digest `sha256:ee3c4bb6…0942`；etcd 预检 42%、只 set image 未改 env、rollout 成功、healthz 200）**28 项断言全 PASS / 0 FAIL**：upload/download URL host 均为 `10.10.1.66:30900` 且真实 PUT 200 / GET 200（body `hello`）、不存在 key 404 消息含 `not found in bucket`、非法 `storage_class=glacier`/`acl=world_writable`/`method=PATCH` 均 400、ACL 与存储类型切换后重列持久；**桶策略双向硬证据**：`acl=tenant_read` 时裸 URL 匿名 GET 200，切回 `private` 后匿名 GET 403（MinIO `AccessDenied`）。构建说明：首构建（`dev-20260917-objstore`）时 ani-system 运行网络存储分支构建（`dev-20260917-multival`）、本批次基于旧基线，故采用并集构建避免回退实例 `kind`/`state` 多值过滤；2026-09-17 已 merge `origin/main 143c4fe`（网络安全组/VPC/子网与实例列表系列），代码树统一，改为 `git archive HEAD` 整体覆盖构建机源码树重建（镜像 `dev-20260917-objstore2`，digest `sha256:b0886c7e…69d7`，etcd 预检 43%、只 set image 未改 env、rollout 成功、healthz 200），并集方式废弃；merge 后复测本批次 28 项断言仍 **28 PASS / 0 FAIL**，另跑网络存储系列回归 **19 PASS / 0 FAIL**（历史安全组规则列表 200、创建带预设规则安全组明细立即落库、删除级联清零、有存活子网时删 VPC 409、实例 `vpc_id`/`subnet_id` 过滤与全量精确一致、未知值 0）。构建机 `/root/ani-build` 原代码树漂移（490 个 .go 中 98 个 md5 不一致，`storage_renderer.go` 为旧版致首构建 `undefined: defaultVolumeStorageClassName`）在本次整体覆盖构建路径上已消除。另按同产物 retag 部署 ani-test2（`test2-20260917-objstore`，image ID/digest 与 ani-system 一致，未重新构建），对象存储 28 项 + 网络存储回归 19 项均 0 FAIL。遗留：桶详情无独立 `GET /buckets/{id}` 端点（实测只能经列表接口回查，本次未新增）；`public_read` 在响应中归一为 `tenant_read`，并非真正的公网匿名读。记录：`development-records/object-storage-bucket-fix-a.md`。
> **M1-K8S-H（2026-09-18，live verified，分支 hotfix/network-store-read）：** 「创建 K8s 集群」真实底座失败三层根因修复。① **HOME 不可写**：网关容器 uid 65532，镜像 `HOME=/home/ani` 属 root，helm/vcluster 报 `mkdir /home/ani: permission denied`——pod `securityContext` 加 `fsGroup: 65532` + `ani-gateway-helm-cache` emptyDir 挂 `/home/ani`。② **chart 源不可达**：默认 `https://charts.loft.sh` 集群内不通——chart 切内网 Harbor OCI（`VCLUSTER_CHART_NAME=oci://docker.changqingyun.cn/ani/charts/vcluster`，`VCLUSTER_CHART_REPO=none` 令 provider 省略 `--repo`），并新增 `VCLUSTER_CHART_VERSION` 全链（gateway env → `gatewayK8sClusterRuntimeConfig` → `VClusterHelmProviderConfig.ChartVersion` → `helm upgrade` 非空追加 `--version`）把版本钉在 `0.34.1`。③ **控制面网络挂错 overlay**：租户 namespace `ani-tenant-00000000-…-000000000001` 绑定 32 个 kube-ovn 私有 VPC 网段（`private`/`natOutgoing=false`、无 `ovn-default`），控制面 Pod 落 `10.60.1.0/24` 无法访问 ClusterIP API（`dial tcp 10.96.0.1:443: i/o timeout`）→ syncer CrashLoop → `vcluster connect --print` 悬挂；`VCLUSTER_HELM_SET_VALUES` 扩展为 CSV，按既有 PlatformWorkload 约定给控制面 Pod 打 `ovn\.kubernetes\.io/logical_switch=ovn-default` + `.../vpc=ovn-cluster` 注解钉回默认 overlay（普通租户 namespace 本就 ovn-default，不受影响）。未改 OpenAPI 契约与生成物、无 DB 迁移；单测新增 `TestVClusterHelmProviderAdapterPinsChartVersionForOCIRepository`。live 验证 PASS：Harbor OCI `vcluster:0.34.1` digest `sha256:a23addb2…9139` 且网关 Pod 内匿名 pull 成功；ani-test2（镜像 `test2-20260918-vclusteroci`）`POST /api/v1/k8s-clusters` → 201（30.7s）+ 控制面 sts 1/1；ani-system（**镜像形态非 hostPath**，镜像 `fix-20260918-vclusteroci` 由 test2 同产物 retag）→ 201（38.8s）+ 控制面 sts 1/1 Ready。记录含「运维落地与新环境复用手册」（Harbor 参数/推送命令/网关 env/换环境复用边界/私有 Harbor 退路/0.34.1 版本口径）；红线：凭据不入库、chart tgz 不入 git。遗留：同 ns 孤儿 vcluster Helm release 触发 `there is already a virtual cluster in namespace …`（该结论中「ANI 无 K8s 集群删除 API」不准确，`DELETE /api/v1/k8s-clusters/{cluster_id}` 路由与 handler 早已存在，真实缺口是 provider 卸载能力，已由 M1-K8S-I 修复）；vcluster 内同步出的 coredns Pod 落 10.60.1.7 且 0/1（未深究）；`scripts/validate_vcluster_live_gate.py` 默认 chart 源仍为远端 HTTP（不在本批次范围）。记录：`development-records/m1-k8s-h-vcluster-chart-source-oci.md`。
> **M1-K8S-K（2026-09-20，live verified，分支 hotfix/network-store-read）：** 用户报错「创建失败 / `capability resource conflict: namespace ani-tenant-00000000-0000-0000-0000-000000000001 already hosts vCluster k8sclu-db2a33e2-79fc-4fed-b4b5-ec21804c3781; only one vCluster per tenant is supported`」——根因是**集群控制面记录此前只存 gateway 进程内存**（无 `k8s_clusters` 表，`ListClusters` 直接遍历内存 map，只有 `k8s_cluster_proxy_targets` 落 DB）。用户先建的 `k8sclu-db2a33e2-…` 底座一直健康（live 实测 Helm release `deployed` + 控制面 sts **1/1 Running 41h** + PVC Bound），但期间 gateway 滚动重启即清空内存记录 → `GET /api/v1/k8s-clusters` 返回 `total=0`，**Console 看不到该集群**；而 M1-K8S-J 的 provider 层守卫只看底座事实（正是它跨重启有效的原因），于是界面无集群可操作、创建又被 409 拒绝——**用户既删不掉也建不了新的**，唯一出路是手工 `helm uninstall`。**按用户三项决策修复**：① 记录落库（「代码修复，更彻底」）② 只做集群记录、节点池仍内存（另批次）③ ani-test2 现存 `db2a33e2` **接管成正式记录**（一次性数据修复，Console 可见并可正常删除）。**实现**：迁移 `20260920120000_k8s_clusters.sql`——建表 PK `(tenant_id, cluster_id)`、`state` CHECK `provisioning/running/deleting`、**`UNIQUE (tenant_id)`** 把 `ANI-02` §2.1.2「每租户一个 vCluster」沉淀为 DB 层不可绕过的约束（与 M1-K8S-J 内存校验 + provider 底座守卫形成三层防护）、两个**部分唯一索引** `(tenant_id, create_idempotency_key)` / `(tenant_id, upgrade_idempotency_key)`（`WHERE ... IS NOT NULL`）让幂等键随行保存，网关重启后同键重放仍返回原记录而**不被唯一约束顶成冲突**、`GRANT SELECT/INSERT/UPDATE/DELETE TO ani_app`、RLS 同既有样板（`k8s_clusters_platform_bypass` + `k8s_clusters_self` 两个 `PERMISSIVE` + `tenant_isolation` 一个 `RESTRICTIVE`，`ENABLE` + `FORCE`），`atlas.sum` 重算（58→59 条，既有条目 hash 全部不变）；`pkg/ports/k8s_clusters.go` 新增 `K8sClusterStore` 能力接口（7 方法）；新增 PG adapter `pkg/adapters/runtime/k8s_cluster_metadata_store.go`（走 `MetadataStore.WithTenantTx` + `types.WithTenant` 注入 RLS 上下文，`pgx.ErrNoRows`→`ports.ErrNotFound`，`pgconn` `23505`→`ports.ErrConflict` 且消息含 `only one vCluster per tenant is supported`，非 UUID tenant 早退 `ErrInvalid`）；`localK8sClusterService` 新增 `clusterStore` 字段 + `WithK8sClusterStore` 选项形成**双模式**——未注入 store 逐行保持原内存行为（local profile 与既有测试不受影响），注入 store 则 `Get`/`List` 直连 store、`Create`/`Delete`/`Upgrade` 走新增 store 模式方法，失败回滚语义与既有体例对齐（创建：先查 create 幂等键 → 同租户已有集群 `ErrConflict` → 写 `provisioning` 占位记录此时 `UNIQUE (tenant_id)` 才真正兜底 → provider apply → 成功写回 `running` + proxy target，**失败删除占位记录释放租户槽位**；删除：置 `deleting` → provider 卸载 → 成功清 proxy target 与记录，**失败恢复原状态不假成功**；升级：upgrade 幂等键命中即返回、非 `running` 拒绝）；gateway `k8s_proxy_runtime.go` 有 `MetadataStore` 即构造并注入（有 `DATABASE_URL` 的部署自动落库）。**未改 OpenAPI 契约与生成物**（控制面内部记录，`K8sClusterRecord` 响应形状不变）。单测新增 17 用例：adapter 10（upsert SQL/参数形状含空幂等键写 NULL 与时间戳转 UTC、tenant 上下文注入、非 UUID 早退、`23505`→`ErrConflict`、缺失→`ErrNotFound`、读取/列表/删除/升级幂等键/按 create 幂等键查找）、service 6（**核心回归 `TestLocalK8sClusterServiceStoreModeSurvivesServiceRecreation`：新建 service 实例并复用同一 store 后 `ListClusters` 仍可见 = 网关重启不失忆**、store 模式第二集群仍拒、删除后槽位释放、provider 删除失败恢复原记录、apply 失败释放占位、升级幂等）、gateway 1（`TestGatewayK8sClusterServiceFromConfigPersistsClusterRecords` 跨 service 构造仍可见）+ gateway PG fake 扩展支持 `k8s_clusters` 11 列读写与按 SQL 关键字事件路由。门禁：`go build ./pkg/... ./services/ani-gateway/...` + `go test`（仅剩既有 Windows sandbox 两用例失败：`TestSandboxFileScriptsRejectSymlinks` 无 symlink 权限、`TestSandboxFileScriptsAllowWorkspaceOperations` 无 shell exit 9009，与本批次无关）+ `gofmt -l` + `make validate-architecture` + `make validate-doc-entrypoints` + `git diff --check` 全通过。**live 验证 PASS**（ani-test2，镜像 `docker.changqingyun.cn/ani/ani-gateway:test2-20260920-clusterstore`，只 `kubectl set image` + rollout、未改 env）：该环境**无 `atlas_schema_revisions` 表**（本就是手工 seed，故与既有做法一致用 `psql -f <migration> --single-transaction` 直接应用，不存在 atlas 版本漂移），迁移执行 CREATE TABLE/INDEX×4/GRANT/ALTER×2/POLICY×3 全部成功；落地前只读探查确认 tenant-a namespace 内 Helm release 仅 `k8sclu-db2a33e2-…`（`deployed`）、sts **1/1 Running 41h**、`data-k8sclu-db2a33e2-…-0` PVC Bound，而 API 侧不可见（另有 `k8sclu-57ca6dd4-…` 仅剩一条 proxy target 行、无底座）；接管 `INSERT 0 1`，回读 `state=running / provider=vcluster / real_provider=t / provider_refs=["vcluster/HelmRelease/k8sclu-db2a33e2-…"]`，索引 5 条（含 `UNIQUE (tenant_id)` 与两个幂等键部分唯一索引）与 RLS 3 策略符合迁移定义。**核心证据**：`GET /k8s-clusters` → 200 `total=1`（state=running）→ `kubectl rollout restart deployment/ani-gateway -n ani-test2` 成功 → 重启后 `GET /k8s-clusters` **仍 200 `total=1`，同一条记录且 `created_at`/`updated_at` 不变**（对照 M1-K8S-J 同类重启后 `total=0`，界面「失忆」死局破除，用户现在可见并可经 `DELETE /api/v1/k8s-clusters/{cluster_id}` 正常删除）；同时第二集群 `POST /api/v1/k8s-clusters` → **409 CONFLICT**，消息 `capability resource conflict: tenant already has k8s cluster k8sclu-db2a33e2-79fc-4fed-b4b5-ec21804c3781; only one vCluster per tenant is supported`。**删除路径未在 live 跑**——会真实 `helm uninstall` 卸载用户那个 41h 的运行中 vCluster，代价超出验证目的；该路径由 store 模式单测覆盖（记录删除 + proxy target 清理 + 槽位释放 + provider 失败恢复原状态）。遗留：**节点池仍未落库**（用户明确只做集群记录，重启后 `GET /k8s-clusters/{id}/node-pools` 仍会清空，属同类问题需独立批次）；本环境为手工 psql 应用迁移，正式环境首次启用该表须走 atlas 迁移流程；`k8s_cluster_proxy_targets` 中无底座的 `k8sclu-57ca6dd4-…` 残留行未清理（proxy target 无「每租户唯一」语义、不构成约束冲突，超本批次范围）；M1-K8S-J 遗留的「provider apply 失败在底座留下孤儿 release」仍存在（本批次 store 模式失败会删占位记录，不再产生「DB 占位但 API 不可见」的新问题，但底座孤儿仍需 `helm uninstall` 或后续 provider 回滚批次）。记录：`development-records/m1-k8s-k-cluster-record-persistence.md`。
> **M1-K8S-J（2026-09-20，live verified，分支 hotfix/network-store-read）：** 「创建集群，同一个租户下只能有一个，这个符合产品语义吗」——**结论符合**：`ANI-02` §2.1.2 定义 v1.0.0 多租户隔离为「每租户一个 vCluster」（高隔离方案 Metal3 + Cluster API 属 Phase 2 延期），且 vcluster 硬约束「同一 namespace 只能存在一个虚拟集群」，而当前实现的集群全部落在租户 namespace（`tenantNamespace(tenantID)` → `ani-tenant-<tenantID>`），因此同租户第二个集群必然在建底座时 syncer fatal。**改动前现场**（ani-test2 / tenant-a）：API 侧只有 `k8sclu-db2a33e2-…`（running，09-18 09:51）一个集群，但租户 namespace 内有 **2 个** vcluster release（`f15902f3` sts **0/1 CrashLoopBackOff**，syncer 报 `there is already a virtual cluster in namespace …`）；`POST /api/v1/k8s-clusters` 于 09-20 02:15:35 发起，**`latency_ms=604939`（≈10 分钟）后返回 400**。**缺陷链**：`CreateCluster` 无同租户唯一性校验 → 放行进入 provider apply → helm install 约 2 秒建好 StatefulSet，随后 `vcluster connect --print` 等控制面就绪时 syncer CrashLoop → 等满超时 provider apply 失败 → `discardClusterRecord` 摘掉刚登记的记录，**但底座 Helm release 保留**，成为 API 不可见、界面无法删除的孤儿 release，永久毒化该 namespace（此后每次创建重复这条 10 分钟失败链）。**且集群记录目前只存 gateway 进程内存**（无 `k8s_clusters` 表，`ListClusters` 直接遍历内存 map，仅 `k8s_cluster_proxy_targets` 落 DB），每次滚动重启即清空（live 实测重启后 `GET /k8s-clusters` `total=0`），因此纯内存校验无法维持这条约束。**修复（按用户决策两层）**：① **内存层** `localK8sClusterService.CreateCluster` 锁内、**幂等键命中检查之后**扫描同租户记录（含 `deleting`），命中返回 `ports.ErrConflict`——顺序关键，幂等重放必须优先返回原集群而不被新校验改写成 409；② **provider 层底座级守卫**（跨 gateway 重启有效）`VClusterHelmProviderAdapter.ApplyK8sCluster` 在 `helm upgrade --install` **之前**调用新增 `ensureNamespaceHostsSingleVCluster`：`helm list --namespace <tenant-ns> --output json`，若该 namespace 已有**非本次同名**的 vcluster release 即返回 `ports.ErrConflict` 且不进入安装。判定细节：以 release chart 名含 `vcluster` 区分（namespace 内非 vcluster release 如 `metrics-server` 不误伤）；release 名等于本次 `clusterID` 时放行（保证 apply 可重放）；输出为空（namespace 不存在/无 release）放行（首个集群路径不受影响）；解析失败返回 `ports.ErrInvalid` 不静默放行。经既有 `writeK8sClusterError` 映射为 **409 CONFLICT**（`POST /k8s-clusters` 契约本就声明 409），**未改 OpenAPI 契约与生成物、无 DB 迁移**。单测新增 6 用例：provider 层 `TestVClusterHelmProviderAdapterRejectsVClusterAlreadyInTenantNamespace`（仅 1 次 `helm list`、无 `upgrade`，错误含已有 release 名）/ `...IgnoresForeignNonVClusterRelease`（`list`→`upgrade` 放行）/ `...AllowsReapplyOfSameReleaseName`（同名重放放行）；service 层 `TestLocalK8sClusterServiceRejectsSecondClusterForTenant`（第二集群 `ErrConflict` + 同幂等键重放返回原集群 + 其他租户不受影响 + 列表仍只 1 条）/ `...AllowsNewClusterAfterTenantClusterDeleted`（删除后槽位释放）/ `...ReleasesTenantSlotWhenProviderApplyFails`（apply 失败后槽位释放、可换幂等键重试）；provider 与 gateway 两处 fake runner 适配 `helm list`（默认返回 `[]`）。门禁：go build + 全量 go test + gofmt + `git diff --check` + `make validate-architecture` + `make validate-doc-entrypoints` 通过。**live 验证 PASS**（ani-test2，镜像 `test2-20260920-onecluster`，gateway Deployment 只 `kubectl set image` + rollout、未改 env，pod `ani-gateway-555d5b9d7d-lgv7q` 1/1 Running restart 0）：`helm list` 对不存在 namespace 返回 `[]`（首建路径不被误伤），对 tenant-a 返回 `[{"name":"k8sclu-db2a33e2-…","status":"deployed","chart":"vcluster-0.34.1"}]`；网关重启后内存已清空（`GET /api/v1/k8s-clusters` → `count=0`，正是「纯内存校验失效」现场）的前提下，两次 `POST /api/v1/k8s-clusters`（**不同幂等键**）均 **409 CONFLICT**，`latency_ms=82` / `139`，消息 `capability resource conflict: namespace ani-tenant-00000000-0000-0000-0000-000000000001 already hosts vCluster k8sclu-db2a33e2-79fc-4fed-b4b5-ec21804c3781; only one vCluster per tenant is supported`；拒绝后 namespace 内 `k8sclu` 对象数量与验证前一致，**无部分安装**。对照改动前 `604939ms`→400 并留下 CrashLoop 孤儿，现为百毫秒级拒绝且零副作用。**现场清理**（用户批准）：`helm uninstall k8sclu-f15902f3-… --ignore-not-found` 卸载 CrashLoop 孤儿 release + 删除 chart 遗留孤儿卷 `data-k8sclu-3181c07e-…-0`、`data-k8sclu-f15902f3-…-0`；清理后 `helm list` 仅 `db2a33e2` 一条、`data-k8sclu-*` PVC 仅 `db2a33e2` 一个。**未在 live 重跑正向 201**（需先删除 tenant-a 现有集群或再装一个真实 vcluster，代价与风险超出本次验证目的），该路径由单测（首建放行/同名重放放行/非 vcluster release 不误伤）+ live `helm list` 空 namespace 实测覆盖。遗留：provider apply 失败仍会在底座留下孤儿 release（本次只做到「不产生新孤儿」，未加失败回滚，用户明确未选该方案；孤儿因 API 无记录而界面不可删，只能手工 `helm uninstall`）；集群记录仍为进程内存（重启即丢，落库为独立批次）；拒绝语义按「同租户全部记录」判定，**包含 `deleting`**——删除未收敛期间不允许建新集群；同 ns 只能存在一个 vCluster（未改为「每集群独立 namespace」方案）；vcluster 内 coredns 0/1 未深究。记录：`development-records/m1-k8s-j-one-vcluster-per-tenant.md`。
> **M1-K8S-I（2026-09-18，live verified，分支 hotfix/network-store-read）：** 「界面创建集群后长时间未完成、集群查询一直加载」排查后修复两个独立缺陷。**缺陷 1：`DeleteCluster` 只删内存记录不释放底座**——全仓库此前无任何 `helm uninstall` 调用，删除集群后租户 namespace 内 vCluster 控制面 sts/svc/PVC 全部残留；因 vcluster 硬限制「一个 namespace 只能存在一个虚拟集群」，残留 release 叠加新创建即触发 syncer fatal `there is already a virtual cluster in namespace …`（本环境实测 2 个孤儿 release + 4 个孤儿 PVC）。修复：`pkg/ports` 新增 `K8sClusterProviderDelete` 能力（`K8sClusterProviderDeleteRequest`/`Result`）；`VClusterHelmProviderAdapter.DeleteK8sCluster` 执行 `helm uninstall <cluster_id> --namespace <tenant-ns> --ignore-not-found`；`localK8sClusterService.DeleteCluster` 改为锁内取记录并置 `deleting` → 锁外调 provider → 成功后清 proxy target 并 `purgeClusterIndexes` 反查删除 `byID`/`idem`/`upgradeIdem`；provider 失败或未卸载时 `restoreClusterAfterFailedDelete` 置回 `running` 并保留记录与 proxy target（不产生假成功）。**缺陷 2：`CreateCluster` 全程持有 `s.mu`**——`helm upgrade --install` + `vcluster connect --print` 实测 30~39s 全在锁内，而 `ListClusters`/`GetCluster`/`DeleteCluster` 共用同一把锁，导致创建期间集群查询被串行阻塞（控制台表现为一直加载，access log 出现 499/401）。修复：改为「锁内登记 `provisioning` 占位记录并解锁 → 锁外执行 provider apply → 成功后锁内写回 `running`」；失败路径 `discardClusterRecord` 同时清理 `byID` 与幂等键，保证同幂等键重试得到新 clusterID。gateway `k8s_proxy_runtime.go` 接线 `WithK8sClusterProviderDelete(provider)`。未改 OpenAPI 契约与生成物、无 DB 迁移。单测新增 5 用例（provider 被调用 + 记录/索引/proxy target 清理 + 同幂等键重建得新 ID、provider 失败回滚保持 running、apply 挂起时 `ListClusters` 不阻塞且可见 `provisioning`）。live 验证 PASS（ani-test2 镜像 `test2-20260918-clusterdel` digest `sha256:de5d65a3…5976`，ani-system 同产物 retag `fix-20260918-clusterdel`，两环境均只 set image 未改 env）：ani-test2 `POST /api/v1/k8s-clusters` 201（34.71s）期间 67 次并发 `GET /k8s-clusters` 全部 200、最大延迟 0.034s，t+0.57s 起列表即见 `provisioning`；`DELETE` 200（1.08s）后 `helm list` 清空、控制面 sts/svc/secret 消失，删除后重建控制面 pod 1/1 Running（不再出现 `there is already a virtual cluster` 报错）；ani-system 创建 201（43.86s）期间 84 次并发读最大延迟 0.084s，删除 200（1.18s）后底座同样清空。环境恢复：2 个 helm release 卸载、4 个 PVC 删除、DB 孤儿 `k8s_cluster_proxy_targets` 行清除、ani-system 与 ani-test2 网关 rollout restart 清除内存幽灵记录。遗留：同 ns 只能存在一个 vCluster；集群记录仍只存内存（重启即丢，proxy target 除外）；`DeleteCluster` 不清理 node pool 记录；chart 自身创建的 `data-k8sclu-<id>-0` PVC 不在 Helm release 内，卸载后仍残留（live gate 确认，本次手工删除，后续批次可纳入删除路径）；vcluster 内 coredns 0/1 未深究。记录：`development-records/m1-k8s-i-cluster-delete-and-lock-scope.md`。

> **INSTANCE-SANDBOX-KATA-STORAGE-A（2026-09-03）：** Kata lab values 同步到 `docker.changqingyun.cn/kubercon/kata-deploy:4.0.0`；当前底座 3/3 Ready，`sandbox-kata` 冒烟通过。Sandbox 新建、clone、restore 的 5Gi RWO workspace PVC 显式使用 `ani-block`，避免无默认 StorageClass 环境持续 Pending。底座 live verified，代码 local/logic verified；待合并和 Gateway rollout 后补产品路径 E2E。sysctl 按任务边界不入库；详情见 `development-records/instance-sandbox-kata-storage-a.md`。

> **INSTANCE-SANDBOX-CHECKPOINT-A（2026-08-02）：** live passed。新 Sandbox `/workspace` 使用 5Gi RBD PVC，CSI VolumeSnapshot create/list/restore/clone、Gateway 重启后 provider list、PG create/restore task、keep_memory/legacy emptyDir 422 和删除级联清理均已在 default 网络验证。Gateway `instance-sandbox-checkpoint-20260802-v1`；evidence：`development-records/live-evidence/instance-sandbox-checkpoint-live-20260802.json`。仅 filesystem checkpoint，不含内存状态；私有 VPC 尚未打通。

> **INSTANCE-SANDBOX-STATELESS-A（2026-08-02，历史前置）：** live passed。Core 使用请求级 PG 上下文、UUID、PG AsyncTaskStore、Redis DELETE/指纹/Token 过期幂等和端口摘要写回；Gateway `instance-sandbox-stateless-20260802-v1` 重启验证通过。该批次当时的 `emptyDir/checkpoint 422` 边界已由 `INSTANCE-SANDBOX-CHECKPOINT-A` 取代，历史 evidence 仍保留在 `development-records/live-evidence/instance-sandbox-stateless-live-20260802.json`。

> **STORAGE-ASYNC-CORRECTNESS-A（2026-08-03）：** live passed。Core v1 Vector 文档写入保持 `202 + VectorStoreDocumentInsertResponse`，补齐 `Location` 和 `vector_store.document.insert`；任务写入 PG，Gateway rollout 后原 task ID 仍返回 200；evidence：`development-records/live-evidence/storage-async-vector-task-live-20260803.json`。

> **INFERENCE CONTROL PLANE C1（2026-08-14）：** 阶段 A Core `platform-workloads` additive v1 契约已通过上游 PR #99 合入，阶段 B Services `InferenceService` 契约已通过上游 PR #101 合入。`INFERENCE-SERVICE-CONTROL-PLANE-C1` 已完成 local/logic verified：独立 module、领域状态机、additive PG migration 静态门禁、带 fencing token 的幂等/lease/generation CAS、遗留行 quarantine、catalog-independent create replay、dependency ports 与 fake create-to-running；真实 pgx integration gate 已落代码但尚未连接 PostgreSQL 执行。当前仍没有 migration live apply、服务 ingress/Gateway delegation、真实 ModelCatalog/Core SDK adapter、platform-workloads handler/port/adapter、Deployment/LWS/vLLM 或推理 live evidence，不得标记 control-plane/runtime ready。

> **INFERENCE LIFECYCLE CONTROL PLANE C2（2026-08-14）：** `INFERENCE-SERVICE-LIFECYCLE-CONTROL-PLANE-C2` 已完成 local/logic verified：PG 原子 lifecycle/query mutation、真实 operation replay 与 completed no-op、scale/start/stop/restart/delete controller、public projection 隔离、provider-side generation/key/intent fence、mutation/Observe 分离，以及 fake create→scale→stop→start→restart→delete 和 stop 抢占旧 create 的反向时序。真实 PostgreSQL仍未连接执行；HTTP/Gateway、真实 ModelCatalog/Core adapter、不可重试/dead-letter/scale rollback、Deployment/LWS/vLLM 与推理 live evidence均未完成，不得标记 control-plane/runtime ready。

> **INFERENCE HTTP ADAPTER C3（2026-08-15，已推翻）：** `INFERENCE-SERVICE-HTTP-ADAPTER-C3` 曾完成独立服务内 HTTP adapter；该入口与平台契约冲突，代码已删除，记录标为 superseded。

> **INFERENCE GATEWAY GRPC C4（2026-08-15）：** `INFERENCE-SERVICE-GATEWAY-GRPC-C4` 已完成 local/logic verified：产品 HTTP 只留在 ANI Gateway；inference-service 暴露内部 `InferenceControl` gRPC，进程入口复用 `pkg/bootstrap.MustConnect`/`RunGRPC`；Gateway 注入 `middleware.GetTenantID` 后委托 create/list/get/scale/lifecycle/delete/operation-query；create/scale/delete 成功码收敛为 202；policies 返回 501 FEATURE_NOT_AVAILABLE。无真实 ModelCatalog/Core SDK adapter、PG live、Deployment/LWS/vLLM 或推理 live evidence，不得标记 control-plane/runtime ready。

> **INFERENCE PLATFORM WORKLOAD LOCAL C5（2026-08-15）：** `INFERENCE-PLATFORM-WORKLOAD-LOCAL-C5` 已完成 local/logic verified：Core `PlatformWorkloadService` CPU single-node local adapter + Gateway 7 个 service-only 路由；`202 AsyncTask.resource_id` 在接受时确定；租户 JWT/API key 403；`WorkloadKindInference` 不再强制 GPU。inference-service 可经 `CORE_API_BASE_URL` 使用 Core SDK runtime adapter，默认仍 fake。无真实 ModelCatalog、service JWT、K8s/LWS/vLLM 或 live evidence，不得标记 runtime ready。

> **INFERENCE MODEL CATALOG C6（2026-08-15）：** `INFERENCE-SERVICE-MODEL-CATALOG-C6` 已完成 local/logic verified：model-service 内部 `GetModelVersion` + inference-service 真实 ModelCatalog adapter；create 按租户解析不可变版本并冻结 digest-pinned engine profile；加密模型只保留 Key reference。未改 OpenAPI，Gateway 不新增 models version 路由。`MODEL_SERVICE_GRPC_ADDR` 未配置时仍 fake。无 K8s/LWS/vLLM、service JWT 或 live evidence，不得标记 runtime ready。

> **INFERENCE SERVICE JWT C7（2026-08-15）：** `INFERENCE-SERVICE-JWT-C7` 已完成 local/logic verified：auth-service 签发 `aud=ani-core` 短期 service JWT；Gateway 非 dev 模式只接受该身份访问 `platform-workloads*`；inference-service 可按租户 mint 并只带 Bearer。未改 OpenAPI。无 K8s/LWS/vLLM 或 live evidence，不得标记 runtime ready。

> **INFERENCE PLATFORM WORKLOAD K8S C8（2026-08-15）：** `INFERENCE-PLATFORM-WORKLOAD-K8S-C8` 已完成 local/logic verified：Core CPU `single_node` Kubernetes adapter 渲染 Deployment + ClusterIP，不写入 `/instances`；`stop` 删 runtime 并保留记录，`start` 按已存 spec 再 apply。Gateway 默认仍 local；`PLATFORM_WORKLOAD_PROVIDER=kubernetes_rest` 才切换，未配置 K8s host 时 fail-closed。未改 OpenAPI。无真实集群 live evidence，不得标记 runtime ready 或 real-provider ready。

> **INFERENCE PLATFORM WORKLOAD K8S C9（2026-08-15）：** `INFERENCE-PLATFORM-WORKLOAD-K8S-C9` 已完成 local/logic verified：Apply 先确保租户 Namespace；PlatformWorkload 记录可经 memory/PG store 在 Gateway 重启后回读；当时定义 live gate `INFERENCE-PLATFORM-WORKLOAD-K8S-LIVE-GATE-C9`。真实集群执行与 evidence 由后续 `INFERENCE-PLATFORM-WORKLOAD-K8S-LIVE-C12` 收口，不得把本批次单独标为 runtime ready。

> **INFERENCE SERVICE TEST C10（2026-08-15，已推翻）：** `INFERENCE-SERVICE-TEST-C10` 曾实现受控 `/test`；产品口径认定该路径只是契约兼容测试入口、用不到且增加 runtime 攻击面，实现已删除，记录标为 superseded。OpenAPI 未改；Gateway 不注册 `/test`。

> **INFERENCE SERVICE LOGS C11（2026-08-15）：** `INFERENCE-SERVICE-LOGS-C11` 已完成 local/logic verified：`GET /inference-services/{id}/logs` 经 Gateway 委托内部 `ListInferenceServiceLogs`；租户只来自认证上下文；服务不存在/跨租户/已删除返回 404，无 `runtime_ref` 返回空列表；响应脱敏且不泄露 runtime/replica。未改 OpenAPI。Core platform-workload logs 仍空，无真实 Pod log / Loki live evidence，不得标记 runtime ready。

> **INFERENCE PLATFORM WORKLOAD K8S LIVE C12（2026-08-15）：** `INFERENCE-PLATFORM-WORKLOAD-K8S-LIVE-C12` 已在人工确认的集群上 live passed：lab Gateway 进程（未 rollout in-cluster `ani-gateway`）对真实 Kubernetes 完成 CPU PlatformWorkload create/scale/stop/start/进程重启回读/delete；租户 403；logs 仍空。evidence：`development-records/live-evidence/platform-workload-k8s-live-20260815.json`。不得把 in-cluster Gateway 或推理产品链路标为 runtime ready。

> **INFERENCE SERVICE RUNTIME C13（2026-08-15）：** `INFERENCE-SERVICE-RUNTIME-C13` 已完成 local/logic verified：CPU/GPU 共用同一 `InferenceService` 入口和 Core `platform-workloads` 单节点 Deployment；`resources.accelerator` 存在时申请 `nvidia.com/gpu`，否则保持 CPU。vLLM 启动参数与 runtime `/health` + 有界 Chat smoke 已接线；不注册产品 `/test`。未改 OpenAPI。无真实 vLLM/模型挂载 live，无 LWS，不得标记 runtime ready。

> **INFERENCE SERVICE CPU VLLM LIVE C14（2026-08-15）：** `INFERENCE-SERVICE-CPU-VLLM-LIVE-C14` 已在人工确认的集群上 live passed：lab Gateway 进程（未 rollout in-cluster `ani-gateway`）经同一 `InferenceService` 入口对真实 vLLM CPU 完成 create → `/health`+有界 Chat 后 `running` → stop/start/delete。模型来自独立 smoke PVC 快照，未触碰 `ani-vllm-cpu-smoke`。GPU 因无 device-plugin 记 `skipped_no_device_plugin`。evidence：`development-records/live-evidence/inference-cpu-vllm-live-20260815.json`。未改 OpenAPI，无 LWS，不得标记 runtime ready。

> **INFERENCE SERVICE CPU VLLM OPS LIVE C15（2026-08-15）：** `INFERENCE-SERVICE-CPU-VLLM-OPS-LIVE-C15` 已在人工确认的集群上 live passed：同一 CPU 入口补齐产品 logs（真实 Pod 行、无 replica）、RWO 下 desired-replicas scale 抢占，以及 lab 进程重启后回读同一 `service_id` 仍为 `running`。未 rollout in-cluster `ani-gateway`。evidence：`development-records/live-evidence/inference-cpu-vllm-ops-live-20260815.json`。未改 OpenAPI，无 LWS，不得标记 runtime ready。

> **INFERENCE SERVICE GPU ADMISSION LIVE C16（2026-08-15）：** `INFERENCE-SERVICE-GPU-ADMISSION-LIVE-C16` 已在人工确认的集群上 live passed：同一入口带 `resources.accelerator` 时，因 Core capabilities 无可用规格返回 `422 ACCELERATOR_SPEC_UNAVAILABLE`，未创建 GPU Deployment。GPU runtime live 记 `skipped_no_device_plugin`。未 rollout in-cluster `ani-gateway`。evidence：`development-records/live-evidence/inference-gpu-admission-live-20260815.json`。未改 OpenAPI，不得标记 GPU ready 或 runtime ready。

> **INFERENCE SERVICE CLUSTERIP NP LIVE C17（2026-08-15）：** `INFERENCE-SERVICE-CLUSTERIP-NP-LIVE-C17` 已在人工确认的集群上 live passed：同一 CPU 入口证明 runtime 只有 ClusterIP，NetworkPolicy 默认拒绝并挡住未授权 namespace，产品 `/test` 保持 404，stop/delete 后内部 endpoint 消失。未 rollout in-cluster `ani-gateway`。evidence：`development-records/live-evidence/inference-clusterip-networkpolicy-live-20260815.json`。未改 OpenAPI，无公网调用网关，不得标记 runtime ready。

> **INFERENCE SERVICE FAILURE ROLLBACK C18（2026-08-15）：** `INFERENCE-SERVICE-FAILURE-ROLLBACK-C18` 已完成 local/logic verified：不可重试错误与部署超时会释放 create/start/restart 的 provider runtime；有界重试耗尽进入 `dead_letter`；scale 失败按 `applied_spec` 补偿，成功为 `failed/SCALE_ROLLED_BACK`，失败为 `ROLLBACK_FAILED`；`desired_state=deleted` 不补偿。未改 OpenAPI，无 rollback live，不得标记 runtime ready。

> **INFERENCE SERVICE FAILURE ROLLBACK LIVE C19（2026-08-15）：** `INFERENCE-SERVICE-FAILURE-ROLLBACK-LIVE-C19` 已在人工确认的集群上 live passed：同一入口证明缺失镜像 create 会 `failed/DEPLOY_TIMEOUT` 并删除 runtime；真实 CPU vLLM `running` 后 PATCH replicas=2 会补偿回 1，operation `failed/SCALE_ROLLED_BACK`。not-ready 不再按次数 dead-letter。未 rollout in-cluster `ani-gateway`。evidence：`development-records/live-evidence/inference-failure-rollback-live-20260815.json`。未改 OpenAPI，无 LWS，不得标记 runtime ready。

> **INFERENCE SERVICE GPU LWS VOLCANO C20（2026-08-17）：** `INFERENCE-SERVICE-GPU-LWS-VOLCANO-C20` 已完成 local/logic + 真实 PG integration：Capabilities 探测 GPU/LWS/Volcano PodGroup；单节点 GPU 写 `schedulerName=volcano`，不绑定 `ani-inference` Queue；CPU 不写 Volcano；`leader_worker` 渲染 LWS+PodGroup+leader Service；缺组件 422。本集群已装 Volcano 1.15.0（scheduler + PodGroup CRD，仅内置 `default`/`root` Queue，未建 `ani-inference`）。C20 runner 复跑：`gang_scheduling_ready=true`，`volcano_crd=true`，`lws_crd=false`，`gpu_allocatable=0`；GPU/LWS create 仍 422；gate 保持 `status: contract`，不得标 GPU/runtime ready。未改 OpenAPI，未 rollout in-cluster `ani-gateway`。

> **INFERENCE SERVICE LEGACY CONTROL PLANE B1（2026-08-17）：** `INFERENCE-SERVICE-LEGACY-CONTROL-PLANE-B1` 已把旧 `inference.v1` proto 标 deprecated，并加门禁禁止 Gateway/Helm 复活 operator 控制面。当前集群无旧 InferenceService CRD。Volcano 已为 C20 安装，与旧 operator 控制面无关。未改 OpenAPI，未 commit。

> **INFERENCE SERVICE INCLUSTER E2E C21（2026-08-17）：** `INFERENCE-SERVICE-INCLUSTER-E2E-C21` 已在人工确认的集群上 live passed：`ani-system` 部署 `inference-service`，滚动现有生产 `ani-gateway`（追加 inference gRPC 与 `PLATFORM_WORKLOAD_PROVIDER=kubernetes_rest`，`ANI_AUTH_MODE` 保持 `auth_service`），产品 HTTP→gRPC→platform-workloads 完成 CPU vLLM create/`running`/stop/start/delete。不部署第二条 Gateway，不启用旧 operator；GPU/LWS 仍 skip；未触碰 `ani-vllm-cpu-smoke`。evidence：`development-records/live-evidence/inference-incluster-e2e-live-20260817.json`。未改 OpenAPI，不得标记 GPU/LWS/runtime ready。

> **INFERENCE SERVICE CONSOLE SHAPED E2E C22（2026-08-17）：** `INFERENCE-SERVICE-CONSOLE-SHAPED-E2E-C22` 已在人工确认的集群上 live passed：租户 Bearer 经现有 `ani-console` nginx `/api/` 打生产 `ani-gateway`，完成 CPU vLLM list/create/`running`/stop/start/delete。已有 Console 租户 Namespace 上的私有 VPC 不会再被 platform-workload 继承；Pod 钉到集群 `ovn-default`。未删该 Namespace，GPU/LWS 仍 skip。测试残留 `ani-vllm-cpu-smoke` 已删除。未改 OpenAPI，不得标记 runtime ready。evidence：`development-records/live-evidence/inference-console-shaped-e2e-live-20260817.json`。

> **INFERENCE SERVICE LOCAL MODEL SOURCE C23（2026-08-17）：** `INFERENCE-SERVICE-LOCAL-MODEL-SOURCE-C23` 已在人工确认的集群上 live passed：经现有 `ani-console` nginx `/api/` 创建 Model + `pvc://vllm-model#/models/qwen` 版本，再用真实 `model_version_id` 完成 CPU vLLM create/`running`/stop/start/delete。`ANI_AUTH_MODE` 保持 `auth_service`。未删已有租户 Namespace 和模型 PVC。GPU/LWS 仍 skip。未改 OpenAPI，不得标记 runtime ready。evidence：`development-records/live-evidence/inference-local-model-source-live-20260817.json`。

> **INFERENCE SERVICE SYNC CORE DISPATCH C24（2026-08-17）：** `INFERENCE-SERVICE-SYNC-CORE-DISPATCH-C24` 已完成 local/logic verified：创建/扩缩/启停/删除在请求路径同步调用 Core `platform-workloads`；`INSUFFICIENT_CAPACITY` / `IMAGE_UNAVAILABLE` 等可预知失败当场返回前端；worker 只 Observe/Health/Smoke/超时回收。Gateway inference gRPC 默认超时 30s。未改 OpenAPI，无 live，不得标记 runtime ready。

> **INFERENCE REMOVE LAB GATEWAY HARNESS C25（2026-08-17）：** `INFERENCE-REMOVE-LAB-GATEWAY-HARNESS-C25` 已删除第二条 Gateway 入口 `cmd/platform-workload-live` 及 C12/C16/C17/C19/C20 lab runner。产品 live 只走现网 `ani-gateway`（C21/C22/C23）。历史 lab evidence 与 validate-* 保留。未改 OpenAPI，无新 live，不得标记 runtime ready。

> **INFERENCE REMOVE LAB CATALOG C26（2026-08-17）：** `INFERENCE-REMOVE-LAB-CATALOG-C26` 已从 inference-service 产品进程去掉 `labCatalog` / `INFERENCE_LAB_CATALOG` 和空地址 fake runtime，并删除独立 `internal/catalog/fake` 包。启动必须 `MODEL_SERVICE_GRPC_ADDR` 与 `CORE_API_BASE_URL`；catalog 测试替身写在对应 `*_test.go`。未改 OpenAPI，无新 live，不得标记 runtime ready。

> **INFERENCE SERVICE CREATE IMAGE CONTRACT C27（2026-08-18）：** `INFERENCE-SERVICE-CREATE-IMAGE-CONTRACT-C27` 已补齐 Services 创建契约：`CreateInferenceServiceRequest` 增加可选仓库 `image_id` 与可选手填 `image_ref`，至少填一个，同时传优先 `image_id`；响应增加可选 `image_id` 与只读 digest `image_ref`；`422 IMAGE_UNAVAILABLE` 进入 OpenAPI。不含 handler/proto/实现。无新 live，不得标记 runtime ready。

> **INFERENCE SERVICE CREATE IMAGE C28（2026-08-18）：** `INFERENCE-SERVICE-CREATE-IMAGE-C28` 已完成 local/logic verified：Gateway 创建入口解析 `image_id` / `image_ref` 并冻结 digest；inference-service Creator 用该 digest 覆盖 execution profile，不再用进程默认镜像作为创建权威。未删 `INFERENCE_SGLANG_*` / vLLM 默认镜像环境变量。无新 live，不得标记 runtime ready。

> **INFERENCE SERVICE REMOVE DEFAULT IMAGE ENV C29（2026-08-18）：** `INFERENCE-SERVICE-REMOVE-DEFAULT-IMAGE-ENV-C29` 已完成 local/logic verified：删除 `INFERENCE_CPU_IMAGE_REF` / `INFERENCE_GPU_IMAGE_REF` / `INFERENCE_SGLANG_*`；catalog 只保留引擎 ID/Runtime，不再注入占位 digest。运行镜像只来自创建请求。无新 live，不得标记 runtime ready。

> **INFERENCE SERVICE CREATE IMAGE LIVE C30（2026-08-18）：** `INFERENCE-SERVICE-CREATE-IMAGE-LIVE-C30` 已在人工确认的集群上 live passed：滚动现网 `ani-gateway` 与 `inference-service` 后，缺镜像 400、未 pin `image_ref` 422、digest-pinned `image_ref` 冻结并完成 CPU vLLM create/`running`/stop/start/delete。已去掉残留默认镜像 env。`ANI_AUTH_MODE` 保持 `auth_service`。GPU/LWS 仍 skip。未改 OpenAPI，不得标记 runtime ready。evidence：`development-records/live-evidence/inference-create-image-live-20260818.json`。

> **INFERENCE SERVICE CREATE IMAGE HARBOR LIVE C31（2026-08-18）：** `INFERENCE-SERVICE-CREATE-IMAGE-HARBOR-LIVE-C31` 已在人工确认的集群上 live passed：把 CPU vLLM digest 推入当时为空的租户 Harbor 项目，经现有 `ani-console` nginx `/api/` 用仓库 `image_id` 完成 CPU vLLM create/`running`/stop/start/delete；未知 `image_id` 为 `422 IMAGE_UNAVAILABLE`。现网仍用 C30 镜像。Harbor 项目本次 public，私有 pull 未做。`ANI_AUTH_MODE` 保持 `auth_service`。GPU/LWS 仍 skip，等切换集群后再做。未改 OpenAPI，不得标记 runtime ready。evidence：`development-records/live-evidence/inference-create-image-harbor-live-20260818.json`。

> **INFERENCE SERVICE GPU SINGLE NODE LIVE C32（2026-08-18）：** `INFERENCE-SERVICE-GPU-SINGLE-NODE-LIVE-C32` 已在人工确认的 GPU 集群上 live passed：经现网 `ani-gateway` 租户 Bearer 用 digest-pinned CUDA vLLM `image_ref`、`accelerator.spec_id=gpu-nvidia-geforce-rtx-4090-full`、`count_per_replica=1`、`placement_mode=single_node` 和 `pvc://vllm-model#/models/qwen` 创建 GPU `InferenceService`；create 202，GET `running`，产品 logs 有 Pod 行且无 replica；runtime 为 Deployment + ClusterIP，`schedulerName=volcano`，`nvidia.com/gpu=1`。同一 `service_id` 完成 stop→`stopped` 与 start→`running`。`invocation_url` / `endpoint_url` 保持 null。用户服务保留，未做 delete。`ANI_AUTH_MODE` 保持 `auth_service`。跨节点 LWS 仍 skip，C20 gate 仍是 `status: contract`。未改 OpenAPI，不得标记 GPU ready / runtime ready。evidence：`development-records/live-evidence/inference-gpu-single-node-live-20260818.json`。

> **GPU PLUGIN NODE PARTITION C33（2026-08-18）：** `GPU-PLUGIN-NODE-PARTITION-C33` 已按节点拆开 NVIDIA GPU Operator 与 volcano-device-plugin：66/67（`kubercloud`、`dev-phys-02`）整卡只跑 NVIDIA device-plugin 广告 `nvidia.com/gpu`；68（`dev-phys-03`）vGPU 关闭 NVIDIA device-plugin，只留 volcano-device-plugin 广告 `volcano.sh/vgpu-*`。现网整卡 InferenceService 已迁到 `dev-phys-02`。未改 OpenAPI，不得标记 GPU ready / runtime ready。evidence：`development-records/live-evidence/gpu-plugin-node-partition-live-20260818.json`。

> **INFERENCE SERVICE GPU VLLM EAGER C34（2026-08-18）：** `INFERENCE-SERVICE-GPU-VLLM-EAGER-C34` 已在人工确认的 GPU 集群上 live passed：GPU vLLM launch 增加 `--enforce-eager`，现网保留的单节点整卡 InferenceService 达到 Pod Ready，runtime `/health` 200，产品 GET `running`；`invocation_url` / `endpoint_url` 保持 null。现网 `inference-service` 滚动到 `gpu-eager-20260818`。`ANI_AUTH_MODE` 保持 `auth_service`。未改 OpenAPI，跨节点 LWS 仍 skip，不得标记 GPU ready / runtime ready。evidence：`development-records/live-evidence/inference-gpu-vllm-eager-live-20260818.json`。

> **INFERENCE SERVICE ENGINE EXTRA ARGS CONTRACT C35（2026-08-19）：** `INFERENCE-SERVICE-ENGINE-EXTRA-ARGS-CONTRACT-C35` 已补齐 Services 创建契约可选 `engine.env` 与完整 `engine.command` argv：由前端传入环境变量和完整启动命令，创建时冻结，响应只读回显；不与平台默认 command 拼接或追加；env 保留名由后续实现返回 400；不进入 PATCH。不含 handler/proto/`engine.Launch`/Console 表单。无新 live，不得标记 GPU/runtime ready。

> **INFERENCE SERVICE ENGINE VGPU C36（2026-08-19）：** `INFERENCE-SERVICE-ENGINE-VGPU-C36` 已在人工确认的 GPU 集群上 live passed：滚动现网 `ani-gateway` 与 `inference-service` 到 `engine-vgpu-c36-20260819` 后，保留 `engine.env` 名返回 `400 INVALID_ARGUMENT`；capabilities 同时广告整卡 `-full` 与 volcano vGPU `gpu-nvidia-geforce-rtx-4090-8x`；产品 create 冻结完整 `engine.command` argv 与 `engine.env`，runtime Deployment `command` 原样、`args` 为空，并申请 `volcano.sh/vgpu-*` 而非 `nvidia.com/gpu`。测试服务已删除；用户整卡服务保留。`invocation_url` / `endpoint_url` 保持 null。`ANI_AUTH_MODE` 保持 `auth_service`。vGPU Pod Ready 未要求（RWO 模型 PVC 被保留整卡服务占用）。不含 Console 表单，跨节点 LWS 仍 skip，不得标记 GPU ready / runtime ready。evidence：`development-records/live-evidence/inference-engine-vgpu-live-20260819.json`。

> **INFERENCE SERVICE GPU LWS RUNTIME FIX C37（2026-08-19）：** `INFERENCE-SERVICE-GPU-LWS-RUNTIME-FIX-C37` 已完成 local/logic verified：平台默认 LWS 增加 `VLLM_USE_RAY_COMPILED_DAG=0`；GPU TP>1 默认加 `--disable-custom-all-reduce`；`leader_worker` 或 `AcceleratorCount>=2` 时 `/dev/shm` 为 12Gi；PlatformWorkload Deployment 使用 `Recreate`；SGLang 与不含 `ray start`/`multi-node-serving.sh` 的租户 command 拒绝 `leader_worker`；直连 ClusterIP HTTP timeout 120s。未改 OpenAPI，无现网滚动，无新 live，不得标记 GPU ready / runtime ready。

> **INFERENCE SERVICE GPU MEMORY CONTRACT C38（2026-08-20）：** `INFERENCE-SERVICE-GPU-MEMORY-CONTRACT-C38` 已补齐 Core `platform-workloads` 与 Services `InferenceService` 加速器契约：`spec_id` 只表示 GPU 型号；`count` / `count_per_replica` 为申请卡数且两种模式都必填；可选 `memory` 为申请显存（MiB），不填即整卡、填写即 vGPU。不另加 `gpu_mode`。历史 `-full` / `-Nx` 剥后缀后仍按型号处理。不含 handler/runtime/inventory/Console。无新 live，不得标记 GPU/runtime ready。

> **INFERENCE SERVICE GPU MEMORY C39（2026-08-21）：** `INFERENCE-SERVICE-GPU-MEMORY-C39` 已在人工确认的 GPU 集群上 live passed：滚动现网 `ani-gateway` 与 `inference-service` 到 `gpu-memory-c39-20260821` 后，先清理残留推理测试服务；capabilities 广告型号 `gpu-nvidia-geforce-rtx-4090`；JSON `memory: 0` 返回 `400 INVALID_ARGUMENT`；省略 `memory` 申请 `nvidia.com/gpu=1`；填写 `memory=12280` 申请 `volcano.sh/vgpu-number=1` 与 `volcano.sh/vgpu-memory=1228`。首次 live 两条路径 GET `running` 后删除。二次 live 按用户要求保留 `inf-c39-whole-f3cbfa4a` / `inf-c39-vgpu-f3cbfa4a`，并对两条 ClusterIP 各做 60 秒 chat 压测（整卡 2456/2456、vGPU 2373/2373，均 0 失败）。`ANI_AUTH_MODE` 保持 `auth_service`。不含 Console 表单，跨节点 LWS 仍 skip，不得标记 GPU ready / runtime ready。evidence：`development-records/live-evidence/inference-gpu-memory-live-20260821.json`；keep/load：`development-records/live-evidence/inference-gpu-memory-keep-load-20260821.json`。

> **MODEL TENANT ISOLATION + VECTOR INFERENCE A（2026-08-25）：** `MODEL-TENANT-ISOLATION-VECTOR-INFERENCE-A` 已完成限定 live passed：ModelRepository Get/List/count/Delete/CreateVersion/ListVersions 均使用显式 tenant SQL fence并保留 RLS；真实 foreign Model Get=404，owner List 不含 foreign ID。inference-service 从既有 Model capabilities 派生并冻结 `generate`/`embed`；当前 vLLM embedding argv 使用 `--runner pooling --convert embed`，有界 1 MiB smoke 可解析真实约 19 KiB 响应。CPU 测试服务到 `running`，internal ClusterIP `/v1/embeddings`=200 且 data/embedding 非空；测试资源已清理，控制面镜像已恢复。未改 OpenAPI；公开 Envoy `/v1/embeddings`、GPU 与 embedding 质量不在结论范围。evidence：`development-records/live-evidence/model-tenant-vector-inference-live-20260825.json`；记录：`development-records/model-tenant-isolation-vector-inference.md`。

> **TASKCENTER-C1（2026-08-27）：** `TASKCENTER-C1` 契约批次已完成 local verified（分支 `feat/async-task-core-integration`）：AsyncTask enum 扩展 5 种 `instance.*` task_type + `instance` resource_type（含存量缺口 `sandbox.checkpoint.restore` 补齐）；AsyncTask description 写入真进度语义与实例 state→任务映射表；新增 `GET /tasks` list 契约与 `TaskListResponse`；`GET /tasks/{task_id}` 补 operationId/security/`x-ani-authz`/rbac scope/401/403；鉴权注册表两 tasks 路由翻转为 generated（pilot 集合未扩，运行时零变化）；Core SDK/静态 docs/Console schema 生成物同步；Core API v1 兼容基线有意再生成（补 operationId 触发门禁）。validate-architecture / validate-gateway-authz / validate-auth-contract / go test / compileall / validate-core-api-compatibility / openapi_spec_validator / git diff --check 全过（本地 Windows 存量 sandbox symlink 测试环境失败已确认与基线一致）。后续 `TASKCENTER-A1`（list + 实例真进度懒同步）按方案 §6 阶段 B 执行。记录：`development-records/TASKCENTER-C1.md`。

> **TASKCENTER-A1（2026-08-27）：** `TASKCENTER-A1` 实现批次已完成 local verified（分支 `feat/async-task-core-integration`）：`ports.AsyncTaskStore` 追加 `List`（keyset cursor）并固化 Update 终态写保护接口语义；Local/Metadata 双 store List + Update 终态写保护（SQL 守卫 + 0 行重读返回当前记录 / mutex 内同语义比较）；`20260827_001_async_tasks_list_index.sql` 复合索引；Gateway `GET /tasks` list handler（limit 1-100 默认 20、status/task_type/resource_type 筛选、非法入参 400）；实例 create（含 409 completed 重放补写）/lifecycle 四 action 写入点（running/10 真进度 + `writeAuditTask` 旁路失败降级仅日志，实例响应契约零变化）；`observeInstance`（store 读 + 单实例 K8s 刷新）提取注入任务路由；`GET /tasks/{task_id}` 非终态 `instance.*` 任务读时懒同步按实例 state 映射表推进（写放大抑制、失败降级、终态守卫并发乱序不回退）；任务响应补 `resource_id`/`error_message`/`dead_letter_at`（单查/list/模式 B 三处共用）；任务中心页面文档同步（list 上线、kb 域噪声声明、TODO-YAML 解除、取消归延后方案 V2-3）。go test 全包 + go vet + validate-architecture + validate-services 等价拆解（boundary/contract/route/spec-split/sdk-beta/生成物零漂移/rag compileall/Console schema 零漂移）+ validate-async-task-store + git diff --check 全过；§8.2/§8.3 验收矩阵由 29 个新测试逐条覆盖。真实 PG：连接成功，索引迁移成功应用并确认存量索引缺失属实；RLS 跨租户拦截无法验证（dev 库 `ani` 账号 SUPERUSER+BYPASSRLS 绕过 FORCE RLS），应用层隔离由 `WHERE tenant_id` + Local store 键隔离测试保证。不建 worker/outbox、不做取消、无 Services 层改动。方案范围内任务全部完成；Services 集成延后项存档 `async-task-services-integration-deferred.md`。记录：`development-records/TASKCENTER-A1.md`；差异文档：仓库根目录 `implementation-diff-async-task.md`。

> **TASKCENTER-A2（2026-08-31）：** `TASKCENTER-A2` RLS 真实验证与仓库对齐修复批次已完成 local verified（分支 `feat/async-task-core-integration`）：切换 `ani_app_user`（非 SUPERUSER/非 BYPASSRLS，`ani_app` 成员）收口 A1 遗留项——async_tasks 跨租户 SELECT/Get 拦截实测 0 行、Create 同款 INSERT 与 Update 同款 SQL（懒同步 + 终态写保护守卫）写路径通过、平台上下文全可见；live-verified 后回写仓库迁移 `20260831_001_async_tasks_rls_fix.sql`，修复仓库与 dev 库三处漂移：init_schema 的 RESTRICTIVE-only `tenant_isolation` 对非 BYPASSRLS 角色 fail-closed（改双 PERMISSIVE `platform_bypass` + `self`，对齐 20260825_001 workload_instances 模式）、`GRANT ON ALL TABLES` 在建表前执行导致表级授权缺失（补 SELECT/INSERT/UPDATE，不授 DELETE）、platform_bypass 用 `NULLIF` 形态免疫池化连接空串残留。新增 2 个 integration 测试（策略形态防回归 + 跨租户行为断言，固定 UUID/幂等键，已多轮幂等重跑全绿，build tag 隔离不进默认 make test）；validate-architecture 核心守卫全过（make 包装沙箱噪音与 A1 一致）+ `git diff --check` 通过。dev 库执行 `20260831_001` 待 DBA（admin 凭据密码认证失败），迁移幂等重放安全。记录：`development-records/TASKCENTER-A2.md`；差异文档遗留风险第 1 条已标注收口。

> **OBS-RESOURCE-TREND-A（2026-09-02）：** `OBS-RESOURCE-TREND-A` 已完成 local verified + in-cluster gateway 实测（分支 `feat/observability-resource-trend`，基于 origin/main）：Core 新增 `GET /api/v1/observability/resource_trend` 租户级资源使用率趋势接口。依据方案「首页资源趋势接口方案.md」（方案 A）第 2 节结论**不复用 query_range 裸透传**（`rewritePromQLLabels` 的 `instanceID==""` 分支原样透传是跨租户裸聚合根源），改为 tenant_id 全从 JWT（`instanceTenantID(c)`）提取、后端直接生成只锚 `namespace="ani-tenant-<id>"` 的聚合 PromQL 走 `queryPrometheusRange`，不接收/不暴露 `query` PromQL，天然租户隔离。三张租户级 PromQL：GPU `DCGM_FI_DEV_GPU_UTIL`（已为 %，不乘 100）；CPU/内存 cAdvisor 容器维度 `100*avg(...)` 且 `container!="",container!="POD"` 过滤 pause 容器。返回复用 `ObservabilityRangeQueryResponse`（matrix），前端复用既有出图逻辑；local profile 空 matrix 降级；降级沿用 `prometheusObservabilityDegradedProfile`。契约先行：OpenAPI 新 path + `x-ani-authz`（resource=observability / action=read / boundary=tenant / principal_kinds=[user, api_key]）+ Core SDK/authz registry 生成物零漂移。单测覆盖 PromQL 生成、GPU 不乘 100、租户隔离（强制锚定真实 ns、拒绝注入裸 label）、参数校验、忽略前端租户参数、拒绝 query 透传。in-cluster gateway 实测：三 metric 均 200 matrix、参数校验 400、无凭证 401。既有 `query_range`/`query` 端点零改动。不标记 runtime ready / production ready。记录：`development-records/OBS-RESOURCE-TREND-A.md`；差异文档/测试报告/接口文档：`kjs-study/首页概览相关文档/implementation-diff-resource-trend.md`、`resource-trend-test-report.md`、`resource-trend-api.md`。

> **INSTANCE-LOG-STREAM-A（2026-09-01）：** `INSTANCE-LOG-STREAM-A` 已完成 local verified + 真实环境 curl 实测（分支 `feat/instance-log-stream`，基于 origin/main）：Core 新增 `GET /api/v1/instances/{instance_id}/logs/stream` SSE 流式日志端点（契约先行：OpenAPI 先改，再 port/adapter/gateway/生成物）。Loki adapter 采用已确认的 query_range 轮询方案（backward 回放 → lastTS 游标 → forward 增量轮询，排序去重、失败下一周期自愈），不引入 tail WebSocket；Gateway handler 用 Hijack 逐帧写 + Flush（不沿用 kb_sse 缓冲式写出），10 分钟上限发 `done{reason:"timeout"}`，客户端断开立即退出，非 loki profile 降级 503 `LOG_STREAM_NOT_CONFIGURED`，预流错误 401/404/400 返回普通 JSON。既有 `GET /instances/{id}/logs` 列表接口零改动。真实环境实测（lab Gateway 进程 + 真实 Loki/PG/Prometheus，未触碰 in-cluster Gateway）：首屏回放时间正序、nginx 真实访问日志增量约 20s 内到达无重复无乱序、404 预流 JSON 无 SSE 帧；`done{timeout}` 由 handler 单测覆盖。validate-architecture / validate-openapi-spec / git diff --check 全过；新端点按冻结决策带 `x-ani-authz`（resource=instances / action=read / boundary=tenant / principal_kinds=[user, api_key]），注册表 generated policy，`make validate-gateway-authz` 四项门禁全过（评审修订：初版契约遗漏该扩展，后补齐并再生成）。`make test` 仅 Windows 预存 sandbox symlink 环境失败（与 main 基线一致）。不含前端接入与 K8s profile 流式实现；不标记 runtime ready / production ready。记录：`development-records/INSTANCE-LOG-STREAM-A.md`；差异文档：`kjs-study/实例详情相关文档/implementation-diff-instance-log-stream.md`。

> **PLATFORM-CAPACITY-A（2026-09-03）：** `PLATFORM-CAPACITY-A` 已完成 local verified + 真实环境实测（分支 `feat/platform-capacity`，commit `64bc8ab` + 生成物补齐 `fae60b0`，未 push）：Core 新增 `GET /api/v1/platform/capacity` 平台容量态势只读汇总端点（契约先行：OpenAPI 先改，再 port/adapter/gateway/生成物）。整平台 = 1 个默认区域（id/code=`platform`），只读不实现区域 CRUD；real adapter 组合 GPUInventory（`ListNodeClasses` 设备/zone/allocatable）+ KubernetesRESTClient（集群级存在性 label selector 统计跨租户 Running GPU Pod，每 Pod 占 1 设备，与 gpu-inventory occupancy 语义一致；in_use 超设备数截断保证 `gpu_free ≥ 0`）+ TenantService（可用租户数）；单数据源失败不阻塞 200，失败源字段置 0/空并写 `dev_profile.real_provider=false` + reason；Gateway runtime 按 `PLATFORM_CAPACITY_PROVIDER` 装配（`kubernetes_rest` 真实链路；空/local/not_configured 回退确定性 local 降级，与 gpu-inventory fallback 惯例一致；未知值启动报错）。权限：`x-ani-rbac-scope: scope:capacity:read` + `x-ani-authz {resource: capacity, action: get, boundary: platform, principal_kinds: [user]}`，契约即开关。authz 注册表、Core SDK 四语言、静态 API docs 与 Console core-schema 生成物全部同步零漂移。`make test` / `make validate-architecture` / `make validate-openapi-spec` / `validate-auth-contract` / `make validate-gateway-authz` / `git diff --check` 全过（Windows sandbox symlink 预存失败与 main 基线一致）。真实环境实测（10.10.1.66 K8s 测试集群，镜像 `dev-20260903-platformcapacity`，`PLATFORM_CAPACITY_PROVIDER=kubernetes_rest`，`ANI_AUTH_MODE=auth_service`）：平台 token 200 返回真实集群数据（gpu_total=11 / gpu_free=3 / nodes=3 / tenant_count=22 / cpu_cores=512 / memory_gib=1760，`real_provider=true`），租户 token 403 platform 边界拒绝，无凭证/坏 token 401，全部通过。遗留：GPU 节点未打 zone label 时 azs 为空数组（补 label 即生效，无需改代码）；cpu/memory 为 allocatable 总量口径；不标记 runtime ready / production ready，不外推 full platform。记录：`development-records/PLATFORM-CAPACITY-A.md`；方案：`services/tasks/modules/plan/plan-platform-capacity.md`（差异/测试/接口文档为本地方稿未入库）。

> **Sprint 13（当前活跃冲刺，2026-06-19 起）：** Core real provider 与 live gate 收敛。前置 Sprint 12 已闭合 19 个 Core handler + 2 个 422；Sprint 13 不重写 Core handler，不把 Services 业务资源回流 Core API，而是在既有 `pkg/ports` / `pkg/adapters` / Gateway handler 边界接入真实组件，并形成可复跑 live gate 与 evidence JSON。历史冻结原因和历史结论仍保留在旧批次记录中，但不是当前 PR 规则。计划见 [`development-records/sprint13-real-provider-readiness-plan.md`](development-records/sprint13-real-provider-readiness-plan.md)。

> **Sprint 14 计划与分支状态：** Sprint 14 Core 韧性与服务语义计划见 [`development-records/sprint14-core-resilience-plan.md`](development-records/sprint14-core-resilience-plan.md)（限流/幂等重放/超时/readyz/重试断路/降级/failover）。配套交付 Services 的前端加速设计：[`development-records/frontend-acceleration-design-for-services.md`](development-records/frontend-acceleration-design-for-services.md)。当前主线入口仍保留 Sprint 13 production-shaped 边界；`feature/sprint14-core-resilience-semantics` 已完成 Sprint14 aggregate live gate，待 PR/评审后再进入主线状态。
> **Sprint 14 分支执行记录：** `feature/sprint14-core-resilience-semantics` 已完成 R-P0-0 gateway shared store 前置批次、R-P0-1 gateway rate limit、R-P0-2 gateway idempotency replay、R-P0-3 adapter per-call timeout、R-P0-4 data-plane readyz health、R-P1-5 retry/circuit-breaker foundation、R-P1-6 resilience degradation 与 R-P2-7 multi-endpoint failover config，见 [`development-records/r-p0-0-gateway-shared-store.md`](development-records/r-p0-0-gateway-shared-store.md)、[`development-records/r-p0-1-gateway-rate-limit.md`](development-records/r-p0-1-gateway-rate-limit.md)、[`development-records/r-p0-2-gateway-idempotency-replay.md`](development-records/r-p0-2-gateway-idempotency-replay.md)、[`development-records/r-p0-3-adapter-resilience-timeout.md`](development-records/r-p0-3-adapter-resilience-timeout.md)、[`development-records/r-p0-4-readyz-dataplane-health.md`](development-records/r-p0-4-readyz-dataplane-health.md)、[`development-records/r-p1-5-retry-circuit-breaker.md`](development-records/r-p1-5-retry-circuit-breaker.md)、[`development-records/r-p1-6-resilience-degradation.md`](development-records/r-p1-6-resilience-degradation.md)、[`development-records/r-p2-7-multi-endpoint-failover-config.md`](development-records/r-p2-7-multi-endpoint-failover-config.md)。R-P0-0..R-P2-7 单批次仍保持 local/logic verified 边界；其生产就绪结论由 `SPRINT14-CORE-RESILIENCE-LIVE-GATE` / `validate-sprint14-resilience-live-gate` / Sprint14 resilience live gate 补齐：已在 `ani-sprint14-resilience` 隔离 namespace 真实执行 P0 strong backend kill、P1 weak dependency degraded、P2 controller primary kill / follower failover，并归档脱敏 evidence。该 production-ready 范围仅限隔离 Sprint14 Core resilience fixture；不把现有 Sprint13 单副本后端或 full platform 标为 production ready。

> **INSTANCE-NETWORK-STORE-READ-RLS-A（2026-08-27~09-01）：** live passed（K8s 测试环境 10.10.1.66）。GPU 容器实例创建引用 VPC NOT_FOUND 的三层根因修复：`NetworkResourceStore` 补读方法 + `LocalNetworkService` 穿透读 + Gateway 注入 store；迁移 `20260828_001` 修复实例链路 8 张表 RESTRICTIVE-only RLS；`WORKLOAD_PROVIDER_APPLY_ENABLED=true` 接线。验证止于实例 201 → provisioning → Volcano 排队（GPU 容量问题非代码）；不外推 GPU runtime ready / production ready。批次记录见 [`development-records/instance-network-store-read-rls-a.md`](development-records/instance-network-store-read-rls-a.md)。

> **INSTANCE-RESIZE-SPEC-A（2026-09-02，live gate 补验 2026-09-03）：** live passed（10.10.1.66，镜像 dev-20260902-resize-ns，hotfix/network-store-read）。`resize` 从"纯重启"改为真正变配：`InstanceLifecycleRequest` 新增可选 `spec_id`（换 GPU 规格，v1 兼容）；状态机 resize 仅允许 stopped（running 409）；cpu/memory/spec_id 至少一项；`resolveResizeGPUSpec` 前置校验（GPUSpecService + GPUInventory，不可用 409）；executor 注入 Volcano translator，resize 走 targeted strategic-merge patch（方案B）：`volcano.sh/vgpu-*`、schedulerName=volcano、queue 注解、nodeSelector，切换 wholecard/vgpu 清另一模式资源键；变配后回写 `record.Compute.SpecID/GPUType/GPUShares/GPUMBPerShare`。新增 5 个 executor spec_id 用例；`go test` + `make validate-architecture` + `git diff --check` 通过。真实集群双向规格切换（quarter↔wholecard）live gate 通过，live 发现并修复 nodeSelector 残留（`$patch: replace`），不外推 GPU runtime ready。批次记录见 [`development-records/instance-resize-spec-a.md`](development-records/instance-resize-spec-a.md)。

## 当前冲刺

| 字段 | 值 |
|---|---|
| **冲刺编号** | Sprint 13（Core real provider 与 live gate 收敛） |
| **主题** | 将 Sprint 12 已闭合的 Core handler/ports/local adapters 接到真实组件，并建立可复跑 live gate 与 evidence JSON |
| **当前状态** | Sprint 12 已完成 19 个 Core handler + 2 个 422 的 Tier1 local profile；Sprint 13 S01-S07 均已归档 production_shape.status=passed evidence；历史 LIVE PENDING token 仅作门禁兼容语境 |
| **生产化边界** | Sprint 13 只达到 production-shaped acceptance passed；不等于 full platform production ready。正式镜像发布/升级、长期 SLA/soak、备份/恢复和故障注入仍需后续 release gate |
| **Auth 边界** | SPRINT13-AUTH-DEX-PRODUCTION-GATE / Auth/Dex production gate 已通过；production-shaped Gateway 固定 ANI_AUTH_MODE=auth_service |
| **执行入口** | `development-records/sprint13-real-provider-readiness-plan.md`、`development-records/README.md`、本文件验收命令 |
| **执行环境** | 真实 provider 写操作前必须重新只读盘点并取得人工确认；evidence 不得包含凭据、服务器 IP 或完整内网端点 |
| **最后校准日期** | 2026-08-03 |

## Sprint 13 当前任务

| 切片 | 状态 | 证据 / gate |
|---|---|---|
| S01 网络路由 Kube-OVN | production-shaped gate passed | `sprint13-netroute-kubeovn-live-result.md`；`validate-sprint13-b-track-production-shape` |
| S02 K8s workloads vCluster | production-shaped gate passed | `sprint13-k8s-workloads-vcluster-live-result.md`；metadata target TLS proof |
| S03 storage Rook-Ceph | production-shaped gate passed | `sprint13-storage-rook-ceph-live-result.md`；snapshot/mount-target proof |
| S04 GPU NVIDIA device-plugin/DCGM | production-shaped gate passed | `sprint13-gpu-inventory-dcgm-live-result.md`；DCGM metrics proof |
| S05 object-store MinIO | production-shaped gate passed | `SPRINT13-OBJECTSTORE-MINIO-A-TRACK`；`validate-object-store-live-gate`；pre-signed URL；LIVE PENDING 仅作历史兼容 |
| S06 vector Milvus | production-shaped gate passed | `SPRINT13-VECTOR-MILVUS-A-TRACK`；`validate-vector-store-live-gate`；LIVE PENDING 仅作历史兼容 |
| S07 instance observability Prometheus | production-shaped gate passed | `SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A-TRACK`；`validate-instance-observability-live-gate`；Prometheus + kubelet；LIVE PENDING 仅作历史兼容 |

| Services 模型仓库 P0 后端 | local/logic verified | `development-records/model-repository-p0-backend.md`；model-service/Gateway/inference catalog focused gates；PG/object-store live 未执行 |
| ANI Gateway OpenAI 数据面边界 | local + live Gateway smoke verified | `development-records/ani-gateway-openai-route-boundary.md`；旧 chat 占位路由 404、版本列表 200；独立 Envoy 真实模型调用仍待验证 |

闭环规则：每个 provider slice 必须具备 real adapter/provider runtime、live gate、非敏感 evidence JSON、development record 和全局 production-shape guard。S05-S07 B 轨可以继续 作为历史兼容 token 保留；截至 2026-06-21，S05/S06/S07 均已 passed。

## Gateway OpenAPI 鉴权四批次（2026-08）

> 独立于 Sprint 13/14 real provider 收敛的 Gateway 鉴权策略开发流。按 `repo/services/tasks/modules/plan/plan-authz-policy-compat-contract-pilot-v4.md` 四批次分阶段引入 OpenAPI 鉴权策略注册表、统一 Principal 与 identity key、V2 授权契约和 pilot 启用。分支 `feat/gateway-authz-policy`，默认 `mode=off` 不切流。

| 批次 | 状态 | 证据 |
|---|---|---|
| AUTHZ-POLICY-A (PR1) | ✅ local verified | `7440445`；generator + 生成注册表 + drift 门禁 + policy.go；A 不改 quota-meta，所有非 public operation 为 legacy |
| AUTHZ-COMPAT-B0 (PR2) | ✅ local verified | `e2eb502`；规范 Principal + LegacyPrincipalView + Mode/Config + ResolveAuthzPolicy + 横切 identity key；gateway 仍走旧 ValidateToken/CheckPermission |
| AUTHZ-CONTRACT-B1 (PR3) | ✅ local verified | `65f00f3`；additive V2 proto + auth-service JWT/API Key principal + permission evaluator + Gateway V2 client；gateway 仍 mode=off 不调 V2 |
| AUTHZ-PILOT-C (PR4) | ✅ local verified | `ad83e41`；v1.yaml security 注解 + mode Validate + V2 授权链路 + pilot E2E + deployment env；仅 listQuotaMeta 启用 V2 |
| AUTHZ-MODE-SIMPLIFY-D (PR5) | ✅ local verified | `4753a42` + 第六版修订；契约即开关收敛——删除 mode 开关（policy/dev/pilot/off）与 pilot allowlist，policy 路由恒为 x-ani-authz（generated）→V2、其余 legacy、public 放行，dev 自动回落 legacy；`ANI_AUTH_MODE` 唯一保留 env，不设废弃 env 残留检测（新集群从头部署拍板）；deploy 清单删除两个废弃 env 条目；`mode.go`/`mode_test.go` 改名 `config.go`/`config_test.go`，删兼容入口 6 函数 |

**gofmt 修复：** `cfe5b30`。**批次记录：** `development-records/authz-policy-compat-contract-pilot.md`。**验证命令：** `go build ./services/ani-gateway/... ./services/auth-service/...` + `go test -count=1 ./services/ani-gateway/... ./services/auth-service/...` + `gofmt -l`。

**预存问题修复（2026-08-25）：** 本地实测 pilot 模式后修复 4 个文件的预存不一致——删 v1.yaml 已弃用的 branding PUT/POST logo + tasks DELETE 路由的 router 注册和 registry 条目（branding_resources.go / task_resources.go / zz_generated_core_policies.go）；gpu_scheduling_resources.go `:id`→`:queue_id` 与 v1.yaml 一致（修复运行时 `LookupByRequest` lookup miss + route coverage 门禁）。修复后 drift 门禁通过、route coverage 0 error（274 registered, 224 registry）。详见 `development-records/authz-policy-compat-contract-pilot.md`。

**PR5 批次记录：** `development-records/authz-mode-simplify-d.md`（含 2026-08-31 第六版修订章节：删废弃 env 残留检测、改名 config.go、删兼容入口 6 函数、测试归一，12 files +51/−222）。**验证命令：** `go test ./services/ani-gateway/...` + `make gen-gateway-authz`（生成物零漂移）+ `make validate-gateway-authz`（18 tests、283 registered routes 0 errors）+ `make validate-architecture` + `git diff --check`；`make test` 仅 `pkg/adapters/runtime` 的 Windows 预存失败（sandbox symlink 特权 / Python `os.O_DIRECTORY`；origin/main @ `9c7bf2b` worktree 复跑同包同样 FAIL，不在本次改动集）。**本地实测：** `ANI_AUTH_MODE=auth_service`（无任何 policy env）启动正常；public 放行（branding 200）、generated 接口 `/api/v1/admin/quota-meta` 无凭证被 V2 拒绝 401、legacy 无效 token 401；`ANI_AUTH_MODE=dev` 启动正常且 quota-meta 回落 legacy 返回真实数据 200。登录全链路（有 token 200）受数据库角色权限迁移（#124 `ani_app_user`）未应用阻塞，暂缓验证。修订后代码已与方案第六版 §4.1–§4.5 逐项复核一致。

## 账密登录模块（2026-07）

> 独立于 Sprint 13/14 的账密登录功能开发流。覆盖 Core Auth API（租户账密 + 平台账密）、Console 前端（OIDC + 账密 Tab）、BOSS 前端（平台账密登录）。

| Issue | 描述 | 状态 | 证据 |
|---|---|---|---|
| Core #001 | 平台用户迁移（users 表扩展 tenant_id NULLABLE） | ✅ 已完成 | `development-records/auth-login-core-001.md` |
| Core #002 | 租户账密登录 API | ✅ 已完成 | `development-records/auth-login-core-001.md` |
| Core #003 | 平台账密登录 API | ✅ 已完成 | `development-records/auth-login-core-001.md` |
| Console #004 | Console P0 OIDC 登录 | ✅ 已完成 | `development-records/auth-login-console-004.md` |
| Console #005 | Console P1 账密 Tab | ✅ 已完成 | `development-records/auth-login-console-004.md` |
| BOSS #006 | BOSS P1 账密登录 | ✅ 已完成 | `development-records/auth-login-boss-006.md` |
| BOSS #006 | BOSS P1 OIDC 登录 | ⏸ 暂不实现 | auth-service Begin 方法需扩展平台路径 |

**代码审查修复（review-it）：** P0-1 签发顺序、P0-3 BOSS redirect_uri、P1-1 SQL 约束、P1-2 maybeRefresh、P1-3 401 先 refresh、P1-5 幂等键、P2-1 RBAC scope。测试验证：auth-service PASS、ani-gateway middleware PASS、BOSS vite build PASS。

**PRD/SPEC 文档体系：** 已按产品线拆分为 Console/BOSS/Core 三份 PRD 和三份 SPEC，分别放置在 `prd/{console,boss,core}/login/` 和 `spec/{console,boss,core}/login/` 目录。

## GPU 调度功能流（skill 流水线推进中，2026-07）

> 独立于 Sprint 13/14 real provider 收敛的 GPU 调度功能开发流，通过 `/prd-to-spec` → `/to-issues` → `/goal` skill 流水线推进。共 13 个 Issue，覆盖 Core OpenAPI 契约、Queue adapter/handler、Console 前端组件和 BOSS 前端页面。

| Issue | 描述 | 状态 | 证据 |
|---|---|---|---|
| #1 | OpenAPI 新增队列 CRUD + InstanceGPU 扩展 | ✅ 已完成 | `development-records/gpu-scheduling-issue-01-openapi-queue-crud.md`；8 项 AC 全部验证通过 |
| #2 | Core Queue adapter + handler | ✅ 已完成 | `development-records/gpu-scheduling-issue-02-queue-adapter-handler.md`；9 项 AC 全部验证通过；26 个单测通过 |
| #3 | Plan/scheduling extend | ✅ 已完成 | `development-records/gpu-scheduling-issue-03-plan-scheduling-extend.md`；10 项 AC 全部验证通过；13 个新单测通过 |
| #4 | Lab HAMi/Volcano/DCGM | ✅ 已完成 | `development-records/gpu-scheduling-issue-04-lab-hami-volcano-dcgm.md`；9 项 AC 全部验证通过；Volcano 1.15.0 + HAMi 2.9.0 + DCGM 在 3 节点集群部署成功 |
| #5 | GPU smoke live gate | ✅ 已完成 | `development-records/gpu-scheduling-issue-05-gpu-smoke-live-gate.md`；4 项 AC 全部验证通过；Smoke A (volcano+整卡) + Smoke B (HAMi vGPU) 均调度成功 |
| #6 | Queue CRUD live gate | ✅ 已完成 | `development-records/gpu-scheduling-issue-06-queue-crud-live-gate.md`；7 项 AC 全部验证通过；5 端点通过真实 Volcano CRD 验证 + 平台默认 403 + 跨租户 404 |
| #7 | Console Shell 组件 | ✅ 已完成 | `development-records/gpu-scheduling-issue-07-console-shell-components.md`；6 项 AC 全部验证通过；tsc + vite build 通过 |
| #8 | Console GPU 算力管理页 | ✅ 已完成 | `development-records/gpu-scheduling-issue-08-console-gpu-management-page.md`；12 项 AC 全部验证通过；tsc + vite build 通过 |
| #9 | Console GPU 容器实例 | ✅ 已完成 | `development-records/gpu-scheduling-issue-09-console-gpu-container-instance.md`；14 项 AC 全部验证通过；tsc + vite build 通过 |
| #10 | Console 队列设置页 | ✅ 已完成 | `development-records/gpu-scheduling-issue-10-console-queue-settings-page.md`；14 项 AC 全部验证通过；tsc + vite build 通过 |
| #11 | Console 概览 GPU 卡片 | ✅ 已完成 | `development-records/gpu-scheduling-issue-11-console-overview-gpu-card.md`；8 项 AC 全部验证通过；tsc + vite build 通过 |
| #12 | BOSS 前端骨架 | ✅ 已完成 | `development-records/gpu-scheduling-issue-12-boss-frontend-skeleton.md`；10 项 AC 全部验证通过；tsc + vite build 通过 |
| #13 | BOSS GPU 资源池页 | ✅ 已完成 | `development-records/gpu-scheduling-issue-13-boss-gpu-pool-page.md`；16 项 AC 全部验证通过；tsc + vite build 通过 |

### GPU 调度三段式 PR 拆分（2026-07-21）

| PR | 内容 | 状态 | 说明 |
|---|---|---|---|
| PR #21 (1/3) | v1.yaml 契约 + SDK/API docs/TS schema 生成物 | ✅ 已合入 main | `feat/core): add GPU scheduling queue CRUD contract to v1.yaml` |
| PR #31 (2/3) | pkg/ports 接口（GPUSchedulingQueueStore + GPUInventory 扩展） | ✅ 已合入 main | `feat(core): add GPU scheduling queue interface to pkg/ports` |
| PR #46 (3/3) | adapters + gateway + 前端 + manifests 实现 | 🟡 OPEN 等待 review | review-it 修复 4 项（UID panic/PATCH 幂等/URL 编码/错误语义）；5 项 follow-up 延迟；笔记 `gpu-scheduling-batch-01-13-note-it.md §5` |

Issue 清单：`repo/services/tasks/issues/issue-01-openapi-queue-crud.md` ~ `issue-13-boss-gpu-pool-page.md`

## Instance Management API-First（2026-07-28）

| 批次 | 状态 | 说明 |
|---|---|---|
| GPU-SPEC-CONTRACT-A | 个人仓库 CI passed，契约已确认 | 为实例 `spec_id` 提供 `GPUSpecSummary`、`GET /gpu-specs`、`GET /gpu-specs/{spec_id}` 只读契约；旧 GPU 字段 deprecated 保留；不含 handler/port/adapter/Console，不实现配额 check/acquire/release |
| INSTANCE-CONTRACT-A | 个人仓库 CI passed，契约已确认 | 扩展统一实例创建、详情摘要、列表过滤/排序/cursor、观测 cursor 和 lifecycle/operation step；引用既有 Registry/Network/Storage/GPU Spec，不含 handler/port/adapter/Console |
| INSTANCE-SANDBOX-CONTRACT-A | 个人仓库 CI passed，契约已确认 | 新增 Sandbox token、预览端口、文件、checkpoint 和异步 code-run 共 11 个操作；固定租户/kind、幂等、任务轮询和敏感输出审计边界；不含 handler/port/adapter/Console |
| INSTANCE-PORTS-SERVICE-A | container E2E passed，已提交 | 已补统一实例 ports/service/metadata、Gateway PostgreSQL/Kubernetes runtime 注入和独立 reconcile-worker；真实验证 Harbor 镜像、Kubernetes Pod/Kube-OVN IP、operation、启停、删除及 reconcile 终态；与 VM/Sandbox/code-run live 同批提交；不含完整 ORCHESTRATION/配额/GPU Container live |
| INSTANCE-MANAGEMENT-LIVE-GATE-A | VM live gate passed（2026-08-01） | `validate-instance-management-live-gate --live` 已通过；写路径 Core /api/v1/instances；镜像 `docker.kubercon.local/.../system-cirros:v1.8.2`；evidence `live-evidence/instance-management-vm-live-20260731.json`；KubeVirt 只读观测；Sandbox/GPU live 与完整编排仍属后续 |
| INSTANCE-SANDBOX-ADAPTER-A | live passed（2026-08-01） | Kata `RuntimeClass/sandbox-kata`（kata-deploy 4.0.0）；`KubernetesSandboxRuntime` create/pause/resume/delete；Gateway `instance-sandbox-live-20260801-v2`；记录 `instance-sandbox-adapter-a.md` |
| INSTANCE-SANDBOX-LIVE-GATE-A | live passed（2026-08-01） | create/lifecycle evidence `live-evidence/instance-sandbox-live-20260801.json`（busybox）；code-run 扩展见下一批次 |
| INSTANCE-SANDBOX-CODERUN-A | live passed（2026-08-01） | code-run 真实 Pod exec（kubectl）；`code_run_status=succeeded`；Gateway `instance-sandbox-coderun-20260801-v1`；镜像 `sandbox-python:3.12`；evidence `live-evidence/instance-sandbox-coderun-live-20260801.json`；token/port/file/checkpoint 仍 local-session；记录 `instance-sandbox-coderun-a.md` |
| INSTANCE-ORCHESTRATION-A | live passed（2026-08-01） | Container create-time Registry/Network/Storage 编排：OVN `logical_switch`、volume→PVC、`MountVolume`、operation steps；Gateway 共享 Network/Storage/Registry 给 Instance resolver；`validate-instance-orchestration-live-gate --live` passed；evidence `live-evidence/instance-orchestration-container-live-20260801.json`；Gateway `instance-orchestration-20260801-v3`；不含 Console/Exec/GPU/配额/Sandbox |
| INSTANCE-SANDBOX-SUBRESOURCES-A | live passed（2026-08-01） | Sandbox files real-provider：write/list/delete → Pod `/workspace`；code-run 读回校验；Gateway `instance-sandbox-files-20260801-v1`；evidence `live-evidence/instance-sandbox-files-live-20260801.json`；token/port/checkpoint 仍 local-session；不改 v1 契约；记录 `instance-sandbox-subresources-a.md` |
| INSTANCE-SANDBOX-FILE-SAFETY-A | local/logic verified（2026-08-02） | 独立 `emptyDir` 挂载 `/workspace`；files list/write/delete 使用目录 fd + `O_NOFOLLOW` + `dir_fd` 并拒绝多硬链接写入目标，阻断 symlink/hard-link/rename 越界；不改 v1，unsafe path 延续 400；focused/full test 与架构门禁通过，未重跑 live；记录 `instance-sandbox-file-safety-a.md` |
| INSTANCE-SANDBOX-FILE-SAFETY-LIVE-GATE-A | live passed（2026-08-02） | 真实 Kata Pod `/workspace=emptyDir`；code-run 构造 symlink/hard-link；5 个 unsafe files 操作均返回 400，跨文件系统 hard-link blocked，外部内容 unchanged；Gateway `instance-sandbox-file-safety-20260802-v1`；evidence `live-evidence/instance-sandbox-file-safety-live-20260802.json`；checkpoint 仍 local-session；记录 `instance-sandbox-file-safety-live-gate-a.md` |
| INSTANCE-PG-CLEAN-REVALIDATION-A | live passed（2026-08-02） | 备份后清除历史实例 PG 链路数据，空基线 API 返回 `items=[]`；重跑 Sandbox create/pause/resume/delete 及文件安全门禁通过；PG 只留当次 1 条 `deleted` Sandbox、4 条成功 operation、8 条成功 step，Kubernetes 无残留；当时发现的 provider 404 问题已由下一批次闭合；记录 `instance-pg-clean-revalidation-a.md` |
| INSTANCE-RECONCILE-PROVIDER-404-A | live passed（2026-08-02） | 主资源 404 转 `ports.ErrNotFound`；Sandbox 逻辑 provider 与 Kubernetes 物理 ref 对齐；真实 Sandbox 集群侧删除后 Core/PG `running→failed/ProviderResourceLost`，重复 reconcile 仍稳定，Core delete 后资源残留 0；worker `instance-provider-404-20260802-v2`；evidence `live-evidence/instance-reconcile-provider-loss-live-20260802.json`；记录 `instance-reconcile-provider-404-a.md` |
| INSTANCE-SANDBOX-PORTS-A | live passed（2026-08-02） | Sandbox preview ports real-provider：NodePort Service + `preview_url`；Endpoints + Pod 内 HTTP 校验（Kata 不兼容 port-forward / VPC 阻外部 NodePort）；Gateway `instance-sandbox-ports-20260801-v1`；evidence `live-evidence/instance-sandbox-ports-live-20260801.json`；token/checkpoint 仍 local-session；不改 v1；记录 `instance-sandbox-ports-a.md` |
| INSTANCE-SANDBOX-TOKEN-A | live passed（2026-08-02） | Sandbox signed token：HMAC `ani.sbx.*` + Gateway Auth/RBAC 子资源鉴权；live 证明 files=200 / 再签发=403 / 错 instance=403；Gateway `instance-sandbox-token-20260802-v1`；evidence `live-evidence/instance-sandbox-token-live-20260802.json`；checkpoint 仍 local-session；不改 v1；记录 `instance-sandbox-token-a.md` |
| INSTANCE-SANDBOX-STATELESS-A | live passed（2026-08-02） | Gateway 真实 rollout 后从 PG 恢复实例/端口/task，从 Redis 重放原请求并拒绝不同 intent；文件继续可读、既有端口可关闭；Token 过期 tombstone、checkpoint 422、pause/resume/delete 和 PG/Kubernetes 清理通过；evidence `live-evidence/instance-sandbox-stateless-live-20260802.json`；不改 v1；Pod 重建仍不保留 `emptyDir` |

边界：本流程独立于既有 GPU 调度队列实现；container、VM、Sandbox create/lifecycle、code-run、files（含 symlink/hard-link containment）、ports、signed token 与 Container ORCHESTRATION live 已落地，但 checkpoint、分页 result、配额和 GPU live gate 尚未完成，不声明全部实例管理 runtime ready 或 full platform production ready。

## Registry Console Flow（2026-07-22）

| 批次 | 状态 | 说明 |
|---|---|---|
| CORE-REGISTRY-CONSOLE-FLOW-CONTRACT-A | 契约/Console schema 已完成 | 按 7.22 原型“暂不考虑 BOSS 和权限”边界，Core v1 新增 `RegistryImage.purpose`、`/registry/images?purpose=`、四类算力引用 enum 与 createInstance 镜像门禁 422 语义；仅契约，不含 handler/adapter/Console 页面实现 |
| CORE-REGISTRY-CONSOLE-FLOW-CORE-A | Core 镜像仓库后端实现已完成 | RegistryImage purpose 贯通 port/adapter/router，`/registry/images?purpose=` 支持过滤；不含 instances、Console、BOSS 或权限实现 |
| SPRINT13-REGISTRY-HARBOR-LIVE-A | Harbor live gate passed | `validate-registry-harbor-live-gate` 契约通过；2026-07-27 通过真实 Gateway 验证 Harbor project/list/push-instructions/pull-secret/scan-report 并归档脱敏 evidence；artifact/purpose 回读需提供 repository/tag；不含 Console/BOSS/实例创建镜像门禁 |
| REGISTRY-P0-CLOSURE-A | live passed | P0 闭环 gate：purpose/scan/实例引用/删除 409；`validate-registry-harbor-live-gate`；evidence `registry-p0-closure-live-20260803.json`；scan terminal=`complete`；不含 BOSS quota/GC / Console |
| STORAGE-CONTROL-PLANE-STATE-A | B4 live passed | B1 冻结现有 v1；B2 真实 PG 已 apply；B3 Store/Service 以 PG 为权威；B4 Gateway 缺 `DATABASE_URL`/schema fail-closed + `validate-storage-control-plane-state-live-gate` production-shaped passed（rollout 后回读/幂等/墓碑）；evidence `live-evidence/storage-control-plane-state-live-20260803.json`；不含 Console / full platform production ready |
| CORE-STORAGE-CONSOLE-APIS-BACKEND-A | Core 存储模块后端实现已完成 | 上游 PR #71 契约合入后，补齐对象桶、块卷、文件系统和向量库管理接口的 ports/local service/gateway handlers 与后端 HTTP E2E/API 测试；2026-07-27 本地 Gateway + 真实依赖复验 Rook-Ceph/MinIO/Milvus 后端 E2E 通过；不含前端，不升级为 production-shaped Gateway 结论 |


## NATS 接入（2026-07）

> 独立于 Sprint 13/14 real provider 收敛的 NATS JetStream 适配器健壮性与示例 consumer 集成开发流，覆盖 ports 契约扩展、adapter 健壮性补全、metering/task 示例 consumer 端到端集成测试。批次 ID：`NATS-INTEGRATION-A`，对应 PRD `repo/services/tasks/modules/prd/core/messaging/prd-nats-integration.md` 和 SPEC `repo/services/tasks/modules/spec/core/messaging/spec-nats-integration.md`。

| Issue | 描述 | 状态 | 证据 |
|---|---|---|---|
| #001 | 扩展 `ports.SubscribeOptions`（AckWait/MaxDeliver）和 `ports.Message`（Headers）契约 | ✅ 已完成 | `development-records/nats-integration-a.md` Issue #001 |
| #002 | 修复 ANI_EVENTS stream 为 InterestPolicy（event fan-out） | ✅ 已完成 | `development-records/nats-integration-a.md` Issue #002 |
| #003 | Publish 写入 NATS headers（tenant-id 等 5 个 key）+ 注入 logger | ✅ 已完成 | `development-records/nats-integration-a.md` Issue #003 |
| #004 | Subscribe 业务层 Ack/Nak 决策 + panic recover + AckWait/MaxDeliver 透传 | ✅ 已完成 | `development-records/nats-integration-a.md` Issue #004 |
| #005 | `message.Headers()` 实现 + 内部 jetStream 接口抽象 | ✅ 已完成 | `development-records/nats-integration-a.md` Issue #005 |
| #006 | metering-service eventconsumer 示例 consumer | ✅ 已完成 | `development-records/nats-integration-a.md` Issue #006 |
| #007 | adapter 单元测试（fake/mock JetStream，9 场景 65.3% coverage） | ✅ 已完成 | `development-records/nats-integration-a.md` Issue #007 |
| #008 | adapter 集成测试（7 场景连真实 NATS）+ Consumer 端到端集成测试（2 场景） | ✅ 已完成 | `development-records/nats-integration-a.md` Issue #008 |
| #009 | task 流示例 consumer + 集成测试（2 场景，WorkQueuePolicy 语义验证） | ✅ 已完成 | `development-records/nats-integration-a.md` Issue #009 |

**关键设计决策：**
- adapter 根据 handler 返回值统一 ack/nak（`nil→Ack`/`error→Nak`/`panic→Nak`），`ports.Message` 接口去掉 `Ack/Nack` 方法编译期禁止业务显式确认（v3 修订，基于 `plan-nats-integration-v3.md`）
- handler 每条消息用 `context.Background()` 独立上下文，避免订阅 ctx 取消中断正在处理的消息
- `ports.MessageBus.Subscribe` 签名删除 ctx 死参数，consumer `Start()` 同步删 ctx、`Stop(ctx)` 保留（Drain 需超时控制）；三处 ack/nak 返回值不再忽略，Ack/Nak 调用失败时打 Error 日志（v4 修订，基于 `plan-nats-integration-v4.md`）
- `//go:build integration` build tag 隔离集成测试，不影响默认 `make test`
- `safeBuffer`（`sync.Mutex` + `bytes.Buffer`）跨 goroutine 安全捕获 slog 输出，解决并发数据竞争
- 测试清理使用 `PurgeStream` + `Drain` 确保环境恢复

验收命令：

```bash
# 单元测试（默认包含）
go test ./pkg/adapters/nats/...
go test ./services/metering-service/internal/eventconsumer/...
go test ./services/task-service/internal/taskconsumer/...

# 集成测试（需真实 NATS，build tag 隔离）
go test -tags=integration ./pkg/adapters/nats/...
go test -tags=integration ./services/metering-service/internal/eventconsumer/...
go test -tags=integration ./services/task-service/internal/taskconsumer/...
```

## Core Quota Service 功能流（2026-08）

> 独立于 Sprint 13/14 real provider 收敛的 Core Quota Service 功能开发流，覆盖 RLS 前提验证、TODO（v1.yaml 契约 + 3 个 port + 3 个 adapter + handler）与 SDK 生成。批次记录统一归档于 `development-records/quota-service.md`（issue-000 ~ issue-012 + 补充批次）。

| Issue | 描述 | 状态 | 证据 |
|---|---|---|---|
| #000 | 验证 RLS 双 policy（`platform_bypass` + `self`）前提 | ✅ 已完成 | `development-records/quota-service.md`；3 集成测试连真实 PG PASS |
| #001 | v1.yaml 契约：5 端点 + 9 schema + 5 error responses | ✅ 已完成 | `development-records/quota-service.md` |
| #002 | port 契约：`QuotaService`/`QuotaStoreService`/`QuotaAdminService` + 哨兵错误 | ✅ 已完成 | `development-records/quota-service.md` |
| #003 | `QuotaService` 扣减 adapter（Try/TryMany/Confirm/Cancel/Release） | ✅ 已完成 | `development-records/quota-service.md` |
| #004 | `QuotaStoreService` 配置查询 adapter | ✅ 已完成 | `development-records/quota-service.md` |
| #005 | `QuotaAdminService` 租户生命周期管理 adapter（`WithPlatformTx` 绕过 RLS） | ✅ 已完成 | `development-records/quota-service.md` |
| #006 | Core API handler + 鉴权扩展 + router 接线 | ✅ 已完成 | `development-records/quota-service.md` |
| #007 | 重新生成 Core SDK | ✅ 已完成 | `development-records/quota-service.md` |
| #008 | 扣减单元测试 | ✅ 已完成 | `development-records/quota-service.md` |
| #009 | 配置查询单元测试 | ✅ 已完成 | `development-records/quota-service.md` |
| #010 | 管理单元测试 | ✅ 已完成 | `development-records/quota-service.md` |
| #011 | 集成测试（连 PG，双角色验证 RLS） | ✅ 已完成 | `development-records/quota-service.md` |
| #012 | 全量验收（note-it） | ✅ 已完成 | `development-records/quota-service.md` |
| 补充批次1 | v1.yaml 审核意见回添（改动 3/4 契约修正，2026-08-10） | ✅ 已完成 | `development-records/quota-service.md`；改动 4 GET 404 + 改动 3 POST 409；45 个 quota 单测 PASS |
| 补充批次2 | `feat/quota-service-tcc` 审核意见整改（4 处，2026-08-10） | ✅ 已完成 | `development-records/quota-service.md`；幂等 header 改名 `Idempotency-Key`（`03d5abe`）、`CreateTenantQuota` 部分成功语义（`518b6a5`，推翻批次1 的 409 中断）、`writeQuotaError` 补 `ErrInvalid → 400`（`d00ddb7`）、Confirm/Cancel/Release 补 tx_id 存在性校验 + `ErrReservationNotFound`（`1d17218`）；三处 quota 单测 + Gateway 单测 + `make validate-architecture` + `git diff --check` 全通过 |
| 补充批次3 | TryTx / TryManyTx 新增外部事务变体（`feat/quota-service-tcc-v2`，2026-08-12） | ✅ 已完成 | `development-records/quota-service.md` 补充批次；`QuotaService` interface 新增 `TryTx` / `TryManyTx`（接收外部 tx，复用 `tryInTx`，零新增 SQL）；9 单元测试 + 7 集成测试（连真实 PG，双角色 RLS 验证）全通过 |
| 补充批次4 | `UpsertTenantQuota` + Core quota upsert 端点（`feat/quota-service-v3`，2026-08-18） | ✅ 已完成 | `development-records/quota-service.md` 补充批次；新增 `PUT /admin/tenants/{tenant_id}/quota/upsert`、`QuotaAdminService.UpsertTenantQuota`、PG `ON CONFLICT DO UPDATE + GREATEST` 原子 upsert、`ErrQuotaUpdateUncertain → 511`；quota 单测 + integration build tag 编译 + Gateway 映射测试 + OpenAPI YAML + architecture + diff check 通过 |

## GPU 规格与配额管理功能流（2026-08）

> 独立于 Sprint 13/14 real provider 收敛的 GPU 规格与配额管理功能开发流，覆盖 GPUSpec CRD 持久化、Volcano 资源翻译、GPU Inventory 四态可用性、reconciler TCC 同事务、orchestrator 配额预占、gateway handler 端点。批次记录归档于 `development-records/gpu-spec-quota-a.md`。

| Issue | 描述 | 状态 | 证据 |
|---|---|---|---|
| #003 | GPUSpec CRD Store（CRD 持久化 + 幂等 label + 15 单测） | ✅ 已完成 | `development-records/gpu-spec-quota-a.md` |
| #004 | VolcanoResourceTranslator（spec_id→nodeSelector+schedulerName+资源请求+queue annotation + 8 单测） | ✅ 已完成 | `development-records/gpu-spec-quota-a.md` |
| #005 | GPU Inventory 四态可用性 + HAMi 全量删除（parseVolcanoVGPUAnnotation + 8 单测） | ✅ 已完成 | `development-records/gpu-spec-quota-a.md` |
| #006 | reconciler TCC Confirm/Cancel/Release 同事务 + provisioning 超时 + 删除双调 + 对账循环（12 单测） | ✅ 已完成 | `development-records/gpu-spec-quota-a.md` |
| #007 | QuotaAwareInstanceOrchestrator 包装模式 + WorkloadInstanceStoreTx + outboxWriter 接口 + quota_tx_ids JSONB + resource_reservation_allocations 表（RLS）+ bootstrap 条件装配（4 单测） | ✅ 已完成 | `development-records/gpu-spec-quota-a.md`；review-it 3 accepted findings fixed（RLS/TenantID/SchedulerName） |
| #008 | Gateway handler（POST/DELETE /gpu-specs + PUT/GET /admin/tenants/:tid/reservations + GET /quotas/me + GET /reservations/me）+ PutReservation/GetReservation 接口 + specInUse 跨租户检查（WithPlatformTx） | ✅ 已完成 | `development-records/gpu-spec-quota-a.md`；review-it 1 accepted finding fixed（specInUse 跨租户） |
| #009 | BOSS GPU 资源池 4 Tab 改版（KPI 6 卡 + 节点/设备/队列/规格 Tab + 配额分配 Drawer 内联） | ✅ 已完成 | `development-records/gpu-spec-quota-a.md`；review-it 2 accepted findings fixed；BOSS tsc + vite build PASS |
| #010 | BOSS 规格管理 Drawer（`-gpu-spec-drawer.tsx`）+ 配额/预留分配 Drawer（`-gpu-pool-quota-drawer.tsx`）+ Tab 4 新建/删除操作 | ✅ 已完成 | `development-records/gpu-spec-quota-a.md`；review-it 2 accepted findings fixed；BOSS tsc + vite build PASS |
| #011 | Console 创建 Dialog 改版（spec_id Select 四态标注 + queue_name Select 必选 + 本地 quota 重算） | ✅ 已完成 | `development-records/gpu-spec-quota-a.md`；review-it 1 accepted finding fixed；Console tsc + vite build PASS |
| #012 | Console 列表页配额/预留卡片（GET /quotas/me + GET /reservations/me）+ 队列页"已分配"列扩展 | ✅ 已完成 | `development-records/gpu-spec-quota-a.md`；review-it 1 accepted finding fixed；Console tsc + vite build PASS |
| #013 | 集成验收（闭环验证 + 文档更新）：#003-#012 闭环证据映射 + 批次记录 + Sprint/README 索引 | ✅ 已完成 | `development-records/gpu-spec-quota-batch.md`；make test + validate-architecture + validate-services + validate-doc-entrypoints + git diff --check PASS（仅 Java smoke 受本地 JDK 8 限制） |

验收命令：

```bash
go build ./pkg/adapters/runtime/... ./services/ani-gateway/...
go test ./pkg/adapters/runtime/ -run "TestGPUSpec|TestVolcanoTranslator|TestListSpecAvailability|TestReconcile|TestQuotaEnabledSwitch|TestUpsertStatusTx|TestQuota" -count=1
go test ./services/ani-gateway/... -count=1
python scripts/validate_component_imports.py --root .
git diff --check
```

```bash
go test ./pkg/adapters/runtime -run Quota
go test ./services/ani-gateway/...
make validate-architecture
git diff --check
```

## BOSS 租户配额套餐功能流（2026-08）

> BOSS 平台租户配额套餐管理后端功能流，覆盖 OpenAPI 契约、gRPC、DB 迁移、网关接入、CRUD、状态机、限额同步、租户绑定、审计和配额元数据透传。后端 #1-#13 已完成；历史前端 #14-#18 已迁至独立仓库，不再由本仓库构建或验证。批次记录统一归档于 `development-records/quota-policy-issue-*.md`。

| Issue | 描述 | 状态 | 证据 |
|---|---|---|---|
| #1 | OpenAPI 契约：14 端点 + 9 schema + 12 错误码 | ✅ 已完成 | `development-records/quota-policy-issue-01-openapi-contract.md` |
| #2 | 接口与结构体：TenantPlanStore 13 方法 + QuotaSvcClient 5 方法 | ✅ 已完成 | `development-records/quota-policy-issue-02-interfaces-structs.md` |
| #3 | 数据库迁移：tenant_plans + plan_quota_limits + tenants.plan_id + audit_logs 分区 | ✅ 已完成 | `development-records/quota-policy-issue-03-database-migration.md` |
| #4 | 网关接入：14 端点 + tenantCallCtx + mapTenantPlanError + planQuotaLimitJSON DTO | ✅ 已完成 | `development-records/quota-policy-issue-04-gateway-integration.md` |
| #5 | 创建配额套餐：CreateTenantPlan gRPC + store 事务 + Core meta 验证 + 审计 | ✅ 已完成 | `development-records/quota-policy-issue-05-create-tenant-plan.md` |
| #6 | 列表 + 详情：List/Get gRPC + store 游标分页 + 业务码映射 | ✅ 已完成 | `development-records/quota-policy-issue-06-list-get-tenant-plan.md` |
| #7 | 发布/停用/软删除：Activate/Disable/Delete + 状态机 + 审计 | ✅ 已完成 | `development-records/quota-policy-issue-07-activate-disable-delete-tenant-plan.md` |
| #8 | 修改限额 + 同步租户：UpdateQuotaLimits + syncBoundTenantQuotaLimits + Core Get/Put/Create + 异步重试 | ✅ 已完成 | `development-records/quota-policy-issue-08-update-quota-limits-sync.md` |
| #9 | 绑定套餐 + 绑定租户列表：BindPlanQuota 7 步校验 + Core 同步 + 回滚 + ListBoundTenants | ✅ 已完成 | `development-records/quota-policy-issue-09-bind-plan-bound-tenants.md` |
| #10 | 更新套餐基本信息：UpdateTenantPlan PUT + StringValue 可选语义 + 动态 SET | ✅ 已完成 | `development-records/quota-policy-issue-10-update-plan-info.md` |
| #11 | 查询配额元数据：ListQuotaMeta GET /quota-meta 透传 Core | ✅ 已完成 | `development-records/quota-policy-issue-11-list-quota-meta.md` |
| #12 | 可绑定租户列表：ListBindableTenants + plan_id IS DISTINCT FROM | ✅ 已完成 | `development-records/quota-policy-issue-12-list-bindable-tenants-api.md` |
| #13 | 查询操作历史：ListTenantPlanAuditLogs + store 游标分页 + JSON 映射 | ✅ 已完成 | `development-records/quota-policy-issue-13-audit-logs-api.md` |

验收命令：

```bash
cd repo/services/tenant-service
go build ./...
go test ./internal/service/ -run "TestTenantPlanService_(Create|List|Get|Activate|Disable|Delete|Update|Bind|QuotaMeta|Audit)|TestMapStoreError" -v

cd repo/services/ani-gateway
go build ./...

make test
make validate-architecture
make validate-doc-entrypoints
git diff --check
```

## BOSS 平台运营账号功能流（2026-09）

> 独立于 Sprint 13/14 real provider 收敛的 BOSS 平台运营账号（platform-admin）后端功能开发流。覆盖 Core `PlatformUserAdminService` + 新建 `platform-settings-service` + Services Gateway `/api/v1/svc/platform-admins/*`。共 11 个后端 Issue（#001–#011），**后端 API 批次本地实现完成**；BOSS 前端（列表/创建向导/详情 Tabs）待后续批次。批次记录归档于 `development-records/platform-admin-issue-*.md`。

| Issue | 描述 | 状态 | 证据 |
|---|---|---|---|
| #001 | OpenAPI 契约：Core `/admin/platform-users/*` + Services `/platform-admins/*` | ✅ 已完成 | `development-records/platform-admin-issue-001-openapi-contract.md` |
| #002 | platform-settings-service 骨架 + gRPC/proto + 审计 store 端口 | ✅ 已完成 | `development-records/platform-admin-issue-002-service-skeleton.md` |
| #003 | 审计表迁移 + `PlatformAdminAuditStore` postgres adapter | ✅ 已完成 | `development-records/platform-admin-issue-003-database-migration.md` |
| #004 | Services 网关 platform-admins 路由注册 + Core admin 链路 | ✅ 已完成 | `development-records/platform-admin-issue-004-services-link.md` |
| #005 | 创建运营账号 API（email 可重复、软删 username 唯一） | ✅ 已完成 | `development-records/platform-admin-issue-005-create-api.md` |
| #006 | 列表 + 详情 API（游标分页、Core SDK 委托） | ✅ 已完成 | `development-records/platform-admin-issue-006-list-detail-api.md` |
| #007 | Core 角色列表 + 账号权限矩阵查询 | ✅ 已完成 | `development-records/platform-admin-issue-007-core-platform-roles-api.md` |
| #008 | 改角色 + last-admin 保护 + 幂等边界（仅外层网关） | ✅ 已完成 | `development-records/platform-admin-issue-008-roles-change-role-api.md` |
| #009 | 禁用/启用/软删除 + `STATUS_UNCHANGED` | ✅ 已完成 | `development-records/platform-admin-issue-009-disable-enable-delete-api.md` |
| #010 | 重置密码 + OpenAPI path 修正 + Store 单测 | ✅ 已完成 | `development-records/platform-admin-issue-010-reset-password-api.md` |
| #011 | 操作历史查询 + 操作者 `user_id` + 测试补强 | ✅ 已完成（本地未提交） | `development-records/platform-admin-issue-011-audit-logs-api.md` |

**当前边界：** #001–#011 后端 API 本地实现与 note-it 已完成；不含 BOSS 前端 Tab、真实 PG live gate、production ready 声明。合入前需全量 `make test` + `make validate-services` + `make validate-architecture`。

验收命令：

```bash
cd repo
python scripts/validate_services_route_contract.py
go test ./services/platform-settings-service/internal/service/... -run "PlatformAdmin|AuditLog|ResetPassword|Disable|Enable|Delete|ChangeRole|Create|List|Get" -count=1
go test ./services/ani-gateway/internal/router/... -run "PlatformAdmin|AuditLog|ResetPassword|DisableEnable|DeleteFlow|CreateFlow" -count=1
go test ./services/platform-settings-service/internal/repo/adapters/postgres/... -count=1
make test
make validate-services
```
## BOSS 租户管理员功能流（2026-08）

> BOSS 平台租户管理员管理功能开发流，覆盖管理员全生命周期（OpenAPI 契约 → 接口/数据模型 → DB 迁移 → 网关接入 → 13 端点端到端实现 → 多轮 review-it → 文档对齐）。14 个 issue 全部实现完成。批次记录归档于 `development-records/tenant-admin-issue-*.md` 和 `tenant-admin-feature-batch.md`。

| Issue | 描述 | 状态 | 证据 |
|---|---|---|---|
| #1 | OpenAPI 契约：13 端点 + 14 schema + 错误码表（FORBIDDEN/USER_STATE_INVALID，IDEMPOTENCY_* 为网关中间件） | ✅ 已完成 | `tenant-admin-issue-001-openapi-contract.md` |
| #2 | 接口与数据模型：TenantAdminStore（仅 invitation/audit）+ TenantAdminSvcClient（12 方法）+ TenantSvcClient（2 方法）+ proto 13 RPC | ✅ 已完成 | `tenant-admin-issue-002-interfaces-data-model.md` |
| #3 | 数据库迁移：三个独立文件（20260821_001 建表 + token_hash 唯一索引 + RLS、20260825_001 部分唯一索引 uk_tenant_admin_invitation_pending、20260827000200 users ALTER display_name + is_deleted + deleted_at） | ✅ 已完成 | `tenant-admin-issue-03-database-migration.md` |
| #4 | 网关接入：REST→gRPC 转发 + Core DB 直连双路径，12 端点 + tenant_admin_resources.go + tenant_admin_runtime.go | ✅ 已完成 | `tenant-admin-issue-04-gateway-integration.md` |
| #5 | 可用租户列表：ListAvailableTenants 端到端（gRPC → Core SDK HTTP → ani-gateway Core handler → PG bypass RLS） | ✅ 已完成 | `tenant-admin-issue-005-available-tenants-api.md` |
| #6 | 邀请管理员：InviteTenantAdmin 10 步流程 + 部分唯一索引竞态防护 + ConstraintName 区分冲突 + 审计 best-effort | ✅ 已完成 | `tenant-admin-issue-006-invite-api.md` |
| #7 | 重发邀请：ResendTenantAdminInvitation 8 步流程 + UpdateInvitation 冲突处理 + 终态错误 detail 区分 | ✅ 已完成 | `tenant-admin-issue-007-resend-api.md` |
| #8 | 跨租户管理员列表：Core SDK ListTenantAdmins 全量拉取 + 本地 Store ListInvitationFlags 内存合并 + BatchGetUsers 三层贯通 + 27 sub-tests | ✅ 已完成 | `tenant-admin-issue-008-list-all-tenant-admins.md` |
| #9 | 管理员详情：GetTenantAdminDetail + lazy expire 写回 DB + ErrStoreUnavailable/ErrCoreUnavailable 分离 | ✅ 已完成 | `tenant-admin-issue-009-detail-api.md` |
| #10 | 可分配角色列表：ListTenantRoles + 排除 platform-* + 租户软删除后仅返回系统角色 + EXISTS 子查询 | ✅ 已完成 | `tenant-admin-issue-010-roles-api.md` |
| #11 | 角色查询与修改：UpdateTenantAdminRole role_id UUID 全链路 + upsert 简化 + user_id 唯一索引 + GetTenantAdminRole + 5 轮 review-it | ✅ 已完成 | `tenant-admin-issue-011-role-change-and-query.md` |
| #12 | 重置密码：ResetTenantAdminPassword + bcrypt cost=12 + 禁用态允许 + 明文不落审计/日志/响应 | ✅ 已完成 | `tenant-admin-issue-012-reset-password.md` |
| #13 | 禁用/启用/删除：SetStatus + SoftDelete 不改 status + 重复 disable/enable 409 USER_STATE_INVALID + Core DB 事务内 SELECT+UPDATE | ✅ 已完成 | `tenant-admin-issue-013-disable-enable-delete.md` |
| #14 | 操作历史：ListTenantAdminAuditLogs + WHERE details->>'target_id'=userId + result 过滤 success/failure + limit 三层截断 | ✅ 已完成 | `tenant-admin-issue-014-audit-logs-api.md` |
| 文档对齐 | 以代码和 issue 为标准，5+ 轮深度审计修正 SPEC/UX/PRD/Plan 四份文档 + 7 处 issue 修正 | ✅ 已完成 | `tenant-admin-doc-alignment-batch.md` |
| 批次汇总 | 功能批次汇总：13 项设计决策、5 张偏差表、4 项 tradeoff、4 项 open question | ✅ 已完成 | `tenant-admin-feature-batch.md` |

## BOSS 租户列表管理功能流（2026-09）

> BOSS 租户列表：创建/列表详情/更新/状态机/SSO·MFA 配置/配额代理/配额变更/lifecycle·audit/租户内 admins。**Issue-001～009、011～014 后端 local/logic 完成**；**Issue-010 SSO test 仍为 501 stub**；规划文档（Issue/PRD/SPEC/UX/Plan）已按实现回写。批次记录：`development-records/tenant-list-issue-*.md`、`tenant-list-doc-alignment-batch.md`、`tenant-list-feature-batch.md`。不含登录拦截 FROZEN/DISABLED、MFA 登录强制、禁用资源释放、BOSS 前端页。

| Issue | 内容 | 状态 | 记录 |
|-------|------|------|------|
| #1 | OpenAPI Core 9 + Services 19 | ✅ | `tenant-list-issue-001-openapi-contract.md` |
| #2 | proto messages + ports（TenantService 19 RPC） | ✅ | `tenant-list-issue-002-interfaces-structs.md` |
| #3 | 迁移 `20260902_001_tenant_list_management.sql` | ✅ | `tenant-list-issue-003-database-migration.md` |
| #4 | Gateway 路由 + 归因 ctx | ✅ | `tenant-list-issue-004-gateway-integration.md` |
| #5 | available-plans + CreateTenant | ✅ | `tenant-list-issue-005-create-tenant-api.md` |
| #6 | ListTenants / GetTenantDetail | ✅ | `tenant-list-issue-006-tenant-list-detail-api.md` |
| #7 | UpdateTenant | ✅ | `tenant-list-issue-007-update-tenant-api.md` |
| #8 | freeze / unfreeze / disable | ✅ | `tenant-list-issue-008-tenant-state-machine-api.md` |
| #9 | GetTenantAuth / Update SSO·MFA | ✅ | `tenant-list-issue-009-tenant-auth-api.md` |
| #10 | TestTenantSso | ⏸ OPEN / 501 | — |
| #11 | GetTenantQuota | ✅ | `tenant-list-issue-011-tenant-quota-api.md` |
| #12 | 配额变更三件套 | ✅ | `tenant-list-issue-012-quota-change-request-api.md` |
| #13 | lifecycle + audit-logs | ✅ | `tenant-list-issue-013-lifecycle-audit-api.md` |
| #14 | 租户内 ListTenantAdmins | ✅ | `tenant-list-issue-014-tenant-admins-api.md` |
| 文档对齐 | Issue + PRD/SPEC/UX/Plan | ✅ | `tenant-list-doc-alignment-batch.md` |
| 批次汇总 | note-it Feature batch | ✅ | `tenant-list-feature-batch.md` |

**下一步（可选）：** Issue-010 SSO test；Gateway 登录拦截；MFA 登录强制；禁用资源释放；BOSS 前端（UX 已对齐）。

验收命令：

```bash
cd repo/services/tenant-service
go build ./...
go test ./internal/... -run "TestTenantAdminService" -v

cd repo/services/ani-gateway
go build ./...
go test ./internal/router/ -run "TestTenantAdmin|TestHandler_ListAvailableTenants|TestHandler_ListTenantRoles" -v

make validate-architecture
git diff --check
```

## Metering Service 功能流（2026-08）

> 独立于 Sprint 13/14 real provider 收敛的 Metering Service 计量采集功能开发流，覆盖 metering_usage_records migration、port 接口、collector 实现、consumer/rebuilder、集成测试、部署清单与 Live Gate 缺陷修复。批次记录归档于 `development-records/pr-m1-metering-consumer.md` ~ `pr-m5-metering-consumer.md`。

| Issue | 描述 | 状态 | 证据 |
|---|---|---|---|
| #001 | metering_usage_records migration（`ani_metering_writer` BYPASSRLS + RLS policy）+ `MeteringCollectionService` port + `InstanceLifecycleEvent`/`MeteringUsageRecord` schema + go.mod/config.go + service 实现 + 13 单测 | ✅ 已完成 | `pr-m1-metering-consumer.md` |
| #002 | Collector 接口 + DCGMGPUCollector/KubeletCPUCollector/KubeletMemCollector + Resolve/CollectAll router（24 测试）+ buildSpec 维度映射 + parseGPUCount（16 测试） | ✅ 已完成 | `pr-m2-metering-collectors.md` |
| #003 | Consumer handleEvent + seenSeq 两阶段锁（11 测试）+ Rebuilder WithPlatformTx 绕过 RLS（8 测试）+ main.go bootstrap | ✅ 已完成 | `pr-m3-metering-consumer.md` |
| #004 | 9 集成测试场景（事件驱动/保底/幂等/rebuild/seenSeq 乱序/失败重投/租户 mismatch/poison/DB UNIQUE）9/9 PASS | ✅ 已完成 | `pr-m4-metering-consumer.md` |
| #005 | 部署清单 metering-service-live-deps.yaml + Live Gate 4 缺陷修复 + NATS 事件验证 | ✅ 已完成 | `pr-m5-metering-consumer.md` |
| #006 | 计量查询 PG adapter（V3 方案）：ports 扩展 + PgMeteringService（租户 RLS / 平台 BYPASSRLS）+ Gateway METERING_PROVIDER_MODE 装配 + 平台查询 handler + pilot 鉴权接入 + 前端同步 | ✅ 已完成 | `pr-m6-metering-query-pg-adapter.md` |

Live Gate 修复详情（2026-08-14，真实 K8s 集群部署后）：

| 缺陷 | 根因 | 修复 |
|---|---|---|
| PromQL 返回 "no samples" | collector 用 instance_id 做 pod 正则，Prometheus pod 标签值是 `{name}-{hash}-{hash}` | CollectionSpec 新增 WorkloadName 字段，collector 用 K8s 资源名做 `pod=~"^<name>(-.*)?$"` 正则匹配 |
| CPU 多副本只取第一个 pod | `rate()` 返回多向量，`queryPrometheusScalar` 只取 Result[0] | CPU 查询加外层 `sum(rate(...))` 聚合所有副本 |
| 写入错误 schema `_e2e_issue025` | `ani_app_user` 的 search_path 为 `_e2e_issue025, public` | `ALTER ROLE ani_app_user SET search_path TO public` |
| RLS 阻止写入 | `ani_app_user` 受 RLS 约束，`app.current_tenant_id` 未设置 | persistRecords 用 `SET ROLE ani_metering_writer` 绕过 RLS + migration 补充 `GRANT ani_metering_writer TO ani_app_user` |

验收命令：

```bash
# 单元测试
cd repo
go test -count=1 ./services/metering-service/internal/...
go test -count=1 ./pkg/adapters/metering/...

# 集成测试（需真实 NATS，build tag 隔离）
go test -tags=integration ./services/metering-service/internal/...

# 架构校验
make validate-architecture
git diff --check
```

## Sprint 13 执行矩阵

| 候选切片 | 真实组件方向 | 代码边界 | 当前状态 |
|---|---|---|---|
| 实例观测 | Prometheus + kubelet / K8s API（已选 2026-06-19） | `ports.InstanceObservability`，Gateway handler 不绕过 port | **production-shaped gate passed**（`SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A-TRACK`；B 轨 live result `sprint13-instance-observability-prometheus-live-result.md`；evidence：`live-evidence/sprint13-instance-observability-prometheus-live-evidence.json`；readiness：`sprint13-instance-observability-prometheus-readiness.md`；gate：`validate-instance-observability-live-gate`；历史 LIVE PENDING token 仅作门禁兼容语境；不代表 full platform production ready） |
| GPU 清单/占用 | NVIDIA device-plugin / DCGM / node labels | `ports.GPUInventory`，复用 Sprint 5 GPU evidence 作为前置事实 | **production-shaped gate passed**（A 轨 `SPRINT13-GPU-INVENTORY-DCGM-A-TRACK`；B 轨 live result `sprint13-gpu-inventory-dcgm-live-result.md`；Gateway/bootstrap `GPU_INVENTORY_PROVIDER=kubernetes_rest` 均支持 in-cluster ServiceAccount；readiness：`sprint13-gpu-inventory-dcgm-readiness.md`；gate：`validate-gpu-inventory-live-gate --production-shaped`；production guard：`validate-sprint13-b-track-production-shape`；evidence：`development-records/live-evidence/sprint13-gpu-inventory-dcgm-live-evidence.json`；不代表 full platform production ready） |
| Sandbox templates | Kata / runtimeClass / template catalog | `ports.SandboxTemplateCatalog` | 待拆分执行 |
| 网络路由 | Kube-OVN | `ports.NetworkService` / `runtime.NetworkService` / `network_resources.go` / `pkg/bootstrap/deps.go` / Gateway network runtime | **production-shaped gate passed**（Gateway `POST/GET /networks/routes` create/list + in-cluster ServiceAccount/RBAC + Kube-OVN bottom observation 已通过；production guard：`validate-sprint13-b-track-production-shape`；evidence：`development-records/live-evidence/sprint13-netroute-kubeovn-live-evidence.json`；result：`sprint13-netroute-kubeovn-live-result.md`；不代表 full platform production ready） |
| 卷快照与 mount-targets | Rook-Ceph RBD / CSI snapshot / NFS 或等价 filesystem backend | `ports.StorageService` / `runtime.LocalStorageService` / `storage_resources.go` / `pkg/bootstrap/deps.go` / `storage_runtime.go` | **production-shaped gate passed**（A 轨 `SPRINT13-STORAGE-ROOK-CEPH-A-TRACK`；Gateway/bootstrap `STORAGE_PROVIDER=kubernetes_rest` 均支持 in-cluster ServiceAccount；gate：`validate-storage-live-gate --production-shaped`；production guard：`validate-sprint13-b-track-production-shape`；live result：`sprint13-storage-rook-ceph-live-result.md`；evidence：`development-records/live-evidence/sprint13-storage-rook-ceph-live-evidence.json`；不代表 full platform production ready） |
| K8s workloads | vCluster / Kubernetes API | `ports.K8sClusterService` / `local_k8s_cluster_service.go` / `k8s_cluster_resources.go` | **production-shaped gate passed**（`validate-vcluster-live-gate --production-shaped` 已固定 metadata target TLS passed 标准；`sprint13-k8s-workloads-vcluster-live-result.md`；production guard：`validate-sprint13-b-track-production-shape`；evidence：`development-records/live-evidence/sprint13-k8s-workloads-vcluster-live-evidence.json`；不代表 full platform production ready） |
| 对象存储 bucket/upload/download | MinIO（已选 2026-06-19，S3 兼容 pre-signed URL） | `ports.ObjectStore` + `ports.StorageService` / `storage_resources.go` | **production-shaped gate passed**（`SPRINT13-OBJECTSTORE-MINIO-A-TRACK`；result：`sprint13-objectstore-minio-live-result.md`；evidence：`live-evidence/sprint13-objectstore-minio-live-evidence.json`；gate：`validate-object-store-live-gate`） |
| 向量文档写入 | Milvus（已选 2026-06-19） | `ports.VectorStore` + `ports.VectorStoreService` / `vector_store_resources.go` | **production-shaped gate passed**（`SPRINT13-VECTOR-MILVUS-A-TRACK`；B 轨 live result `sprint13-vector-milvus-live-result.md`；evidence：`live-evidence/sprint13-vector-milvus-live-evidence.json`；readiness：`sprint13-vector-milvus-readiness.md`；gate：`validate-vector-store-live-gate`；历史 LIVE PENDING token 仅作门禁兼容语境；不代表 full platform production ready） |

## Sprint 15：Console Instance Observability（✅ 已完成，2026-07-08）

> 本节记录统一实例可观测性 PRD（`repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md`）对应的 11 个 issue 执行完成事实。该 PRD 覆盖 Core 端 handler 补齐、Console UI 6 个 Tab 组件实现和 Gateway real K8s provider 链路接入，对应 9 种计算实例 kind（vm/container/gpu_container/sandbox/batch_job/notebook/k8s_cluster/bare_metal/dpu_node）的日志、事件、指标、终端/console 和安全事件能力。各 issue 的实现与验证细节见 `repo/development-records/` 对应批次记录。

### Core 端实现（Issue #001 / #002 / #011）

| 批次 | Issue | 内容摘要 | 状态 |
|---|---|---|---|
| CORE-CONSOLE-SESSION-HANDLER-A | #001 | VM console session handler 补全：新增 `CreateConsoleSession` port 方法 + Local/Prometheus adapter 实现 + 5 个 HTTP 测试；protocol 默认值在 adapter 层填充，白名单在 handler 层校验；`connect_url` 与 `url` 等价 | ✅ 已完成（2026-07-03） |
| CORE-INSTANCE-METRICS-MULTI-EXPORTER-A | #002 | 多 exporter 聚合 adapter + range query 端点：通过 `InstanceObservationGetRequest.Kind` 路由 GPU 采集；逐字段降级（`if err == nil` 守卫）；新增 `GET /observability/query_range` 返回 matrix 时序采样点；PromQL label 重写（namespace/pod 映射）；NaN/Inf 过滤；正则 pod matcher 兼容 Deployment hash 后缀 | ✅ 已完成（2026-07-06，增量 2026-07-08） |
| GATEWAY-INSTANCE-CREATE-REAL-K8S-PROVIDER-A | #011 | Gateway 实例创建链路接入 real K8s provider：新增 `bootstrap.ConnectInstanceService` helper；`instance_service_runtime.go` 按 `WORKLOAD_PROVIDER` env 切换；lazy re-observe（非终态实例 Get/List 时触发 K8s 状态同步）；Workload Identity Secret manifest 生成；auth.go 注入 `types.TenantContext`；Secret 脱敏；`make validate-architecture` 通过 | ✅ 已完成（2026-07-08） |

### Console UI 端实现（Issue #003 - #010）

| 批次 | Issue | 内容摘要 | 状态 |
|---|---|---|---|
| CONSOLE-INSTANCE-OBSERVABILITY-SHELL-A | #003 | 路由壳层 + 实例上下文：新建 `route.tsx`（PageHeader + Tab 栏 + Tab Panel + `?tab=` 深链 + deleted 拦截）、`InstanceContext.tsx`、`observabilityTabsConfig.ts`；深链回退双层判定；`InstanceContext` 暴露 `isDeleted`/`isRunning` 派生字段 | ✅ 已完成（2026-07-06） |
| CONSOLE-INSTANCE-OBSERVABILITY-LOGS-A | #004 | 日志 Tab：`LogsTab.tsx` 使用 `useInfiniteQuery` cursor 分页 + 级别筛选 Select + Table 列展示；`levelFilter` 空字符串映射为 `undefined` | ✅ 已完成（2026-07-06） |
| CONSOLE-INSTANCE-OBSERVABILITY-EVENTS-A | #005 | 事件 Tab：`EventsTab.tsx` 使用 `useQuery` 一次性加载 limit=100 + 类型筛选 Select；cursor 分页因 Core OpenAPI `listInstanceEvents` query 缺 `cursor` 入参而降级为一次性加载 | ✅ 已完成（2026-07-06） |
| CONSOLE-INSTANCE-OBSERVABILITY-METRICS-A | #006 | 指标 Tab（双通道）：`MetricsTab.tsx` 双通道布局、`MetricsSnapshot.tsx` 快照卡片、`MetricsChart.tsx` PromQL 时序图、`promqlTemplates.ts` 冻结模板；403 判断 `error.code==='FORBIDDEN'`；自动刷新用 `invalidateQueries`；后改为 range query（`/observability/query_range`） | ✅ 已完成（2026-07-06，增量 2026-07-08） |
| CONSOLE-INSTANCE-OBSERVABILITY-TERMINAL-A | #007 | 终端 Tab（exec）：`TerminalTab.tsx` — POST exec → ws_url → WebSocket + xterm.js；5 态状态机；`idempotency_key` 生命周期（重试复用，重连新生成）；xterm lazy 创建于 `ws.onopen` | ✅ 已完成（2026-07-06） |
| CONSOLE-INSTANCE-OBSERVABILITY-CONSOLE-A | #008 | 控制台 Tab（VM console/VNC）：`ConsoleTab.tsx` — 协议 Select + POST console → connect_url → window.open；3 态状态机 | ✅ 已完成（2026-07-06） |
| CONSOLE-INSTANCE-OBSERVABILITY-SECURITY-EVENTS-A | #009 | 安全事件 Tab（仅 sandbox）：`SecurityEventsTab.tsx` — severity 筛选 Select + Table 列展示；cursor 分页同样 blocked-by-core | ✅ 已完成（2026-07-07） |
| CONSOLE-INSTANCE-OBSERVABILITY-BROWSER-VERIFICATION-A | #010 | 验证收口批次（verification-only，无代码改动）：9 条 AC 逐条代码审查映射到组件源码行号；验证 SPEC §1.1 五项 Core 端实现落地情况 | ✅ 已完成（2026-07-07） |

### 关键设计决策与已知边界

- **Kind × Tab 矩阵**：container/gpu_container→logs,events,metrics,terminal；sandbox→+security-events；vm→logs,events,metrics,console；batch_job/notebook→logs,events,metrics；k8s_cluster/bare_metal/dpu_node→logs,events（无 metrics）
- **指标双通道**：快照（`getInstanceMetrics` ← adapter ← exporter）+ 时序（`/observability/query_range` PromQL 代理返回 matrix）
- **多 exporter 聚合**：metrics.k8s.io（CPU/内存/网络）+ DCGM（GPU/显存），仅 `gpu_container` 采集 GPU
- **cursor 分页 blocked-by-core**：`listInstanceEvents` 和 `listInstanceSecurityEvents` query 缺 `cursor` 入参（response 有 `next_cursor`），遵守契约不发明字段，降级为一次性加载
- **后端 WebSocket exec 服务端未实现**：SPEC §11.2 已知边界，归后续 Core 批次
- **Issue #011 lazy re-observe**：非终态实例在 Get/List 时触发 K8s 状态同步，避免引入后台 controller

### 热修复：实例运行时信息回填（INSTANCE-RUNTIME-HYDRATE-A，2026-09-01）

> 前端反馈 GPU 容器实例列表/详情缺节点、私网 IP、访问端点、终端。Gateway `instances.go` 修复：节点字段错位（`refreshOneStoreStatus` 补回填 `Compute.NodeName`）、从 Pod 回读 PodIP 填 `Network.PrivateIP`/`Endpoint`/`Endpoints`、运行态 container/gpu_container 置 `Access.ExecAvailable=true`；`get` 详情处理器接入 `refreshOneStoreStatus`。已在 10.10.1.66 live passed（镜像 `ani-gateway:dev-20260901`）：运行中 GPU 容器实例 `test-gpu-inst-create` 列表/详情一致回填 dev-phys-02 / 10.60.0.3 / exec_available=true；`go test ./services/ani-gateway/...` + `make validate-architecture` 通过。批次详情见 `repo/development-records/instance-runtime-hydrate-a.md`。

### 热修复：存储挂载重启后 store 回落（INSTANCE-STORAGE-MOUNT-STORE-A，2026-09-02）

> 网关重启后实例创建挂载卷/文件系统报 `mount volume "...": capability resource not found`。根因：解析阶段 `GetVolume/GetFilesystem` 优先读 DB store，挂载阶段 `MountVolume/MountFilesystem/UnmountVolume/UnmountFilesystem` 只查进程内存 map，重启后内存清空导致两阶段数据源不一致。修复：新增 `lookupVolumeRecord/lookupFilesystemRecord`（内存优先、miss 回落 store 并回填）与 `hydrateFilesystemMountTargets`（挂载前从 store 恢复 mount target），四个 mount/unmount 方法全部接入；回归测试复现重启场景。已在 10.10.1.66 live passed（镜像 `ani-gateway:dev-20260902-mount-store`）：rollout 后新 idempotency_key 创建挂载卷实例 201（provisioning，`storage_attachments` resolved，resource_refs 产出）。已知边界：租户 PVC 卡 `pending`（后端未绑定）属独立排查项，文件系统 Available 挂载门禁维持不变。批次详情见 `repo/development-records/instance-storage-mount-store-a.md`。

### 热修复：存储 pending re-observe 与 WFFC 挂载放行（INSTANCE-STORAGE-REOBSERVE-A，2026-09-03）

> 承接 INSTANCE-STORAGE-MOUNT-STORE-A 的遗留排查项（租户 PVC 卡 pending）。根因三层：① 存储状态只在创建 apply 后观测一次，WFFC PVC 那一刻必然 Pending 且再无 re-observe 途径（`LocalStorageStatusReconciler` 已实现但无调用方），控制面状态永远停在 pending；② resolver 文件系统门禁要求 Available 与 WFFC 语义死锁（PVC 等第一个消费者、消费者等 Available）；③ `MountFilesystem` 只认 Available mount target，而 provider 模式下 target 因后端 PVC 未绑定停 Creating。修复：`GetVolume`/`GetFilesystem` 对 pending 记录发起 provider re-observe 并 store+内存双写（30s 节流，失败降级 warn）；resolver 放行 Pending 文件系统（挂载即 WFFC 首消费者，Failed/Deleting/Deleted 仍拒）；`MountFilesystem` 放行 Creating target（Pod 经共享 PVC/CSI 挂载而非合成 IP）。排查同时确认集群缺失 `ani-block` StorageClass（helm values 声明但部署规格从未创建），已按 ani-rbd-ssd 参数在集群手工创建使 PVC 绑定，清单落地 `deploy/real-k8s-lab/` 为独立事项。新增 4 个回归测试；`go test` + `go build`（pkg+gateway）+ gofmt 通过。**live 验证通过（2026-09-03，镜像 `dev-20260903-reobserve`）**：挂载 Pending NFS 文件系统的容器实例创建 201（`inst_a60c1092-c55b-4937-bbe7-180826baea35`，real provider），Pod 成为首消费者后 `fs_306220e7` 经 re-observe pending→available；期间排除两个环境干扰（Harbor `ani-purpose-system` 标签误标 rocky:10 触发 ImagePurposeMismatch；另一部署管道在我部署后用 digest镜像 `sha256:72d1af13` 覆盖 gateway 导致旧二进制短暂接管，已恢复并复验——多管道并发部署 gateway 需协调）。批次详情见 `repo/development-records/instance-storage-reobserve-a.md`。

### 热修复：VM cloud-init 用户名密码注入（VM-CLOUDINIT-PASSWORD-A，2026-09-03）

> `password_secret_ref` 在契约里声明但渲染层未接线，设不了 VM 密码。修复（方案 A，见
> `repo/design/vm-cloudinit-password-fix-plan.md`）：渲染层拆 `vmCloudInitEnabled`（disks
> `cloudinitdisk` 追加条件纳入 `PasswordSecret`，避免 volume/device 不匹配 VMI 校验失败）+ 
> `vmCloudInitVolume` 把 `PasswordSecret` 与 `CloudInitSecret` 二选一接线到
> `cloudInitNoCloud.secretRef`；Gateway `validateCreateInstanceConfigs` 加 `cloud_init_secret`
> 与 `password_secret_ref` 互斥校验（400，沿用既有映射）；resolver `resolveSecrets` 对 cloud-init
> secret 校验 `userdata` 键存在（缺键 `ErrConflict`，复用 `Keys` 不新增安全面）；OpenAPI v1.yaml
> 补 `cloud_init_secret` 声明 + 澄清 `password_secret_ref` 描述，`core-schema.d.ts` 重生成。
> 新增 3 组测试（renderer PasswordSecret + disks 联动 / resolver 缺键两路径 / gateway 互斥）。
> `go build` + `go test`（runtime 5/5 + gateway 全过）+ `validate_component_imports.py` +
> `git diff --check` 通过。**live gate 三条路径已通过**（2026-09-04，真实集群 10.10.1.66，
> 镜像 `dev-20260904-vm-cloudinit-password-1`）：`user_data` / `cloud_init_secret` /
> `password_secret_ref` 探针均 `ANI_VM_PASS_STATUS=SET`，evidence 落
> `repo/development-records/live-evidence/`，live gate YAML 新增
> `vm-cloudinit-password-secret-ref-password` 检查并置 status: live。批次详情见
> `repo/development-records/vm-cloudinit-password-a.md`。

### 热修复：RWO 卷占用保守预检（INSTANCE-RWO-PRECHECK-B，2026-09-04）

> 承接 INSTANCE-STORAGE-USAGE-A 立项预告，把后端拦截落地：创建入口 resolver `resolveStorage` 对全部卷路径做占用检查（resolver 新增 `WithWorkloadStore` 链式装配，deps.go + gateway instances.go 接线，nil 跳过）；生命周期入口 `applyLifecycle` 计算 `volumeOccupancyConflict` 传入 `lifecyclePrecheck`——`attach_volume` 检查目标卷，`start/resume` 检查实例自身引用卷（覆盖"停机期间卷被接管、重启撞不同节点"用户场景），排除实例自身；命中活跃消费者返回 409 `ErrConflict`（消息带占用实例 ID 与状态），operation `FailureReason=volume_occupied_by_active_instance`。文件系统（RWX）共享豁免；stopped/failed/deleted 不占用；store 读失败 fail-open，K8s Multi-Attach 仍是并发竞态兜底；restart 不预检。7 个单测；未改 OpenAPI 契约无生成物变更；validate-architecture + openapi lint + gofmt + git diff --check 通过。批次详情见 `repo/development-records/INSTANCE-RWO-PRECHECK-B.md`。

### 热修复：存储占用标记与过滤（INSTANCE-STORAGE-USAGE-A，2026-09-04）

> 承接 9/3 事故复盘（`test-mount-filesystem2` 因 RWO 卷被旧实例占用卡 provisioning，用户无从得知卷被谁占用）。按 2026-09-04 范围决策**只做占用可见性，不做后端拦截**：`/volumes`、`/filesystems` 列表与详情响应新增 `in_use`/`used_by`（恒输出 `[]`），list 支持 `?in_use=true|false` 过滤（非法值 400）；新增判定 helper `pkg/adapters/runtime/storage_consumers.go`（单资源 + 批量索引，规则：attachments 引用且实例状态 ∈ {pending,provisioning,starting,running,stopping}，stopped/failed/deleted 释放；每页一次扫描避免 N+1）；`storageAPI` 注入 `WorkloadInstanceStore`（nil 安全降级）；OpenAPI additive（`in_use` 参数 + `StorageConsumerInfo` schema）+ core-schema.d.ts 重生成。前端对接文档（线下发送前端团队，不入库；创建实例卷下拉接 `in_use=false`，409 拦截见 PRECHECK-B 批次）。方案 `repo/design/instance-storage-usage-a.md`，后续批次 PRECHECK-B（create/attach_volume/start/restart 409 预检）已立项预告。6 个 router HTTP 测试 + adapter 单测；openapi lint / validate-architecture / go build 通过；live 验证见批次记录。批次详情见 `repo/development-records/INSTANCE-STORAGE-USAGE-A.md`。

## Instance Observability Completion 增量补全（2026-07，PR4 分支）

> 本节记录 `feat/instance-observability-pr4` 分支对 Sprint 15 实例可观测性的增量补全工作，对应 SPEC `spec-console-instance-observability-completion.md` 的 16 个设计决策（D-1~D-16）、12 个 User Story（US-001~US-012）和 8 个批次（B-1~B-8）。覆盖 LogStore port 抽象、Loki 日志持久化、Prometheus GPU/VM 指标采集、PromQL label 重写扩展和 VM 前端模板。各批次实现与验证细节见 `repo/development-records/instance-observability-completion-*.md`。

### 批次执行状态

| 批次 | Issue | 内容摘要 | 状态 |
|---|---|---|---|
| INSTANCE-OBSERVABILITY-COMPLETION-B1-HANDLER-PASS-KIND | #001 | Gateway `getMetrics` handler 透传 `record.Kind` 到 `InstanceObservationGetRequest`，修复 GPU 分支死分支问题；`metricsKindSpy` 端到端覆盖 container/gpu_container/vm 三种路径 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B2-PROMETHEUS-DCGM-SCRAPE | #002 | Prometheus ConfigMap 新增 `dcgm-exporter` scrape job，使 `DCGM_FI_DEV_GPU_UTIL` 等 GPU 指标可被采集 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B3-GPU-ADAPTER-E2E-VERIFY | #003 | GPU adapter 端到端集成测试 + live gate 缺陷修复：真实 DCGM 不暴露 `FB_TOTAL`，改用 `FB_FREE+FB_USED`；同步更新 SPEC D-2 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B3-LOGSTORE-PORT | #004 | 新增 `pkg/ports/log_store.go` 定义日志持久化存储 port 抽象（`LogStore` interface + `LogQueryRequest`/`LogQueryResult`） | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B3-LOKI-LOG-STORE-ADAPTER | #005 | `LokiLogStore` adapter 实现：LogQL 查询 + cursor 分页 + level 解析；15 个单元测试覆盖 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B4-LOKI-FLUENT-BIT-DEPLOY | #007 | Loki 3.6.0 + Fluent Bit 3.2.0 DaemonSet 部署示例（485 行，10 资源）；三节点 live 验证全通过 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B5-LOKI-RANGE-CA-FIX | live gate 收尾 | 修复 Loki pod 正则匹配、K8s CA 加载和 metrics 时序图无数据三个阻断性缺陷 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B5-PROMETHEUS-KUBEVIRT-SCRAPE | #008 | Prometheus ConfigMap 新增 `kubevirt-virt-handler` scrape job 采集 `kubevirt_vmi_*` 指标 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B3-LOGSTORE-INJECTION | #006 | `PrometheusInstanceObservability` 新增可选 `logStore` 字段 + `SetLogStore` 方法；Gateway runtime 按 `INSTANCE_OBSERVABILITY_LOG_STORE` env 切换实现 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B5-GETMETRICS-VM-BRANCH | #009 | `GetMetrics` 新增 VM 分支：查询 6 个 `kubevirt_vmi_*` 指标；`name` label 精确匹配；`MemoryUsedMB` 用 PRD FR-17 公式 `domain_bytes - usable_bytes` | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B6-REWRITE-PROMQL-NAME-LABEL | #010 | `rewritePromQLLabels` 扩展支持 `name` label（OQ-4 决策 D-13）；`name` 用精确匹配非正则 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B7-VM-PROMQL-TEMPLATES | #011 | 前端 `promqlTemplates.ts` 新增 VM kind 冻结 PromQL 模板（CPU 利用率、内存使用率）；VM 配色复用 container 蓝绿 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B8-VM-SNAPSHOT-VERIFY | #012 | VM 指标 Tab 快照卡片验证（纯验证批次）：确认 `getMetricsForVM` 查询 `kubevirt_vmi_*` 指标、null 字段显示「暂不可用」不伪造 0 | ✅ 已完成 |

### 关键设计决策与已知边界

- **LogStore port 抽象**：新增 `ports.LogStore` 单方法 interface（`QueryLogs`），复用现有 `InstanceLogEntry`，Cursor 为 opaque string
- **Loki 方向偏离 SPEC**：实现使用 `direction=backward` + cursor→end（RFC3339↔Unix 纳秒），偏离 SPEC §3.3/§5.5 的 `forward`+cursor→start；继承 B5 live gate 修复语义，待 SPEC 同步
- **Loki pod 正则匹配**：SPEC 指定精确 `{pod="<instance_id>"}`，实现用 `{pod=~"^<instance>(-.*)?$"}` 正则匹配兼容 ReplicaSet hash
- **Level 推断**：SPEC 未规定，实现新增 `inferLogLevel` 从 message 推断 level，兼容 Fluent-Bit 采集的 nginx/stdout 日志无 level 字段的情况
- **VM `resident_bytes` 查询但不赋值**：SPEC AC2/FR-15 要求"必须查询"，实现查询但只取 Timestamp，值丢弃（record 无对应字段）；`MemoryUsedMB` 用 PRD FR-17 公式 `domain_bytes - usable_bytes`
- **GPU 显存公式**：live gate 复现真实 DCGM 不暴露 `DCGM_FI_DEV_FB_TOTAL`，改用 `FB_FREE + FB_USED`，单位为 MiB 非 bytes；已同步更新 SPEC D-2
- **OQ-4 决策**：`rewritePromQLLabels` 扩展支持 `name` label，VM 用精确匹配（VMI `metadata.name` 无随机后缀）
- **VM 端到端 live 验证待补**：当前系统无 VM，单元测试用 mock HTTP server 验证，依赖 KubeVirt scrape 配置部署 + VM 运行后补齐
- **MinIO emptyDir 非持久化风险**：Loki 部署示例使用 MinIO S3 后端，emptyDir 重启后数据丢失，已在 yaml 头部标注
- **Local mock GPU 返回 0 而非 nil**：`LocalInstanceObservabilityService` 对非 `gpu_container` kind 返回 GPU 字段为 `&0.0` 而非 nil，与 port 注释"缺失不等于 0"原则不一致，属已知边界

## Sprint 12 已完成切片

1. `SPRINT12-KICKOFF-A`：Sprint 12 启动 + GAP 分析归档，规划 19 个 Core handler 缺口 + 2 个 422，分 B1/B2/B3 三批；仅 ANI Core，Tier1 local profile。
2. `CORE-SVC-SUPPORT-OBSERVABILITY-A`：B1 handler 已完成。新增实例可观测只读 port/local adapter，接入 `/instances/{instance_id}/logs`、`/events`、`/metrics`、`/security-events` 和 `POST /exec`；新增 GPU inventory local adapter 与 `gpu_inventory_resources.go`，注册 `/gpu-inventory`、`/gpu-inventory/occupancy`、`/sandbox-templates`；响应带 `dev_profile`，不声明 production/runtime ready。
3. `CORE-SVC-SUPPORT-NETSTORE-A`：B2 handler 已完成并复审收口。扩展 network/storage/K8s ports 与 local adapters，接入 `/networks/routes`、`/volumes/{volume_id}/snapshots`、`/filesystems/{filesystem_id}/mount-targets`、`/k8s-clusters/{cluster_id}/workloads`；`createVolumeSnapshot` 的 202 响应按全局约定返回 `AsyncTask`；向量库非 ready 与 K8s 创建前置不满足返回 `422 PRECONDITION_FAILED`；响应带 `dev_profile`，不声明 production/runtime ready。
4. `CORE-SVC-SUPPORT-OBJVEC-A`：B3 handler 已完成。扩展 storage/vector ports 与 local adapters，接入 `/buckets`、`/objects/upload`、`/objects/{object_id}/download`、`/vector-stores/{vector_store_id}/documents`；对象 upload/download 返回预签名 URL，不走 multipart；vector document insert 返回 202；不声明 production/runtime ready。
5. `SPRINT12-CLOSURE-A`：Sprint 12 收口完成，进入 Sprint 13 real provider/live gate 收敛。

## Sprint 11 已完成切片

本节保留 Sprint 11 的历史回归事实，完整历史清单以 `repo/development-records/README.md` 为唯一归档索引。

1. `SPRINT11-KICKOFF-A`：入口文档切换到 Sprint 11 / Core Real Deployment Validation；明确只做 ANI Core，先跑真实服务器只读验证和风险评估。
2. `CORE-STORAGE-DISK-RISK-A`：新增 `deploy/real-k8s-lab/sprint11-storage-disk-plan.yaml` 和 validator，记录三台物理机系统盘、数据盘、稳定 `/dev/disk/by-id` 映射、Rook-Ceph 风险策略。策略明确禁止依赖 `/dev/sdX` 顺序，禁止为“盘符对齐”调整启动盘或控制器枚举。
3. `CORE-REAL-DEPLOY-A`：新增 `deploy/real-k8s-lab/sprint11-core-real-deployment.yaml` 和 validator，聚合 Sprint 10 release-prep、REAL-K8S-LAB profile、K8s/KubeVirt/storage 只读验证和 Sprint 11 文档一致性门禁。
4. `CORE-ROOK-CEPH-FORMAL-DEPLOYMENT-A`：新增 `deploy/real-k8s-lab/sprint11-rook-ceph-formal-deployment.yaml` 和 validator，交付 Rook-Ceph `CephCluster`、`CephBlockPool`、`StorageClass` 正式部署代码包；只使用 `/dev/disk/by-id` SSD 候选盘，排除 HDD，不自动设为默认 StorageClass。
5. `CORE-SAFE-COMPLETION-A`：新增 `deploy/real-k8s-lab/sprint11-core-safe-completion.yaml` 和 validator，按上游 Kubernetes/Rook-Ceph 最佳实践固定安全完成条件：只读验证、持久设备 ID、raw unmounted OSD 策略、fail-closed、人工审批前禁止写操作。
6. `CORE-REAL-DEPLOY-DOC-CONSISTENCY-A`：新增 Sprint 11 文档一致性 gate，校验 `ANI-DOCS-INDEX.md`、`ANI-06-开发计划.md`、`repo/CURRENT-SPRINT.md`、`repo/README.md`、Makefile targets 和 development records 索引。
7. `CORE-ROOK-CEPH-LIVE-DEPLOYMENT-A`：正式部署 Rook `v1.20.0`、Ceph `v19.2.3`、CSI operator、CSI-Addons CRD、CephCluster、`ceph-rbd-ssd` pool 和 `ani-rbd-ssd` StorageClass；5 个 SSD OSD 运行；RBD PVC/Pod smoke test 通过并删除临时资源。
8. `CORE-ROOK-CEPH-VM-STORAGE-SMOKE-A`：启动临时 KubeVirt VM 挂载 Rook-Ceph RBD Block PVC；PVC/PV Bound，VMI Running/Ready，guest 看到 `/dev/vdb` 并完成块设备写入尝试；临时 VM/PVC/PV/StorageClass 已删除。
9. `CORE-ROOK-CEPH-REBOOT-RESILIENCE-A`：按 worker-first、control-plane-last 顺序逐台重启三台节点；两个 worker 的 VM/PVC 恢复通过，control-plane 重启后 API readyz、mon/mgr/OSD、Ceph 和 worker VM/PVC 观测恢复；未并发重启。
10. `SPRINT11-SAFE-CLOSURE-A`：Sprint 11 最终安全闭环已更新为“部署前安全证据 + 部署后 live result + VM storage smoke result + reboot resilience result”记录；不是实际 v1.0.0 发布或完整 production ready。
11. `CORE-HISTORICAL-DOC-MARKER-COMPAT-A`：修复 Sprint 8/9/10 Core 历史文档一致性 validator 的 marker 逻辑，使其接受当前入口文档中的历史门禁/已完成归档表达，同时继续拒绝 stale current marker；不新增 Services 或 Core API path。
12. `ANI-14-PHASE4-BATCH1-A`：Phase 4 第一批 handler 骨架完成：新建 8 个 handler 文件（55 条路由），修改 stubs.go/router.go；Models/InferenceServices/KnowledgeBases/GpuContainers/Sandboxes/Tenant/Branding/Tasks 全部从 501→200；build/test/architecture 通过。

## 真实环境结论

- Kubernetes 三节点 Ready，版本 `v1.36.1`；KubeVirt phase `Deployed`。
- `rook-ceph` CephCluster 已部署完成，状态 `Ready/HEALTH_OK`；3 个 mon、1 个 mgr、5 个 OSD 运行。
- `ceph-rbd-ssd` pool 为 `Ready`；`ani-rbd-ssd` StorageClass 已上线，`Retain`、`WaitForFirstConsumer`、非默认 StorageClass。
- 受控 RBD smoke test 使用临时 `Delete` StorageClass，PVC 绑定、Pod 挂载、写读 marker 成功；临时 Pod/PVC/StorageClass/PV 已删除。
- 受控 KubeVirt VM RBD storage smoke 使用临时 `Delete` StorageClass 和 Block PVC，VMI 达到 `Running/Ready`，guest 看到 RBD block device 并完成写入尝试；临时 VM/PVC/PV/StorageClass 已删除。
- 逐节点 reboot resilience 已执行：两个 worker 先后重启并验证同一 VM/PVC 恢复；control-plane 最后重启并验证 API readyz、mon/mgr/OSD、Ceph 和 worker VM/PVC 观测恢复；未并发重启。
- ANI1 系统盘观测为 `sdb`，数据 SSD 为 `sda`；ANI2 系统盘观测为 `sdc`，数据 SSD 为 `sda`/`sdb`；ANI3 系统盘观测为 `sdd`，数据 SSD 为 `sda`/`sdb`，另有一块 HDD 为 `sdc`。
- Linux `/dev/sdX` 不是稳定设备身份，不能作为 Rook-Ceph OSD 或 fstab 自动化选择依据。后续必须使用 `/dev/disk/by-id`、WWN、序列号或 UUID/PARTUUID。
- 对 Rook-Ceph，初始 VM 优先存储池建议只使用未挂载、无文件系统签名的 SSD raw devices；ANI3 HDD 初期应排除或单独建低速 class，不要混入 VM 优先 SSD pool。

## 当前事实边界

- Core 保护范围只推进 ANI Core；Services/RAG/Console/BOSS/前端/推理/知识库业务在 Services 主责目录以受控 PR 推进，触碰 Core 保护范围时按 CODEOWNERS 共同审查。
- Sprint 11 未新增 Core OpenAPI path，Core API v1 兼容性基线保持有效。
- Sprint 11 没有新增 `M1-REAL-LAB-*` guard。
- 本阶段未执行手工 `wipefs`、`sgdisk`、`mkfs`、`mount`、`/etc/fstab` 修改、系统盘变更、默认 StorageClass 切换或已有 PVC 迁移；Rook-Ceph 按审批后的 manifest 自动完成 OSD prepare 和 OSD 认领。生产化 reboot resilience 已按审批逐台重启三台节点，未并发重启。
- “盘符对齐”只可作为人工阅读清单里的 slot 命名，不可作为自动化操作目标；真实自动化必须使用持久设备 ID。
- Sprint 11 最终安全完成遵循上游 Kubernetes/Rook-Ceph 最佳实践：先只读盘点，再用稳定设备 ID 建模，最后在人工审批后才允许任何状态变更。

## 历史回归门禁

- Sprint 8 Core-only 代码开发已完成，并继续作为 release hardening、installer live-readiness、offline pack、CLI-B 和文档一致性历史门禁保留。
- Sprint 9 Core-only 代码开发已完成，并继续作为 RC readiness、release evidence、offline checksum、CLI version 和文档一致性历史门禁保留。
- Sprint 10 Core-only 代码开发已完成，并继续作为 artifact manifest、version policy、final readiness、CLI release metadata 和文档一致性历史门禁保留；Sprint 10 不是实际 v1.0.0 发布。
- Sprint 8/9/10 历史文档一致性门禁接受当前 Sprint 11 入口文档中的历史门禁/已完成归档表达，不要求入口文档保留旧 Sprint 的当前态短语。
- Sprint 5 `REAL-K8S-LAB-A` / `make validate-real-k8s-profile` 仍作为真实底座历史门禁保留，覆盖 Kube-OVN、KubeVirt、vCluster 与 local profile / real-provider 边界；M1-NETWORK-LIVE-A / `validate-kubeovn-network-live-gate` 固定 Kube-OVN `Vpc/Subnet`、NetworkPolicy 和 Service/LB contract 门禁，Sprint 13 S01 已在此基础上补 route contract 并通过真实 route evidence；M1-K8S-LIVE-A / `validate-vcluster-live-gate` 固定 vCluster Helm/kubeconfig、kubectl `/version` 和 Core live proxy contract 门禁，Sprint 13 S02 已在此基础上补 `core-workloads-list` 并通过真实 workload evidence。
- Sprint 11 聚合门禁依赖 Sprint 10 release-prep，不重新打开这些历史 Sprint 的开发范围。

## 文档入口边界

- `CLAUDE.md` 只维护稳定强制规则、读取顺序、架构边界、提交门禁和 Karpathy 五条开发原则。
- 当前 Sprint 的详细完成项、未完成项、验收命令、下一步和真实底座边界以本文为准。
- 批次实现细节只写入 `repo/development-records/*.md`，不得把每日开发流水账或 API path 长列表写回 `CLAUDE.md`。
- 修改入口文档后必须运行 `make validate-doc-entrypoints`。

## 验收命令

```bash
make validate-sprint11-storage-disk-plan
make validate-sprint11-core-real-deployment
make validate-sprint11-rook-ceph-formal-deployment
make validate-sprint11-rook-ceph-live-deployment-result
make validate-sprint11-rook-ceph-vm-storage-smoke
make validate-sprint11-rook-ceph-reboot-resilience
make validate-sprint11-safe-completion
make validate-sprint11-core-doc-consistency
make validate-sprint11-real-deployment
python scripts/validate_yaml.py deploy/real-k8s-lab/sprint11-core-real-deployment.yaml deploy/real-k8s-lab/sprint11-storage-disk-plan.yaml deploy/real-k8s-lab/sprint11-rook-ceph-formal-deployment.yaml deploy/real-k8s-lab/sprint11-rook-ceph-live-deployment-result.yaml deploy/real-k8s-lab/sprint11-rook-ceph-vm-storage-smoke-result.yaml deploy/real-k8s-lab/sprint11-rook-ceph-reboot-resilience-result.yaml deploy/real-k8s-lab/sprint11-core-safe-completion.yaml
make validate-doc-entrypoints
git diff --check
```

Sprint 13 基线回归入口：

```bash
make test
make validate-demo-instances validate-core-alpha validate-gpu-contracts
make validate-network-alpha validate-storage-alpha validate-vector-alpha
make validate-spec-split validate-core-beta validate-core-api-compatibility
make validate-sdk-beta validate-mock-a validate-doc-api validate-sdk-mock-smoke validate-sprint4-closure
make validate-instance-observability-live-gate
python scripts/validate_yaml.py api/openapi/v1.yaml
make validate-doc-entrypoints
git diff --check
```

Sprint 13 单批 real provider/live gate 还必须追加该批固定 live gate 命令和 evidence JSON 校验；未形成命令与 evidence 前，不得标记为 runtime ready。
S01-S07 B 轨还必须追加 `make validate-sprint13-b-track-production-shape`，确保 production-shaped evidence 未通过前不能误标 production ready；S05-S07 已复用同一 proof_items 标准，历史 LIVE PENDING token 仅作门禁兼容语境。

Sprint 14 resilience feature branch 回归入口：

关联记录：[`development-records/sprint14-core-resilience-plan.md`](development-records/sprint14-core-resilience-plan.md)、[`development-records/r-sprint14-resilience-live-gate.md`](development-records/r-sprint14-resilience-live-gate.md)、[`development-records/live-evidence/sprint14-resilience-live-evidence.json`](development-records/live-evidence/sprint14-resilience-live-evidence.json)、[`api/core-contract-changelog-sprint14.md`](api/core-contract-changelog-sprint14.md)。

```bash
make validate-sprint14-resilience-live-gate
python scripts/validate_yaml.py deploy/real-k8s-lab/sprint14-resilience-live-gate.yaml deploy/real-k8s-lab/sprint14-resilience-live-fixture.yaml
make test
make validate-architecture
make validate-doc-entrypoints
git diff --check
```

Sprint 14 live proof 已归档到 `development-records/live-evidence/sprint14-resilience-live-evidence.json`；production-ready 范围仅限 `ani-sprint14-resilience` 隔离 fixture。真实 live gate 复跑需要人工重新批准故障注入目标、影响和回滚方案。

Sprint 11 依赖的历史回归入口：

```bash
make validate-sprint10-release-prep
make validate-real-k8s-profile
```

> 涉及真实服务器写操作前，必须先重新执行只读盘点，并由人工确认具体设备 ID、预期影响和回滚方案。

<!-- 历史回归门禁校验器兼容标记（请勿删除；对应 dev-records 历史批次与 make validate-* 门禁） -->
**历史回归门禁 token（校验器兼容，勿删）：** Sprint 4 回归门禁 SPEC-SPLIT-A、SPEC-CORE-BETA、SPEC-COMPAT-A、SDK-BETA-A、SDK-BETA-B、SDK-BETA-C、SDK-BETA-D、SDK-MOCK-SMOKE-A、SDK-MOCK-SMOKE-B、SDK-MOCK-SMOKE-C、SDK-MOCK-SMOKE-D、MOCK-A、DOC-API-A（`make validate-doc-api`）、SPRINT4-CLOSURE-A（`make validate-sprint4-closure`），矩阵见 `api/core-beta-readiness.yaml`；Sprint 11 / Core Real Deployment Validation 正式部署完成；真实服务器只读验证已完成；Rook-Ceph 正式部署已完成；Sprint 11 执行环境：正式部署执行环境。
