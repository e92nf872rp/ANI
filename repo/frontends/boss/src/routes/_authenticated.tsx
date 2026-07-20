import { createFileRoute, Link, Outlet, redirect, useNavigate } from '@tanstack/react-router'
import { Button, Layout, Menu, MessagePlugin } from 'tdesign-react'
import {
  DashboardIcon,
  ServerIcon,
  SettingIcon,
} from 'tdesign-icons-react'
import { logout, setAuthToken } from '@/api/auth'
import { getSession, isSessionValid, safeReturnTo } from '@/auth/session'

const { Header, Aside, Content } = Layout

/**
 * BOSS 受保护布局路由（pathless）。
 *
 * beforeLoad 门禁：
 *   - 无 token 或已过期 → 保存 returnTo（path + search）→ 跳转 /login?returnTo=...
 *   - 有效 token → setAuthToken 注入 Bearer middleware
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
        <span style={{ fontWeight: 600, fontSize: 18 }}>ANI 平台运营台</span>
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
          <Menu defaultValue="overview" theme="light">
            <Menu.MenuItem value="overview" icon={<DashboardIcon />}>
              <Link to="/">运营总览</Link>
            </Menu.MenuItem>
            <Menu.MenuItem value="ops" icon={<ServerIcon />}>
              <Link to="/">运营管理</Link>
            </Menu.MenuItem>
            <Menu.MenuItem value="settings" icon={<SettingIcon />}>
              <Link to="/">平台设置</Link>
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
