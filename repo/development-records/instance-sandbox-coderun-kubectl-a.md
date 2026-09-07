# INSTANCE-SANDBOX-CODERRUN-KUBECTL-A：gateway 镜像内置 kubectl 修复 code-run 执行

> 状态：已完成（live 验证通过，10.10.1.66 rollout 镜像 `dev-20260907-kubectl-a`）
> 批次类型：Feature batch / hotfix（修复沙箱 code-run 的 `kubectl not found`）
> 分支：ani-hotfix

## 1. 背景与目标

模板镜像修复上线后，新建沙箱能起 Pod；但调用运行代码接口
（`POST /instances/{id}/sandbox/code-runs`）仍报：

```
{"code":"BAD_REQUEST","message":"exec: \"kubectl\": executable file not found in $PATH"}
```

定位：`KubernetesSandboxRuntime` 的代码执行走 `KubectlSandboxPodExecutor`，
通过 `exec.Command("kubectl", "exec", ...)` 在 gateway 容器内 shell-out 到 `kubectl`
（见 `pkg/adapters/runtime/kubernetes_sandbox_pod_exec.go`）。而 gateway 镜像 `FROM alpine:3.20`
未安装 kubectl，导致 `runKubectlExec` 在启动子进程时直接报 `executable file not found in $PATH`。

## 2. 实现

`services/ani-gateway/Dockerfile` 最终运行镜像阶段增加 kubectl：

```dockerfile
FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget && \
    wget -q https://dl.k8s.io/release/v1.36.0/bin/linux/amd64/kubectl -O /usr/local/bin/kubectl && \
    chmod +x /usr/local/bin/kubectl && \
    adduser -D -H -u 65532 ani
```

依赖项确认（复用既有能力边界，无新增组件）：

- **in-cluster 认证**：kubectl 运行于 Pod 内时，检查 `KUBERNETES_SERVICE_HOST/PORT` 与
  `/var/run/secrets/kubernetes.io/serviceaccount/token` 后自动走 in-cluster 配置，无需 kubeconfig。
- **RBAC**：`ani-gateway` ServiceAccount 绑定的 `ani-gateway-core-provider` ClusterRole
  已含 `pods/exec`（cluster-scope `get/list/watch/create/update/patch/delete`），
  `kubectl auth can-i create pods/exec --as system:serviceaccount:ani-system:ani-gateway` 返回 yes。

## 3. 验证

live 验证（10.10.1.66，rollout `docker.changqingyun.cn/ani/ani-gateway:dev-20260907-kubectl-a`）：

```
kubectl exec -n ani-system deploy/ani-gateway -- kubectl version --client   # v1.36.0
kubectl exec -n ani-system deploy/ani-gateway -- kubectl get ns             # in-cluster 认证成功
kubectl exec -n ani-system deploy/ani-gateway -- kubectl exec -n <tenant-ns> <sandbox-pod> -- python3 -c "print(...)"
#  以 ani-gateway SA 上下文成功 exec 进沙箱 Pod，kubectl 链路（认证+RBAC+连接）全部打通
```

线上 gateway Pod 1/1 Running、`/healthz` 200；新旧 Pod 正常滚动切换。

## 4. 已知边界与后续

- **code-run 仍需沙箱实例处于可运行状态**：实例 `replicas=0`（paused）或沙箱 Pod 未 Running 时，
  `waitReadySandboxPod` 会返回 `PRECONDITION_FAILED: sandbox pod is not ready`——这是预期行为，
  不是 kubectl 问题。
- **镜像体积**：alpine 运行镜像新增约 45MB kubectl 二进制；如需进一步瘦身后续可用
  `kubectl` 的 `--cache-dir=/dev/null` 或改用 client-go 直连（`pods/exec`），但属后续优化，不在本热修范围。
- 本改动只解决 gateway 侧执行依赖，未改动沙箱模板、API 契约或生成物。