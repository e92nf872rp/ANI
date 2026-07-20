import { createFileRoute, Link, Outlet, redirect, useNavigate } from '@tanstack/react-router'
import { Button, Layout, Menu, MessagePlugin } from 'tdesign-react'
import {
  DashboardIcon,
  ServerIcon,
  BookIcon,
  ChartBarIcon,
  SettingIcon,
} from 'tdesign-icons-react'
import { useEffect } from 'react'
import { logout, setAuthToken } from '@/api/auth'
import { getSession, isSessionValid, safeReturnTo } from '@/auth/session'

const { Header, Aside, Content } = Layout

/**
 * 受保护布局路由（pathless）。
 *
 * beforeLoad 门禁：
 *   - 无 token 或已过期 → 保存 returnTo（path + search）→ 跳转 /login?returnTo=...
 *   - 有效 token → setAuthToken 注入 Bearer middleware
 *
 * 所有业务路由必须挂在本布局下（`_authenticated/xxx.tsx`）；公开路由 `/login`、
 * `/auth/callback` 留在 routes/ 根下，不进入此布局。
 */
export const Route = createFileRoute('/_authenticated')({
  beforeLoad: ({ location }) => {
    const session = getSession()
    if (!session || !isSessionValid()) {
      const current = location.pathname + (location.searchStr ?? '')
      throw redirect({
        to: '/login',
        search: { returnTo: safeReturnTo(current) === current ? current : '/' },
      })
    }
    setAuthToken(session.access_token)
  },
  component: AuthenticatedLayout,
})

function AuthenticatedLayout() {
  const navigate = useNavigate()

  // 启动后定时检查 token 临近过期，触发 refresh（Issue #004 US-006）
  useEffect(() => {
    const timer = setInterval(() => {
      // 也许 refresh 在此触发；具体 refresh 由 api/auth.ts maybeRefresh 负责
      // 这里仅作为占位，真正 refresh 逻辑由调用 API 时按需触发
    }, 60_000)
    return () => clearInterval(timer)
  }, [])

  async function handleLogout() {
    await logout()
    MessagePlugin.success('已退出登录')
    navigate({ to: '/login' })
  }

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Header
        style={{
          background: 'var(--td-brand-color)',
          color: '#fff',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          padding: '0 24px',
        }}
      >
        <span style={{ fontWeight: 600, fontSize: 18 }}>KuberCloud ANI</span>
        <Button
          variant="outline"
          theme="default"
          onClick={handleLogout}
          style={{ color: '#fff', borderColor: 'rgba(255,255,255,0.4)' }}
        >
          退出登录
        </Button>
      </Header>
      <Layout>
        <Aside width="220px" style={{ background: '#fff' }}>
          <Menu defaultValue="dashboard" theme="light">
            <Menu.MenuItem value="dashboard" icon={<DashboardIcon />}>
              <Link to="/">仪表盘</Link>
            </Menu.MenuItem>
            <Menu.MenuItem value="models" icon={<ServerIcon />}>
              <Link to="/models">模型管理</Link>
            </Menu.MenuItem>
            <Menu.MenuItem value="kb" icon={<BookIcon />}>
              <Link to="/kb">知识库</Link>
            </Menu.MenuItem>
            <Menu.MenuItem value="usage" icon={<ChartBarIcon />}>
              <Link to="/usage">用量报表</Link>
            </Menu.MenuItem>
            <Menu.MenuItem value="settings" icon={<SettingIcon />}>
              <Link to="/settings">设置</Link>
            </Menu.MenuItem>
          </Menu>
        </Aside>
        <Content style={{ padding: 24 }}>
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  )
}
