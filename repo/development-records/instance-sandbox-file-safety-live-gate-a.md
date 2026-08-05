# INSTANCE-SANDBOX-FILE-SAFETY-LIVE-GATE-A

> 日期：2026-08-02
> 范围：ANI Core / Instance Management / Sandbox files real-provider safety gate

## 目标

在真实 Kata Sandbox Pod 中验证 `INSTANCE-SANDBOX-FILE-SAFETY-A` 的工作区隔离和文件路径防护，不把 local/logic 测试结果外推为真实 provider 结论。

## 实现与验收

- `validate_sandbox_live_gate.py` 强制观察 Deployment 的 `sandbox-workspace` `emptyDir` 与 `/workspace` mount
- 通过真实 code-run 创建目录 symlink、文件 symlink、工作区内 hard-link，并尝试跨文件系统 hard-link
- files API 验证 list symlink、write symlink parent、write symlink target、write hard-link target、delete symlink parent 均返回 HTTP 400
- code-run 复核 Pod 外部测试文件内容不变，且没有生成越界文件
- live gate 保留既有正常 write/list/delete、token、ports、code-run、pause/resume/delete 验收

## 真实环境结果

- 结果：`status=passed`
- Gateway：`docker.changqingyun.cn/ani/ani-gateway:instance-sandbox-file-safety-20260802-v1`
- 镜像 digest：`sha256:27313f070be45cd7378480fc30d845132391eab9a4f97b949b7821475803c824`
- `/workspace`：独立 `emptyDir`
- 跨文件系统 hard-link：blocked
- unsafe files API：5/5 返回 400
- 外部内容：unchanged
- 资源清理：Core lifecycle delete 后无对应 Pod/Deployment/Service 残留
- evidence：`development-records/live-evidence/instance-sandbox-file-safety-live-20260802.json`

## 验证命令

```bash
cd repo
python3 scripts/validate_sandbox_live_gate_test.py
python3 scripts/validate_sandbox_live_gate.py
python3 scripts/validate_yaml.py deploy/real-k8s-lab/instance-sandbox-live-gate.yaml
python3 scripts/validate_sandbox_live_gate.py --live \
  --gateway-url http://<node>:30080 \
  --ani-bearer-token '<token>' \
  --tenant-id 11111111-1111-1111-1111-111111111111 \
  --name ani-sandbox-file-safety-live2 \
  --image-ref docker.kubercon.local/11111111-1111-1111-1111-111111111111/sandbox-python:3.12 \
  --evidence-output development-records/live-evidence/instance-sandbox-file-safety-live-20260802.json
```

## 边界

- 不改 Core OpenAPI v1，不改变既有 HTTP 语义
- checkpoint 仍为 local-session，未在本批实现真实持久化
- 本结果只证明 Sandbox files 安全闭环，不声明完整实例管理或平台 production ready
