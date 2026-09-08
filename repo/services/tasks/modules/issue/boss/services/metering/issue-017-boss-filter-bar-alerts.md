# [BOSS] PlatformFilterBar + ApiNotReadyAlert + DevProfileAlert

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/boss/services/metering/spec-boss-metering-service.md`

## Description

创建筛选区组件 + Alert 组件。PlatformFilterBar: DateRangePicker + 指标视角 Select（聚合页可切换，专页 hidden）+ 租户 Select（filterable, clearable）+ group_by Select（含 tenant_id）。ApiNotReadyAlert: 平台 API 404/501 时全页 Alert。DevProfileAlert: dev_profile Warning 横幅。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/features/platform-metering/`

## Acceptance Criteria
- [ ] DateRangePicker: 必填，start < end 校验
- [ ] 指标视角 Select: 聚合页可切换 resource_type；专页不提供（hidden）
- [ ] 租户 Select: filterable, clearable
- [ ] group_by Select: tenant_id / day / hour
- [ ] ApiNotReadyAlert: 文案「平台计量接口尚未上线，暂无法展示跨租户排行」
- [ ] DevProfileAlert: 文案「当前为联调/开发环境数据，非生产真实计量；生产可用性待 live 验证。」
- [ ] [UI] Matches UX §6.2 状态 + §7.2 文案

## Dependencies
#15（Shell 组件 + 根布局）, #16（feature/platform-metering 基础模块）

## Type
boss

## Priority
high

## Labels
boss

## Batch
TBD

## References
- SPEC: BOSS §5.3, §6.1
- UX: §4.2, §5.2, §6.2, §7.2
