import { createFileRoute } from '@tanstack/react-router'
import { systemQuery } from '@/api/queries/system'
import { SystemOverview } from '@/features/system/SystemOverview'

export const Route = createFileRoute('/system/')({
  loader: ({ context }) => {
    void context.queryClient.prefetchQuery(systemQuery)
  },
  component: SystemOverview,
})
