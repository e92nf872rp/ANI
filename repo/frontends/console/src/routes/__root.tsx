import { createRootRoute, Link, Outlet } from '@tanstack/react-router'
import { Layout, Menu } from 'tdesign-react'
import {
  DashboardIcon,
  ServerIcon,
  BookIcon,
  ChartBarIcon,
  SettingIcon,
  CpuIcon,
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

export const Route = createRootRoute({ component: RootLayout })
