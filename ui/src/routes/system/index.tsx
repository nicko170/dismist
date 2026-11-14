import { createFileRoute } from '@tanstack/react-router'
import { systemQuery } from '@/api/queries/system'
import { SystemOverview } from '@/features/system/SystemOverview'

export const Route = createFileRoute('/system/')({
  // The loader rides in the route's lazy chunk (initial JS budget, design §7.9).
  codeSplitGroupings: [['loader', 'component'], ['notFoundComponent'], ['errorComponent']],
  loader: ({ context }) => {
    void context.queryClient.prefetchQuery(systemQuery)
  },
  component: SystemOverview,
})
