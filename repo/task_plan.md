# 老 ANI 剩余验收收敛计划

## Goal
完成老 ANI 模型 snapshot 到真实推理实例的集群验收，并核实身份链路、大模型导入和历史失败服务边界；不能用伪造状态替代真实依赖证据。

## Phases
- [complete] 盘点推理创建契约、物化镜像、身份/RBAC和历史失败任务
- [complete] 用已导入 snapshot 模型创建真实推理服务并验证就绪、发布和调用
- [complete] 执行可控的大于 1 GiB 模型导入并核对持久化结果
- [complete] 验证 Workload/IAM 身份链路；区分开发租户登录与生产身份
- [complete] 分类历史失败推理服务并确认没有活动 runtime；保留历史记录

## Errors Encountered
| Error | Attempt | Resolution |
|---|---:|---|
| snapshot CPU 试验 OOM | 1 | 删除该测试实例；按既定 GPU 语义改用可用 vGPU 规格 |
| 公共 GPU inventory ID 不被 inference admission 接受 | 1 | 使用 Core capability 的权威 `gpu-nvidia-geforce-rtx-4090` 重新创建 |
| 新模型服务镜像健康探针未通过 | 1 | 首次构建误用了 Dockerfile 默认的 worker stage；改用明确 `--target runtime` 构建 model-service，并明确 `--target model-import-worker` 构建 worker，两个 digest 均已 rollout 且健康检查通过 |

## Final gate status
- [complete] 修复 logical snapshot size 与 manifest storage size 混用，并在新导入模型 API 回读中验证。
- [not_verified] 历史失败推理服务的业务归档/残留 Service 清理流程。当前只确认无活动 Deployment/Pod，未修改历史数据。
- [not_verified] 长时间运行退化与跨长窗口恢复；已验证一次 worker Pod 重启恢复，不外推长期稳定性。
