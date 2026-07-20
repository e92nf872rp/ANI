import { createFileRoute } from '@tanstack/react-router'
import { Card } from 'tdesign-react'

/**
 * BOSS 首页占位：`/` → 运营总览。
 * SPEC 冻结路径前以占位呈现；具体业务页由后续 Issue 实现。
 */
export const Route = createFileRoute('/_authenticated/')({
  component: OverviewPage,
})

function OverviewPage() {
  return (
    <div>
      <h2 style={{ marginBottom: 24 }}>运营总览</h2>
      <Card>
        <p style={{ color: 'var(--td-text-color-secondary)' }}>
          平台运营台业务页由后续 Issue 实现。当前登录链路（OIDC + 账密）已就绪。
        </p>
      </Card>
    </div>
  )
}
