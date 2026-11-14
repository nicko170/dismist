import { createFileRoute } from '@tanstack/react-router'
import { MigrationPage } from '@/features/system/MigrationPage'

export const Route = createFileRoute('/system/migration')({
  staticData: { title: 'Migration' },
  component: MigrationPage,
})
