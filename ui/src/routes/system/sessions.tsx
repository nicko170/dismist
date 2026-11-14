import { createFileRoute } from '@tanstack/react-router'
import { SessionsPage } from '@/features/system/SessionsPage'

export const Route = createFileRoute('/system/sessions')({
  staticData: { title: 'Sessions' },
  component: SessionsPage,
})
