# K8s GPU 调度（HAMi + Volcano）— 研发交付包

> **版本：** PRD v1.0（P0 决策已收口）  
> **更新：** 2026-07-03  
> **用途：** 发给研发团队的独立文档包；需求 → 交互说明，技术 SPEC 待 `/prd-to-spec` 生成。

---

## 阅读顺序

| 顺序 | 文档 | 读者 | 内容 |
|------|------|------|------|
| 1 | [prd-k8s-gpu-hami-volcano-scheduling.md](./prd-k8s-gpu-hami-volcano-scheduling.md) | 全员 | 需求范围、User Story、P0 已定决策 |
| 2 | [ux-console-gpu-scheduling.md](./ux-console-gpu-scheduling.md) | Console 前端 | 租户侧页面交互、TDesign 组件、状态与文案 |
| 3 | [ux-boss-gpu-pool.md](./ux-boss-gpu-pool.md) | BOSS 前端 | 平台侧 GPU 资源池页交互 |

**模块主维护（字段口径，非本包重复）：**

- Console：`../console-modules/compute/gpu-management.md`、`gpu-container-instance-management.md`
- BOSS：`../boss-modules/ops/gpu-pool-management.md`

**UI 规范（实现时必读）：**

- `UI规范/产品设计规范-设计原则-2.0.md`
- `UI规范/产品设计规范-TDesign组件与Token-2.0.md`
- `UI规范/产品设计规范-页面模板-2.0.md`

---

## P0 范围速览

| 层级 | P0 交付 |
|------|---------|
| **Core** | HAMi + Volcano lab 门禁；`listGPUInventory` / `getGPUOccupancy`；**队列 OpenAPI CRUD**；DCGM 强制 |
| **Console** | GPU 算力管理、GPU 容器创建（整卡/vGPU）、设置 → GPU 调度队列 |
| **BOSS** | GPU 资源池管理（集群级 KPI）；租户排行 **占位**（P1） |

**界面不出现：** HAMi、Volcano、MIG、device plugin 等底座术语。

---

## 文档链（ani-workflow）

```text
PRD  →  UX（本目录）  →  SPEC（待生成）  →  Issues  →  实现
```

SPEC 建议路径：`repo/services/tasks/modules/spec/...`（生成后在本 README 补链接）。

---

## 关联代码路径（实现参考）

| 产品 | 代码目录 |
|------|----------|
| Console | `repo/frontends/console/` |
| BOSS | `repo/frontends/boss/`（骨架待落地） |
| Core API 契约 | `repo/api/openapi/v1.yaml` |

---

## 变更记录

| 日期 | 说明 |
|------|------|
| 2026-07-03 | 初版：PRD + Console/BOSS UX |
| 2026-07-03 | Console UX 补全：GPU 容器列表/详情三态、队列表列、state_reason 文案（US-008） |
