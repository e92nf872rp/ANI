import { createFileRoute, Link, Outlet, useNavigate } from '@tanstack/react-router'
import { Button, Layout, Menu, MessagePlugin } from 'tdesign-react'
import {
  CpuIcon,
  DashboardIcon,
  RootListIcon,
  ServerIcon,
  SettingIcon,
  UsergroupIcon,
} from 'tdesign-icons-react'
import { useEffect } from 'react'
import { logout, maybeRefresh, setAuthToken } from '@/api/auth'
import { getSession, isSessionValid } from '@/auth/session'

const { Header, Aside, Content } = Layout

/**
 * BOSS 布局路由（pathless）。
 *
 * 临时关闭登录门禁：未登录也可进入内部页面做联调。
 * 若本地已有有效 session，仍注入 Bearer 并做临近过期 refresh。
 */
export const Route = createFileRoute('/_authenticated')({
  beforeLoad: async () => {
    const session = getSession()
    if (session && isSessionValid()) {
      await maybeRefresh()
      setAuthToken(session.access_token)
    }
  },
  component: AuthenticatedLayout,
})

function AuthenticatedLayout() {
  const navigate = useNavigate()
  const loggedIn = !!getSession() && isSessionValid()

  useEffect(() => {
    if (!loggedIn) return
    const timer = setInterval(() => {
      void maybeRefresh()
    }, 60_000)
    return () => clearInterval(timer)
  }, [loggedIn])

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
        {loggedIn ? (
          <Button
            variant="outline"
            theme="default"
            onClick={handleLogout}
            style={{ color: '#fff', borderColor: 'rgba(255,255,255,0.4)' }}
          >
            退出登录
          </Button>
        ) : (
          <Button
            variant="outline"
            theme="default"
            onClick={() => navigate({ to: '/login' })}
            style={{ color: '#fff', borderColor: 'rgba(255,255,255,0.4)' }}
          >
            登录
          </Button>
        )}
      </Header>
      <Layout>
        <Aside width="220px" style={{ background: '#fff' }}>
          <Menu defaultValue="overview" theme="light">
            <Menu.MenuItem value="overview" icon={<DashboardIcon />}>
              <Link to="/">运营总览</Link>
            </Menu.MenuItem>
            <Menu.SubMenu value="ops" title="资源池与基础设施" icon={<ServerIcon />}>
              <Menu.MenuItem value="gpu-pool" icon={<CpuIcon />}>
                <Link to="/ops/gpu-pool">GPU 资源池管理</Link>
              </Menu.MenuItem>
            </Menu.SubMenu>
            <Menu.SubMenu value="tenant" title="租户管理" icon={<UsergroupIcon />}>
              <Menu.MenuItem value="tenant-quotas" icon={<RootListIcon />}>
                <Link to="/tenants/quotas">配额策略</Link>
              </Menu.MenuItem>
            </Menu.SubMenu>
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
