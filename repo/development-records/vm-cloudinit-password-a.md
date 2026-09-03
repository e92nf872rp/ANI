# VM-CLOUDINIT-PASSWORD-A

> 日期：2026-09-03（契约 + 实现 local verified）；2026-09-04（live gate 三条路径通过）
> 范围：ANI Core / Gateway / VM cloud-init 用户名密码注入（password_secret_ref 接线）
> 状态：live passed（10.10.1.66 真实集群，镜像 dev-20260904-vm-cloudinit-password-1；
> user_data / cloud_init_secret / password_secret_ref 三条路径探针均 ANI_VM_PASS_STATUS=SET）
> 分支：ani-hotfix（hotfix worktree）
> 方案文档：repo/design/vm-cloudinit-password-fix-plan.md

## 问题背景

VM 用户名密码设置走 cloud-init nocloud，两条路径已生效：m_config.user_data 内联
#cloud-config、m_config.cloud_init_secret 引用含 userdata 键的 Secret。但已在 OpenAPI 契约
声明的 password_secret_ref **只被 Gateway 解析和 resolver 校验，渲染层未接线**——用户无法通过它
设置 VM 密码，字段"解析了但没生效"。缺陷根因在 dryrun_renderer.go 的 mCloudInitVolume()
（只处理 CloudInitSecret 与 UserData，完全忽略 spec.VM.PasswordSecret）。

另发现契约缺口：cloud_init_secret 已在生产路径生效且被 Gateway 结构体承接，却未在
OpenAPI 契约声明，API 客户端无法感知。

## 设计决策（已确认，方案 A）

- **D1 接线路径**：password_secret_ref 接线到 cloudInitNoCloud.secretRef，与
  cloud_init_secret 同路径（Secret 含 userdata 键，值为 #cloud-config）。
- **D2 互斥**：cloud_init_secret 与 password_secret_ref 互斥，同时设置返回 400
  （沿用 instanceSpecFromRequest 既有错误映射，非新增 422）。
- **D3 键校验**：对 cloud-init 类 secret（Password/CloudInit）在 resolver 校验 userdata 键
  存在，缺键返回 ErrConflict——把"键写错 → VM 启动后静默无密码"提前到创建时。
- **D4 安全面**：不新增控制面读取 Secret 值的能力（SecretService.GetSecret 只返回 Keys
  键名不含值）；键校验复用以 Keys，零新增安全面。未选方案 B（读值合成 cloud-config，需安全评审）。

## 实现

- epo/api/openapi/v1.yaml：CreateVMInstanceConfig 补 cloud_init_secret 声明（与
  password_secret_ref 互斥、需 userdata 键）；澄清 password_secret_ref 描述（同 cloud-init
  Secret 语义）。v1 兼容（新增可选字段，非破坏）。
- epo/pkg/adapters/runtime/dryrun_renderer.go：
  - 拆出 mCloudInitEnabled(spec)：磁盘设备 cloudinitdisk 追加条件纳入 PasswordSecret
    （只接线 volume 会生成 volume 却缺 disk entry，VMI spec 校验失败）。
  - mCloudInitVolume：CloudInitSecret || PasswordSecret 二选一生成 secretRef（else if
    兜底互斥），UserData 独立共存。
- epo/services/ani-gateway/internal/router/instances.go：alidateCreateInstanceConfigs
  VM 分支追加 cloud_init_secret 与 password_secret_ref 互斥校验（400）。
- epo/pkg/adapters/runtime/instance_resource_resolver.go：esolveSecrets 增加
  cloudInitSecretIDs map[string]struct{} 参数，cloud-init secret 校验 userdata 键存在（缺则
  ErrConflict）；新增 secretHasKey helper；container 调用点传
il，VM 调用点传
  Password+CloudInit 集合。

## 测试

- dryrun_renderer_test.go 新增 TestKubernetesDryRunRendererRendersVMPasswordSecretRef：
  单独 PasswordSecret 时 cloudInitNoCloud.secretRef.name + disks 含 cloudinitdisk（覆盖
  :827 联动）。既有 TestKubernetesDryRunRendererRendersVMCloudInitSecret 不变。
- instance_resource_resolver_test.go：既有 TestLocalInstanceResourceResolverValidatesVMSecretRefs
  的 secret 补 userdata 键；新增
  TestLocalInstanceResourceResolverRejectsCloudInitSecretMissingUserdataKey（Password/CloudInit
  各一，缺键 → ErrConflict）。
- instances_test.go 新增 TestValidateCreateInstanceConfigsRejectsCloudInitAndPasswordSecretRef：
  两字段同设 → error。

## 验证命令

`ash
cd repo
go build ./pkg/adapters/runtime/... ./services/ani-gateway/...                          # 全过
go test -run "TestLocalInstance|TestKubernetesDryRun|TestVM" ./pkg/adapters/runtime/...  # 5/5 PASS
go test ./services/ani-gateway/internal/router/...                                       # 全过
python scripts/validate_component_imports.py --root .                                    # architecture guardrails valid
git diff --check                                                                         # 通过
node frontends/console/scripts/gen-core-schema.mjs                                       # core-schema.d.ts 已含 cloud_init_secret
`

> 注：go test ./pkg/adapters/runtime/... 全量在 Windows 本机有存量
> TestSandboxFileScripts* symlink 失败（Python/os.O_DIRECTORY 兼容），与本次改动无关
> （改动前即存在，CI/Linux 正常）已用 -run 圈定相关用例验证通过。

## Live gate（2026-09-04 完成）

方案 §4.6 第 4 点：`validate_vm_cloudinit_password_live_gate.py` 新增第三条路径，用
`password_secret_ref` 替代 `cloud_init_secret`，复用既有探针断言（用户创建 + shadow 密码已设置）。
已在真实集群（10.10.1.66，镜像 dev-20260904-vm-cloudinit-password-1）执行三条路径全部通过，
evidence 落 `repo/development-records/live-evidence/`：

- `vm-cloudinit-password-userdata-live-20260904.json`（user_data，dev-phys-03）
- `vm-cloudinit-password-secret-live-20260904.json`（cloud_init_secret，dev-phys-03）
- `vm-cloudinit-password-password-secret-ref-live-20260904.json`（password_secret_ref，dev-phys-03）

三条路径 `password_injected: true`、`probe_markers_complete: true`，探针 VM/Secret 已清理。
live gate YAML 已新增 `vm-cloudinit-password-secret-ref-password` 检查并置 status: live。

## 能力边界

- 本批为 live passed：契约、渲染接线、互斥/键校验、生成物已闭环并测试通过；三条 cloud-init
  密码注入路径已在真实集群验证，VM password 注入 runtime ready。
- password_secret_ref 与 cloud_init_secret 功能等价，长期建议收敛为一个字段（决策项 D1，
  方案文档）。
- 明文密码以 cloud-init user-data 进入 VM cloud-init 盘，与 user_data 现状一致；文档需提示用户。
- 不涉及 SSH key（ssh_key_ref）注入、Windows VM 密码机制。
