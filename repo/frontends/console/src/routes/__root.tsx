import { createRootRoute, Link, Outlet } from '@tanstack/react-router'
import { Layout, Menu } from 'tdesign-react'
import {
  DashboardIcon,
  ServerIcon,
  BookIcon,
  ChartBarIcon,
  SettingIcon,
} from 'tdesign-icons-react'

const { Header, Aside, Content } = Layout

function RootLayout() {
  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Header style={{ background: 'var(--td-brand-color)', color: '#fff' }}>
        <span style={{ fontWeight: 600, fontSize: 18 }}>KuberCloud ANI</span>
      </Header>
      <Layout>
        <Aside width="220px">
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

export const Route = createRootRoute({ component: RootLayout })
