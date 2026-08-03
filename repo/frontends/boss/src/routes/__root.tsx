import { createRootRoute, Outlet } from '@tanstack/react-router'

/**
 * BOSS Root 路由：仅渲染 `<Outlet />`，不含 Header/Aside。
 *
 * 业务壳层在 `_authenticated.tsx` 受保护布局；公开路由（`/login`、`/auth/callback`）
 * 直接在根下，无壳层。
 */
function RootLayout() {
  return <Outlet />
}

export const Route = createRootRoute({ component: RootLayout })
