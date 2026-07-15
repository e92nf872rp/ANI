## 1. 背景与目标

<!-- 必须填写 Issue：Closes #N 或 Refs #N。说明为什么做，而不是只复述 diff。 -->

Closes #

**问题与目标：**

## 2. 变更范围

- 关键文件和行为变化：
- 是否涉及 Core / Services / API / Proto / DB / Console / CI：
- 是否存在跨层依赖，依赖哪个 PR 或 Issue：
- 是否超出 400 行建议切片范围，为什么必须一次完成：

## 3. 验证证据

- [ ] make test
- [ ] make validate-architecture
- [ ] make validate-services（如涉及 Services/API/handler/Console）
- [ ] make validate-doc-entrypoints（如涉及文档入口）
- [ ] 其他端到端或 live gate：

实际命令和结果：

~~~text
在这里粘贴关键摘要或 CI 链接；不能把 skipped/setup failure 写成 passed。
~~~

## 4. AI Coding 声明

- AI 工具/模型：
- AI 生成比例：0% / 1-49% / 50-99% / 100%
- [ ] 本人已阅读并理解关键变更
- [ ] 本人已验证上述命令
- [ ] 已列出不确定位置和风险
- [ ] 100% AI 代码已完成二次阅读

AI 风险点 / NEEDS-VERIFY：

## 5. 合并与回滚

- 回滚方式：revert 本 PR 的 squash commit / 其他：
- 权限、租户、幂等性、兼容性影响：
- Reviewer 重点关注：

