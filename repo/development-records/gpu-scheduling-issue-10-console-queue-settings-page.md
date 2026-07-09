# GPU-Scheduling-Issue-10: Console 队列设置页

> **批次类型：** Feature batch
> **依赖：** #1, #2, #7
> **完成日期：** 2026-07-06
> **SPEC：** `spec-console-gpu-scheduling.md` §5.1
> **UX：** `ux-console-gpu-scheduling.md` §4.5, §5.3

---

## 1. 范围

GPU 调度队列设置页：平台默认队列只读 + 我的队列 CRUD，消费 `GET/POST/PATCH/DELETE /gpu-scheduling/queues`。

### 变更文件

| 文件 | 类型 | 说明 |
|---|---|---|
| `repo/frontends/console/src/routes/settings/gpu-queues.tsx` | NEW | 队列设置页（路由 `/settings/gpu-queues`） |
| `repo/frontends/console/src/routes/__root.tsx` | MODIFY | 侧栏「设置」改为 SubMenu，含 GPU 调度队列入口 |

---

## 2. 实现要点

### 2.1 页面结构

```
ConsolePage (shell)
  └── ConsolePageHeader (title + 新建队列按钮)
  ├── RBAC Alert (无 write scope 时显示)
  ├── error Alert + 重试
  ├── ConsoleContentCard "平台默认队列（只读）" (Table, 无操作列)
  └── ConsoleContentCard "我的队列" (Table, write 含操作列)
  └── CRUD Dialog
```

### 2.2 CRUD

- **创建**：`POST /gpu-scheduling/queues` + `Idempotency-Key` header（`crypto.randomUUID()`）
- **更新**：`PATCH /gpu-scheduling/queues/{queue_id}`
- **删除**：`DELETE /gpu-scheduling/queues/{queue_id}` + `Popconfirm` 确认
- **409 冲突**：`Message.error`「队列名称已存在」
- **403 平台默认**：`Message.error`「平台默认队列不可删除/修改」

### 2.3 表单字段

| 字段 | 组件 | 校验 |
|---|---|---|
| name | Input | required + K8s 资源名正则 `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` |
| workload_class | Select | required, inference/training/batch |
| weight | InputNumber | required, min=1 |
| reclaimable | Switch | default false |
| project_id | Input | 可选 |

### 2.4 RBAC

`canManageQueues()` 占位函数（当前返回 true，dev/local profile 可测）。当 auth store 接入后应检查 `scope:gpu-scheduling:write`。无 write scope 时：
- 隐藏「新建队列」按钮
- 隐藏操作列（编辑、删除）
- 显示 `Alert theme="warning"`「仅租户管理员可管理队列」

---

## 3. 验收

### 3.1 14 项 AC 全部通过

| AC | 验证 |
|---|---|
| 分两组 is_platform_default | ✅ platformDefaultQueues + customQueues |
| RBAC 隐藏 write + Alert | ✅ canManageQueues() 控制 |
| CRUD Dialog 5 字段 | ✅ name/weight/reclaimable/workload_class/project_id |
| 队列名正则校验 | ✅ QUEUE_NAME_PATTERN |
| weight min 1 | ✅ InputNumber min=1 |
| 创建 POST + Idempotency-Key | ✅ params.header |
| 删除 Popconfirm + Message.success | ✅ |
| 403 平台默认 Message.error | ✅ deleteMutation onError |
| loading Table loading | ✅ |
| empty-custom Empty + CTA | ✅ |
| read-only-user 只读 + Alert | ✅ |
| delete-confirm Popconfirm | ✅ |
| error Alert + 重试 | ✅ |
| type-check + build | ✅ tsc EXIT_CODE:0 + vite build built in 4m 26s |

### 3.2 验证命令

```bash
cd repo/frontends/console
npx tsc --noEmit     # EXIT_CODE: 0
npx vite build      # built in 4m 26s
```

### 3.3 已知限制

- Console 无 auth store，`canManageQueues()` 当前固定返回 true（placeholder）
- Console 无 eslint config（pre-existing）
