# KUBEVIRT-VM-LIFECYCLE-SUBRESOURCE-A — KubeVirt VM start/stop subresource 修复

完成日期：2026-07-10

## 范围

修复 VM stop/start 在真实 KubeVirt API 上返回 400 的问题。

> 边界声明：本批次只修正 Core runtime 的 Kubernetes lifecycle adapter。它不修改 Services / `frontends/`，不改变 Core OpenAPI 契约，不声明 production ready。

## 根因

`KubernetesLifecycleExecutor` 对 KubeVirt `VirtualMachine` stop/start 使用了主资源路径：

```text
PUT /apis/kubevirt.io/v1/namespaces/{namespace}/virtualmachines/{name}?stop=true
```

并发送 `{}`。KubeVirt 将该请求按主资源更新处理，因 body 缺少 `kind: VirtualMachine` 返回：

```text
the object provided is unrecognized (must be of type VirtualMachine): Object 'Kind' is missing in '{}'
```

## 修复

- VM `stop` 改为：
  `PUT /apis/subresources.kubevirt.io/v1/namespaces/{namespace}/virtualmachines/{name}/stop`
- VM `start` 改为：
  `PUT /apis/subresources.kubevirt.io/v1/namespaces/{namespace}/virtualmachines/{name}/start`
- 请求体改为 KubeVirt subresource options object：
  `{"apiVersion":"subresources.kubevirt.io/v1","kind":"StopOptions"}` / `StartOptions`。
- Deployment/Job lifecycle 仍保持 scale subresource 行为不变。

## 验证

```bash
cd /root/kubercon/ANI/repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'TestKubernetesLifecycleExecutor' -count=1
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -count=1
python3 scripts/validate_component_imports.py --root .
python3 scripts/validate_doc_entrypoints.py
PATH=/tmp/ani-pybin:$PATH make test
git diff --check
```

## 相关文件

- `repo/pkg/adapters/runtime/kubernetes_lifecycle_executor.go`
- `repo/pkg/adapters/runtime/kubernetes_lifecycle_executor_test.go`
