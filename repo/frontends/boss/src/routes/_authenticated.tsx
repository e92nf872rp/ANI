import { createFileRoute, Link, Outlet, redirect, useNavigate } from '@tanstack/react-router'
import { Button, Layout, Menu, MessagePlugin } from 'tdesign-react'
import {
  ChartIcon,
  CpuIcon,
  DashboardIcon,
  RootListIcon,
  ServerIcon,
  SettingIcon,
  UsergroupIcon,
} from 'tdesign-icons-react'
import { useEffect } from 'react'
import { logout, maybeRefresh, setAuthToken } from '@/api/auth'
import { getSession, isSessionValid, safeReturnTo, saveReturnTo } from '@/auth/session'

const { Header, Aside, Content } = Layout

/**
 * BOSS 布局路由（pathless）。
 * 未登录或 session 已过期 → 保存 returnTo 并重定向到 /login。
 */
export const Route = createFileRoute('/_authenticated')({
  beforeLoad: async ({ location }) => {
    const session = getSession()
    if (!session || !isSessionValid()) {
      const current = location.pathname + (location.searchStr ?? '')
      if (safeReturnTo(current) === current) {
        saveReturnTo(current)
      }
      throw redirect({
        to: '/login',
        search: { returnTo: safeReturnTo(current) === current ? current : '/' },
      })
    }
    await maybeRefresh()
    setAuthToken(session.access_token)
  },
  component: AuthenticatedLayout,
})

function AuthenticatedLayout() {
  const navigate = useNavigate()

  useEffect(() => {
    const timer = setInterval(() => {
      void maybeRefresh()
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
            <Menu.SubMenu value="ops" title="资源池与基础设施" icon={<ServerIcon />}>
              <Menu.MenuItem value="gpu-pool" icon={<CpuIcon />}>
                <Link to="/ops/gpu-pool">GPU 资源池管理</Link>
              </Menu.MenuItem>
            </Menu.SubMenu>
            <Menu.SubMenu value="tenant" title="租户管理" icon={<UsergroupIcon />}>
              <Menu.MenuItem value="tenant-quotas" icon={<RootListIcon />}>
                <Link to="/tenants/quotas">配额策略</Link>
              </Menu.MenuItem>
              <Menu.MenuItem value="tenant-usage-billing">
                <Link to="/tenant/usage-billing">租户计费与用量</Link>
              </Menu.MenuItem>
            </Menu.SubMenu>
            <Menu.SubMenu value="metering" title="平台计量与结算" icon={<ChartIcon />}>
              <Menu.MenuItem value="metering-gpu-hours">
                <Link to="/metering/gpu-hours">平台 GPU-Hours</Link>
              </Menu.MenuItem>
              <Menu.MenuItem value="metering-cpu-hours">
                <Link to="/metering/cpu-hours">平台 CPU-Hours</Link>
              </Menu.MenuItem>
              <Menu.MenuItem value="metering-memory-gbhours">
                <Link to="/metering/memory-gbhours">平台 Memory-GBHours</Link>
              </Menu.MenuItem>
              <Menu.MenuItem value="metering-input-tokens">
                <Link to="/metering/input-tokens">平台 Input Tokens</Link>
              </Menu.MenuItem>
              <Menu.MenuItem value="metering-output-tokens">
                <Link to="/metering/output-tokens">平台 Output Tokens</Link>
              </Menu.MenuItem>
              <Menu.MenuItem value="metering-storage-gbdays">
                <Link to="/metering/storage-gbdays">平台 Storage-GBDays</Link>
              </Menu.MenuItem>
              <Menu.MenuItem value="metering-kb-queries">
                <Link to="/metering/kb-queries">平台 KB Queries</Link>
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
