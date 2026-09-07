# INSTANCE-SANDBOX-TEMPLATE-IMAGE-A：内置沙箱模板镜像接入可复用 python 镜像

> 状态：已完成（本地验证通过；真实环境 live 验证与 rollout 待执行）
> 批次类型：Feature batch / hotfix（修复沙箱 code-run 镜像不可达）
> 分支：ani-hotfix

## 1. 背景与目标

进入沙箱实例详情页调用运行代码接口（`POST /instances/{id}/sandbox/code-runs`）报

```
{"code":"PRECONDITION_FAILED","message":"capability precondition failed: sandbox pod is not ready"}
```

排查定位到内置沙箱模板目录（`template_id=python-secure` 等）的 `Image` 是本地占位串
`registry.local/ani/sandbox-python:dev`，该镜像在集群内不可达，沙箱 Pod 一直 `ImagePullBackOff`，
`code-run` 前置 `waitReadySandboxPod` 等满 `timeout_seconds` 后返回 not ready。

本批次把内置模板默认镜像接入一个**已验证在 10.10.1.66 可拉取的复用镜像**
`docker.changqingyun.cn/hub/library/python:3.12`（探针验证拉取成功、容器内 `python3 -V` = Python 3.12.10，
满足 code-run 的 `python3 -c` 执行），使新建沙箱能起 Pod 并跑 Python 代码。

## 2. 实现

`pkg/adapters/runtime/local_sandbox_template_catalog.go`：

- `python-secure` 模板：`Image` `registry.local/ani/sandbox-python:dev` → `docker.changqingyun.cn/hub/library/python:3.12`
- `cuda-notebook-secure` 模板：`Image` `registry.local/ani/sandbox-cuda-notebook:dev` → 同上可复用 python 镜像
  （当前集群无可用的 GPU/notebook 专用镜像，先用可拉取的 python 镜像承接 code-run，Description 已如实标注；GPU/notebook 专用镜像待后续接入）
- `Description` 同步澄清：内置模板镜像已由可复用 python 镜像支撑，不再是本地占位

该 catalog 同时被 `/sandbox-templates` 列表与沙箱实例创建路径消费，模板 `Image` 经
`image_ref → spec.Image → SandboxCreateRequest.Image` 直落到 Deployment，因此改此一处即接通"模板 → 创建路径"。

## 3. 测试

`pkg/adapters/runtime/local_sandbox_template_catalog_test.go` 新增防回归断言：所有内置模板镜像必须以
`docker.changqingyun.cn/` 开头，防止再落回本地占位镜像。

验收命令：

```
go build ./pkg/adapters/runtime/                          # 通过
go test ./pkg/adapters/runtime/ -run TestLocalSandboxTemplateCatalog -count=1   # ok
python scripts/validate_component_imports.py --root .     # passed
python scripts/validate_inference_legacy_control_plane.py # passed
git diff --check                                          # 无空白错误
```

未改 OpenAPI 契约、无生成物变更（仅改模板默认镜像与描述）。

## 4. 已知边界与后续

- **线上不自动生效**：需重新构建/滚动更新 `ani-gateway` 后，**新建**的沙箱才用新镜像。
- **存量实例不自动恢复**：已用旧占位镜像创建的实例（Deployment 已固化 `registry.local/...`）需删除重建才会吃到新模板镜像；其当前 `running` 为假状态，`code-run` 依旧 not ready。
- `cuda-notebook-secure` 暂以可复用 python 镜像承接，真实 GPU/notebook 镜像待后续接入。
- 长期应引入可配置的真实镜像源（如 live-gate `ANI_SANDBOX_LIVE_IMAGE_REF`，或 push 到 `docker.changqingyun.cn/ani/sandbox-python:<tag>`），不长期依赖 hub 缓存仓库。