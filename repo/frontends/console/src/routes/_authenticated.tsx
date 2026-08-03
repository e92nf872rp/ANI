import { createFileRoute, Link, Outlet, redirect, useNavigate } from '@tanstack/react-router'
import { Button, Layout, Menu, MessagePlugin } from 'tdesign-react'
import {
  CpuIcon,
  DashboardIcon,
  ServerIcon,
  BookIcon,
  ChartBarIcon,
  SettingIcon,
} from 'tdesign-icons-react'
import { useEffect } from 'react'
import { logout, maybeRefresh, setAuthToken } from '@/api/auth'
import { getSession, isSessionValid, safeReturnTo, saveReturnTo } from '@/auth/session'

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
  beforeLoad: async ({ location }) => {
    const session = getSession()
    if (!session || !isSessionValid()) {
      const current = location.pathname + (location.searchStr ?? '')
      // 仅保存同源相对路径作为 returnTo，防 open redirect
      if (safeReturnTo(current) === current) {
        saveReturnTo(current)
      }
      throw redirect({
        to: '/login',
        search: { returnTo: safeReturnTo(current) === current ? current : '/' },
      })
    }
    // 路由切换时检查 token 临近过期，自动续期（剩余 < 5 分钟触发）
    await maybeRefresh()
    setAuthToken(session.access_token)
  },
  component: AuthenticatedLayout,
})

function AuthenticatedLayout() {
  const navigate = useNavigate()

  // 定时检查 token 临近过期，触发 refresh（US-006）。
  // 间隔 1 分钟；refresh 真正逻辑由 api/auth.ts maybeRefresh 负责（剩余 < 5 分钟触发）。
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
            <Menu.SubMenu value="compute" title="算力与云资源" icon={<CpuIcon />}>
              <Menu.MenuItem value="compute-gpu">
                <Link to="/compute/gpu">GPU 算力管理</Link>
              </Menu.MenuItem>
              <Menu.MenuItem value="compute-gpu-containers">
                <Link to="/compute/gpu-containers">GPU 容器实例</Link>
              </Menu.MenuItem>
            </Menu.SubMenu>
            <Menu.MenuItem value="gpu-inventory" icon={<CpuIcon />}>
              <Link to="/gpu-inventory">GPU 清单</Link>
            </Menu.MenuItem>
            <Menu.SubMenu value="settings" title="设置" icon={<SettingIcon />}>
              <Menu.MenuItem value="settings-gpu-queues">
                <Link to="/settings/gpu-queues">GPU 调度队列</Link>
              </Menu.MenuItem>
              <Menu.MenuItem value="settings-general">
                <Link to="/settings">通用设置</Link>
              </Menu.MenuItem>
            </Menu.SubMenu>
          </Menu>
        </Aside>
        <Content style={{ padding: 24 }}>
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  )
}
