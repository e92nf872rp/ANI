# VM 登录用户名 / 密码设置 前端对接文档

> 版本：v1.1（2026-09-04）
> 适用范围：Console 前端对接 Core API 的 VM 初始化（cloud-init nocloud）用户名 / 密码注入能力
> 契约来源：`repo/api/openapi/v1.yaml`（Core API 唯一真实来源）
> 关联后端批次：`VM-CLOUDINIT-PASSWORD-A`（ani-hotfix worktree；已 live passed，hotfix 分支待合入）

***

## 1. 背景

VM 实例的初始化配置走 **cloud-init nocloud**。设置用户名 / 密码等初始化动作，可以通过三种途径传入：

| 途径                   | 对应字段                            | 适用场景                                              |
| -------------------- | ------------------------------- | ------------------------------------------------- |
| 内联 user-data         | `vm_config.user_data`           | 简单初始化，直接把 `#cloud-config` 文本塞进请求体                 |
| 引用 cloud-init Secret | `vm_config.cloud_init_secret`   | 把整段 user-data 放进 Kubernetes Secret，前端只传 Secret 名称 |
| 仅设置登录密码              | `vm_config.password_secret_ref` | 只关心 VM 登录密码，Secret 里只放密码相关 user-data（推荐）          |

本次修复的核心问题：`password_secret_ref` 此前在契约里声明了，但后端渲染层没接线，**传了也不生效、设不了密码**。现已接通——两个 Secret 引用字段都能真正生效。

**重要约束（互斥）**：`cloud_init_secret` 与 `password_secret_ref` **二选一**，同时传返回 400。`user_data` 可与任一 Secret 引用共存。

***

## 2. 接口位置

创建 VM 实例仍走统一的创建实例接口：

```
POST /api/v1/instances
```

`kind = "vm"`，初始化相关字段都放在 `vm_config` 下（推荐用 `vm_config`，不要用扁平的兼容别名）。

请求体相关字段（`CreateVMInstanceConfig`）：

| 字段                              | 类型     | 必填 | 说明                                                        |
| ------------------------------- | ------ | -- | --------------------------------------------------------- |
| `vm_config.user_data`           | string | 否  | cloud-init user-data（`#cloud-config` 文本）；**不得包含长期明文凭据**   |
| `vm_config.cloud_init_secret`   | string | 否  | cloud-init user-data 所在 Secret 的名称；Secret 需含 `userdata` 键 |
| `vm_config.password_secret_ref` | string | 否  | 登录密码 cloud-init Secret 名称；Secret 需含 `userdata` 键          |
| `vm_config.ssh_username`        | string | 否  | 默认 `"ubuntu"`，VM SSH 用户名                                  |

> 顶层 `idempotency_key`（1\~128 字符）必须传；重试复用同一个 key。

***

## 3. 三种设置方式的用法

### 3.1 方式一：内联 `user_data`（最直接）

前端直接构造一段 cloud-config 文本：

```json
{
  "name": "web-vm-01",
  "kind": "vm",
  "idempotency_key": "create-vm-20260903-001",
  "vm_config": {
    "ssh_username": "ubuntu",
    "user_data": "#cloud-config\nusers:\n  - name: ops\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    groups: sudo\n    shell: /bin/bash\n    plain_text_passwd: NewPassw0rd!\n    lock_passwd: false\nssh_pwauth: true\n"
  }
}
```

- 适合一次性、无需前端持有 Secret 的场景

- 缺点：密码明文出现在请求体里，后端也不建议长期明文凭据走这条路

### 3.2 方式二：引用 cloud-init Secret（`cloud_init_secret`）

后端已有一个含 cloud-init 整段 user-data 的 Secret，前端只传名称：

```json
{
  "name": "web-vm-02",
  "kind": "vm",
  "idempotency_key": "create-vm-20260903-002",
  "vm_config": {
    "ssh_username": "ubuntu",
    "cloud_init_secret": "vm-init-ops-001"
  }
}
```

Secret 要求（见第 4 节）：键名为 `userdata`，值为 `#cloud-config` 开头。

### 3.3 方式三：仅引用密码 Secret（`password_secret_ref`，推荐）

只设登录密码，Secret 里只放密码相关的 user-data，交互语义最清晰、也是本次修复的字段：

```json
{
  "name": "web-vm-03",
  "kind": "vm",
  "idempotency_key": "create-vm-20260903-003",
  "vm_config": {
    "ssh_username": "ubuntu",
    "password_secret_ref": "vm-pass-ops-001"
  }
}
```

- 与 `cloud_init_secret` **互斥**，二选一

- 与 `user_data` 可共存：想把"设置密码"和"其它初始化脚本"分开配置时，可 `password_secret_ref + user_data` 一起用

***

## 4. Secret 创建方式（后端/运维侧）

前端通常不直接建 Secret，但流程闭环需要知道约定。两种 Secret 引用，**同一套约定**：

1. 在**目标租户命名空间**下创建 Kubernetes Secret
2. Secret 的 `data` 必须包含键 **`userdata`**
3. `userdata` 的值为一段 `#cloud-config` 开头的初始化配置

以设置登录密码为例（`plain_text_passwd`，经真实集群验证可正确注入 /etc/shadow，无需 `chpasswd`）：

```bash
kubectl -n ani-tenant-<tenant> create secret generic vm-pass-ops-001 \
  --from-literal=userdata=$'#cloud-config\nusers:\n  - name: ops\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    groups: sudo\n    shell: /bin/bash\n    plain_text_passwd: NewPassw0rd!\n    lock_passwd: false\nssh_pwauth: true'
```

等价 YAML：

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: vm-pass-ops-001
  namespace: ani-tenant-<tenant>
type: Opaque
stringData:
  userdata: |
    #cloud-config
    users:
      - name: ops
        sudo: ALL=(ALL) NOPASSWD:ALL
        groups: sudo
        shell: /bin/bash
        plain_text_passwd: NewPassw0rd!
        lock_passwd: false
    ssh_pwauth: true
```

> **为什么用** **`plain_text_passwd`** **而不是** **`chpasswd`**：真实集群验证时发现 `runcmd` 模块在含特殊字符时可能整段失败，而 `users.plain_text_passwd` 在 `users` 模块就能直接写入 shadow，不受 runcmd 失败影响，更稳。

**前端提示**：创建 VM 前应提示用户"已在租户命名空间准备好含 `userdata` 键的 Secret"。后端校验了 `userdata` 键存在性，缺键会在创建时直接报错，不会出现"VM 起来后静默无密码"。

***

## 5. 校验规则与错误码

| 场景                                              | HTTP      | code                     | 说明                                                                                     |
| ----------------------------------------------- | --------- | ------------------------ | -------------------------------------------------------------------------------------- |
| `cloud_init_secret` 与 `password_secret_ref` 同时传 | 400       | `INSTANCE_CREATE_FAILED` | `vm_config.cloud_init_secret and vm_config.password_secret_ref are mutually exclusive` |
| Secret 引用但不含 `userdata` 键                       | 409       | `CONFLICT`               | `cloud-init secret "<name>" must contain a "userdata" key (value: #cloud-config)`      |
| Secret 不存在 / 状态非可用                              | 409       | `CONFLICT`               | resolver 对 Secret 状态校验失败                                                               |
| 实例不存在（后续 lifecycle）                             | 404       | `INSTANCE_NOT_FOUND`     | —                                                                                      |
| 未认证 / 无权限                                       | 401 / 403 | —                        | RBAC scope：`scope:instances:create` 等                                                  |

**前端建议**：

- 400 互斥错误：表单层面就做互斥（`cloud_init_secret` / `password_secret_ref` 字段二选一），避免用户同时填；若仍收到 400 直接展示 message

- 409 缺 `userdata` 键错误：提示"请确认 Secret 包含名为 `userdata` 的键，值为 `#cloud-config`"

***

## 6. 交互流程建议

```
用户选择"创建 VM"
  → 选择镜像 / CPU / 内存 / 网络
  → 密码设置区域（三种方式）：
       a) 内联初始化脚本（user_data）
       b) 引用 cloud-init Secret（cloud_init_secret）
       c) 仅设置登录密码（password_secret_ref，推荐）
  → 校验：b / c 互斥，只能选一个；选 Secret 引用时提示提前建含 userdata 键的 Secret
  → POST /api/v1/instances { kind:"vm", vm_config:{ ... }, idempotency_key }
  → 轮询创建状态，成功后展示实例及可 SSH 登录信息
```

***

## 7. 幂等与重试

- 每个创建请求必须携带 `idempotency_key`（1\~128 字符）

- 网络超时 / 未知结果时，**复用同一个 key 重试**，服务端去重返回原结果

- 不要每次重试生成新 key

***

## 8. 注意事项与边界

1. **互斥**：`cloud_init_secret` / `password_secret_ref` 只能二选一；`user_data` 可与之共存。
2. **不返回明文**：密码存放在 Secret 中，API 不返回任何明文密码字段；Secret 的值后端也不读取（只读键名）。
3. **安全**：优先使用 Secret 引用（方式二/三），避免把用户密码明文内联进请求体。
4. **用户名**：登录用户名由 Secret 里 `#cloud-config` 的 `users[].name` 决定；请求体的 `vm_config.ssh_username` 默认 `"ubuntu"`，具体以镜像 + Secret 约定为准。
5. `user_data` / Secret 引用均为 **v1 additive 新增/澄清字段**，老客户端不受影响。
6. 本能力是 cloud-init 初始化，VM 创建后初始化一次；若初始化失败（如网络、Secret 问题）不会自动重试，需删除重建。

***

## 附：CloudConfig 常用片段参考

设置密码（方式二/三的 Secret 内推荐）：

```yaml
#cloud-config
users:
  - name: ops
    sudo: ALL=(ALL) NOPASSWD:ALL
    groups: sudo
    shell: /bin/bash
    plain_text_passwd: YourPassw0rd!
    lock_passwd: false
ssh_pwauth: true
```

> `lock_passwd: false` + `ssh_pwauth: true` 可同时开放密码 SSH 登录（按需取舍）。

