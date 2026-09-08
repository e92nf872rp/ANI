# [BOSS] Shell 组件 + 根布局——BossPage / BossPageHeader / BossContentCard + __root.tsx

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/boss/services/metering/spec-boss-metering-service.md`

## Description

创建 BOSS shell 组件 + 根布局 `__root.tsx`（Header + Aside + Menu + Outlet）。侧栏菜单含「租户与客户 → 租户计费与用量」、「平台计量与结算」子项（7 专页入口）。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/components/shell/`, `repo/frontends/boss/src/routes/__root.tsx`

## Acceptance Criteria
- [ ] `BossPage` / `BossPageHeader` / `BossContentCard` 组件
- [ ] `__root.tsx`: TDesign Layout (Header + Aside + Content) + Menu + Outlet
- [ ] 侧栏菜单: 「租户与客户 → 租户计费与用量」+「平台计量与结算」7 专页子项
- [ ] TDesign Token 与 Console 一致（UX §8.4）
- [ ] Typecheck 通过

## Dependencies
#13（BOSS scaffold）

## Type
boss

## Priority
high

## Labels
boss

## Batch
TBD

## References
- SPEC: BOSS §2.2, §2.4
- UX: §2.1, §2.2
