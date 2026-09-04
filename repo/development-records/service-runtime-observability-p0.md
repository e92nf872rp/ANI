# OBS-RUNTIME-P0 — 七服务运行时观测与平台聚合 API

完成日期：2026-09-04
对应阶段：Sprint 13 / Services 受控并行 PR
结果：P0-A `live verified`；P0-B 后端/API `live verified`；BOSS/Console 前端按用户明确决定 `not_applicable`；P1 未开始

## 结论与证据等级

OBS-RUNTIME-P0-0～P0-7 作为一个 Feature batch 已在获批的隔离 Kubernetes namespace 中闭环。七个 canonical 服务统一接入内部 `/healthz`、`/readyz`、`/metrics`、OTel Resource identity 和单一 `ani-components` Kubernetes discovery job；Core 新增只读平台聚合接口 `GET /api/v1/platform/services/health`，并验证 reachable、unreachable、unknown、鉴权拒绝和 Prometheus 数据源故障时 fail closed。

| 层级 | 结果 | 可声明范围 |
|---|---|---|
| L0 | pass | `contract checked`：OpenAPI/生成/架构/依赖/promtool/供应链门禁 |
| L1 | pass | `local/logic verified`：runtimeadmin、Prometheus reader、Gateway handler 和反例单测 |
| L2 | pass | `local integration verified`：本地七进程、fake Prometheus；不替代 live |
| L3 | pass | `live verified in kubernetes-admin@kubernetes / ani-service-observability-e2e-0cedae8-0904` |
| L4 | not_applicable | 属于 P1，未执行、未宣称 production-shaped 或 production ready |

持久化脱敏证据：`development-records/live-evidence/service-runtime-observability-p0-live-20260904.json`，SHA256 `d874f9569d1b89d7344a407c76dd70bdd6cc65aabd91060a566da3c6694c5aa4`。原始 runner evidence 为 `/tmp/ani-obs-runtime-p0-0cedae8-evidence-20260904-model-missing.json`，SHA256 `1b58227dc9862e1936f1e1255bb7f55c46287352870a7a89b07981f1318e0809`；仓库归档版本仅将 cluster server 地址改为 `redacted`，其余结果保持一致。

## 固定输入与工作区基线

| 项目 | 值 / 结果 |
|---|---|
| Goal 原始 `ANI_BASELINE` | `ae0bfbd9d0304117adca50c3410f551dc5dd2856` |
| 用户权威覆盖 | 用户明确指定手动切换后的 `main` 为权威源码 |
| 实际固定 HEAD | `0cedae825a489d936cf41815dc27f278f6d3213c` |
| 初始 dirty baseline | 仅未跟踪 `repo/services/tasks/modules/plan/plan-service-runtime-observability-p0-p1.md` |
| PLAN SHA256 | `c9c16e4e5df3a395b22fc8060a5811dab3a508d5d3baa3ba7fc5f9ed1b575034`，收尾复核未变 |
| Core v1 compatibility baseline SHA256 | `a771f3c409a5c0136bbce470df4af525a892b0344a2e98e9923034745dd21faf` |
| 最终 dirty 状态 | 40 个 tracked modified + 40 个 untracked（其中包含用户固定 PLAN）；0 个 staged |
| Git publication | 无 stage、commit、push、tag、PR、branch/remote 修改 |

方案文件始终作为用户已有未跟踪输入保留，未修改、删除、清理或暂存。

## 人工决策与授权

| 决策 | 实际处置 |
|---|---|
| H0 | 新增中立 module `github.com/kubercloud/ani/runtimeadmin`；既有 `ani_workload_reconcile_*` exposition 保留，未删除、改名或改语义 |
| H1 | 原决定为 BOSS 归属；随后用户明确说明 ANI 前端项目已废弃，要求不处理前端、只做接口测试，因此 BOSS/Console 实现与 npm 门禁为 `not_applicable_by_user`，不是 pass |
| H2 | 批准并实现 `GET /api/v1/platform/services/health` / `getPlatformServiceHealth` / `scope:observability:read` / `scope=ani_services` / `coverage=partial` |
| Session API license | `github.com/zhangzhe-ctrl/ani-session-gateway/api@v0.1.0` 按第一方内部模块例外；锁定 module sum 与 commit，未修改外部仓库 |
| Core compatibility baseline | 用户批准对固定 baseline 做精确扩权；仅登记本批 API additive 变更 |
| IMAGE_PUBLISH | `h4-image-publish-32b5a4a9f0ec221cf74ae7ca` 批准七服务；`h4-image-publish-d603259a060857e04a1717d5` 批准四个 fixture 标签；之后未新增发布 |
| H4 | `h4-image-publish-d7530577afbc21ff1ccdfe8b` 确认 fixture manifest；最终 delta `h4-image-publish-04e504947ca91fc8c85d36c1` 仅把 missing scale 目标改为 `model-service`，不新增镜像发布 |
| fixture manifest | `sha256:c3c93e8f1af76e7d137895123e31c9a02005e340d1b9b9da7e1339da361dbc07` |

## P0-0～P0-7 实际改动

| 工作包 | 完成内容 |
|---|---|
| P0-0 | 固定七服务资产/端口/Secret 矩阵、依赖版本、OpenAPI 和人工边界；锁定 Prometheus 与构建来源；完成 license/SBOM/govulncheck 设计与门禁 |
| P0-1 | 新增 `repo/runtimeadmin`：独立 registry、OTel identity/`target_info`、三管理端点、并发 readiness、超时/取消/脱敏、幂等 shutdown；无动态注册 SPI |
| P0-2 | auth/model/task/inference/metering 经 Core bootstrap 接入；迁移前行为由 characterization tests 固定；reconcile-worker 继续走 legacy handler |
| P0-3 | tenant-service 经 Services bootstrap 接入；Gateway 新增独立 9200 management listener；公网 health 兼容且公网 `/metrics` 保持 404 |
| P0-4 | 七服务补齐受控 build/image target、Dockerfile、Deployment/Service/Secret 与 Downward API identity；根 Make/CI 覆盖新增 module 和目标服务 |
| P0-5 | 现有 Prometheus 配置只新增一个 `ani-components` Pod SD job；严格按 opt-in、named health port、Running phase 和七服务白名单过滤；补静态/render/promtool/live runner |
| P0-6 | OpenAPI-first 新增平台聚合契约、四语言 Core SDK/API docs/authz 生成物、`PlatformServiceHealthReader`、Prometheus adapter 和 Gateway handler；四条固定查询使用同一 evaluation time，逐 target freshness/join，数据源故障 fail closed |
| P0-7 | 完成隔离 L3 的 discovery/target/鉴权/up=0/missing/staleness/Pod 删除自动恢复/Prometheus 故障恢复，清理 namespace 并完成四份文档闭环 |

未实现任何 P1 能力：未做完整 strong/weak 依赖审计、后台 sampler、Kubernetes readinessProbe 切换、transport RED、recording rules/P99、Kratos fixture、traces/log collection 或动态 exporter/lifecycle SPI。

## 七服务资产矩阵

所有 workload 均有同名 Deployment/ClusterIP Service，management container port 与 Service `targetPort=health` 一致；管理端口无 Ingress/NodePort。运行时 Secret 统一为 `ani-service-runtime-observability-p0`，证据只记录 key 名称，不记录值。

| canonical service / binary | image repository | product port | management port |
|---|---|---:|---:|
| ani-gateway | `docker.changqingyun.cn/ani/ani-gateway` | HTTP 8080 | 9200 |
| auth-service | `docker.changqingyun.cn/ani/ani-auth-service` | gRPC 9101 | 9201 |
| model-service | `docker.changqingyun.cn/ani/model-service` | gRPC 9103 | 9203 |
| task-service | `docker.changqingyun.cn/ani/task-service` | gRPC 9104 | 9204 |
| inference-service | `docker.changqingyun.cn/ani/inference-service` | gRPC 9104 | 9204 |
| tenant-service | `docker.changqingyun.cn/ani/tenant-service` | gRPC 9105 | 9205 |
| metering-service | `docker.changqingyun.cn/ani/metering-service` | 无额外产品端口 | 9210 |

Task 与 Inference 位于不同 Pod，共用 9204 符合契约。`ANI_SERVICE_NAME`、`ANI_SERVICE_VERSION`、`POD_UID` 由部署资产注入；Kubernetes 中名称不一致或 Pod UID 缺失时启动失败。

## 不可变镜像

| 镜像 | live 使用 digest |
|---|---|
| ani-gateway | `sha256:268ad6bbb569cc3657b3c8f43b3c079a26950da37e75426d18abe4c213ff15d9` |
| ani-auth-service | `sha256:98d2b70a405ffe51c5ee875f2219bc0043950cb4bc015b3671633d3050624b63` |
| model-service | `sha256:403fdae7f8b84a6f4b6533bd6ec2fab7b2f669e67a6c90a5b80759f5b1514df6` |
| task-service | `sha256:412f25f552ed071a77d9ec8ed5a01465838903cd1a33574f027f0fad34bbe169` |
| inference-service | `sha256:c123250101b7ce7c4baf5f4469f6c6ed394ff7981c867756dde6ec7221b7f30c` |
| tenant-service | `sha256:39c879574b4434a39400c10b7569c14d37fed99da4398b2faff5485e60d00cf6` |
| metering-service | `sha256:62cd4e777e69369af0750a9d76d32dfb57ed27e0cc77906ced0f8e6d8d25fe22` |
| prometheus fixture | `sha256:2659f4c2ebb718e7695cb9b25ffa7d6be64db013daba13e05c875451cf51b0d3` |
| postgres fixture | `sha256:5660c2cbfea50c7a9127d17dc4e48543eedd3d7a41a595a2dfa572471e37e64c` |
| nats fixture | `sha256:b83efabe3e7def1e0a4a31ec6e078999bb17c80363f881df35edc70fcb6bb927` |
| redis fixture | `sha256:ff02b58f971e7d7d156a1267e283fcbbeee91773b6aa36c49dac28ecfe28eadf` |

七服务 tag 为 `obs-runtime-p0-0cedae8`；fixture tag 分别为 `obs-runtime-p0-2.55.1`、`obs-runtime-p0-16.4-alpine`、`obs-runtime-p0-2.10-alpine`、`obs-runtime-p0-7.4-alpine`。最终 H4 delta 后未发生任何额外 push。

## L3 环境、对象与结果

- Context：`kubernetes-admin@kubernetes`
- Namespace：`ani-service-observability-e2e-0cedae8-0904`
- SSH alias：`ani`；remote kubectl：`/usr/bin/kubectl`；remote kubeconfig：`/home/kubercloud/.kube/config`
- 创建对象：30；修改既有对象：0；pull secret：none
- 临时操作：精确 NetworkPolicy 替换/恢复；按 namespace+run-id+service selector 解析并删除一个 auth Pod；`model-service` replicas `1→0→1`；隔离 Prometheus replicas `1→0→1`
- Secret 预检：20 项 permission checks 通过；`source_secret_read=false`

13 项 live check 全部 passed：对象应用、Deployment available、七服务三端点、discovery/target_info/排除项、缺凭据 401、租户域 403、七服务 reachable、真实 up=0→unreachable、scrape 恢复、auth Pod UID 变化与自动恢复、model-service missing/stale→unknown、Prometheus unavailable→Gateway 503 fail closed、最终全恢复。

排除项 `reconcile-worker`、`envoy-authz-adapter`、`kb-service` 的 target 数量均为 0；其源码、go.mod、清单、metrics 行为无 diff。外部 `ani-session-gateway` 仓库无修改。

## 验证命令与状态

下表中的 `pass` 均为本批实际执行；前端条目为用户明确范围覆盖后的 N/A，不以未运行冒充成功。

| 命令 / gate | 状态 | 证据范围 |
|---|---|---|
| `go -C runtimeadmin test -race ./...`；`go -C runtimeadmin vet ./...`；`go -C runtimeadmin mod verify` | pass | runtime contract/race/vet/dependency |
| `go -C pkg test ./...`；`go -C services/pkg test ./...` | pass | Core/Services bootstrap 与 reader |
| `go -C services/{ani-gateway,auth-service,model-service,task-service,inference-service,tenant-service,metering-service} test ./...` | pass | 七服务 focused tests |
| 对全部十个受影响 Go module 执行 `go vet ./...` 和 `go mod verify` | pass | build/static/module integrity |
| `go generate ./...`（cwd `repo/services/ani-gateway`） | pass | 后端 Go 生成物 |
| `make gen-core-sdk gen-api-docs gen-gateway-authz` | pass | 四语言 SDK、静态 docs、authz registry |
| `make gen-api` | not_applicable_by_user | 后端 `go generate` 已单独 pass；目标随后强制进入已废弃 Console 的 `npx`，该前端子步骤不在现行范围 |
| `make validate-openapi-spec`；`make validate-core-api-compatibility`；`make validate-gateway-authz` | pass | API、兼容基线、权限生成链 |
| `python scripts/{render_service_runtime_observability_test,render_service_runtime_observability_l3_test,run_service_runtime_observability_l3_test,validate_generated_idempotence_test,validate_runtime_image_workflow_test,validate_runtime_observability_sbom_test,validate_service_runtime_observability_test}.py` | pass | 53 OBS-RUNTIME Python tests |
| `make validate-service-runtime-observability` | pass | contract、promtool、license、SBOM、govulncheck；十个 module 无 reachable vulnerabilities |
| `make test`；`make validate-services`；`make validate-architecture` | pass | repository aggregate gates |
| `npm --prefix frontends/boss ...` 与 Console schema generation | not_applicable_by_user | ANI 前端废弃；用户要求只做接口测试，不实现/验证页面 |
| L2 本地七服务 + fake Prometheus integration | pass | local integration only，明确不是 live |
| L3 runner（下列精确命令） | pass | 隔离集群 live evidence |
| `make validate-doc-entrypoints`；`git diff --check` | pass | 文档与 whitespace 收口 |

中间诊断命令也保留如下，均已闭环，不是最终失败门禁：

| 命令 | 当时结果 | 闭环 |
|---|---|---|
| 首次 `make gen-api gen-core-sdk gen-api-docs gen-gateway-authz` | exit 2；后端 Go 生成已完成，随后废弃 Console 的 `npx` 访问外部 npm 镜像时 `ECONNRESET` | 前端按用户决定 N/A；后端 `go generate ./...` 与其余三个生成 target 分拆复跑 pass |
| 受限 sandbox 内的 L3 runner unit tests | exit 1；loopback socket `PermissionError` | 获准在非 sandbox 环境复跑，14/14 pass |
| 系统 Python 的 `make validate-openapi-spec` | exit 2；缺 `openapi_spec_validator` | 使用任务专用固定依赖 venv 复跑，两份 OpenAPI 均 pass |
| 文档写入后的首次 `make validate-service-runtime-observability` | exit 2；精确 allowlist 漏 README/evidence，Git 对中文路径做 quote | 新增红转绿回归测试；只加入两个固定路径并用 NUL 分隔 UTF-8 读取 Git 路径；完整门禁复跑 pass，任意其他 development record 仍拒绝 |

L3 调试期间的失败 evidence 仅保存在 `/tmp`：依次定位了外部镜像拉取、fixture capability、Kubernetes Service proxy、loopback proxy、SSH port-forward ready 信号、已建立 scrape connection，以及 auth-service 作为 API 授权依赖不适合作为 missing target 等问题。最终经批准只把 missing scale 目标改为 `model-service`；最终 pass 结论只引用上文归档 evidence。

L3 精确命令：

```bash
python3 repo/scripts/run_service_runtime_observability_l3.py \
  --live \
  --kubectl-binary /usr/bin/kubectl \
  --kubeconfig /home/kubercloud/.kube/config \
  --ssh-host ani \
  --ssh-config /home/chabking/.ssh/config \
  --context kubernetes-admin@kubernetes \
  --expected-server '<approved-cluster-server>' \
  --namespace ani-service-observability-e2e-0cedae8-0904 \
  --version obs-runtime-p0-0cedae8 \
  --images-file /tmp/ani-obs-runtime-p0-0cedae8-images-published.json \
  --evidence-output /tmp/ani-obs-runtime-p0-0cedae8-evidence-20260904-model-missing.json \
  --confirmation h4-image-publish-04e504947ca91fc8c85d36c1
```

集群地址在仓库记录中按敏感信息规则脱敏；runner 执行时使用了审批中绑定的精确值并通过 identity check。

## 回滚、清理与剩余边界

- 最终先验证七服务与隔离 Prometheus 全恢复，再删除精确 Namespace；runner cleanup=`passed`，随后独立只读检查确认 namespace 不存在。
- 未修改共享/生产 Prometheus、既有 workload、shared namespace 或生产资源。registry 中获批镜像仍保留；未执行镜像删除。
- 无数据库 migration、消息 subject、租户数据模型或数据补偿。
- 管理 `/readyz` 在 P0 只证明 wire/迁移等价性；尚未完成 P1 的完整业务依赖语义审计，也未接入 Kubernetes readinessProbe。
- 生产 rollout、共享/生产 Prometheus 接入、长期 SLA/soak、L4/P1、真实多副本业务 readiness、BOSS/Console 页面均未验证，不得从本记录外推为 full platform production ready。
- API 的 `up`/freshness 只表达 Prometheus scrape 可达性，不表达业务健康、desired replicas 或全平台健康。
