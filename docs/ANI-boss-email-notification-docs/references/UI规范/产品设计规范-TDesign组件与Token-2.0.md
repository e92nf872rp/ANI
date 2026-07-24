# 产品设计规范 · TDesign 组件2.0

> 本文档是设计师与前端联调的 **组件选型手册**。  
> 所有 Console / BOSS 界面必须基于 **TDesign React 1.x** 与 **tdesign-icons-react** 实现。

---

## 1. 技术栈边界


| 类别    | 指定方案                                 | 禁止                                            |
| ----- | ------------------------------------ | --------------------------------------------- |
| UI 组件 | `tdesign-react`                      | Ant Design、MUI、Element Plus、自研平行 Button/Table |
| 图标    | `tdesign-icons-react`                | Font Awesome、Heroicons 等与 TDesign 混用          |
| 图表    | `echarts` + `echarts-for-react`      | 其他图表库（除非评审通过且仍须对齐 TDesign 色板）                 |
| 样式    | TDesign Design Token（CSS 变量）         | Tailwind 语义类、页面内散落 hex                        |
| 布局壳   | TDesign `Layout`、`Menu`、`Breadcrumb` | 完全自定义 shell 且不映射 TDesign                      |


**共享组件目录（工程约定）：**

```text
repo/frontends/console/src/components/   ← 业务复用组件，内部仍基于 TDesign 拼装
repo/frontends/console/src/components/shell/  ← Console 页面基线（ConsolePage / cp-*）
repo/frontends/console/src/routes/       ← 页面，不复制粘贴整页 UI
```

### 2.6 Console 页面基线间距（工程映射）

| 设计语义 | 数值 | 工程类 / 变量 |
|----------|------|----------------|
| 页面栈间距 | 16px | `ConsolePage` → `.cp-page { gap: 16px }` |
| 栅格 gutter | 16px（仅水平） | `Row gutter={16}` 或 `[16, 0]` |
| 壳层 Content padding | 20px 24px 32px | `.console-content` |
| 卡片圆角（Console 定稿） | 4px | `.cp-stat-card` / `.cp-section-card` / `.cp-content-card` |

---

## 2. Design Token 对照表

设计交付时，请在本表「设计语义」列勾选，并在标注中使用「TDesign Token」列变量名。

### 2.1 背景与表面


| 设计语义     | TDesign CSS 变量                     | 典型场景         |
| -------- | ---------------------------------- | ------------ |
| 页面背景     | `--td-bg-color-page`               | 主内容区底色       |
| 容器背景     | `--td-bg-color-container`          | Card、面板、表格容器 |
| 容器 hover | `--td-bg-color-container-hover`    | 列表项 hover    |
| 二级容器     | `--td-bg-color-secondarycontainer` | 嵌套分组、代码块底    |


### 2.2 文本


| 设计语义 | TDesign CSS 变量                | 典型场景              |
| ---- | ----------------------------- | ----------------- |
| 主文本  | `--td-text-color-primary`     | 标题、表格正文           |
| 次级文本 | `--td-text-color-secondary`   | 说明、副标题            |
| 占位文本 | `--td-text-color-placeholder` | Input placeholder |
| 反色文本 | `--td-text-color-anti`        | 品牌色顶栏上的白字         |
| 禁用文本 | `--td-text-color-disabled`    | 禁用控件              |


### 2.3 品牌与状态


| 设计语义 | TDesign CSS 变量           | 典型场景         |
| ---- | ------------------------ | ------------ |
| 品牌色  | `--td-brand-color`       | 主按钮、顶栏、链接    |
| 品牌浅色 | `--td-brand-color-light` | 选中行、轻强调底     |
| 品牌焦点 | `--td-brand-color-focus` | focus ring   |
| 成功   | `--td-success-color`     | 运行中、成功 Tag   |
| 警告   | `--td-warning-color`     | 待处理、配额告警     |
| 错误   | `--td-error-color`       | 失败、校验错误、危险提示 |


### 2.4 边框与分割


| 设计语义 | TDesign CSS 变量          | 典型场景                |
| ---- | ----------------------- | ------------------- |
| 组件边框 | `--td-component-border` | Card、Table、Input 边框 |
| 分割线  | `--td-component-stroke` | 页头底部分割              |


### 2.5 设计语义 → 按钮映射


| 设计语义         | TDesign Button 写法   | 示例场景         |
| ------------ | ------------------- | ------------ |
| Primary      | `theme="primary"`   | 「创建」「保存」「部署」 |
| Secondary    | `theme="default"`   | 「取消」「返回」     |
| Outline      | `variant="outline"` | 次要强调         |
| Ghost / Text | `variant="text"`    | 表格内「详情」      |
| Destructive  | `theme="danger"`    | 「删除」「卸载」     |
| Loading      | `loading`           | 异步提交中        |


**尺寸：** `size="small" | "medium" | "large"`，同一操作组保持一致。

---

## 3. 组件选型指南

以下列出 ANI B 端 **高频组件** 及设计注意事项。未列出的能力先查 [TDesign React 文档](https://tdesign.tencent.com/react/overview)，再申请纳入本规范。

### 3.1 布局与导航


| 场景   | TDesign 组件                          | 设计要点                             |
| ---- | ----------------------------------- | -------------------------------- |
| 整体壳层 | `Layout`、`Header`、`Aside`、`Content` | 顶栏固定；侧栏可折叠（P1）                   |
| 侧栏导航 | `Menu`                              | `theme="light"` 或 `dark` 与顶栏对比协调 |
| 面包屑  | `Breadcrumb`                        | 反映资源层级，末级为当前页                    |
| 页内标签 | `Tabs`                              | 详情页多视图；不宜超过 7 个 Tab              |
| 步骤条  | `Steps`                             | 向导型表单（如模型导入）                     |


**Console 壳层推荐结构：**

```text
┌─────────────────────────────────────────────────────────┐
│ Header（品牌色 --td-brand-color，Logo + 租户 + 用户）      │
├──────────┬──────────────────────────────────────────────┤
│ Menu     │ Breadcrumb + Page Header + Content Body      │
│ 侧栏     │                                              │
│          │                                              │
└──────────┴──────────────────────────────────────────────┘
```

### 3.2 数据展示


| 场景   | TDesign 组件             | 设计要点                               |
| ---- | ---------------------- | ---------------------------------- |
| 资源列表 | `Table`                | 必设计 loading / empty / error；操作列右对齐 |
| 简单列表 | `List`                 | 设置页、通知列表                           |
| 卡片网格 | `Row` + `Col` + `Card` | 概览 KPI，单行 4–6 卡                    |
| 统计数字 | `Statistic`            | 仪表盘核心指标                            |
| 状态标签 | `Tag`                  | 与资源状态枚举一一对应，全站一致                   |
| 空状态  | `Empty`                | 说明原因 + 主操作按钮                       |
| 加载   | `Loading`、`Skeleton`   | 首屏 Skeleton，局部 Loading             |


**Table 设计清单：**

- 列宽：名称列可宽，状态/时间列固定
- 操作列：文字按钮或 `Dropdown` 收纳低频操作
- 批量操作：选中后出现工具栏（`Alert` 或顶栏 sticky 区）
- 分页：底部 `Pagination`，位置固定

### 3.3 表单与输入


| 场景   | TDesign 组件                     | 设计要点                     |
| ---- | ------------------------------ | ------------------------ |
| 表单容器 | `Form`、`FormItem`              | label 在上或左，全站统一          |
| 文本   | `Input`                        | 必填星号、help、error 三件套      |
| 多行   | `Textarea`                     | 最小高度 80px                |
| 下拉   | `Select`                       | 选项 >10 考虑 `Select` 可搜索模式 |
| 开关   | `Switch`                       | 即时生效须配 `Message` 反馈      |
| 日期   | `DatePicker`、`DateRangePicker` | 审计、用量筛选                  |
| 上传   | `Upload`                       | 模型/文档分片上传需进度             |


**FormItem 标准结构：**

```text
Label（必填 *）
Control
Helper text（可选，--td-text-color-secondary）
Error message（--td-error-color，字段下方）
```

### 3.4 反馈与浮层


| 场景   | TDesign 组件     | 设计要点                |
| ---- | -------------- | ------------------- |
| 轻提示  | `Message`      | 成功/失败 3s 自动消失       |
| 页内告警 | `Alert`        | 配额、权限、系统公告          |
| 确认   | `Dialog`       | 危险操作必用；标题+影响说明+主次按钮 |
| 抽屉   | `Drawer`       | 窄屏详情、筛选、AI 侧栏       |
| 下拉菜单 | `Dropdown`     | 行内「更多」；不放主创建        |
| 通知中心 | `Notification` | 长任务完成、后台作业（P1）      |


### 3.5 图表（ECharts）


| 场景  | 组件      | 设计要点             |
| --- | ------- | ---------------- |
| 趋势  | 折线图     | 用量、调用量 7 日趋势     |
| 占比  | 饼图 / 环图 | GPU 分配占比（慎用过多饼图） |
| 对比  | 柱状图     | 租户/资源对比          |


**色板须使用 TDesign 语义色**，避免高饱和彩虹色：

- 主系列：`--td-brand-color`
- 辅助系列：success / warning / error 的 light 变体
- 坐标轴与网格线：`--td-component-border`

---

## 4. 状态 Tag 语义（Console 示例）

设计师须与后端状态枚举对齐，**同一状态全站同一 Tag 色**：


| 状态含义      | Tag 建议 | theme / variant                    |
| --------- | ------ | ---------------------------------- |
| 运行中 / 成功  | 绿色     | `theme="success"`                  |
| 部署中 / 处理中 | 蓝色     | `theme="primary"` 或 default + icon |
| 警告 / 即将过期 | 橙色     | `theme="warning"`                  |
| 失败 / 异常   | 红色     | `theme="danger"`                   |
| 已停止 / 草稿  | 灰色     | `theme="default"`                  |


具体枚举以各模块 OpenAPI `status` 字段为准。

---

## 5. 共享业务组件边界

以下场景 **允许** 在 `src/components/` 封装，但 **内部必须是 TDesign 组合**：


| 组件名（建议）               | 组成                                 | 用途     |
| --------------------- | ---------------------------------- | ------ |
| `PageHeader`          | 标题 + 描述 + 操作区 Slot                 | 统一页头   |
| `ResourceTable`       | `Table` + empty/error + pagination | 资源列表   |
| `ConfirmDeleteDialog` | `Dialog` + danger 文案模板             | 删除确认   |
| `StatusTag`           | `Tag` + 状态映射表                      | 统一状态色  |
| `MetricCard`          | `Card` + `Statistic`               | 概览 KPI |
| `FilterBar`           | `Form` inline + `Button`           | 列表筛选   |


**禁止：**

- 复制 TDesign 源码改样式
- 新建与 TDesign Button 并行的 `AniButton` 除非有文档化的扩展 variant

---

## 6. AI 辅助面板（可选增强）

使用 TDesign `Drawer` 或右侧 `Layout.Content` 分栏，**不作为默认壳层**。

推荐结构：

1. **头部**：标题、清空、关闭
2. **主体**：`Empty`（示例 prompt）/ 消息列表（用户 vs 系统样式区分）
3. **输入区**：`Textarea` + `Button theme="primary"` 发送

规则：

- 用户消息底：`--td-brand-color-light`
- 系统消息底：`--td-bg-color-secondarycontainer`
- 处理中：`Loading` inline，不阻断主列表操作

---

## 7. 设计稿标注规范

交付开发时，每个页面附 **组件标注表**：


| 区域    | TDesign 组件      | 关键 props            | Token                     |
| ----- | --------------- | ------------------- | ------------------------- |
| 页头主按钮 | Button          | `theme="primary"`   | —                         |
| 列表    | Table           | `loading` / `empty` | `--td-bg-color-container` |
| 删除    | Dialog + Button | `theme="danger"`    | `--td-error-color`        |


Figma 建议使用 **TDesign 官方设计资源**（如有）或按本 Token 表建 Local Styles，命名与 CSS 变量一致便于联调。

---

## 8. 反模式（评审直接驳回）

1. 设计稿出现 Ant Design 组件命名或交互（如 `Modal.okText` 文案习惯但未映射 TDesign）
2. 每页不同主色 hex，未走 Token
3. 列表页缺少 empty / error 态
4. 同一状态在不同页面用不同颜色 Tag
5. 危险操作无 Dialog，仅 Message 提示
6. 主操作区出现多个 primary 按钮
7. 图标无文字且无 aria 说明
8. 引入 shadcn / Tailwind 类名作为实现说明

---

## 9. 参考链接

- TDesign React 组件总览：[https://tdesign.tencent.com/react/overview](https://tdesign.tencent.com/react/overview)
- TDesign 设计价值观：[https://tdesign.tencent.com/design/values](https://tdesign.tencent.com/design/values)
- ANI 工程约定：`ANI-SERVICES-TEAM-GUIDE.md` §1.2
- Console 路由与页面：`ANI-11-代码实现规范.md` §6.1

