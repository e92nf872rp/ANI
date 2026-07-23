# Console 终端 Tab（exec）

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability.md

## Description

实现实例详情终端 Tab：适用 `container`/`gpu_container`/`sandbox`（不含 batch_job、notebook），通过 `createInstanceExecSession` 获取 `ws_url` 后建立 WebSocket 连接。对齐 PRD US-012 与 SPEC §4.1.5、§5.3、§5.6.2、§9.4(US-012)。

## Scope
- Product line: console
- Code paths allowed: repo/frontends/console/src/features/instance-observability/

## Acceptance Criteria
- [ ] 新建 `TerminalTab.tsx`，仅 `container`/`gpu_container`/`sandbox` kind 渲染（其余 kind 无此 Tab）
- [ ] `batch_job`、`notebook` **不展示**终端 Tab
- [ ] 仅 `state=running` + `scope:instances:exec` 权限可连接
- [ ] 非 running：连接按钮 disabled + `Tooltip`「仅运行中的实例可连接终端」
- [ ] 无 exec 权限：`Alert theme="warning"`「当前账号无终端访问权限」
- [ ] 连接流程：POST `/instances/{id}/exec`（body 含 `idempotency_key`）→ 返回 `InstanceExecSession` → 用 `ws_url` 建立 WebSocket
- [ ] 连接状态 Tag：未连接 / 连接中 / 已连接 / 已过期
- [ ] 连接中：Button loading
- [ ] 连接成功：终端区 active（xterm.js 渲染，细节见 SPEC §5.3）
- [ ] session 过期（now > expires_at）：`Alert theme="warning"`「终端会话已过期，请重新连接」+ 重新连接按钮
- [ ] exec 4xx/422 失败：`Message.error` + 保留 idle 态
- [ ] 终端容器 min-height 400px，bg container，monospace
- [ ] idle 态：`Empty`「点击连接终端开始会话」
- [ ] WebSocket 帧格式遵循 SPEC §5.3.2 客户端契约（stdin/resize → stdout/stderr/exit/error）
- [ ] Typecheck/lint 通过
- [ ] browser 验证：disabled / connecting / error

## Dependencies
#3（Console 路由壳层 + InstanceContext）

## Type
console

## Priority
high

## Labels
console

## Batch
TBD

## SPEC Reference
- §4.1.5 exec API 调用契约
- §5.3 exec WebSocket 客户端协议契约
- §5.6.2 终端组件
- §9.4(US-012) 测试矩阵

## UX Reference
- §4.5 终端 Tab 布局
- §5.5 终端组件映射
- §6.4 终端 Tab 状态
- §7.2 Messages（非 running、无权限、会话过期）
