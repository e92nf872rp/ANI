# Notebook · 前端开发日志

> **维护者**：前端开发  
> **用途**：按日记录 Console / BOSS 前端改动、决策与待办。  
> **规范依据**：[ANI-SERVICES-TEAM-GUIDE.md](../../../../ANI-SERVICES-TEAM-GUIDE.md)

## 命名规则

| 项 | 格式 | 示例 |
|---|---|---|
| 文件名 | `YYYY-MM-DD.md` | `2026-06-18.md` |
| 正文标题 | `前端开发日志（YYYY.MM.DD）` | `前端开发日志（2026.06.18）` |

每天一篇独立文档；新建当天日志时，复制下方模板到新文件即可。

## 日志索引

| 日期 | 文档 |
|---|---|
| 2026-06-18 | [2026-06-18.md](2026-06-18.md) |

## 新建日志模板

将 `YYYY-MM-DD` 换成当天日期，复制为 `notebook/YYYY-MM-DD.md`：

```markdown
# 前端开发日志（YYYY.MM.DD）

> **代码落点**：`repo/frontends/console/`、`repo/frontends/boss/`  
> **规范依据**：[ANI-SERVICES-TEAM-GUIDE.md](../../../../ANI-SERVICES-TEAM-GUIDE.md)

### 今日目标
- 

### 完成内容
- 

### 涉及文件
- `path/to/file`

### 验证方式
- [ ] `npm run dev`
- [ ] `npm run type-check`
- [ ] `npm run build`
- [ ] 浏览器手动点验：路径 / 页面名

### 遇到的问题 / 结论
- 

### 明日计划
- 
```

## 书写原则

- 写「做了什么、为什么、怎么验证」，少贴大段代码（代码以 Git diff 为准）。
- API 变更需注明是否改了 `repo/api/openapi/services/v1.yaml`，以及是否跑了 `make gen-console-api`。
- 新建日志后，在本页「日志索引」表格追加一行。
