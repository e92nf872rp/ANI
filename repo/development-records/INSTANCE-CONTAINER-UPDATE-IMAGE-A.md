# INSTANCE-CONTAINER-UPDATE-IMAGE-A

> **日期**：2026-09-16　**分支**：`hotfix/network-store-read`（fork: djm-afk）　**PR**：e92nf872rp/ANI#168　**状态**：live verified（隔离测试环境 ani-test2，镜像 tag `test2-20260915-o`）
> **性质**：Feature batch（hotfix 系列，容器/GPU 容器实例生命周期能力补齐）
> **来源**：测试异常结果记录容器系列与 `特有Bug修复问题清单.md`（容器-4）

## 背景

容器实例详情页「发布与回滚」的「更新镜像」按钮实际已接通请求（能收到后端报错），但后端 `KubernetesLifecycleExecutor.execute` 只有 Start/Stop/Restart/Scale 四个 case，`update_image` 落入 default 返回 `unsupported Kubernetes lifecycle action "update_image"`：校验层（action 合法 kind 含 container/gpu_container）与 API 契约早已支持，缺口在 provider apply 层从未把新镜像刷到 Deployment。本批次补齐该能力，一处修复同时覆盖 container 与 gpu_container（两者均渲染为 Deployment，共用同一 executor 分支）。

## 修复内容

### update_image 接入真实 Deployment 镜像热更新（提交 d16d43e）

三层实现：

1. **executor 接入**：`kubernetes_lifecycle_executor.go` 新增 `applyKubernetesUpdateImage`，对现有 Deployment 做 strategic-merge patch `spec.template.spec.containers[*].image`（容器名 = workload 名，与渲染器一致），触发滚动更新而非重建；env/ports/volumes 不动。仅 container/gpu_container + Deployment workload 支持；ImageRef 缺失返回 ErrInvalid。
2. **镜像解析**：服务层新增 `resolveLifecycleImage`，复用 create 的 `ResolveCreate` 路径（租户 project 校验、镜像 purpose 校验——container 实例只能更新 container purpose 镜像、GPU 实例只能更新 gpu purpose 镜像、漏洞扫描门禁，与创建时行为完全一致）；解析在 `lifecycleIntentFingerprint` 之前完成（ImageRef 参与指纹，保证重放校验稳定）。ports `WorkloadInstanceLifecycleRequest` 新增内部 `ImageRef` 字段（API 契约不变，仍为 `image_id` + `strategy`）。
3. **入库与收敛**：apply 后将完整 `InstanceImageSummary`（ID/Ref/Digest/Name/Tag）写入 record（此前仅写 image_id 清空 ref/digest，详情页读到不一致数据）；`Container.RolloutStatus` 置 `progressing`，由既有 reconciler 观测 Deployment 收敛后翻转，与 scale 同机制，未新增等待逻辑。

## 变更文件

- `repo/pkg/adapters/runtime/kubernetes_lifecycle_executor.go`：`update_image` 分支 + `applyKubernetesUpdateImage`
- `repo/pkg/adapters/runtime/instance_service.go`：`resolveLifecycleImage` + record.Image 完整入库
- `repo/pkg/ports/workload_runtime.go`：lifecycle 请求新增 `ImageRef` 内部字段
- 对应单测（executor patch/拒绝 + service 解析/入库，4 用例）
- 未改：Core OpenAPI 契约、SDK/生成物、Services 层、前端

## 验证汇总

- 单测：`go build ./...`、`go vet`、`go test ./adapters/runtime/ ./ports/`（Windows 下 sandbox symlink 用例为既有环境限制，CI Linux 正常）；gofmt 干净
- 真实环境（ani-test2 隔离环境，gateway 镜像 `test2-20260915-o`）：
  - **container**：nginx:latest → fedora:42，API 200、record 镜像+digest 即时更新、Deployment gen 1→2 镜像已变更、Pod 重建；反向 fedora:42 → nginx:latest，10s 内 Deployment gen3 observedGeneration=3、updated=ready=desired=1，实例 state=running、rollout_status=running、record 含完整 digest（fedora 为无长驻进程基础镜像，Pod CrashLoop 属镜像选择问题非代码缺陷）
  - **gpu_container**：gpu-cuda:12.4.1-base → 用户新上传 gpu-cuda:12.4.1-runtime，API 200、record 镜像+digest（purpose=gpu）更新、Deployment gen 5→6 镜像已变更；实例挂 RWO 块卷，滚动 surge Pod 被调度到异节点触发 Multi-Attach Pending（容器-3 同款 RWO 平台约束，旧 Pod 持续提供服务）；手动缩 0 扩 1 强制重建后新 Pod 以 runtime 镜像 Running，实例 state=running、rollout=running、ready 1/1 收敛——证明 Recreate 路径（maxSurge=0 语义）可收敛
- CI：PR #168 GitHub Actions 为准

## 遗留

- 挂 RWO 块卷的实例做滚动更新（maxSurge>0）会因 surge Pod 跨节点 Multi-Attach 卡住：建议 RWO 块卷实例的 update_image 走 maxSurge=0（Recreate 语义）或为 Pod 加卷节点亲和，与容器-3 的 RWO 预检（create/scale 拦截）一并评估
- 目标镜像无长驻进程（如基础镜像）时 patch 下发后 Pod CrashLoop、实例停留 provisioning/progressing，滚动失败的超时判定与自动回滚依赖 reconciler 既有观测，可作为后续跟进
- 测试实例 `test`（inst_d7050e5b）在验证前已存在 Deployment spec 漂移（Deployment 实际跑 python:3.12 与 record 不一致），验证后已收敛到 runtime 镜像
