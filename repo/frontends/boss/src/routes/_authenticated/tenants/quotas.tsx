import { createFileRoute, Outlet } from '@tanstack/react-router'

/** `/tenants/quotas` 布局：列表 index + `/new` 创建页 */
export const Route = createFileRoute('/_authenticated/tenants/quotas')({
  component: () => <Outlet />,
})
