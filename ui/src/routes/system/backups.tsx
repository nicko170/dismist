import { createFileRoute } from '@tanstack/react-router'
import { BackupsPage } from '@/features/system/BackupsPage'

export const Route = createFileRoute('/system/backups')({
  staticData: { title: 'Backups' },
  component: BackupsPage,
})
