# {批次名} — {主题}

> **使用说明：** 复制本文件，替换 {} 占位内容，文件名改为 `{批次名小写-用连字符}.md`
> 例：`m1-instance-t-operation-semantics.md`、`m1-health-a-health-endpoints.md`

完成日期：YYYY-MM-DD
对应 Sprint：Sprint N（2026-MM-DD ~ MM-DD）
验证结果：make test EXIT:0，N tests passed，make validate-architecture passed

## 实现了什么

（1-3 句话，说核心变化是什么，不说怎么实现的。）

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/xxx.go` | 修改 | 新增接口/字段 |
| `pkg/adapters/runtime/xxx.go` | 新增 | 接口实现 |
| `deploy/migrations/YYYYMMDD_XXX.sql` | 新增 | DB 表变更 |
| `services/ani-gateway/internal/router/xxx.go` | 新增 | HTTP handler |

## 完工标准达成

- [x] make test 全通（N tests）
- [x] make validate-architecture 通过
- [x] {具体验收条件，与 CURRENT-SPRINT.md 或 ANI-06 中该批次的完工标准对应}

## 备注（可选）

（未覆盖的 edge case、下一批次依赖、已知技术债等）
