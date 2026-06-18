import { createFileRoute, Link } from '@tanstack/react-router'
import { List } from 'tdesign-react'

export const Route = createFileRoute('/settings/')({
  component: () => (
    <div>
      <h2>设置</h2>
      <List>
        <List.ListItem><Link to="/settings/api-keys">API Key 管理</Link></List.ListItem>
      </List>
    </div>
  ),
})
