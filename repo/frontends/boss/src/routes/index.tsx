import { createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/')({
  component: BossDashboard,
})

function BossDashboard() {
  return (
    <div>
      <h2 style={{ marginBottom: 24 }}>运营总览</h2>
      <p style={{ color: 'var(--td-text-color-secondary)' }}>
        BOSS 运营后台首页。GPU 资源池管理请从侧栏进入「资源池与基础设施 → GPU 资源池管理」。
      </p>
    </div>
  )
}
