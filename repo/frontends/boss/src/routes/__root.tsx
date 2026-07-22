import { createRootRoute, Link, Outlet } from '@tanstack/react-router'
import { Layout, Menu } from 'tdesign-react'
import { CpuIcon, DashboardIcon } from 'tdesign-icons-react'

const { Header, Aside, Content } = Layout

function BossRootLayout() {
  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Header style={{ background: 'var(--td-brand-color)', color: '#fff' }}>
        <span style={{ fontWeight: 600, fontSize: 18 }}>KuberCloud ANI BOSS</span>
      </Header>
      <Layout>
        <Aside width="220px">
          <Menu defaultValue="gpu-pool" theme="light">
            <Menu.MenuItem value="dashboard" icon={<DashboardIcon />}>
              <Link to="/">运营总览</Link>
            </Menu.MenuItem>
            <Menu.SubMenu value="ops" title="资源池与基础设施">
              <Menu.MenuItem value="gpu-pool" icon={<CpuIcon />}>
                <Link to="/ops/gpu-pool">GPU 资源池管理</Link>
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

export const Route = createRootRoute({ component: BossRootLayout })
