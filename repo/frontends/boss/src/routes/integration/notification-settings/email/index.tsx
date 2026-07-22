import { createFileRoute, redirect } from '@tanstack/react-router'

export const Route = createFileRoute('/integration/notification-settings/email/')({
  beforeLoad: () => {
    throw redirect({ to: '/integration/notification-settings/email/smtp' })
  },
})
