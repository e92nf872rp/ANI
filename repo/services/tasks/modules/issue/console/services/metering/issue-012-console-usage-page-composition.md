# [Console] usage 页面组合 + 状态机——重写 _authenticated/usage.tsx

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/console/services/metering/spec-console-metering-service.md`

## Description

重写 `_authenticated/usage.tsx` 组合 UsageFilterBar + UsageChart + UsageTable。实现完整状态机：idle/success、loading、empty、error、forbidden、dev_profile 横幅、invalid range、tab disabled。移除旧版 `routes/usage.tsx`。

## Scope
- Product line: console
- Code paths allowed: `repo/frontends/console/src/routes/_authenticated/usage.tsx`

## Acceptance Criteria
- [ ] 组合 FilterBar + Chart + Table，三者共享 queryKey
- [ ] 默认时间范围: 近 30 天
- [ ] 调用 `coreApi.GET('/metering/usage')`（FR-4，不绕路 Services）
- [ ] empty 态: Empty「当前时间范围内暂无用量数据」
- [ ] error 态: Alert + 重试按钮，保留筛选
- [ ] forbidden 态(403): Alert「您没有权限查看用量报表」+ 隐藏数据区
- [ ] dev_profile 横幅: `real_provider=false` 时 Warning Alert 固定文案（FR-12）
- [ ] [UI] Matches UX §6.1 全部 8 个状态
- [ ] 旧版 routes/usage.tsx 移除；routeTree.gen.ts 更新
- [ ] Typecheck/lint 通过

## Dependencies
#9（UsageFilterBar）, #10（UsageChart）, #11（UsageTable）

## Type
console

## Priority
high

## Labels
console

## Batch
TBD

## References
- SPEC: Console §5.3, §6.1
- UX: §3.1, §6.1, §7.2
